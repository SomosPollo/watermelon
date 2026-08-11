package cli

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestRunCommandRequiresConfig(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	err = runRun()
	if err == nil {
		t.Error("expected error when no config exists")
	}
}

func TestRunCommandLoadsConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	config := `
[vm]
image = "ubuntu-22.04"

[network]
allow = []
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	// Just test that config loads without error
	// (actual VM operations would require Lima installed)
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.VM.Image != "ubuntu-22.04" {
		t.Errorf("expected ubuntu-22.04, got %s", cfg.VM.Image)
	}
}

func TestRunPrintsSSHHost(t *testing.T) {
	// This tests that the vmName is converted to SSH host format
	vmName := "watermelon-test-12345678"
	expectedHost := "lima-" + vmName

	host := lima.GetSSHHost(vmName)
	if host != expectedHost {
		t.Errorf("expected SSH host %q, got %q", expectedHost, host)
	}
}

func TestSaveAndReadPort(t *testing.T) {
	dir := t.TempDir()
	if err := preparePrivateRuntimeDirectory(filepath.Join(dir, ".watermelon")); err != nil {
		t.Fatal(err)
	}

	port := readSavedPort(dir)
	if port != 0 {
		t.Errorf("readSavedPort() = %d, want 0 for non-existent file", port)
	}

	if err := savePort(dir, 39285); err != nil {
		t.Fatal(err)
	}
	port = readSavedPort(dir)
	if port != 39285 {
		t.Errorf("readSavedPort() = %d, want 39285", port)
	}
	info, err := os.Lstat(filepath.Join(dir, ".watermelon", "verdict-port"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ownedByCurrentUser(info) {
		t.Fatalf("saved verdict-port mode/owner = %v, want current-user-owned regular 0600 file", info.Mode())
	}
}

func TestSavePortRejectsVictimSymlinkWithoutFollowingIt(t *testing.T) {
	project := t.TempDir()
	watermelonDir := filepath.Join(project, ".watermelon")
	if err := preparePrivateRuntimeDirectory(watermelonDir); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(watermelonDir, "verdict-port")); err != nil {
		t.Fatal(err)
	}

	err := savePort(project, 39285)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("savePort() error = %v, want symlink rejection", err)
	}
	contents, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "do not overwrite" {
		t.Fatalf("verdict-port symlink victim was overwritten: %q", contents)
	}
	if got := readSavedPort(project); got != 0 {
		t.Fatalf("readSavedPort() followed unsafe symlink and returned %d", got)
	}
}

func TestReadSavedPortRejectsInvalidFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(*testing.T, string)
	}{
		{
			name: "directory",
			create: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "insecure mode",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("39285\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "invalid value",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("70000\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			watermelonDir := filepath.Join(project, ".watermelon")
			if err := preparePrivateRuntimeDirectory(watermelonDir); err != nil {
				t.Fatal(err)
			}
			test.create(t, filepath.Join(watermelonDir, "verdict-port"))
			if got := readSavedPort(project); got != 0 {
				t.Fatalf("readSavedPort() = %d, want 0 for invalid saved state", got)
			}
		})
	}
}

func TestAskCreationPropagatesVerdictPortSaveFailureBeforeStart(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"ask\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(t.TempDir(), "watermelon-nfqd")
	if err := os.WriteFile(sidecar, []byte("sidecar"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATERMELON_NFQD_BINARY", sidecar)
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldStart, oldSavePort, oldSavePortAt := cliGetVMStatus, cliStartVM, cliSaveVerdictPort, cliSaveVerdictPortAt
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	startCalls := 0
	cliStartVM = func(string, string) error {
		startCalls++
		return nil
	}
	saveErr := errors.New("port state unavailable")
	listenerHeldThroughSave := false
	cliSaveVerdictPort = func(string, int) error { return saveErr }
	cliSaveVerdictPortAt = func(_ string, port int) error {
		probe, bindErr := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if bindErr == nil {
			_ = probe.Close()
			return errors.New("verdict listener was released before its port was saved")
		}
		if !errors.Is(bindErr, syscall.EADDRINUSE) {
			return fmt.Errorf("checking verdict listener during save: %w", bindErr)
		}
		listenerHeldThroughSave = true
		return saveErr
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStartVM = oldStart
		cliSaveVerdictPort = oldSavePort
		cliSaveVerdictPortAt = oldSavePortAt
	})

	err = runRunWithOptions(runOptions{OpenShell: true})
	if !errors.Is(err, saveErr) {
		t.Fatalf("run error = %v, want verdict-port save failure preserved", err)
	}
	if startCalls != 0 {
		t.Fatalf("start calls = %d, want 0 before durable verdict-port state", startCalls)
	}
	if !listenerHeldThroughSave {
		t.Fatal("ask listener was not kept open through the durable port save")
	}
}

func TestAskVerdictListenerExplainsSingleForegroundController(t *testing.T) {
	setupNamedVMIdentityTest(t)
	first, err := listenForAskVerdicts("ask-dev", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	port := first.Addr().(*net.TCPAddr).Port

	second, err := listenForAskVerdicts("ask-dev", port)
	if second != nil {
		_ = second.Close()
		t.Fatal("second ask listener unexpectedly acquired the active prompt port")
	}
	if err == nil || !strings.Contains(err.Error(), "already in use") || !strings.Contains(err.Error(), "unrelated process") {
		t.Fatalf("second ask listener error = %v, want actionable single-controller guidance", err)
	}
}

func TestExistingAskVMRequiresStrictSavedVerdictPort(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk
	instance, err := reserveNamedVMIdentity(project, "ask-existing-port", cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, loadErr := savedAskVerdictPortForExistingVM(instance); loadErr == nil || !strings.Contains(loadErr.Error(), "no saved verdict port") {
		t.Fatalf("missing saved port error = %v, want strict missing-state rejection", loadErr)
	}
	if err := os.WriteFile(instance.Paths.VerdictPortPath, []byte("not-a-port\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, loadErr := savedAskVerdictPortForExistingVM(instance); loadErr == nil || !strings.Contains(loadErr.Error(), "parsing saved verdict port") {
		t.Fatalf("malformed saved port error = %v, want strict parse rejection", loadErr)
	}
	if err := savePortAt(instance.Paths.VerdictPortPath, 39285); err != nil {
		t.Fatal(err)
	}
	if port, err := savedAskVerdictPortForExistingVM(instance); err != nil || port != 39285 {
		t.Fatalf("valid saved port = %d, err %v; want 39285", port, err)
	}
}

func TestInstallBuiltNfqdReplacesDestinationSymlinkWithoutFollowingIt(t *testing.T) {
	binDir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(binDir, "watermelon-nfqd")
	if err := os.Symlink(victim, dest); err != nil {
		t.Fatal(err)
	}

	err := installBuiltNfqd(dest, func(outputPath string) error {
		if outputPath == dest {
			t.Fatal("build output reused the attacker-controlled destination path")
		}
		return os.WriteFile(outputPath, []byte("safe binary"), 0644)
	})
	if err != nil {
		t.Fatal(err)
	}

	victimData, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(victimData) != "do not overwrite" {
		t.Fatalf("symlink target was overwritten: %q", victimData)
	}
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("installed binary mode = %v, want regular file", info.Mode())
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("installed binary permissions = %04o, want 0700", info.Mode().Perm())
	}
	installed, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "safe binary" {
		t.Fatalf("installed binary = %q, want safe build output", installed)
	}
}

func TestEnsureNfqdBinaryCreatesPrivateOwnedRuntimeDirectories(t *testing.T) {
	project := t.TempDir()
	watermelonDir := filepath.Join(project, ".watermelon")
	if err := os.Mkdir(watermelonDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "watermelon-nfqd")
	if err := os.WriteFile(source, []byte("trusted sidecar"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATERMELON_NFQD_BINARY", source)

	if err := ensureNfqdBinary(project); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(watermelonDir, "bin")
	for _, dir := range []string{watermelonDir, binDir} {
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
			t.Fatalf("runtime directory %q mode = %v, want real 0700 directory", dir, info.Mode())
		}
		if !ownedByCurrentUser(info) {
			t.Fatalf("runtime directory %q is not owned by current user", dir)
		}
	}
	binaryInfo, err := os.Lstat(filepath.Join(binDir, "watermelon-nfqd"))
	if err != nil {
		t.Fatal(err)
	}
	if !binaryInfo.Mode().IsRegular() || binaryInfo.Mode().Perm() != 0700 {
		t.Fatalf("installed sidecar mode = %v, want regular 0700 file", binaryInfo.Mode())
	}
}

func TestEnsureNfqdBinaryRejectsSymlinkedRuntimeDirectoriesBeforeInstall(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{
			name: "watermelon directory",
			setup: func(t *testing.T, project, external string) {
				if err := os.Symlink(external, filepath.Join(project, ".watermelon")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bin directory",
			setup: func(t *testing.T, project, external string) {
				watermelonDir := filepath.Join(project, ".watermelon")
				if err := os.Mkdir(watermelonDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(watermelonDir, "bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			external := t.TempDir()
			victim := filepath.Join(external, "watermelon-nfqd")
			if err := os.WriteFile(victim, []byte("do not overwrite"), 0600); err != nil {
				t.Fatal(err)
			}
			test.setup(t, project, external)
			source := filepath.Join(t.TempDir(), "trusted-source")
			if err := os.WriteFile(source, []byte("trusted sidecar"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WATERMELON_NFQD_BINARY", source)

			err := ensureNfqdBinary(project)
			if err == nil || !strings.Contains(err.Error(), "real directory") {
				t.Fatalf("ensureNfqdBinary() error = %v, want symlinked-directory rejection", err)
			}
			contents, readErr := os.ReadFile(victim)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(contents) != "do not overwrite" {
				t.Fatalf("external victim was overwritten: %q", contents)
			}
		})
	}
}

func TestHashPreparedNfqdBinary(t *testing.T) {
	project := t.TempDir()
	binDir := filepath.Join(project, ".watermelon", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "watermelon-nfqd"), []byte("abc"), 0755); err != nil {
		t.Fatal(err)
	}

	digest, err := hashPreparedNfqdBinary(project)
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if digest != want {
		t.Fatalf("network interceptor digest = %q, want %q", digest, want)
	}
}

func TestHashPreparedNfqdBinaryRejectsUnsafeFileTypesAndPathComponents(t *testing.T) {
	t.Run("final symlink", func(t *testing.T) {
		project := t.TempDir()
		binDir := filepath.Join(project, ".watermelon", "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("attacker-selected"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(binDir, "watermelon-nfqd")); err != nil {
			t.Fatal(err)
		}

		if _, err := hashPreparedNfqdBinary(project); err == nil || !strings.Contains(err.Error(), "without following symlinks") {
			t.Fatalf("symlink hash error = %v, want no-follow rejection", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		project := t.TempDir()
		binaryPath := filepath.Join(project, ".watermelon", "bin", "watermelon-nfqd")
		if err := os.MkdirAll(binaryPath, 0755); err != nil {
			t.Fatal(err)
		}

		if _, err := hashPreparedNfqdBinary(project); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("directory hash error = %v, want regular-file rejection", err)
		}
	})

	t.Run("symlinked parent", func(t *testing.T) {
		project := t.TempDir()
		external := t.TempDir()
		if err := os.MkdirAll(filepath.Join(external, "bin"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(external, "bin", "watermelon-nfqd"), []byte("attacker-selected"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, filepath.Join(project, ".watermelon")); err != nil {
			t.Fatal(err)
		}

		if _, err := hashPreparedNfqdBinary(project); err == nil || !strings.Contains(err.Error(), "directory component") {
			t.Fatalf("parent symlink hash error = %v, want no-follow rejection", err)
		}
	})
}

func TestGenerateConfigForInstanceAuthenticatesAskModeNfqd(t *testing.T) {
	project := t.TempDir()
	bootstrapDir := t.TempDir()
	nfqdPath := filepath.Join(bootstrapDir, "watermelon-nfqd")
	if err := os.WriteFile(nfqdPath, []byte("abc"), 0700); err != nil {
		t.Fatal(err)
	}
	identity := &namedVMInstanceIdentity{Paths: namedVMIdentityPaths{
		BootstrapDir: bootstrapDir,
		NfqdPath:     nfqdPath,
	}, VerdictAuthKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	oldGenerate := cliGenerateConfig
	generateCalls := 0
	cliGenerateConfig = func(_ *config.Config, gotProject string, _ map[string]string, opts lima.GenerateOptions) (string, error) {
		generateCalls++
		if gotProject != project {
			t.Fatalf("generator project = %q, want %q", gotProject, project)
		}
		if opts.VerdictServerPort != 39285 {
			t.Fatalf("verdict port = %d, want 39285", opts.VerdictServerPort)
		}
		const wantDigest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
		if opts.NfqdSHA256 != wantDigest {
			t.Fatalf("nfqd digest = %q, want %q", opts.NfqdSHA256, wantDigest)
		}
		if opts.VerdictAuthKey != identity.VerdictAuthKey {
			t.Fatalf("verdict authentication key was not bound to the instance")
		}
		if opts.BootstrapHostDir != bootstrapDir {
			t.Fatalf("bootstrap host dir = %q, want %q", opts.BootstrapHostDir, bootstrapDir)
		}
		return "generated", nil
	}
	t.Cleanup(func() { cliGenerateConfig = oldGenerate })

	generated, err := generateConfigForInstanceWithIdentity(cfg, project, nil, 39285, identity)
	if err != nil {
		t.Fatal(err)
	}
	if generated != "generated" || generateCalls != 1 {
		t.Fatalf("generation result = %q with %d calls", generated, generateCalls)
	}
}

func TestGenerateConfigForInstanceSkipsNfqdHashOutsideAskMode(t *testing.T) {
	project := t.TempDir()
	cfg := config.NewConfig()

	oldGenerate := cliGenerateConfig
	cliGenerateConfig = func(_ *config.Config, _ string, _ map[string]string, opts lima.GenerateOptions) (string, error) {
		if opts.NfqdSHA256 != "" {
			t.Fatalf("non-ask nfqd digest = %q, want empty", opts.NfqdSHA256)
		}
		return "generated", nil
	}
	t.Cleanup(func() { cliGenerateConfig = oldGenerate })

	if _, err := generateConfigForInstance(cfg, project, nil, 0); err != nil {
		t.Fatalf("non-ask generation unexpectedly required nfqd: %v", err)
	}
}

func TestGenerateConfigForInstanceDoesNotGenerateWhenNfqdHashFails(t *testing.T) {
	project := t.TempDir()
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	oldGenerate := cliGenerateConfig
	generateCalls := 0
	cliGenerateConfig = func(_ *config.Config, _ string, _ map[string]string, _ lima.GenerateOptions) (string, error) {
		generateCalls++
		return "", nil
	}
	t.Cleanup(func() { cliGenerateConfig = oldGenerate })

	if _, err := generateConfigForInstance(cfg, project, nil, 0); err == nil {
		t.Fatal("ask generation unexpectedly accepted a missing nfqd binary")
	}
	if generateCalls != 0 {
		t.Fatalf("generator calls = %d, want 0 after hash failure", generateCalls)
	}
}

func TestSaveAndAssessAppliedPolicySnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	configPath := filepath.Join(dir, ".watermelon.toml")
	if err := os.WriteFile(configPath, []byte("[network]\nallow = []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := saveAppliedPolicySnapshot(dir, cfg); err != nil {
		t.Fatalf("saveAppliedPolicySnapshot() error = %v", err)
	}
	assessment := assessAppliedPolicy(dir, lima.StatusRunning, cfg)
	if assessment.State != policyCurrent {
		t.Fatalf("applied policy state = %v, want current (error: %v)", assessment.State, assessment.Err)
	}
	if assessment.Snapshot.Enforcement != config.EnforcementFail {
		t.Fatalf("applied enforcement = %q, want fail", assessment.Snapshot.Enforcement)
	}
}

func TestSavedAskRuleMakesAppliedPolicyStaleUntilNamedVMRecreation(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	vmName := "ask-saved-rule"
	configPath := filepath.Join(project, ".watermelon.toml")
	contents := `[vm]
name = "ask-saved-rule"

[network]
allow = []

[security]
enforcement = "ask"
`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	applied, err := loadProjectConfig(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedPolicySnapshotForVM(project, vmName, applied); err != nil {
		t.Fatal(err)
	}

	added, err := ask.AddDomainToConfig(configPath, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("new ask rule was not added")
	}
	configured, err := loadProjectConfig(project)
	if err != nil {
		t.Fatal(err)
	}
	assessment := assessAppliedPolicyForVM(project, vmName, lima.StatusStopped, configured)
	if assessment.State != policyStale {
		t.Fatalf("saved ask-rule policy state = %v, want stale (error: %v)", assessment.State, assessment.Err)
	}

	wantCommand := "watermelon destroy --name ask-saved-rule --force && watermelon run --name ask-saved-rule"
	policyErr := requireCurrentAppliedPolicyForVM(project, vmName, lima.StatusStopped, configured, true)
	if policyErr == nil || !strings.Contains(policyErr.Error(), wantCommand) {
		t.Fatalf("saved ask-rule recovery error = %v, want %q", policyErr, wantCommand)
	}
}

func TestAppliedPolicySnapshotDetectsEffectiveUIDMismatch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Watermelon VM creation requires an unprivileged effective host UID")
	}
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	cfg := config.NewConfig()

	host, err := resolveAppliedPolicyHostContext(project, cfg)
	if err != nil {
		t.Fatal(err)
	}
	currentUID := uint32(os.Geteuid())
	if host.Recorded.EffectiveUID != currentUID {
		t.Fatalf("recorded effective UID = %d, want %d", host.Recorded.EffectiveUID, currentUID)
	}
	alternateUID := currentUID + 1
	if alternateUID == 0 || alternateUID == ^uint32(0) {
		alternateUID = currentUID - 1
	}
	host.Recorded.EffectiveUID = alternateUID
	if err := saveAppliedPolicySnapshotWithHost(host, cfg); err != nil {
		t.Fatal(err)
	}

	assessment := assessAppliedPolicy(project, lima.StatusRunning, cfg)
	if assessment.State != policyStale {
		t.Fatalf("effective UID mismatch state = %v, want stale (error: %v)", assessment.State, assessment.Err)
	}
}

func TestExistingVMLegacySnapshotFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	tomlData := []byte("[network]\nallow = []\n")
	cfg, err := config.Parse(tomlData)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.Enforcement != config.EnforcementFail {
		t.Fatalf("omitted enforcement = %q, want fail", cfg.Security.Enforcement)
	}
	stateDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacyDigest := fmt.Sprintf("%x\n", sha256.Sum256(tomlData))
	if err := os.WriteFile(legacyConfigDigestPath(dir), []byte(legacyDigest), 0644); err != nil {
		t.Fatal(err)
	}

	assessment := assessAppliedPolicy(dir, lima.StatusRunning, cfg)
	if assessment.State != policyUnverifiedLegacy {
		t.Fatalf("legacy policy state = %v, want policyUnverifiedLegacy", assessment.State)
	}
	err = requireCurrentAppliedPolicy(dir, lima.StatusRunning, cfg)
	if err == nil || !strings.Contains(err.Error(), recreatePolicyCommand) || !strings.Contains(err.Error(), "does not record") {
		t.Fatalf("legacy policy error = %v; want fail-closed recreation instruction", err)
	}
}

func TestPolicyRecoveryCommandsPreserveCustomVMName(t *testing.T) {
	dir := t.TempDir()
	vmName := "shared-dev"
	recreate := recreatePolicyCommandForVM(dir, vmName)
	if !strings.Contains(recreate, "destroy --name shared-dev") || !strings.Contains(recreate, "run --name shared-dev") {
		t.Fatalf("custom recreate command = %q", recreate)
	}
	restart := restartPolicyCommandForVM(dir, vmName)
	if !strings.Contains(restart, "stop --name shared-dev") || !strings.Contains(restart, "run --name shared-dev") {
		t.Fatalf("custom restart command = %q", restart)
	}

	derived := lima.VMNameFromPath(dir)
	if got := recreatePolicyCommandForVM(dir, derived); got != recreatePolicyCommand {
		t.Fatalf("derived recreate command = %q, want %q", got, recreatePolicyCommand)
	}
	wantDeferred := "watermelon destroy --name " + derived + " --force && watermelon run --name " + derived
	if got := deferredRecreatePolicyCommandForVM(dir, derived); got != wantDeferred {
		t.Fatalf("deferred derived recreate command = %q, want VM-pinned %q", got, wantDeferred)
	}
	if got := restartPolicyCommandForVM(dir, derived); got != "watermelon stop && watermelon run" {
		t.Fatalf("derived restart command = %q", got)
	}
}

func TestPolicyRecoveryCommandsPreserveExplicitDerivedSelection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[vm]\nname = \"configured-other\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	derived := lima.VMNameFromPath(dir)
	target, err := resolveConfiguredTarget(dir, derived)
	if err != nil {
		t.Fatal(err)
	}
	if !target.NameExplicit || target.VMName != derived {
		t.Fatalf("selection = %+v, want explicit derived VM %q", target, derived)
	}

	wantDestroy := "watermelon destroy --name " + derived + " --force"
	wantRun := "watermelon run --name " + derived
	wantStop := "watermelon stop --name " + derived
	if got := destroyPolicyCommandForVM(dir, derived, target.NameExplicit); got != wantDestroy {
		t.Fatalf("explicit derived destroy command = %q, want %q", got, wantDestroy)
	}
	if got := recreatePolicyCommandForVM(dir, derived, target.NameExplicit); got != wantDestroy+" && "+wantRun {
		t.Fatalf("explicit derived recreate command = %q", got)
	}
	if got := restartPolicyCommandForVM(dir, derived, target.NameExplicit); got != wantStop+" && "+wantRun {
		t.Fatalf("explicit derived restart command = %q", got)
	}

	policyErr := requireCurrentAppliedPolicyForVM(dir, derived, lima.StatusStopped, target.Config, target.NameExplicit)
	if policyErr == nil || !strings.Contains(policyErr.Error(), wantDestroy+" && "+wantRun) {
		t.Fatalf("snapshot recovery error = %v, want explicit derived selection", policyErr)
	}

	oldVerify := cliVerifyPolicy
	cliVerifyPolicy = func(string) error { return errors.New("marker missing") }
	t.Cleanup(func() { cliVerifyPolicy = oldVerify })
	runtimeErr := requireRuntimePolicyApplied(dir, derived, false, target.NameExplicit)
	if runtimeErr == nil || !strings.Contains(runtimeErr.Error(), wantStop+" && "+wantRun) ||
		!strings.Contains(runtimeErr.Error(), wantDestroy+" && "+wantRun) {
		t.Fatalf("runtime recovery error = %v, want explicit restart and recreate commands", runtimeErr)
	}
}

func TestExistingVMMissingOrMalformedSnapshotFailsClosed(t *testing.T) {
	cfg := config.NewConfig()

	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", "")
		assessment := assessAppliedPolicy(dir, lima.StatusStopped, cfg)
		if assessment.State != policyUnverifiedMissing {
			t.Fatalf("state = %v, want policyUnverifiedMissing", assessment.State)
		}
		if err := requireCurrentAppliedPolicy(dir, lima.StatusStopped, cfg); err == nil || !strings.Contains(err.Error(), recreatePolicyCommand) {
			t.Fatalf("missing snapshot error = %v", err)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", "")
		path, err := appliedPolicySnapshotPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not json\n"), 0600); err != nil {
			t.Fatal(err)
		}
		assessment := assessAppliedPolicy(dir, lima.StatusRunning, cfg)
		if assessment.State != policyUnverifiedInvalid {
			t.Fatalf("state = %v, want policyUnverifiedInvalid", assessment.State)
		}
		if err := requireCurrentAppliedPolicy(dir, lima.StatusRunning, cfg); err == nil || !strings.Contains(err.Error(), recreatePolicyCommand) {
			t.Fatalf("malformed snapshot error = %v", err)
		}
	})
}

func TestExistingVMStalePolicyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	applied := config.NewConfig()
	if err := saveAppliedPolicySnapshot(dir, applied); err != nil {
		t.Fatal(err)
	}
	configured := config.NewConfig()
	configured.Security.Enforcement = config.EnforcementLog

	assessment := assessAppliedPolicy(dir, lima.StatusRunning, configured)
	if assessment.State != policyStale {
		t.Fatalf("state = %v, want policyStale", assessment.State)
	}
	err := requireCurrentAppliedPolicy(dir, lima.StatusRunning, configured)
	if err == nil || !strings.Contains(err.Error(), "configured policy is log") || !strings.Contains(err.Error(), "recorded policy is fail") {
		t.Fatalf("stale policy error = %v", err)
	}
}

func TestExistingVMStaleConfigWithSameEnforcementExplainsConfigDrift(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	applied := config.NewConfig()
	if err := saveAppliedPolicySnapshot(dir, applied); err != nil {
		t.Fatal(err)
	}
	configured := config.NewConfig()
	configured.Resources.CPUs = 2

	err := requireCurrentAppliedPolicy(dir, lima.StatusRunning, configured)
	if err == nil {
		t.Fatal("same-enforcement config drift unexpectedly accepted")
	}
	message := err.Error()
	if !strings.Contains(message, "VM-affecting settings changed") || !strings.Contains(message, "recorded enforcement remains fail") {
		t.Fatalf("same-enforcement stale error = %q", message)
	}
	if strings.Contains(message, "configured policy is fail") && strings.Contains(message, "recorded policy is fail") {
		t.Fatalf("same-enforcement stale error misleadingly compares fail with fail: %q", message)
	}
}

func TestNewVMDoesNotRequireSnapshot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	if err := requireCurrentAppliedPolicy(t.TempDir(), lima.StatusNotFound, config.NewConfig()); err != nil {
		t.Fatalf("new VM policy check error = %v", err)
	}
}

func TestAppliedPolicySnapshotUsesHostOnlyStateAndClearsBeforeCreation(t *testing.T) {
	dir := t.TempDir()
	hostConfig := privateTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", hostConfig)
	t.Setenv("LIMA_HOME", "")

	path, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, dir+string(os.PathSeparator)) {
		t.Fatalf("applied-policy snapshot is project-local: %s", path)
	}
	if !strings.HasPrefix(path, hostConfig+string(os.PathSeparator)) {
		t.Fatalf("applied-policy snapshot %s is not under host config %s", path, hostConfig)
	}

	if err := saveAppliedPolicySnapshot(dir, config.NewConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved snapshot missing: %v", err)
	}
	if err := clearAppliedPolicySnapshot(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old snapshot was not cleared, stat error = %v", err)
	}
}

func TestAppliedPolicySnapshotRespectsLimaHome(t *testing.T) {
	dir := t.TempDir()
	hostConfig := privateTempDir(t)
	limaHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", hostConfig)
	t.Setenv("LIMA_HOME", limaHome)

	firstPath, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(firstPath, hostConfig+string(os.PathSeparator)) {
		t.Fatalf("snapshot path is not in host-only config state: %s", firstPath)
	}

	t.Setenv("LIMA_HOME", t.TempDir())
	secondPath, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if firstPath == secondPath {
		t.Fatalf("distinct LIMA_HOME values share applied-policy snapshot %s", firstPath)
	}
}

func TestAppliedPolicyHostStateRejectsProjectControlledConfigHome(t *testing.T) {
	project := privateTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", project)
	t.Setenv("LIMA_HOME", "")

	_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("project-controlled HOME error = %v, want overlap rejection", err)
	}
}

func TestAppliedPolicyHostStateRejectsRelativeEnvironmentPaths(t *testing.T) {
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "relative-config")
		t.Setenv("LIMA_HOME", t.TempDir())
		_, err := resolveAppliedPolicyHostContext(t.TempDir(), config.NewConfig())
		if err == nil || !strings.Contains(err.Error(), "relative") {
			t.Fatalf("relative XDG_CONFIG_HOME error = %v", err)
		}
	})

	t.Run("HOME fallback", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "relative-home")
		t.Setenv("LIMA_HOME", "")
		_, err := resolveAppliedPolicyHostContext(t.TempDir(), config.NewConfig())
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Fatalf("relative HOME error = %v", err)
		}
	})
}

func TestAppliedPolicyHostStateRejectsXDGConfigSymlinkIntoProject(t *testing.T) {
	project := privateTempDir(t)
	link := filepath.Join(t.TempDir(), "config-link")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", link)
	t.Setenv("LIMA_HOME", t.TempDir())

	_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("symlinked XDG config error = %v, want overlap rejection", err)
	}
}

func TestAppliedPolicyHostStateRejectsLexicalXDGConfigSymlinkInsideProject(t *testing.T) {
	project := privateTempDir(t)
	safeConfig := privateTempDir(t)
	link := filepath.Join(project, "config-link")
	if err := os.Symlink(safeConfig, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", link)
	t.Setenv("LIMA_HOME", t.TempDir())

	_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
	if err == nil || !strings.Contains(err.Error(), "lexical user config") || !strings.Contains(err.Error(), "project") {
		t.Fatalf("guest-controlled lexical XDG config error = %v, want overlap rejection", err)
	}
}

func TestAppliedPolicyHostStateRejectsReadWriteMountOverlap(t *testing.T) {
	project := t.TempDir()
	hostConfig := privateTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", hostConfig)
	t.Setenv("LIMA_HOME", t.TempDir())

	cfg := config.NewConfig()
	cfg.Mounts = map[string]config.Mount{
		filepath.Join(hostConfig, "watermelon", "shared"): {
			Target: "/host-state",
			Mode:   "rw",
		},
	}
	_, err := resolveAppliedPolicyHostContext(project, cfg)
	if err == nil || !strings.Contains(err.Error(), "read-write mount") {
		t.Fatalf("RW state mount error = %v, want overlap rejection", err)
	}

	cfg.Mounts[filepath.Join(hostConfig, "watermelon", "shared")] = config.Mount{
		Target: "/host-state",
		Mode:   "ro",
	}
	if _, err := resolveAppliedPolicyHostContext(project, cfg); err != nil {
		t.Fatalf("read-only state mount unexpectedly rejected: %v", err)
	}
}

func TestAppliedPolicyHostStateRejectsGroupWritableConfigDirectory(t *testing.T) {
	project := t.TempDir()
	hostConfig := privateTempDir(t)
	if err := os.Chmod(hostConfig, 0770); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", hostConfig)
	t.Setenv("LIMA_HOME", t.TempDir())

	_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
	if err == nil || !strings.Contains(err.Error(), "writable by group") {
		t.Fatalf("group-writable config directory error = %v", err)
	}
}

func TestAppliedPolicyHostStateRejectsLimaHomeExposure(t *testing.T) {
	t.Run("relative LIMA_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", "relative-lima")
		_, err := resolveAppliedPolicyHostContext(t.TempDir(), config.NewConfig())
		if err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Fatalf("relative LIMA_HOME error = %v", err)
		}
	})

	t.Run("project contains LIMA_HOME", func(t *testing.T) {
		project := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", filepath.Join(project, ".lima"))
		_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
		if err == nil || !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "LIMA_HOME") {
			t.Fatalf("project/Lima overlap error = %v", err)
		}
	})

	t.Run("lexical LIMA_HOME symlink inside project", func(t *testing.T) {
		project := t.TempDir()
		safeLimaHome := t.TempDir()
		limaLink := filepath.Join(project, "lima-link")
		if err := os.Symlink(safeLimaHome, limaLink); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", limaLink)

		_, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
		if err == nil || !strings.Contains(err.Error(), "lexical LIMA_HOME") || !strings.Contains(err.Error(), "project") {
			t.Fatalf("guest-controlled lexical LIMA_HOME error = %v, want overlap rejection", err)
		}
	})

	t.Run("read-only mount contains LIMA_HOME", func(t *testing.T) {
		project := t.TempDir()
		limaParent := t.TempDir()
		limaHome := filepath.Join(limaParent, ".lima")
		t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
		t.Setenv("LIMA_HOME", limaHome)
		cfg := config.NewConfig()
		cfg.Mounts = map[string]config.Mount{
			limaParent: {Target: "/lima-state", Mode: "ro"},
		}
		_, err := resolveAppliedPolicyHostContext(project, cfg)
		if err == nil || !strings.Contains(err.Error(), "mount source") || !strings.Contains(err.Error(), "LIMA_HOME") {
			t.Fatalf("read-only Lima mount error = %v", err)
		}
	})
}

func TestAppliedPolicySnapshotCanonicalizesLimaAndProjectAliases(t *testing.T) {
	hostConfig := privateTempDir(t)
	realLima := t.TempDir()
	limaAlias := filepath.Join(t.TempDir(), "lima-link")
	if err := os.Symlink(realLima, limaAlias); err != nil {
		t.Fatal(err)
	}
	realProject := t.TempDir()
	projectAlias := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realProject, projectAlias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", hostConfig)

	t.Setenv("LIMA_HOME", realLima)
	realPath, err := appliedPolicySnapshotPath(realProject)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", limaAlias)
	aliasPath, err := appliedPolicySnapshotPath(projectAlias)
	if err != nil {
		t.Fatal(err)
	}
	if realPath != aliasPath {
		t.Fatalf("canonical aliases produced distinct paths:\nreal  %s\nalias %s", realPath, aliasPath)
	}
	if namespace := filepath.Base(filepath.Dir(realPath)); !strings.HasPrefix(namespace, "lima-") || len(namespace) != len("lima-")+sha256.Size*2 {
		t.Fatalf("snapshot namespace %q does not contain a full SHA-256 identity", namespace)
	}
	if key := strings.TrimSuffix(filepath.Base(realPath), ".json"); len(key) != sha256.Size*2 {
		t.Fatalf("snapshot key %q does not contain a full SHA-256 identity", key)
	}
}

func TestAppliedPolicySnapshotUsesEffectiveDefaultLimaHome(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", "")
	t.Setenv("HOME", t.TempDir())
	first, err := appliedPolicySnapshotPath(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	second, err := appliedPolicySnapshotPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different effective default Lima homes share snapshot %s", first)
	}
}

func TestAppliedPolicySnapshotDetectsRetargetedMount(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	firstTarget := t.TempDir()
	secondTarget := t.TempDir()
	mountLink := filepath.Join(t.TempDir(), "mount-link")
	if err := os.Symlink(firstTarget, mountLink); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	cfg.Mounts = map[string]config.Mount{
		mountLink: {Target: "/cache", Mode: "ro"},
	}
	if err := saveAppliedPolicySnapshot(project, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(mountLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secondTarget, mountLink); err != nil {
		t.Fatal(err)
	}

	assessment := assessAppliedPolicy(project, lima.StatusRunning, cfg)
	if assessment.State != policyStale {
		t.Fatalf("retargeted mount state = %v, want stale (error: %v)", assessment.State, assessment.Err)
	}
}

func TestAppliedPolicySnapshotSecuresModesAndOwnership(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	host, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(host.StateDir, 0777); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{host.AppStateRoot, filepath.Join(host.AppStateRoot, "vms"), host.StateDir} {
		if err := os.Chmod(path, 0777); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveAppliedPolicySnapshot(project, config.NewConfig()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{host.AppStateRoot, filepath.Join(host.AppStateRoot, "vms"), host.StateDir} {
		assertOwnedMode(t, path, 0700)
	}
	assertOwnedMode(t, host.SnapshotPath, 0600)
}

func TestAppliedPolicySnapshotRejectsSymlinkAndNonRegularFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(string) error
	}{
		{
			name: "symlink",
			create: func(path string) error {
				target := filepath.Join(filepath.Dir(path), "attacker.json")
				if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
					return err
				}
				return os.Symlink(target, path)
			},
		},
		{
			name: "directory",
			create: func(path string) error {
				return os.Mkdir(path, 0700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
			t.Setenv("LIMA_HOME", t.TempDir())
			host, err := resolveAppliedPolicyHostContext(project, config.NewConfig())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(host.StateDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := test.create(host.SnapshotPath); err != nil {
				t.Fatal(err)
			}
			assessment := assessAppliedPolicy(project, lima.StatusRunning, config.NewConfig())
			if assessment.State != policyUnverifiedInvalid {
				t.Fatalf("non-regular snapshot state = %v, want invalid", assessment.State)
			}
		})
	}
}

func TestAppliedPolicySnapshotRejectsInsecureRecordMode(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	if err := saveAppliedPolicySnapshot(project, config.NewConfig()); err != nil {
		t.Fatal(err)
	}
	path, err := appliedPolicySnapshotPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	assessment := assessAppliedPolicy(project, lima.StatusRunning, config.NewConfig())
	if assessment.State != policyUnverifiedInvalid || !strings.Contains(assessment.Err.Error(), "insecure mode") {
		t.Fatalf("insecure-mode assessment = state %v, error %v", assessment.State, assessment.Err)
	}
}

func assertOwnedMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		t.Errorf("%s is not owned by current uid %d", path, os.Geteuid())
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExplicitLogPolicyWarns(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementLog
	var out bytes.Buffer
	warnIfNonStrictPolicy(&out, cfg)
	if warning := out.String(); !strings.Contains(warning, "Warning:") || !strings.Contains(warning, "not strict") || !strings.Contains(warning, "allowed") {
		t.Fatalf("log warning = %q", warning)
	}

	out.Reset()
	warnIfNonStrictPolicy(&out, config.NewConfig())
	if out.Len() != 0 {
		t.Fatalf("strict fail policy unexpectedly warned: %q", out.String())
	}
}

func TestRequireVMProjectBindingRejectsCollidingProject(t *testing.T) {
	project := t.TempDir()
	collidingProject := t.TempDir()
	oldProjectSource := cliProjectMountSource
	cliProjectMountSource = func(string) (string, error) { return collidingProject, nil }
	t.Cleanup(func() { cliProjectMountSource = oldProjectSource })

	err := requireVMProjectBinding(project, lima.VMNameFromPath(project))
	if err == nil || !strings.Contains(err.Error(), "not the current project") {
		t.Fatalf("binding error = %v, want colliding-project refusal", err)
	}
}

func TestInvalidOrUnreadableConfigStopsBoundRunningVM(t *testing.T) {
	for _, test := range []struct {
		name    string
		content *string
		want    string
	}{
		{name: "missing", want: "no .watermelon.toml"},
		{name: "invalid", content: stringPointer("[security]\nenforcement = \"invalid\"\n"), want: "invalid config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			if test.content != nil {
				if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte(*test.content), 0600); err != nil {
					t.Fatal(err)
				}
			}

			oldStatus, oldProjectSource, oldStop := cliGetVMStatus, cliProjectMountSource, cliStopVM
			cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
			cliProjectMountSource = func(string) (string, error) { return project, nil }
			stopCalls := 0
			cliStopVM = func(string) error {
				stopCalls++
				return nil
			}
			t.Cleanup(func() {
				cliGetVMStatus = oldStatus
				cliProjectMountSource = oldProjectSource
				cliStopVM = oldStop
			})

			_, err := loadValidatedProjectConfigFailClosed(project)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "was stopped") {
				t.Fatalf("config error = %v, want original error plus stop notice", err)
			}
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func TestConfigErrorRemainsWrappedAfterAutomaticStop(t *testing.T) {
	project := t.TempDir()
	originalErr := errors.New("config read failed")

	oldStatus, oldProjectSource, oldStop := cliGetVMStatus, cliProjectMountSource, cliStopVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	cliStopVM = func(string) error { return nil }
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
		cliStopVM = oldStop
	})

	err := stopBoundVMForConfigError(project, originalErr)
	if !errors.Is(err, originalErr) {
		t.Fatalf("automatic-stop error = %v, want original config error preserved", err)
	}
}

func TestConfigErrorDoesNotStopCollidingVM(t *testing.T) {
	project := t.TempDir()
	collidingProject := t.TempDir()
	oldStatus, oldProjectSource, oldStop := cliGetVMStatus, cliProjectMountSource, cliStopVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return collidingProject, nil }
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
		cliStopVM = oldStop
	})

	_, err := loadValidatedProjectConfigFailClosed(project)
	if err == nil || !strings.Contains(err.Error(), "identity could not be verified") {
		t.Fatalf("config error = %v, want binding refusal", err)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", stopCalls)
	}
}

func TestStalePolicyRechecksBindingBeforeAutomaticStop(t *testing.T) {
	project := t.TempDir()
	collidingProject := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())

	oldProjectSource, oldStop := cliProjectMountSource, cliStopVM
	cliProjectMountSource = func(string) (string, error) { return collidingProject, nil }
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliProjectMountSource = oldProjectSource
		cliStopVM = oldStop
	})

	err := requireCurrentAppliedPolicyAndStopUnsafe(project, lima.VMNameFromPath(project), lima.StatusRunning, config.NewConfig())
	if err == nil || !strings.Contains(err.Error(), "could not be re-verified") {
		t.Fatalf("policy error = %v, want replacement refusal", err)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", stopCalls)
	}
}

func TestStaleAndUnverifiedPoliciesStopBoundRunningVM(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*testing.T, string) *config.Config
		want      string
	}{
		{
			name: "unverified",
			configure: func(t *testing.T, project string) *config.Config {
				return config.NewConfig()
			},
			want: "no applied-policy snapshot",
		},
		{
			name: "stale",
			configure: func(t *testing.T, project string) *config.Config {
				applied := config.NewConfig()
				if err := saveAppliedPolicySnapshot(project, applied); err != nil {
					t.Fatal(err)
				}
				configured := config.NewConfig()
				configured.Resources.CPUs++
				return configured
			},
			want: "configuration is stale",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
			t.Setenv("LIMA_HOME", t.TempDir())
			cfg := test.configure(t, project)

			oldProjectSource, oldStop := cliProjectMountSource, cliStopVM
			cliProjectMountSource = func(string) (string, error) { return project, nil }
			stopCalls := 0
			cliStopVM = func(string) error {
				stopCalls++
				return nil
			}
			t.Cleanup(func() {
				cliProjectMountSource = oldProjectSource
				cliStopVM = oldStop
			})

			err := requireCurrentAppliedPolicyAndStopUnsafe(project, lima.VMNameFromPath(project), lima.StatusRunning, cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "was stopped") {
				t.Fatalf("policy error = %v, want policy reason plus stop notice", err)
			}
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func TestRunExecAndCodeStopBoundVMWhenRuntimePolicyMarkerIsMissing(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{
			name: "run",
			run:  func() error { return runRunWithOptions(runOptions{OpenShell: false}) },
		},
		{
			name: "exec",
			run: func() error {
				cmd := NewExecCmd()
				return cmd.RunE(cmd, []string{"true"})
			},
		},
		{
			name: "code",
			run:  runCode,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
			t.Setenv("LIMA_HOME", t.TempDir())
			if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadProjectConfig(project)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveAppliedPolicySnapshot(project, cfg); err != nil {
				t.Fatal(err)
			}
			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(project); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(originalDir) })

			oldStatus, oldProjectSource, oldStop, oldVerify := cliGetVMStatus, cliProjectMountSource, cliStopVM, cliVerifyPolicy
			cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
			cliProjectMountSource = func(string) (string, error) { return project, nil }
			markerErr := errors.New("policy marker missing")
			cliVerifyPolicy = func(string) error { return markerErr }
			stopCalls := 0
			cliStopVM = func(string) error {
				stopCalls++
				return nil
			}
			t.Cleanup(func() {
				cliGetVMStatus = oldStatus
				cliProjectMountSource = oldProjectSource
				cliStopVM = oldStop
				cliVerifyPolicy = oldVerify
			})

			err = test.run()
			if !errors.Is(err, markerErr) || !strings.Contains(err.Error(), "VM was stopped") {
				t.Fatalf("command error = %v, want preserved marker failure plus stop notice", err)
			}
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func TestRuntimePolicyFailureDoesNotStopAlreadyStoppedVM(t *testing.T) {
	markerErr := errors.New("policy marker missing")
	oldStatus, oldStop, oldVerify := cliGetVMStatus, cliStopVM, cliVerifyPolicy
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusStopped }
	cliVerifyPolicy = func(string) error { return markerErr }
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStopVM = oldStop
		cliVerifyPolicy = oldVerify
	})

	err := requireRuntimePolicyAppliedAndStopUnsafe(t.TempDir(), "watermelon-test-12345678", false)
	if !errors.Is(err, markerErr) {
		t.Fatalf("runtime policy error = %v, want marker error preserved", err)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0 for already stopped VM", stopCalls)
	}
}

func TestRuntimePolicyFailurePreservesStopFailure(t *testing.T) {
	project := t.TempDir()
	markerErr := errors.New("policy marker missing")
	stopErr := errors.New("stop failed")
	oldStatus, oldProjectSource, oldStop, oldVerify := cliGetVMStatus, cliProjectMountSource, cliStopVM, cliVerifyPolicy
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	cliVerifyPolicy = func(string) error { return markerErr }
	cliStopVM = func(string) error { return stopErr }
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
		cliStopVM = oldStop
		cliVerifyPolicy = oldVerify
	})

	err := requireRuntimePolicyAppliedAndStopUnsafe(project, lima.VMNameFromPath(project), false)
	if !errors.Is(err, markerErr) || !errors.Is(err, stopErr) {
		t.Fatalf("runtime policy error = %v, want marker and stop failures preserved", err)
	}
}

func TestNewVMStartFailureStopsCreatedRunningInstanceWithoutSavingPolicy(t *testing.T) {
	project := t.TempDir()
	hostConfig := privateTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", hostConfig)
	t.Setenv("LIMA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldProjectSource, oldStart, oldStop := cliGetVMStatus, cliProjectMountSource, cliStartVM, cliStopVM
	statusCalls := 0
	cliGetVMStatus = func(string) lima.VMStatus {
		statusCalls++
		if statusCalls == 1 {
			return lima.StatusNotFound
		}
		return lima.StatusRunning
	}
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	startErr := errors.New("provisioning failed")
	cliStartVM = func(string, string) error {
		return &lima.StartError{Stage: lima.StartStageStart, Err: startErr}
	}
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
		cliStartVM = oldStart
		cliStopVM = oldStop
	})

	err = runRunWithOptions(runOptions{OpenShell: false})
	if !errors.Is(err, startErr) || !strings.Contains(err.Error(), "VM was stopped") {
		t.Fatalf("run error = %v, want preserved start failure plus stop notice", err)
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
	snapshotPath, pathErr := appliedPolicySnapshotPath(project)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(snapshotPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed creation recorded applied policy: %v", statErr)
	}
}

func TestNewVMIncompleteProvisioningStopsBeforeSavingPolicySnapshot(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldProjectSource := cliGetVMStatus, cliProjectMountSource
	oldStart, oldStop, oldVerify := cliStartVM, cliStopVM, cliVerifyPolicy
	oldGenerate, oldSave := cliGenerateConfig, cliSavePolicySnapshot
	statusCalls := 0
	cliGetVMStatus = func(string) lima.VMStatus {
		statusCalls++
		if statusCalls == 1 {
			return lima.StatusNotFound
		}
		return lima.StatusRunning
	}
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	cliStartVM = func(string, string) error { return nil }
	cliGenerateConfig = func(*config.Config, string, map[string]string, lima.GenerateOptions) (string, error) {
		return "generated", nil
	}
	completionErr := errors.New("completion marker missing")
	cliVerifyPolicy = func(string) error { return completionErr }
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	saveCalls := 0
	cliSavePolicySnapshot = func(appliedPolicyHostContext, *config.Config) error {
		saveCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
		cliStartVM = oldStart
		cliStopVM = oldStop
		cliVerifyPolicy = oldVerify
		cliGenerateConfig = oldGenerate
		cliSavePolicySnapshot = oldSave
	})

	err = runRunWithOptions(runOptions{OpenShell: false})
	if !errors.Is(err, completionErr) || !strings.Contains(err.Error(), "sandbox provisioning is not complete") || !strings.Contains(err.Error(), "VM was stopped") {
		t.Fatalf("run error = %v, want fail-closed provisioning error and stop notice", err)
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
	if saveCalls != 0 {
		t.Fatalf("snapshot save calls = %d, want 0 before provisioning attestation", saveCalls)
	}
}

func TestNewLogModeVMStopsWhenAppliedPolicySnapshotSaveFails(t *testing.T) {
	for _, test := range []struct {
		name        string
		stopErr     error
		wantStopped bool
	}{
		{name: "stopped", wantStopped: true},
		{name: "stop failure preserved", stopErr: errors.New("stop failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
			t.Setenv("LIMA_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"log\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(project); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(originalDir) })

			oldStatus := cliGetVMStatus
			oldProjectSource := cliProjectMountSource
			oldStart := cliStartVM
			oldStop := cliStopVM
			oldVerify := cliVerifyPolicy
			oldGenerate := cliGenerateConfig
			oldSave := cliSavePolicySnapshot
			cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
			cliProjectMountSource = func(string) (string, error) { return project, nil }
			cliStartVM = func(string, string) error { return nil }
			cliVerifyPolicy = func(string) error { return nil }
			cliGenerateConfig = func(*config.Config, string, map[string]string, lima.GenerateOptions) (string, error) {
				return "generated", nil
			}
			saveErr := errors.New("snapshot save failed")
			saveCalls := 0
			cliSavePolicySnapshot = func(appliedPolicyHostContext, *config.Config) error {
				saveCalls++
				return saveErr
			}
			stopCalls := 0
			cliStopVM = func(string) error {
				stopCalls++
				return test.stopErr
			}
			t.Cleanup(func() {
				cliGetVMStatus = oldStatus
				cliProjectMountSource = oldProjectSource
				cliStartVM = oldStart
				cliStopVM = oldStop
				cliVerifyPolicy = oldVerify
				cliGenerateConfig = oldGenerate
				cliSavePolicySnapshot = oldSave
			})

			err = runRunWithOptions(runOptions{OpenShell: false})
			if !errors.Is(err, saveErr) {
				t.Fatalf("run error = %v, want snapshot save failure preserved", err)
			}
			if test.stopErr != nil && !errors.Is(err, test.stopErr) {
				t.Fatalf("run error = %v, want stop failure preserved", err)
			}
			if test.wantStopped && !strings.Contains(err.Error(), "VM was stopped") {
				t.Fatalf("run error = %v, want stop notice", err)
			}
			if saveCalls != 1 {
				t.Fatalf("snapshot save calls = %d, want 1", saveCalls)
			}
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func TestCreateStageFailureDoesNotStopConcurrentWinningInstance(t *testing.T) {
	project := t.TempDir()
	createErr := errors.New("instance already exists")
	oldStatus, oldStart, oldStop := cliGetVMStatus, cliStartVM, cliStopVM
	statusCalls := 0
	cliGetVMStatus = func(string) lima.VMStatus {
		statusCalls++
		return lima.StatusRunning
	}
	cliStartVM = func(string, string) error {
		return &lima.StartError{Stage: lima.StartStageCreate, Err: createErr}
	}
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStartVM = oldStart
		cliStopVM = oldStop
	})

	err := startVMFailClosed(project, lima.VMNameFromPath(project), "/tmp/watermelon.yaml")
	if !errors.Is(err, createErr) {
		t.Fatalf("start error = %v, want create failure preserved", err)
	}
	if statusCalls != 0 {
		t.Fatalf("status calls = %d, want no cleanup inspection after create-stage failure", statusCalls)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want concurrent winner left running", stopCalls)
	}
}

func TestRunExecAndCodeStartFailureStopsExistingVM(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "run", run: func() error { return runRunWithOptions(runOptions{OpenShell: false}) }},
		{
			name: "exec",
			run: func() error {
				cmd := NewExecCmd()
				return cmd.RunE(cmd, []string{"true"})
			},
		},
		{name: "code", run: runCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
			t.Setenv("LIMA_HOME", t.TempDir())
			if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadProjectConfig(project)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveAppliedPolicySnapshot(project, cfg); err != nil {
				t.Fatal(err)
			}
			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(project); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(originalDir) })

			oldStatus, oldProjectSource, oldStart, oldStop := cliGetVMStatus, cliProjectMountSource, cliStartVM, cliStopVM
			statusCalls := 0
			cliGetVMStatus = func(string) lima.VMStatus {
				statusCalls++
				if statusCalls == 1 {
					return lima.StatusStopped
				}
				return lima.StatusRunning
			}
			cliProjectMountSource = func(string) (string, error) { return project, nil }
			startErr := errors.New("provisioning failed")
			cliStartVM = func(string, string) error {
				return &lima.StartError{Stage: lima.StartStageStart, Err: startErr}
			}
			stopCalls := 0
			cliStopVM = func(string) error {
				stopCalls++
				return nil
			}
			t.Cleanup(func() {
				cliGetVMStatus = oldStatus
				cliProjectMountSource = oldProjectSource
				cliStartVM = oldStart
				cliStopVM = oldStop
			})

			err = test.run()
			if !errors.Is(err, startErr) || !strings.Contains(err.Error(), "VM was stopped") {
				t.Fatalf("command error = %v, want preserved start failure plus stop notice", err)
			}
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
