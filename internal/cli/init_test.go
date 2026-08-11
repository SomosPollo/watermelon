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

func TestInitCommandCreatesNestedProjectEvenWithAncestorConfig(t *testing.T) {
	parent := t.TempDir()
	parentConfig := filepath.Join(parent, projectConfigName)
	if err := os.WriteFile(parentConfig, []byte("parent config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "nested-project")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}

	if err := runInit(child); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(child, projectConfigName)); err != nil {
		t.Fatalf("nested config was not created: %v", err)
	}
	data, err := os.ReadFile(parentConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "parent config\n" {
		t.Fatalf("ancestor config changed: %q", data)
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
