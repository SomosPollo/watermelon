package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestRegisteredNoMountVMBindingUsesReadOnlyIdentityMount(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.MountProject = boolPointerCLI(false)
	cfg.VM.Name = "fixed-no-mount"
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}

	oldMount := cliInstanceMount
	oldProjectMount := cliProjectMountSource
	cliProjectMountSource = func(string) (string, error) {
		t.Fatal("no-mount identity must not require /project")
		return "", nil
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		if name != cfg.VM.Name || target != "/mnt/watermelon/bootstrap" {
			t.Fatalf("mount lookup = %q %q", name, target)
		}
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	t.Cleanup(func() {
		cliInstanceMount = oldMount
		cliProjectMountSource = oldProjectMount
	})

	if err := requireVMProjectBinding(project, cfg.VM.Name); err != nil {
		t.Fatalf("valid identity binding rejected: %v", err)
	}

	otherProject := t.TempDir()
	if err := requireVMProjectBinding(otherProject, cfg.VM.Name); !errors.Is(err, errNamedVMOwnerMismatch) {
		t.Fatalf("other-project binding error = %v, want owner mismatch", err)
	}

	cliInstanceMount = func(_, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target, Writable: true}, nil
	}
	if err := requireVMProjectBinding(project, cfg.VM.Name); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("writable bootstrap error = %v", err)
	}

	cliInstanceMount = func(_, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: t.TempDir(), MountPoint: target}, nil
	}
	if err := requireVMProjectBinding(project, cfg.VM.Name); err == nil || !strings.Contains(err.Error(), "not the registered bootstrap") {
		t.Fatalf("retargeted bootstrap error = %v", err)
	}
}

func TestRunRejectsFixedNameReservedByAnotherProjectBeforeLifecycleCalls(t *testing.T) {
	owner, _, _ := setupNamedVMIdentityTest(t)
	const vmName = "fixed-collision"
	ownerCfg := config.NewConfig()
	ownerCfg.VM.Name = vmName
	if _, err := reserveNamedVMIdentity(owner, vmName, ownerCfg); err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, ".watermelon.toml"), []byte("[vm]\nname = \""+vmName+"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldStart := cliGetVMStatus, cliStartVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	startCalls := 0
	cliStartVM = func(string, string) error {
		startCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStartVM = oldStart
	})

	err = runRunWithOptions(runOptions{OpenShell: false})
	if !errors.Is(err, errNamedVMIdentityExists) {
		t.Fatalf("collision error = %v, want identity-exists refusal", err)
	}
	if startCalls != 0 {
		t.Fatalf("lifecycle calls = %d, want zero", startCalls)
	}
}

func TestRunCreatesRegisteredNoMountVMWithoutProjectDependency(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	const vmName = "fixed-no-mount-create"
	configData := `[vm]
name = "` + vmName + `"
mount_project = false

[tools]
"alpine:3.20" = ["sh"]

[security]
enforcement = "ask"
`
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(t.TempDir(), "watermelon-nfqd")
	if err := os.WriteFile(sidecar, []byte("test-sidecar"), 0700); err != nil {
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

	oldStatus, oldStart, oldShell := cliGetVMStatus, cliStartVM, cliShellVM
	oldVerify, oldMount := cliVerifyPolicy, cliInstanceMount
	oldProjectMount := cliProjectMountSource
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	cliVerifyPolicy = func(string) error { return nil }
	cliProjectMountSource = func(string) (string, error) {
		t.Fatal("no-mount VM unexpectedly required a /project binding")
		return "", nil
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		instance, err := loadOwnedNamedVMIdentity(project, name)
		if err != nil {
			return lima.LimaMount{}, err
		}
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	var generated string
	cliStartVM = func(name, configPath string) error {
		if name != vmName || configPath == "" {
			t.Fatalf("start target = %q with config %q", name, configPath)
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		generated = string(data)
		return nil
	}
	cliShellVM = func(name string, workdir ...string) error {
		if name != vmName {
			t.Fatalf("shell target = %q, want %q", name, vmName)
		}
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStartVM = oldStart
		cliVerifyPolicy = oldVerify
		cliInstanceMount = oldMount
		cliProjectMountSource = oldProjectMount
		cliShellVM = oldShell
	})

	if err := runRunWithOptions(runOptions{OpenShell: true}); err != nil {
		t.Fatalf("run no-mount creation error = %v", err)
	}
	if strings.Contains(generated, "/project") {
		t.Fatalf("generated no-mount VM retains /project dependency:\n%s", generated)
	}
	for _, want := range []string{
		"mountPoint: /mnt/watermelon/bootstrap",
		"writable: false",
		"mountPoint: /mnt/watermelon/state",
		`-v "$_WM_WORKDIR:$_WM_WORKDIR"`,
		"/mnt/watermelon/bootstrap/watermelon-nfqd",
		"watermelon-nfqd.service",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated no-mount VM missing %q", want)
		}
	}
	instance, err := loadOwnedNamedVMIdentity(project, vmName)
	if err != nil {
		t.Fatalf("created VM identity unavailable: %v", err)
	}
	if instance.Identity.MountProject {
		t.Fatal("created identity incorrectly records a project mount")
	}
	if _, err := os.Stat(instance.Paths.GuestStateDir); err != nil {
		t.Fatalf("guest log state directory unavailable: %v", err)
	}
}

func TestRunNoShellRejectsAskWithoutBackgroundVerdictServer(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"ask\"\n"), 0600); err != nil {
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
	statusCalls := 0
	cliGetVMStatus = func(string) lima.VMStatus {
		statusCalls++
		return lima.StatusNotFound
	}
	t.Cleanup(func() { cliGetVMStatus = oldStatus })

	err = runRunWithOptions(runOptions{OpenShell: false})
	if err == nil || !strings.Contains(err.Error(), "foreground verdict server") {
		t.Fatalf("headless ask error = %v", err)
	}
	if statusCalls != 1 {
		t.Fatalf("status calls = %d, want 1 so fail-closed policy checks run before the headless ask rejection", statusCalls)
	}
}

func TestRunNoShellAskStopsRunningVMWithStalePolicyBeforeRejecting(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"ask\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	applied := config.NewConfig()
	applied.Security.Enforcement = config.EnforcementLog
	if err := saveAppliedPolicySnapshot(project, applied); err != nil {
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

	oldStatus, oldProjectMount, oldStop := cliGetVMStatus, cliProjectMountSource, cliStopVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliStopVM = oldStop
	})

	err = runRunWithOptions(runOptions{OpenShell: false})
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "was stopped") {
		t.Fatalf("headless stale-policy error = %v, want stale-policy stop before ask-mode UX rejection", err)
	}
	if strings.Contains(err.Error(), "foreground verdict server") {
		t.Fatalf("headless stale-policy error = %v, ask-mode UX guard ran before fail-closed policy handling", err)
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
}

func TestDestroyDoesNotFallBackAfterMalformedConfigWithName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[vm\n"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldDelete := destroyGetStatus, destroyDelete
	destroyGetStatus = func(string) lima.VMStatus {
		t.Fatal("destroy must not inspect a fallback VM after a config error")
		return lima.StatusUnknown
	}
	destroyDelete = func(string) error {
		t.Fatal("destroy must not delete after a config error")
		return nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("name", "explicit-vm"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	err = cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("destroy error = %v, want original config parse failure", err)
	}
}

func TestDestroyExplicitNameRecoversOwnedVMAfterMalformedConfig(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "recover-owned-vm"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[vm\n"), 0600); err != nil {
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

	oldStatus, oldStop, oldDelete, oldMount := destroyGetStatus, destroyStop, destroyDelete, cliInstanceMount
	destroyGetStatus = func(name string) lima.VMStatus {
		if name != cfg.VM.Name {
			t.Fatalf("status queried for %q, want %q", name, cfg.VM.Name)
		}
		return lima.StatusRunning
	}
	destroyStop = func(name string) error {
		if name != cfg.VM.Name {
			t.Fatalf("stopped %q, want %q", name, cfg.VM.Name)
		}
		return nil
	}
	deleteCalls := 0
	destroyDelete = func(name string) error {
		deleteCalls++
		if name != cfg.VM.Name {
			t.Fatalf("deleted %q, want %q", name, cfg.VM.Name)
		}
		return nil
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyStop = oldStop
		destroyDelete = oldDelete
		cliInstanceMount = oldMount
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("name", cfg.VM.Name); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("destroy recovery failed: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
	if _, err := loadNamedVMIdentity(cfg.VM.Name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity remains after recovery destroy: %v", err)
	}
}

func TestDestroyRemovesStaleOwnedIdentityWhenLimaVMIsMissing(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "stale-owned-vm"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	configText := "[vm]\nname = \"" + cfg.VM.Name + "\"\nmount_project = false\n"
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte(configText), 0600); err != nil {
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

	oldStatus, oldDelete := destroyGetStatus, destroyDelete
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	destroyDelete = func(string) error {
		t.Fatal("stale identity cleanup must not ask Lima to delete a missing VM")
		return nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("stale identity cleanup failed: %v", err)
	}
	if _, err := os.Lstat(instance.Paths.InstanceDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale identity state remains: %v", err)
	}
}

func TestStopCommandStopsOwnedVMAfterMalformedConfig(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "stop-on-config-error"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[vm\n"), 0600); err != nil {
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

	oldStatus, oldStop, oldMount := cliGetVMStatus, cliStopVM, cliInstanceMount
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name == cfg.VM.Name {
			return lima.StatusRunning
		}
		return lima.StatusNotFound
	}
	var stopped []string
	cliStopVM = func(name string) error {
		stopped = append(stopped, name)
		return nil
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStopVM = oldStop
		cliInstanceMount = oldMount
	})

	cmd := NewStopCmd()
	err = cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "parsing config") || !strings.Contains(err.Error(), "was stopped") {
		t.Fatalf("stop error = %v, want config error plus fail-closed stop notice", err)
	}
	if len(stopped) != 1 || stopped[0] != cfg.VM.Name {
		t.Fatalf("stopped VMs = %v, want %q", stopped, cfg.VM.Name)
	}
}

func TestMalformedConfigStopsRegisteredOwnedVMWithoutNameHint(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "owned-on-config-error"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[vm\n"), 0600); err != nil {
		t.Fatal(err)
	}

	oldStatus, oldStop, oldMount := cliGetVMStatus, cliStopVM, cliInstanceMount
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name == cfg.VM.Name {
			return lima.StatusRunning
		}
		return lima.StatusNotFound
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	var stopped []string
	cliStopVM = func(name string) error {
		stopped = append(stopped, name)
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStopVM = oldStop
		cliInstanceMount = oldMount
	})

	_, err = loadValidatedProjectConfigFailClosed(project)
	if err == nil || !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("config error = %v", err)
	}
	if len(stopped) != 1 || stopped[0] != cfg.VM.Name {
		t.Fatalf("stopped VMs = %v, want %q", stopped, cfg.VM.Name)
	}
}

func TestMalformedConfigStopsValidOwnedVMDespiteCorruptRegistryEntry(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "owned-beside-corrupt-entry"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}

	corruptKey := strings.Repeat("f", sha256HexLength)
	if corruptKey == filepath.Base(instance.Paths.InstanceDir) {
		corruptKey = strings.Repeat("e", sha256HexLength)
	}
	corruptDir := filepath.Join(instance.Paths.InstancesDir, corruptKey)
	if err := os.Mkdir(corruptDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "identity.json"), []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[vm\n"), 0600); err != nil {
		t.Fatal(err)
	}

	oldStatus, oldStop, oldMount := cliGetVMStatus, cliStopVM, cliInstanceMount
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name == cfg.VM.Name {
			return lima.StatusRunning
		}
		return lima.StatusNotFound
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		if name != cfg.VM.Name {
			t.Fatalf("identity mount checked for unexpected VM %q", name)
		}
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	var stopped []string
	cliStopVM = func(name string) error {
		stopped = append(stopped, name)
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliStopVM = oldStop
		cliInstanceMount = oldMount
	})

	_, err = loadValidatedProjectConfigFailClosed(project)
	if err == nil || !strings.Contains(err.Error(), "parsing config") ||
		!strings.Contains(err.Error(), "was stopped") ||
		!strings.Contains(err.Error(), "could not be completely enumerated") ||
		!strings.Contains(err.Error(), "parsing named VM identity registry entry") {
		t.Fatalf("config error = %v, want parse error, stop notice, and corrupt-entry diagnostic", err)
	}
	if len(stopped) != 1 || stopped[0] != cfg.VM.Name {
		t.Fatalf("stopped VMs = %v, want valid owned VM %q", stopped, cfg.VM.Name)
	}
}

func TestListIncludesRegisteredCustomNamesAndExcludesUnmanagedOnes(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "custom-visible"
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}

	oldList, oldMount, oldStatus := cliListAllVMs, cliInstanceMount, cliGetVMStatus
	cliListAllVMs = func() ([]lima.VMInfo, error) {
		return []lima.VMInfo{
			{Name: cfg.VM.Name, Status: "Running"},
			{Name: "unmanaged-custom", Status: "Running"},
			{Name: "watermelon-legacy-12345678", Status: "Stopped", ProjectDir: "/legacy"},
		}, nil
	}
	cliInstanceMount = func(name, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name == cfg.VM.Name {
			return lima.StatusRunning
		}
		return lima.StatusNotFound
	}
	t.Cleanup(func() {
		cliListAllVMs = oldList
		cliInstanceMount = oldMount
		cliGetVMStatus = oldStatus
	})

	vms, err := listOwnedWatermelonVMs()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 {
		t.Fatalf("listed VMs = %#v, want registered custom plus legacy", vms)
	}
	if vms[0].Name != cfg.VM.Name || vms[0].ProjectDir != instance.Identity.OwnerProject {
		t.Fatalf("custom VM listing = %#v", vms[0])
	}
	if vms[1].Name != "watermelon-legacy-12345678" {
		t.Fatalf("legacy VM listing = %#v", vms[1])
	}
}

func TestStatusExplicitNameUsesEffectiveNamedPolicySnapshot(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const vmName = "explicit-status-vm"
	cfg := config.NewConfig()
	cfg.VM.Name = vmName
	instance, err := reserveNamedVMIdentity(project, vmName, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedPolicySnapshotForVM(project, vmName, cfg); err != nil {
		t.Fatal(err)
	}

	oldStatus, oldMount, oldProjectMount := cliGetVMStatus, cliInstanceMount, cliProjectMountSource
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliInstanceMount = func(_, target string) (lima.LimaMount, error) {
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, nil
	}
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliInstanceMount = oldMount
		cliProjectMountSource = oldProjectMount
	})

	var out bytes.Buffer
	if err := runStatusForName(&out, project, vmName); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Config:   current") {
		t.Fatalf("explicit named status did not match its snapshot:\n%s", out.String())
	}
}

func boolPointerCLI(value bool) *bool { return &value }
