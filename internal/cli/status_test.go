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
	if err := runStatus(&out, dir); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(out.String(), "Config:   unreadable") {
		t.Errorf("status output should mention unreadable config:\n%s", out.String())
	}
}
