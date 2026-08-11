package ask

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestAddDomainToConfigPreservesTOMLBytesAndMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".watermelon.toml")
	initial := `# project comment
[network] # policy comment
allow = [
    # registry comment
    "registry.npmjs.org", # keep inline comment
    "github.com"
]

[security] # formatting must survive
enforcement   =   "ask"
`
	want := `# project comment
[network] # policy comment
allow = [
    # registry comment
    "registry.npmjs.org", # keep inline comment
    "github.com",
    "evil.com",
]

[security] # formatting must survive
enforcement   =   "ask"
`
	if err := os.WriteFile(configPath, []byte(initial), 0640); err != nil {
		t.Fatal(err)
	}

	added, err := AddDomainToConfig(configPath, "EVIL.COM")
	if err != nil {
		t.Fatalf("AddDomainToConfig: %v", err)
	}
	if !added {
		t.Fatal("AddDomainToConfig reported a no-op")
	}
	assertFileContent(t, configPath, want)
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("file mode = %04o, want 0640", got)
	}
}

func TestAddDomainToConfigSingleLineArrays(t *testing.T) {
	for _, test := range []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "empty",
			initial: "[network]\nallow = []\n",
			want:    "[network]\nallow = [\"new-domain.com\"]\n",
		},
		{
			name:    "populated",
			initial: "[network]\nallow = [\"registry.npmjs.org\", \"github.com\"] # keep\n",
			want:    "[network]\nallow = [\"registry.npmjs.org\", \"github.com\", \"new-domain.com\"] # keep\n",
		},
		{
			name:    "trailing comma",
			initial: "[network]\nallow = [\"registry.npmjs.org\", ]\n",
			want:    "[network]\nallow = [\"registry.npmjs.org\", \"new-domain.com\"]\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.initial, 0600)
			added, err := AddDomainToConfig(path, "new-domain.com")
			if err != nil || !added {
				t.Fatalf("AddDomainToConfig() = (%v, %v), want (true, nil)", added, err)
			}
			assertFileContent(t, path, test.want)
		})
	}
}

func TestAddDomainToConfigPreservesInlineAndTrailingComments(t *testing.T) {
	initial := "[network]\nallow = [\n\t\"one.example\" # comment before the inserted comma\n\t# trailing comment\n]\n"
	want := "[network]\nallow = [\n\t\"one.example\", # comment before the inserted comma\n\t# trailing comment\n\t\"two.example\",\n]\n"
	path := writeConfig(t, initial, 0600)
	added, err := AddDomainToConfig(path, "two.example")
	if err != nil || !added {
		t.Fatalf("AddDomainToConfig() = (%v, %v), want (true, nil)", added, err)
	}
	assertFileContent(t, path, want)
}

func TestAddDomainToConfigAddsMissingKeyOrSection(t *testing.T) {
	for _, test := range []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "key",
			initial: "[network] # keep\n# existing network note\n\n[security]\nenforcement = \"ask\"\n",
			want:    "[network] # keep\nallow = [\"example.com\"]\n# existing network note\n\n[security]\nenforcement = \"ask\"\n",
		},
		{
			name:    "section with final newline",
			initial: "# keep\n[security]\nenforcement = \"ask\"\n",
			want:    "# keep\n[security]\nenforcement = \"ask\"\n[network]\nallow = [\"example.com\"]\n",
		},
		{
			name:    "section without final newline",
			initial: "[security]\nenforcement = \"ask\"",
			want:    "[security]\nenforcement = \"ask\"\n[network]\nallow = [\"example.com\"]\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.initial, 0600)
			added, err := AddDomainToConfig(path, "example.com")
			if err != nil || !added {
				t.Fatalf("AddDomainToConfig() = (%v, %v), want (true, nil)", added, err)
			}
			assertFileContent(t, path, test.want)
		})
	}
}

func TestAddDomainToConfigPreservesCRLF(t *testing.T) {
	initial := "[network]\r\nallow = [\r\n    # keep\r\n]\r\n[security]\r\nenforcement = \"ask\"\r\n"
	want := "[network]\r\nallow = [\r\n    # keep\r\n    \"example.com\",\r\n]\r\n[security]\r\nenforcement = \"ask\"\r\n"
	path := writeConfig(t, initial, 0600)
	added, err := AddDomainToConfig(path, "example.com")
	if err != nil || !added {
		t.Fatalf("AddDomainToConfig() = (%v, %v), want (true, nil)", added, err)
	}
	assertFileContent(t, path, want)
}

func TestAddDomainToConfigSemanticDuplicateIsByteForByteNoOp(t *testing.T) {
	initial := "# unchanged\n[network]\nallow = [\"EXAMPLE.COM\"]\n"
	path := writeConfig(t, initial, 0644)
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := AddDomainToConfig(path, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("semantic duplicate was reported as added")
	}
	assertFileContent(t, path, initial)
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("semantic duplicate unexpectedly replaced the file")
	}
}

func TestAddDomainToConfigRejectsInvalidPromptedRuleWithoutMutation(t *testing.T) {
	original := "[network]\nallow = []\n"
	path := writeConfig(t, original, 0600)
	for _, domain := range []string{"bad domain", "*.example.com", "example.com:443", "127.0.0.1; touch /tmp/pwned"} {
		added, err := AddDomainToConfig(path, domain)
		if err == nil || added {
			t.Errorf("AddDomainToConfig(%q) = (%v, %v), want (false, error)", domain, added, err)
		}
	}
	assertFileContent(t, path, original)
}

func TestAddDomainToConfigMalformedOrUnsupportedTOMLDoesNotMutate(t *testing.T) {
	for _, initial := range []string{
		"[network\nallow = []\n",
		"[network]\nallow = [\"bad domain\"]\n",
		"[\"network\"]\nallow = []\n",
	} {
		path := writeConfig(t, initial, 0600)
		added, err := AddDomainToConfig(path, "example.com")
		if err == nil || added {
			t.Errorf("AddDomainToConfig() = (%v, %v), want (false, error)", added, err)
		}
		assertFileContent(t, path, initial)
	}
}

func TestAddDomainToConfigConcurrentWritersDoNotLoseRules(t *testing.T) {
	path := writeConfig(t, "[network]\nallow = []\n", 0644)
	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			added, err := AddDomainToConfig(path, fmt.Sprintf("host-%d.example", i))
			if err != nil {
				errs <- err
			} else if !added {
				errs <- fmt.Errorf("writer %d unexpectedly reported no-op", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("updated config does not parse: %v\n%s", err, data)
	}
	want := make([]string, writers)
	for i := range writers {
		want[i] = fmt.Sprintf("host-%d.example", i)
	}
	for _, host := range want {
		if !slices.Contains(cfg.Network.Allow, host) {
			t.Errorf("allow list is missing %q: %v", host, cfg.Network.Allow)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".lock") || strings.HasPrefix(entry.Name(), ".watermelon.toml.tmp") {
			t.Errorf("writer left runtime file %q", entry.Name())
		}
	}
}

func writeConfig(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".watermelon.toml")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("config bytes differ\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
