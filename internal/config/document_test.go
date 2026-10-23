package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateDocumentPreservesUnchangedBytes(t *testing.T) {
	original := []byte("# My policy\r\n[resources] # workstation\r\nmemory = '8GB' # keep room\r\ncpus=4\r\n[network]\r\nallow = [\r\n  'example.com:443', # custom API\r\n]\r\n")
	cfg, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := UpdateDocument(original, cfg)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatalf("no-op changed document: %s, %v", unchanged, err)
	}
	cfg.Resources.CPUs = 6
	updated, err := UpdateDocument(original, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(original, []byte("cpus=4"), []byte("cpus=6"), 1)
	if !bytes.Equal(updated, want) {
		t.Fatalf("unrelated bytes changed:\n%s", updated)
	}
}

func TestUpdateDocumentPreservesCommentsInChangedAndRemovedValues(t *testing.T) {
	original := []byte(`# policy
[network]
allow = [
  "old.example", # generated domain
  'api.example', # our service
] # runtime policy
[tools] # our wrappers
"node:24-bookworm" = [
  "node", # runtime
  "npm",
]
"debian:bookworm" = ['custom-tool'] # keep this
`)
	cfg, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Network.Allow = []string{"new.example", "api.example"}
	delete(cfg.Tools, "node:24-bookworm")
	cfg.Tools["node:22-bookworm"] = []string{"node", "npm"}
	updated, err := UpdateDocument(original, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"# policy", "# generated domain", "# our service", "# runtime policy", "# our wrappers", "# runtime", `"debian:bookworm" = ['custom-tool'] # keep this`} {
		if !strings.Contains(string(updated), comment) {
			t.Fatalf("lost %q:\n%s", comment, updated)
		}
	}
}

func TestUpdateDocumentHandlesTOMLLayouts(t *testing.T) {
	for name, source := range map[string]string{
		"empty":            "# new sandbox\n",
		"dotted":           "# dotted keys\nresources.memory = '8GB'\nresources.cpus = 3\n",
		"quoted":           "['resources'] # table\n'memory' = '8GB'\n'cpus' = 3\n",
		"indented tables":  "  [resources]\n  memory = '8GB'\n  [ide] # editor\n  command = 'code'\n",
		"inline":           "resources = { memory = '8GB', cpus = 3, disk = '10GB' } # inline\n",
		"multiline string": "[vm]\nworkdir = '''/home/\n[resources]\n# not a comment\n'''\n[resources]\nmemory='8GB'\ncpus=3\n",
	} {
		t.Run(name, func(t *testing.T) {
			original := []byte(source)
			cfg, err := Parse(original)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Resources.Memory = "12GB"
			cfg.Resources.CPUs = 5
			cfg.Network.Allow = []string{"api.example:443"}
			updated, err := UpdateDocument(original, cfg)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := Parse(updated)
			if err != nil || actual.Resources.Memory != "12GB" || actual.Resources.CPUs != 5 || len(actual.Network.Allow) != 1 {
				t.Fatalf("bad update: %s; %v", updated, err)
			}
		})
	}
}

func TestUpdateDocumentPreservesExampleConfigurations(t *testing.T) {
	paths, err := filepath.Glob("../../docs/examples/*/.watermelon.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("example configs unavailable: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Resources.Memory = "12GB"
			cfg.Resources.CPUs = 7
			cfg.Ports.Forward = append(cfg.Ports.Forward, 65432)
			cfg.Network.Allow = append(cfg.Network.Allow, "custom.example:443")
			updated, err := UpdateDocument(data, cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") && !bytes.Contains(updated, []byte(line)) {
					t.Fatalf("example comment lost: %q", line)
				}
			}
		})
	}
}

func TestUpdateDocumentAddsAndRemovesArrayTables(t *testing.T) {
	original := []byte(`# generated projects
[setup]
profile = 'multi'
[setup.runtimes]
node = '24'
[[setup.projects]] # frontend
directory = '.'
profile = 'node'
[ide]
command = 'code' # preferred editor
`)
	cfg, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Setup.Projects = append(cfg.Setup.Projects, SetupProject{Directory: "backend", Profile: "python"})
	cfg.Setup.Runtimes = map[string]string{"node": "22", "python": "3.13"}
	updated, err := UpdateDocument(original, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "# frontend") || !strings.Contains(string(updated), "command = 'code' # preferred editor") {
		t.Fatalf("comments lost: %s", updated)
	}
	cfg.Setup = SetupConfig{Profile: "node", Runtime: "22"}
	updated, err = UpdateDocument(updated, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "# frontend") {
		t.Fatalf("removed table's comment lost: %s", updated)
	}
}
