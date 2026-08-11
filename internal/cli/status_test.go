package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

func useNoVMStatus(t *testing.T) {
	t.Helper()
	oldStatus := cliGetVMStatus
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	t.Cleanup(func() { cliGetVMStatus = oldStatus })
}

func TestStatusCommand(t *testing.T) {
	useNoVMStatus(t)
	dir := t.TempDir()

	var out bytes.Buffer
	if err := runStatus(&out, dir); err != nil {
		t.Errorf("status command error = %v", err)
	}
	if !strings.Contains(out.String(), "Config:   missing") {
		t.Errorf("status output should mention missing config:\n%s", out.String())
	}
}

func TestStatusShowsConfigSummary(t *testing.T) {
	useNoVMStatus(t)
	dir := t.TempDir()
	config := `[vm]
image = "ubuntu-22.04"

[network]
allow = ["registry.npmjs.org"]

[tools]
"node:20-slim" = ["npm", "node"]

[ports]
forward = [5173, 3000]

[resources]
memory = "4GB"
cpus = 2
disk = "15GB"
`
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "logs.log"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(&out, dir); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	rendered := out.String()
	for _, want := range []string{
		"Config:   valid (not yet applied)",
		"Configured Policy: fail (blocks and logs connections outside the allowlist)",
		"Applied Policy:    none (VM not created)",
		"Network:  1 allow rule, 0 process rules",
		"Tools:    node:20-slim [node, npm]",
		"Ports:    3000, 5173",
		"Resources: 4GB memory, 2 CPUs, 15GB disk",
		"Logs:     2 entries",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("status output missing %q:\n%s", want, rendered)
		}
	}
}

func TestStatusReportsStaleNamedIdentityLogAndCleanupRemedy(t *testing.T) {
	useNoVMStatus(t)
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "stale-status-vm"
	cfg.VM.MountProject = boolPointerCLI(false)
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	configText := "[vm]\nname = \"" + cfg.VM.Name + "\"\nmount_project = false\n"
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Paths.GuestNetworkLogPath, []byte("blocked\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatusForName(&out, project, cfg.VM.Name); err != nil {
		t.Fatalf("status stale named identity: %v", err)
	}
	rendered := out.String()
	for _, want := range []string{
		"Status:   Not found",
		"Logs:     1 entry",
		"Next:     watermelon destroy --name stale-status-vm --force && watermelon run --name stale-status-vm",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("status output missing %q:\n%s", want, rendered)
		}
	}
}

func TestStatusRecoveryKeepsExplicitPathDerivedSelection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[vm]\nname = \"configured-other\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	derived := lima.VMNameFromPath(dir)

	oldStatus, oldProjectSource := cliGetVMStatus, cliProjectMountSource
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name != derived {
			t.Fatalf("status checked for VM %q, want explicitly selected %q", name, derived)
		}
		return lima.StatusStopped
	}
	cliProjectMountSource = func(name string) (string, error) {
		if name != derived {
			t.Fatalf("project mount checked for VM %q, want %q", name, derived)
		}
		return dir, nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectSource
	})

	var out bytes.Buffer
	if err := runStatusForName(&out, dir, derived); err != nil {
		t.Fatalf("runStatusForName() error = %v", err)
	}
	want := "Next:     watermelon destroy --name " + derived + " --force && watermelon run --name " + derived
	if !strings.Contains(out.String(), want) {
		t.Fatalf("status output missing safe explicit-selection remedy %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "Next:     "+recreatePolicyCommand) {
		t.Fatalf("status output contains unsafe implicit remedy:\n%s", out.String())
	}
}

func TestFormatAppliedPolicyDistinguishesCurrentStaleAndLegacy(t *testing.T) {
	current := formatAppliedPolicy(appliedPolicyAssessment{
		State: policyCurrent,
		Snapshot: config.AppliedPolicySnapshot{
			Enforcement: config.EnforcementFail,
		},
	})
	if !strings.Contains(current, "fail") || !strings.Contains(current, "recorded, current") || strings.Contains(current, "verified") {
		t.Errorf("current applied policy = %q", current)
	}

	stale := formatAppliedPolicy(appliedPolicyAssessment{
		State: policyStale,
		Snapshot: config.AppliedPolicySnapshot{
			Enforcement: config.EnforcementLog,
		},
	})
	if !strings.Contains(stale, "log") || !strings.Contains(stale, "recorded, stale") || !strings.Contains(stale, "not strict") {
		t.Errorf("stale applied policy = %q", stale)
	}

	legacy := formatAppliedPolicy(appliedPolicyAssessment{State: policyUnverifiedLegacy})
	if !strings.Contains(legacy, "unverified") || !strings.Contains(legacy, "does not record enforcement") {
		t.Errorf("legacy applied policy = %q", legacy)
	}

	unavailable := formatAppliedPolicy(appliedPolicyAssessment{
		State: policyComparisonUnavailable,
		Snapshot: config.AppliedPolicySnapshot{
			Enforcement: config.EnforcementFail,
		},
	})
	if !strings.Contains(unavailable, "recorded") || strings.Contains(unavailable, "verified") {
		t.Errorf("comparison-unavailable applied policy = %q", unavailable)
	}
}

func TestStatusReportsUnreadableConfig(t *testing.T) {
	useNoVMStatus(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("not = [valid"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runStatus(&out, dir)
	if err == nil || !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("runStatus() error = %v, want fail-closed parse error", err)
	}
	if out.Len() != 0 {
		t.Errorf("status must not report a path-derived VM after config failure:\n%s", out.String())
	}
}
