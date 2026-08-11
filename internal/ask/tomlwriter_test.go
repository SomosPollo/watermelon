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

func TestAddDomainToConfigAtomicWrite(t *testing.T) {
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

	if err := AddDomainToConfig(configPath, "new.com"); err != nil {
		t.Fatal(err)
	}

	// Verify no temp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".watermelon.toml.tmp") {
			t.Errorf("temp file %q left behind after successful write", e.Name())
		}
	}

	// Verify content is correct
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), "new.com") {
		t.Error("expected config to contain new domain")
	}
	if !strings.Contains(string(data), "registry.npmjs.org") {
		t.Error("expected config to still contain original domain")
	}
}

func TestAddDomainToConfigRejectsInvalidPromptedRule(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")
	original := []byte("[network]\nallow = []\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"bad domain", "*.example.com", "example.com:443", "127.0.0.1; touch /tmp/pwned"} {
		if err := AddDomainToConfig(configPath, domain); err == nil {
			t.Errorf("AddDomainToConfig(%q) unexpectedly succeeded", domain)
		}
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid prompted rule changed config: %q", got)
	}
}
