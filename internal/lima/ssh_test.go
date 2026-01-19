package lima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetSSHHost(t *testing.T) {
	vmName := "watermelon-myproject-a1b2c3d4"
	host := GetSSHHost(vmName)
	expected := "lima-watermelon-myproject-a1b2c3d4"
	if host != expected {
		t.Errorf("GetSSHHost(%q) = %q, expected %q", vmName, host, expected)
	}
}

func TestEnsureSSHConfig(t *testing.T) {
	// Create temp home directory
	tmpHome, err := os.MkdirTemp("", "watermelon-ssh-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpHome)

	sshDir := filepath.Join(tmpHome, ".ssh")
	configPath := filepath.Join(sshDir, "config")

	t.Run("creates ssh config if not exists", func(t *testing.T) {
		os.RemoveAll(sshDir)
		err := EnsureSSHConfigAt(configPath)
		if err != nil {
			t.Fatalf("EnsureSSHConfigAt failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}
		if !strings.Contains(string(content), "Include ~/.lima/*/ssh.config") {
			t.Error("expected Include directive in new config")
		}

		// Check permissions
		info, _ := os.Stat(configPath)
		if info.Mode().Perm() != 0600 {
			t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
		}
	})

	t.Run("prepends to existing config", func(t *testing.T) {
		os.RemoveAll(sshDir)
		os.MkdirAll(sshDir, 0700)
		existing := "Host example\n  HostName example.com\n"
		os.WriteFile(configPath, []byte(existing), 0600)

		err := EnsureSSHConfigAt(configPath)
		if err != nil {
			t.Fatalf("EnsureSSHConfigAt failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}
		// Include should be at the start
		if !strings.HasPrefix(string(content), "# Added by watermelon") {
			t.Error("expected Include directive at start of config")
		}
		// Original content preserved
		if !strings.Contains(string(content), "Host example") {
			t.Error("expected original content to be preserved")
		}
	})

	t.Run("skips if already configured", func(t *testing.T) {
		os.RemoveAll(sshDir)
		os.MkdirAll(sshDir, 0700)
		existing := "Include ~/.lima/*/ssh.config\n\nHost example\n"
		os.WriteFile(configPath, []byte(existing), 0600)

		err := EnsureSSHConfigAt(configPath)
		if err != nil {
			t.Fatalf("EnsureSSHConfigAt failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}
		// Should not duplicate
		count := strings.Count(string(content), "Include ~/.lima/")
		if count != 1 {
			t.Errorf("expected 1 Include directive, found %d", count)
		}
	})
}
