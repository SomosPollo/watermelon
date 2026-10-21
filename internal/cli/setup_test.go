package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/project"
	"github.com/spf13/cobra"
)

func projectSetupFixture(t *testing.T) (string, *config.Config, *map[string][]byte, *int) {
	return profileSetupFixture(t, map[string][]byte{"package.json": []byte(`{"engines":{"node":">=22"}}`), "package-lock.json": []byte(`{"lockfileVersion":3}`)}, "24.1.0\n")
}

func profileSetupFixture(t *testing.T, files map[string][]byte, version string) (string, *config.Config, *map[string][]byte, *int) {
	t.Helper()
	dir, _, _ := setupNamedVMIdentityTest(t)
	plan, err := project.Detect(files, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	mount := false
	cfg.VM.MountProject = &mount
	cfg.VM.Workdir = "/workspace/project"
	cfg.Workspace = config.WorkspaceConfig{Disk: workspaceDiskName(dir), Source: "https://github.com/example/project"}
	configureProfile(cfg, plan)
	if err := saveWorkspaceState(dir, workspaceState{Version: 1, Owner: dir, Disk: cfg.Workspace.Disk, Source: cfg.Workspace.Source, DiskCreated: true, Cloned: true, Stage: "source cloned"}); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectConfig(dir, cfg, true); err != nil {
		t.Fatal(err)
	}
	vm := derivedVMName(dir)
	instance, err := reserveNamedVMIdentity(dir, vm, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedPolicySnapshotForVM(dir, vm, cfg); err != nil {
		t.Fatal(err)
	}
	oldStart, oldOutput, oldExecute := setupStartVM, setupGuestOutput, setupExecute
	oldStatus, oldMount, oldVerify := cliGetVMStatus, cliInstanceMount, cliVerifyPolicy
	setupStartVM = func(runOptions) error { return nil }
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliInstanceMount = func(_, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	cliVerifyPolicy = func(string) error { return nil }
	setupGuestOutput = func(_ context.Context, _, _ string, args []string, _ int64) ([]byte, error) {
		if args[0] == "/usr/bin/python3" {
			if len(args) < 2 || args[1] != "-I" {
				t.Fatal("metadata reader can import repository modules")
			}
			encoded := map[string]string{}
			for name, data := range files {
				encoded[name] = base64.StdEncoding.EncodeToString(data)
			}
			return json.Marshal(encoded)
		}
		return []byte(version), nil
	}
	calls := 0
	setupExecute = func(_ *cobra.Command, _ targetContext, args []string) error {
		if args[0] != "/usr/local/bin/watermelon-project" {
			t.Fatalf("installation bypassed runtime: %v", args)
		}
		calls++
		return nil
	}
	t.Cleanup(func() {
		setupStartVM, setupGuestOutput, setupExecute = oldStart, oldOutput, oldExecute
		cliGetVMStatus, cliInstanceMount, cliVerifyPolicy = oldStatus, oldMount, oldVerify
	})
	return dir, cfg, &files, &calls
}

func setupTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader("y\n"))
	return cmd, &out
}

func TestUpPreparesOnceAndReinstallsChangedManifests(t *testing.T) {
	dir, _, files, calls := projectSetupFixture(t)
	cmd, out := setupTestCommand()
	if err := runUp(cmd, dir, "", setupOptions{}); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("install calls=%d", *calls)
	}
	cmd.SetIn(strings.NewReader(""))
	out.Reset()
	if err := runUp(cmd, dir, "", setupOptions{}); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || !strings.Contains(out.String(), "unchanged") {
		t.Fatalf("unchanged dependencies reinstalled: %d %s", *calls, out.String())
	}
	(*files)["package.json"] = []byte(`{"version":"2.0.0"}`)
	if err := runUp(cmd, dir, "", setupOptions{}); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatal("changed manifests not installed")
	}
	if err := runUp(cmd, dir, "", setupOptions{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatal("refresh ignored")
	}
}

func TestSetupFailureRetainsRecoveryAndRetries(t *testing.T) {
	dir, _, _, _ := projectSetupFixture(t)
	cmd, _ := setupTestCommand()
	execute := setupExecute
	setupExecute = func(*cobra.Command, targetContext, []string) error { return errors.New("blocked dependency download") }
	if err := runUp(cmd, dir, "", setupOptions{}); err == nil {
		t.Fatal("setup unexpectedly succeeded")
	}
	state, err := loadWorkspaceState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReadyFingerprint != "" || state.LastError == "" || !state.Cloned || !state.DiskCreated {
		t.Fatalf("bad recovery state: %+v", state)
	}
	setupExecute = execute
	cmd.SetIn(strings.NewReader(""))
	if err := runUp(cmd, dir, "", setupOptions{}); err != nil {
		t.Fatal(err)
	}
	state, err = loadWorkspaceState(dir)
	if err != nil || state.Stage != "ready" || state.LastError != "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestUpRefusesUnapprovedSetupBeforeExecution(t *testing.T) {
	dir, _, _, calls := projectSetupFixture(t)
	cmd, _ := setupTestCommand()
	cmd.SetIn(strings.NewReader("n\n"))
	if err := runUp(cmd, dir, "", setupOptions{}); err == nil {
		t.Fatal("approval ignored")
	}
	if *calls != 0 {
		t.Fatal("project executed before approval")
	}
}

func TestUpDryRunDoesNotUseVM(t *testing.T) {
	dir, _, _, _ := projectSetupFixture(t)
	cmd, _ := setupTestCommand()
	setupStartVM = func(runOptions) error { t.Fatal("dry-run started VM"); return nil }
	setupGuestOutput = func(context.Context, string, string, []string, int64) ([]byte, error) {
		t.Fatal("dry-run read guest")
		return nil, nil
	}
	if err := runUp(cmd, dir, "", setupOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
}

func TestUpStopsStalePolicyBeforeRejectingAskPreparation(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	cfg.Security.Enforcement = config.EnforcementAsk
	if err := writeProjectConfig(dir, cfg, false); err != nil {
		t.Fatal(err)
	}
	old := cliStopVM
	t.Cleanup(func() { cliStopVM = old })
	stopped := false
	cliStopVM = func(string) error { stopped = true; return nil }
	cmd, _ := setupTestCommand()
	if err := runUp(cmd, dir, "", setupOptions{Yes: true}); err == nil {
		t.Fatal("stale policy accepted")
	}
	if !stopped {
		t.Fatal("unsupported setup mode left a stale VM running")
	}
}

func TestWorkspaceOwnershipCannotBeCopiedOrRetargeted(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	if err := requireWorkspaceOwnership(dir, derivedVMName(dir), cfg); err != nil {
		t.Fatal(err)
	}
	if err := requireWorkspaceOwnership(dir, "another-vm", cfg); err == nil {
		t.Fatal("name override accepted")
	}
	other := t.TempDir()
	if err := requireWorkspaceOwnership(other, derivedVMName(other), cfg); err == nil {
		t.Fatal("copied disk policy accepted")
	}
	path, err := workspaceStatePath(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "victim"), path); err != nil {
		t.Fatal(err)
	}
	if err := requireWorkspaceOwnership(dir, derivedVMName(dir), cfg); err == nil {
		t.Fatal("symlink ownership record accepted")
	}
}

func TestWorkspaceDiskFitsLimaExt4Label(t *testing.T) {
	name := workspaceDiskName("/example/project")
	if len("lima-"+name) > 16 {
		t.Fatalf("disk label would be truncated by ext4: %s", name)
	}
}

func TestInitDetectsProfileAndManualRemainsAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInit(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Setup.Profile != "node" || len(cfg.Tools) == 0 || len(cfg.Network.Allow) == 0 {
		t.Fatalf("unusable config: %+v", cfg)
	}
	manual := t.TempDir()
	if err := os.WriteFile(filepath.Join(manual, "package.json"), []byte(`broken json`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInitWithTemplate(manual, "manual", ""); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeCloneArgumentsFailBeforeFilesystemChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "should-not-exist")
	cmd := NewCloneCmd()
	cmd.SetArgs([]string{"https://token@github.com/org/repo", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("credential URL accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("invalid clone changed filesystem")
	}
}

func TestDestroyPreservesWorkspaceUnlessExplicitlyDeleted(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain", true: "delete"}[permanent], func(t *testing.T) {
			dir, cfg, _, _ := projectSetupFixture(t)
			oldStatus, oldStop, oldDelete, oldDisk, oldRelease := destroyGetStatus, destroyStop, destroyDelete, destroyWorkspaceDisk, releaseWorkspaceDisk
			t.Cleanup(func() {
				destroyGetStatus, destroyStop, destroyDelete, destroyWorkspaceDisk, releaseWorkspaceDisk = oldStatus, oldStop, oldDelete, oldDisk, oldRelease
			})
			destroyGetStatus = func(string) lima.VMStatus { return lima.StatusStopped }
			destroyStop = func(string) error { return nil }
			destroyDelete = func(string) error { return nil }
			releaseWorkspaceDisk = func(disk, vm string) error {
				if disk != cfg.Workspace.Disk || vm != derivedVMName(dir) {
					t.Fatal("wrong disk released")
				}
				return nil
			}
			deleted := false
			destroyWorkspaceDisk = func(disk string) error {
				if disk != cfg.Workspace.Disk {
					t.Fatal("wrong disk deleted")
				}
				deleted = true
				return nil
			}
			cmd := newDestroyCmdAt(dir)
			args := []string{"--force"}
			if permanent {
				args = append(args, "--delete-workspace")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if deleted != permanent {
				t.Fatalf("deleted=%t", deleted)
			}
			state, err := loadWorkspaceState(dir)
			if err != nil {
				t.Fatal(err)
			}
			if state.DiskCreated == permanent {
				t.Fatalf("bad disk state: %+v", state)
			}
		})
	}
}

func TestPermanentDeletionRefusesActiveSetup(t *testing.T) {
	dir, _, _, _ := projectSetupFixture(t)
	lock, err := acquireVMLifecycleLock("wm-setup-" + fullPathDigest(dir)[:24])
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	cmd := newDestroyCmdAt(dir)
	cmd.SetArgs([]string{"--force", "--delete-workspace"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "preparation is active") {
		t.Fatalf("active setup deletion=%v", err)
	}
}

func TestRetainedWorkspaceCanBeDeletedAfterVMRemoval(t *testing.T) {
	dir, _, _, _ := projectSetupFixture(t)
	oldStatus, oldDelete, oldDisk, oldRelease := destroyGetStatus, destroyDelete, destroyWorkspaceDisk, releaseWorkspaceDisk
	t.Cleanup(func() {
		destroyGetStatus, destroyDelete, destroyWorkspaceDisk, releaseWorkspaceDisk = oldStatus, oldDelete, oldDisk, oldRelease
	})
	status := lima.StatusStopped
	destroyGetStatus = func(string) lima.VMStatus { return status }
	destroyDelete = func(string) error { status = lima.StatusNotFound; return nil }
	releaseWorkspaceDisk = func(string, string) error { return nil }
	deleted := false
	destroyWorkspaceDisk = func(string) error { deleted = true; return nil }
	first := newDestroyCmdAt(dir)
	first.SetArgs([]string{"--force"})
	if err := first.Execute(); err != nil {
		t.Fatal(err)
	}
	second := newDestroyCmdAt(dir)
	second.SetIn(strings.NewReader("y\n"))
	second.SetArgs([]string{"--delete-workspace"})
	if err := second.Execute(); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("retained disk could not be removed without a VM")
	}
}

func TestPortConflictDiagnostic(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := checkForwardedPorts([]int{port}); err == nil || !strings.Contains(err.Error(), "ports.forward") {
		t.Fatalf("conflict=%v", err)
	}
}

func TestRetainedDiskGenerationNeverFormats(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	state, err := loadWorkspaceState(dir)
	if err != nil {
		t.Fatal(err)
	}
	state.DiskInitialized = true
	if err := saveWorkspaceState(dir, state); err != nil {
		t.Fatal(err)
	}
	instance, err := loadOwnedNamedVMIdentity(dir, derivedVMName(dir))
	if err != nil {
		t.Fatal(err)
	}
	yaml, err := generateConfigForInstanceWithIdentity(cfg, dir, nil, 0, &instance)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "format: false") || strings.Contains(yaml, "format: true") {
		t.Fatal("retained disk can be formatted")
	}
}

func TestSetupCancellationInterruptsCommandAndApproval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &setupRunner{ctx: ctx}
	if err := runner.Run(exec.Command("sleep", "30")); !errors.Is(err, context.Canceled) {
		t.Fatalf("command cancellation=%v", err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	cmd, _ := setupTestCommand()
	cmd.SetContext(ctx)
	cmd.SetIn(reader)
	done := make(chan error, 1)
	go func() { done <- approveSetup(cmd, false) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not cancel")
	}
}

func TestCloneUsesSameGitTransportURLForWebAndGitURLs(t *testing.T) {
	web := cloneGuestArgs("https://github.com/owner/project")
	git := cloneGuestArgs("https://github.com/owner/project.git")
	if web[len(web)-1] != "https://github.com/owner/project.git" || web[len(web)-1] != git[len(git)-1] {
		t.Fatal("transport URL was not normalized")
	}
}

func TestGoRustSetupVersionChecksAndReadiness(t *testing.T) {
	for _, tt := range []struct {
		profile, version, minimum string
		fail                      bool
	}{
		{"go", "go version go1.27.3 linux/arm64\n", "1.25.0", false},
		{"go", "go version go1.24.1 linux/arm64\n", "1.25.0", true},
		{"rust", "rustc 1.99.1 (abc123 2026-10-01)\n", "1.85", false},
		{"rust", "rustc 1.84.0 (abc123 2025-01-01)\n", "1.85", true},
	} {
		t.Run(tt.profile+tt.version, func(t *testing.T) {
			manifest, lock, body := "go.mod", "go.sum", "module example.com/app\ngo "+tt.minimum+"\n"
			if tt.profile == "rust" {
				manifest, lock, body = "Cargo.toml", "Cargo.lock", "[package]\nname='app'\nversion='0.1.0'\nrust-version='"+tt.minimum+"'\n"
			}
			dir, _, files, calls := profileSetupFixture(t, map[string][]byte{manifest: []byte(body)}, tt.version)
			cmd, _ := setupTestCommand()
			err := runUp(cmd, dir, "", setupOptions{})
			if tt.fail {
				if err == nil || !strings.Contains(err.Error(), "does not satisfy") || *calls != 0 {
					t.Fatalf("incompatible runtime executed setup: %v calls=%d", err, *calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if *calls != 1 {
				t.Fatalf("setup calls=%d", *calls)
			}
			if err := runUp(cmd, dir, "", setupOptions{}); err != nil || *calls != 1 {
				t.Fatalf("unchanged setup repeated: %v calls=%d", err, *calls)
			}
			(*files)[lock] = []byte("updated dependency lock")
			if err := runUp(cmd, dir, "", setupOptions{}); err != nil || *calls != 2 {
				t.Fatalf("lock change not installed: %v calls=%d", err, *calls)
			}
		})
	}
}

func TestGuestAndLocalDiscoveryAgreeOnNestedMetadata(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	for name, body := range map[string]string{"frontend/package.json": `{}`, "backend/requirements.txt": "idna==3.10", "backend/.python-version": "3.13", "node_modules/bad/package.json": "invalid", ".git/hidden/package.json": "invalid"} {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	readGuest := func() ([]byte, error) {
		command := exec.Command(python, append([]string{"-I", "-c", guestManifestReader}, project.ManifestNames...)...)
		command.Dir = root
		return command.CombinedOutput()
	}
	if err := os.WriteFile(filepath.Join(root, "json.py"), []byte("raise RuntimeError('repository import executed')"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := readGuest()
	if err != nil {
		t.Fatalf("reader: %v %s", err, output)
	}
	var encoded map[string]string
	if err := json.Unmarshal(output, &encoded); err != nil {
		t.Fatal(err)
	}
	local, err := project.ReadLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != len(local) {
		t.Fatalf("local/guest differ: %v %v", local, encoded)
	}
	for name, data := range local {
		if encoded[name] != base64.StdEncoding.EncodeToString(data) {
			t.Fatalf("different metadata for %s", name)
		}
	}
	if err := os.Symlink(filepath.Join(root, "frontend", "package.json"), filepath.Join(root, "backend", "package.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := readGuest(); err == nil {
		t.Fatal("guest reader followed manifest symlink")
	}
}

func TestInvalidMultiRuntimeCloneFailsBeforeCreatingDirectory(t *testing.T) {
	for _, runtime := range []string{"node=", "go=1.27;bad", "node=22,node=24"} {
		dir := filepath.Join(t.TempDir(), "new")
		command := NewCloneCmd()
		command.SetArgs([]string{"--runtime", runtime, "https://github.com/example/project", dir})
		if err := command.Execute(); err == nil {
			t.Fatal("invalid runtime accepted")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid override created settings")
		}
	}
}

func TestCombinedPlanPreservesReviewedCustomSettings(t *testing.T) {
	single, err := project.Detect(map[string][]byte{"package.json": []byte(`{}`)}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	configureProfile(cfg, single)
	cfg.Network.Allow = append(cfg.Network.Allow, "example.com:443")
	cfg.Tools["debian:bookworm"] = []string{"custom-tool"}
	cfg.Resources.Memory = "8GB"
	combined, err := project.DetectConfigured(map[string][]byte{"package.json": []byte(`{}`), "backend/requirements.txt": nil}, cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	proposed := proposeProfile(cfg, combined)
	if proposed.Setup.Profile != "multi" || proposed.Resources.Memory != "8GB" || proposed.Resources.Disk != "20GB" || len(proposed.Tools["debian:bookworm"]) != 1 {
		t.Fatalf("custom settings lost: %+v", proposed)
	}
	found := false
	for _, rule := range proposed.Network.Allow {
		found = found || rule == "example.com:443"
	}
	if !found {
		t.Fatal("custom network grant lost")
	}
	if cfg.Setup.Profile != "node" || cfg.Resources.Disk != "10GB" {
		t.Fatal("proposing a plan mutated the approved config")
	}
	if err := config.Validate(&proposed); err != nil {
		t.Fatal(err)
	}
	before, err := config.AppliedConfigDigest(&proposed)
	if err != nil {
		t.Fatal(err)
	}
	again := proposeProfile(&proposed, combined)
	after, err := config.AppliedConfigDigest(&again)
	if err != nil || before != after {
		t.Fatal("unchanged setup rewrote custom settings")
	}
}

func TestUpRuntimeChangeRequiresApprovalBeforeReplacement(t *testing.T) {
	dir, _, _, calls := projectSetupFixture(t)
	command, out := setupTestCommand()
	command.SetIn(strings.NewReader("n\n"))
	if err := runUp(command, dir, "", setupOptions{Runtime: "node=22"}); err == nil {
		t.Fatal("runtime change bypassed approval")
	}
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Setup.Runtime != "24" || *calls != 0 {
		t.Fatal("unapproved plan changed config or installed dependencies")
	}
	if !strings.Contains(out.String(), "node:22-bookworm") || !strings.Contains(out.String(), "replaces the VM") {
		t.Fatalf("replacement not reviewable: %s", out.String())
	}
}

func TestExtendedProfilesGenerateValidPolicy(t *testing.T) {
	for name, body := range map[string]string{"pom.xml": "<project/>", "App.csproj": "<Project/>", "Gemfile": "source 'https://rubygems.org'", "composer.json": "{}", "CMakeLists.txt": "project(example)", "package.json": `{"packageManager":"pnpm@10.20.0"}`, "pyproject.toml": "[tool.uv]\npackage=false"} {
		p, err := project.Detect(map[string][]byte{name: []byte(body)}, "", "")
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.NewConfig()
		configureProfile(cfg, p)
		if err := config.Validate(cfg); err != nil {
			t.Fatalf("%s invalid policy: %v", name, err)
		}
		if p.Manager != "" {
			found := false
			manager, _, _ := strings.Cut(p.Manager, "@")
			for _, command := range cfg.Tools[p.Image] {
				found = found || command == manager
			}
			if !found {
				t.Fatal("manager has no declared tool wrapper")
			}
		}
	}
}

func TestProfileRemovalDropsAutomaticNetworkGrants(t *testing.T) {
	files := map[string][]byte{"package.json": []byte(`{"packageManager":"yarn@4.10.3"}`), "requirements.txt": nil}
	plan, err := project.Detect(files, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	configureProfile(cfg, plan)
	cfg.Network.Allow = append(cfg.Network.Allow, "example.com:443")
	delete(files, "package.json")
	plan, err = project.DetectConfigured(files, cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	proposed := proposeProfile(cfg, plan)
	for _, rule := range proposed.Network.Allow {
		if strings.Contains(rule, "npmjs") || strings.Contains(rule, "yarnpkg") {
			t.Fatalf("obsolete grant retained: %s", rule)
		}
	}
	if strings.Join(proposed.Network.Allow, ",") != "pypi.org:443,files.pythonhosted.org:443,example.com:443" {
		t.Fatalf("unexpected grants: %v", proposed.Network.Allow)
	}
}

type setupApprovalReader func([]byte) (int, error)

func (r setupApprovalReader) Read(p []byte) (int, error) { return r(p) }

func TestUpRejectsSettingsChangedDuringApproval(t *testing.T) {
	dir, cfg, _, calls := projectSetupFixture(t)
	cmd, _ := setupTestCommand()
	cmd.SetIn(setupApprovalReader(func(p []byte) (int, error) {
		cfg.Security.Enforcement = config.EnforcementLog
		if err := writeProjectConfig(dir, cfg, false); err != nil {
			return 0, err
		}
		return copy(p, "yes\n"), nil
	}))
	err := runUp(cmd, dir, "", setupOptions{})
	if err == nil || !strings.Contains(err.Error(), "settings changed") || *calls != 0 {
		t.Fatalf("changed policy approved: %v calls=%d", err, *calls)
	}
	state, err := loadWorkspaceState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.ApprovedConfig != "" {
		t.Fatal("approval committed for different settings")
	}
}

func TestSetupStartRejectsSettingsChangedAfterApproval(t *testing.T) {
	dir, cfg, _, calls := projectSetupFixture(t)
	cmd, _ := setupTestCommand()
	starts := 0
	setupStartVM = func(options runOptions) error {
		starts++
		if starts == 1 {
			return nil
		} // Acquisition VM has no project execution.
		cfg.Network.Allow = append(cfg.Network.Allow, "example.com:443")
		if err := writeProjectConfig(dir, cfg, false); err != nil {
			return err
		}
		return runRunWithOptions(options)
	}
	err := runUp(cmd, dir, "", setupOptions{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "settings changed") || *calls != 0 {
		t.Fatalf("unapproved settings reached provisioning: %v calls=%d", err, *calls)
	}
}

func TestUpChangesSelectedNamedRuntimeBeforeFirstVM(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := project.Detect(map[string][]byte{"package.json": []byte(`{}`)}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	configureProfile(cfg, plan)
	if err := writeProjectConfig(dir, cfg, true); err != nil {
		t.Fatal(err)
	}
	oldStatus, oldDestroyStatus, oldStart := cliGetVMStatus, destroyGetStatus, setupStartVM
	t.Cleanup(func() { cliGetVMStatus, destroyGetStatus, setupStartVM = oldStatus, oldDestroyStatus, oldStart })
	const selected = "watermelon-selected-runtime"
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name != selected {
			t.Fatalf("setup inspected wrong VM: %s", name)
		}
		return lima.StatusNotFound
	}
	destroyGetStatus = func(name string) lima.VMStatus {
		if name != selected {
			t.Fatalf("setup destroyed wrong VM: %s", name)
		}
		return lima.StatusNotFound
	}
	stop := errors.New("stop after preparing approved configuration")
	setupStartVM = func(options runOptions) error {
		if options.ExpectedVMName != selected || options.Name != selected || options.ExpectedConfigDigest == "" {
			t.Fatalf("unbound startup: %+v", options)
		}
		return stop
	}
	cmd, _ := setupTestCommand()
	if err := runUp(cmd, dir, selected, setupOptions{Yes: true, Runtime: "22"}); !errors.Is(err, stop) {
		t.Fatalf("profile update before first VM failed: %v", err)
	}
	got, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Setup.Runtime != "22" || got.VM.Name != "" {
		t.Fatalf("runtime/override persisted incorrectly: %+v", got)
	}
}

func TestSetupPlanEscapesUntrustedCommandArguments(t *testing.T) {
	plan, err := project.Detect(map[string][]byte{"misleading\x1b[2J.csproj": []byte(`<Project/>`)}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	configureProfile(cfg, plan)
	cmd, out := setupTestCommand()
	printSetupPlan(cmd, cfg, plan)
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), `misleading\x1b[2J.csproj`) {
		t.Fatalf("approval display permits terminal injection: %q", out.String())
	}
}

func TestRebuildRefusesRetargetDuringConfirmation(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "reviewed-vm"
	if err := writeProjectConfig(dir, cfg, true); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	oldStatus := destroyGetStatus
	t.Cleanup(func() { destroyGetStatus = oldStatus })
	destroyGetStatus = func(string) lima.VMStatus {
		t.Fatal("retargeted rebuild reached destruction")
		return lima.StatusNotFound
	}
	cmd := NewRebuildCmd()
	cmd.SetIn(setupApprovalReader(func(p []byte) (int, error) {
		cfg.VM.Name = "unreviewed-vm"
		if err := writeProjectConfig(dir, cfg, false); err != nil {
			return 0, err
		}
		return copy(p, "yes\n"), nil
	}))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "settings changed") {
		t.Fatalf("rebuild accepted changed VM: %v", err)
	}
}

func TestRebuildManualWorkspaceDoesNotRunAutomaticSetup(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	cfg.Setup = config.SetupConfig{}
	if err := writeProjectConfig(dir, cfg, false); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	oldStatus, oldRelease := destroyGetStatus, releaseWorkspaceDisk
	t.Cleanup(func() { destroyGetStatus, releaseWorkspaceDisk = oldStatus, oldRelease })
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	releaseWorkspaceDisk = func(string, string) error { return nil }
	setupGuestOutput = func(context.Context, string, string, []string, int64) ([]byte, error) {
		t.Fatal("manual rebuild inspected manifests for automatic setup")
		return nil, nil
	}
	starts := 0
	setupStartVM = func(options runOptions) error {
		starts++
		if options.ExpectedConfigDigest == "" || options.ExpectedVMName != derivedVMName(dir) {
			t.Fatal("manual startup is not bound to reviewed settings")
		}
		return nil
	}
	cmd := NewRebuildCmd()
	cmd.SetArgs([]string{"--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("manual runtime starts=%d", starts)
	}
}
