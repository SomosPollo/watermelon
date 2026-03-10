package ask

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddDomainToConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	initial := `[network]
allow = ["registry.npmjs.org", "github.com"]

[security]
enforcement = "ask"

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	if err := AddDomainToConfig(configPath, "evil.com"); err != nil {
		t.Fatalf("AddDomainToConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "evil.com") {
		t.Error("expected config to contain 'evil.com'")
	}
	if !strings.Contains(content, "registry.npmjs.org") {
		t.Error("expected config to still contain 'registry.npmjs.org'")
	}
}

func TestAddDomainToConfigNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	initial := `[network]
allow = ["registry.npmjs.org"]

[security]
enforcement = "ask"

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	AddDomainToConfig(configPath, "registry.npmjs.org")

	data, _ := os.ReadFile(configPath)
	count := strings.Count(string(data), "registry.npmjs.org")
	if count != 1 {
		t.Errorf("expected 1 occurrence of domain, got %d", count)
	}
}

func TestAddDomainToConfigEmptyAllowList(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")

	initial := `[network]
allow = []

[security]
enforcement = "ask"

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	if err := AddDomainToConfig(configPath, "new-domain.com"); err != nil {
		t.Fatalf("AddDomainToConfig: %v", err)
	}

	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), "new-domain.com") {
		t.Error("expected config to contain 'new-domain.com'")
	}
}
