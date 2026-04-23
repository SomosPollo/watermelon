package cli

import (
	"os"
	"path/filepath"
	"testing"

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

	err = runRun("", "")
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

	port := readSavedPort(dir)
	if port != 0 {
		t.Errorf("readSavedPort() = %d, want 0 for non-existent file", port)
	}

	savePort(dir, 39285)
	port = readSavedPort(dir)
	if port != 39285 {
		t.Errorf("readSavedPort() = %d, want 39285", port)
	}
}
