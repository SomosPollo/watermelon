package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestRequireCompatibleLimaAddsDoctorGuidance(t *testing.T) {
	old := cliRequireCompatibleLima
	cliRequireCompatibleLima = func() error { return errors.New("limactl is too old") }
	t.Cleanup(func() { cliRequireCompatibleLima = old })

	err := requireCompatibleLima()
	if err == nil {
		t.Fatal("requireCompatibleLima() succeeded")
	}
	for _, want := range []string{"environment preflight failed", "limactl is too old", "watermelon doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("requireCompatibleLima() error = %q, want %q", err, want)
		}
	}
}

func TestRequireLimaHostOS(t *testing.T) {
	if err := requireLimaHostOS(lima.InstallationInfo{HostOS: "linux"}, "linux"); err != nil {
		t.Fatalf("matching host OS failed: %v", err)
	}
	err := requireLimaHostOS(lima.InstallationInfo{HostOS: "darwin"}, "linux")
	if err == nil || !strings.Contains(err.Error(), `reports host OS "darwin"`) {
		t.Fatalf("host mismatch error = %v", err)
	}
}

func TestRequireSupportedWorkloadHostEnforcesMacOSFloorOnlyOnDarwin(t *testing.T) {
	macOSCalls := 0
	probe := func() (string, error) {
		macOSCalls++
		return "12.7.6", nil
	}
	if err := requireSupportedWorkloadHost("linux", "amd64", probe); err != nil {
		t.Fatalf("Linux workload host failed: %v", err)
	}
	if macOSCalls != 0 {
		t.Fatalf("macOS probe called %d times on Linux", macOSCalls)
	}

	err := requireSupportedWorkloadHost("darwin", "arm64", probe)
	if err == nil || !strings.Contains(err.Error(), "requires macOS 13 or newer") {
		t.Fatalf("old macOS workload host error = %v", err)
	}
	if macOSCalls != 1 {
		t.Fatalf("macOS probe calls = %d, want 1", macOSCalls)
	}

	if err := requireSupportedWorkloadHost("darwin", "amd64", func() (string, error) {
		return "15.6.1", nil
	}); err != nil {
		t.Fatalf("supported macOS workload host failed: %v", err)
	}
}

func TestCopyRunsCompatibilityCheckBeforeTransfer(t *testing.T) {
	oldCheck, oldCopy := cliRequireCompatibleLima, cliCopyVM
	checkCalls := 0
	copyCalls := 0
	cliRequireCompatibleLima = func() error {
		checkCalls++
		return errors.New("unsupported Lima")
	}
	cliCopyVM = func(string, string, bool) error {
		copyCalls++
		return nil
	}
	t.Cleanup(func() {
		cliRequireCompatibleLima = oldCheck
		cliCopyVM = oldCopy
	})

	cmd := NewCopyCmd()
	err := cmd.RunE(cmd, []string{"./file.txt", "test-vm:/tmp/"})
	if err == nil || !strings.Contains(err.Error(), "watermelon doctor") {
		t.Fatalf("copy error = %v, want compatibility guidance", err)
	}
	if checkCalls != 1 {
		t.Fatalf("compatibility checks = %d, want 1", checkCalls)
	}
	if copyCalls != 0 {
		t.Fatalf("copy calls = %d, want 0", copyCalls)
	}
}

func TestVMUseCommandsRequireCompatibleLima(t *testing.T) {
	project := t.TempDir()
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

	preflightErr := errors.New("unsupported Lima test fixture")
	old := cliRequireCompatibleLima
	cliRequireCompatibleLima = func() error { return preflightErr }
	t.Cleanup(func() { cliRequireCompatibleLima = old })

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "run", run: func() error { return runRunWithOptions(runOptions{OpenShell: false}) }},
		{name: "exec", run: func() error {
			cmd := NewExecCmd()
			return cmd.RunE(cmd, []string{"true"})
		}},
		{name: "code", run: runCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, preflightErr) || !strings.Contains(err.Error(), "watermelon doctor") {
				t.Fatalf("command error = %v, want wrapped compatibility failure with doctor guidance", err)
			}
		})
	}
}
