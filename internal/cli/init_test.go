package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCommand(t *testing.T) {
	dir := t.TempDir()

	err := runInit(dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	configPath := filepath.Join(dir, ".watermelon.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	content := string(data)
	checks := []string{
		"[vm]",
		"[network]",
		"[resources]",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("config should contain %q", check)
		}
	}
	if !strings.Contains(content, `enforcement = "fail"`) {
		t.Errorf("generated config should use strict fail enforcement:\n%s", content)
	}
	if strings.Contains(content, `enforcement = "log"`) {
		t.Errorf("generated config should not opt into non-strict log enforcement:\n%s", content)
	}
}

func TestInitCommandExisting(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	// Create existing config
	if err := os.WriteFile(configPath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	err := runInit(dir)
	if err == nil {
		t.Error("expected error when config already exists")
	}
}
