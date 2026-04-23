package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveVMNameFlagTakesPriority(t *testing.T) {
	dir := t.TempDir()
	// Write a config with a different vm.name
	configPath := filepath.Join(dir, ".watermelon.toml")
	if err := os.WriteFile(configPath, []byte("[vm]\nname = \"config-name\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveVMName("flag-name", dir)
	if got != "flag-name" {
		t.Errorf("resolveVMName() = %q, want %q", got, "flag-name")
	}
}

func TestResolveVMNameFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")
	if err := os.WriteFile(configPath, []byte("[vm]\nname = \"somospollo-vm\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveVMName("", dir)
	if got != "somospollo-vm" {
		t.Errorf("resolveVMName() = %q, want %q", got, "somospollo-vm")
	}
}

func TestResolveVMNameFallsBackToHash(t *testing.T) {
	dir := t.TempDir()
	// No config file
	got := resolveVMName("", dir)
	if len(got) < 11 || got[:11] != "watermelon-" {
		t.Errorf("resolveVMName() = %q, expected watermelon- prefix", got)
	}
}

func TestResolveVMNameIgnoresMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")
	if err := os.WriteFile(configPath, []byte("not valid toml !!!###"), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveVMName("", dir)
	if len(got) < 11 || got[:11] != "watermelon-" {
		t.Errorf("resolveVMName() = %q, expected watermelon- prefix for malformed config", got)
	}
}

func TestResolveVMNameFromConfigHelper(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		flagName   string
		configName string
		wantPrefix string
		wantExact  string
	}{
		{"flag wins over config", "flag-name", "config-name", "", "flag-name"},
		{"config wins over hash", "", "config-name", "", "config-name"},
		{"falls back to hash", "", "", "watermelon-", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVMNameFromConfig(tt.flagName, tt.configName, dir)
			if tt.wantExact != "" && got != tt.wantExact {
				t.Errorf("resolveVMNameFromConfig() = %q, want %q", got, tt.wantExact)
			}
			if tt.wantPrefix != "" && (len(got) < len(tt.wantPrefix) || got[:len(tt.wantPrefix)] != tt.wantPrefix) {
				t.Errorf("resolveVMNameFromConfig() = %q, expected prefix %q", got, tt.wantPrefix)
			}
		})
	}
}
