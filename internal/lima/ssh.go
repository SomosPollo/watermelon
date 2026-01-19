package lima

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sshConfigHeader = "# Added by watermelon\nInclude ~/.lima/*/ssh.config\n\n"

// GetSSHHost returns the SSH hostname for a VM
func GetSSHHost(vmName string) string {
	return "lima-" + vmName
}

// EnsureSSHConfig adds the Include directive to ~/.ssh/config if not present
func EnsureSSHConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}
	configPath := filepath.Join(home, ".ssh", "config")
	return EnsureSSHConfigAt(configPath)
}

// EnsureSSHConfigAt adds the Include directive to the specified ssh config path
func EnsureSSHConfigAt(configPath string) error {
	sshDir := filepath.Dir(configPath)

	// Ensure .ssh directory exists
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("creating .ssh directory: %w", err)
	}

	// Read existing config if it exists
	var existingContent string
	if data, err := os.ReadFile(configPath); err == nil {
		existingContent = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading ssh config: %w", err)
	}

	// Check if already configured
	if strings.Contains(existingContent, "Include ~/.lima/") {
		return nil // Already configured
	}

	// Prepend our Include directive
	newContent := sshConfigHeader + existingContent

	// Write with secure permissions
	if err := os.WriteFile(configPath, []byte(newContent), 0600); err != nil {
		return fmt.Errorf("writing ssh config: %w", err)
	}

	return nil
}
