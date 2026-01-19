package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig(t *testing.T) {
	// Create temp config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	content := `
[vm]
image = "ubuntu-22.04"

[network]
allow = ["registry.npmjs.org", "github.com"]

[tools]
"node:20-slim" = ["node", "npm"]

[ports]
forward = [3000, 5173]

[resources]
memory = "4GB"
cpus = 2

[security]
on_violation = "fail"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseFile(configPath)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if cfg.VM.Image != "ubuntu-22.04" {
		t.Errorf("expected image ubuntu-22.04, got %s", cfg.VM.Image)
	}
	if len(cfg.Network.Allow) != 2 {
		t.Errorf("expected 2 network allows, got %d", len(cfg.Network.Allow))
	}
	if len(cfg.Tools["node:20-slim"]) != 2 {
		t.Errorf("expected 2 commands for node image, got %d", len(cfg.Tools["node:20-slim"]))
	}
	if cfg.Resources.Memory != "4GB" {
		t.Errorf("expected memory 4GB, got %s", cfg.Resources.Memory)
	}
	if cfg.Security.OnViolation != "fail" {
		t.Errorf("expected on_violation fail, got %s", cfg.Security.OnViolation)
	}
}

func TestParseConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	// Minimal config - should get defaults for unspecified fields
	content := `
[network]
allow = ["example.com"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseFile(configPath)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	// Should have default values
	if cfg.Resources.Memory != "2GB" {
		t.Errorf("expected default memory 2GB, got %s", cfg.Resources.Memory)
	}
	if cfg.Security.OnViolation != "log" {
		t.Errorf("expected default on_violation log, got %s", cfg.Security.OnViolation)
	}
}

func TestParseIDEConfig(t *testing.T) {
	tomlData := `
[ide]
command = "cursor"
`
	cfg, err := Parse([]byte(tomlData))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if cfg.IDE.Command != "cursor" {
		t.Errorf("expected IDE.Command = 'cursor', got %q", cfg.IDE.Command)
	}
}

func TestParseIDEConfigDefault(t *testing.T) {
	tomlData := `
[network]
allow = []
`
	cfg, err := Parse([]byte(tomlData))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if cfg.IDE.Command != "code" {
		t.Errorf("expected IDE.Command default = 'code', got %q", cfg.IDE.Command)
	}
}
