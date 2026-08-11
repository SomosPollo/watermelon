package config

import (
	"os"
	"path/filepath"
	"strings"
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
enforcement = "fail"
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
	if cfg.Security.Enforcement != "fail" {
		t.Errorf("expected enforcement fail, got %s", cfg.Security.Enforcement)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "top level", data: "mystery = true\n", want: "mystery"},
		{name: "unknown table field", data: "[resources]\ncpu = 4\n", want: "resources.cpu"},
		{name: "mount project typo", data: "[vm]\nmount_projet = false\n", want: "vm.mount_projet"},
		{name: "nested mount field", data: "[mounts]\n\"./cache\" = { target = \"/mnt/watermelon/cache\", modd = \"rw\" }\n", want: `mounts."./cache".modd`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want unknown key containing %q", err, test.want)
			}
		})
	}
}

func TestParseStrictKeysAllowsDynamicMaps(t *testing.T) {
	cfg, err := Parse([]byte(`
[network.process]
node = ["registry.npmjs.org"]

[tools]
"node:20-slim" = ["node", "npm"]

[mounts]
"./cache" = { target = "/mnt/watermelon/cache", mode = "rw" }
`))
	if err != nil {
		t.Fatalf("Parse() rejected dynamic map keys: %v", err)
	}
	if len(cfg.Network.Process["node"]) != 1 || len(cfg.Tools["node:20-slim"]) != 2 || cfg.Mounts["./cache"].Mode != "rw" {
		t.Fatalf("dynamic maps were not decoded: process=%#v tools=%#v mounts=%#v", cfg.Network.Process, cfg.Tools, cfg.Mounts)
	}
}

func TestDocumentedExampleConfigsUseKnownValidKeys(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*", ".watermelon.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no documented example configs found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse(%s): %v", path, err)
			}
			if err := Validate(cfg); err != nil {
				t.Fatalf("Validate(%s): %v", path, err)
			}
		})
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
	if cfg.Security.Enforcement != EnforcementFail {
		t.Errorf("expected default enforcement fail, got %s", cfg.Security.Enforcement)
	}
}

func TestParseConfigRetainsExplicitLogEnforcement(t *testing.T) {
	cfg, err := Parse([]byte("[security]\nenforcement = \"log\"\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Security.Enforcement != EnforcementLog {
		t.Fatalf("explicit log enforcement = %q, want %q", cfg.Security.Enforcement, EnforcementLog)
	}
}

func TestParseIDEConfig(t *testing.T) {
	tomlData := `
[ide]
command = "cursor"
workdir = "/workspace/ide"
`
	cfg, err := Parse([]byte(tomlData))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if cfg.IDE.Command != "cursor" {
		t.Errorf("expected IDE.Command = 'cursor', got %q", cfg.IDE.Command)
	}
	if cfg.IDE.Workdir != "/workspace/ide" {
		t.Errorf("expected IDE.Workdir = '/workspace/ide', got %q", cfg.IDE.Workdir)
	}
}

func TestParseVMEnhancements(t *testing.T) {
	tomlData := `
[vm]
name = "shared.dev-1"
image = "ubuntu-24.04"
mount_project = false
workdir = "/workspace/app"

[provision]
scripts = ["./scripts/bootstrap.sh", "./vm/setup.sh"]
`
	cfg, err := Parse([]byte(tomlData))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.VM.Name != "shared.dev-1" {
		t.Errorf("VM.Name = %q", cfg.VM.Name)
	}
	if cfg.VM.Image != "ubuntu-24.04" {
		t.Errorf("VM.Image = %q", cfg.VM.Image)
	}
	if MountProjectEnabled(&cfg.VM) {
		t.Error("MountProjectEnabled() = true, want false")
	}
	if cfg.VM.Workdir != "/workspace/app" {
		t.Errorf("VM.Workdir = %q", cfg.VM.Workdir)
	}
	if len(cfg.Provision.Scripts) != 2 || cfg.Provision.Scripts[0] != "./scripts/bootstrap.sh" || cfg.Provision.Scripts[1] != "./vm/setup.sh" {
		t.Errorf("Provision.Scripts = %#v", cfg.Provision.Scripts)
	}
}

func TestParseMountProjectDefaultsToEnabled(t *testing.T) {
	cfg, err := Parse([]byte("[vm]\nname = \"dev\"\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !MountProjectEnabled(&cfg.VM) {
		t.Error("MountProjectEnabled() = false, want true")
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

func TestParseNetworkProcess(t *testing.T) {
	tomlData := `
[network]
allow = ["registry.npmjs.org"]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex = ["api.openai.com"]
`
	cfg, err := Parse([]byte(tomlData))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(cfg.Network.Process) != 2 {
		t.Errorf("expected 2 process entries, got %d", len(cfg.Network.Process))
	}

	claudeDomains := cfg.Network.Process["claude"]
	if len(claudeDomains) != 2 {
		t.Errorf("expected 2 domains for claude, got %d", len(claudeDomains))
	}
	if claudeDomains[0] != "api.anthropic.com" {
		t.Errorf("expected first claude domain to be 'api.anthropic.com', got %q", claudeDomains[0])
	}

	codexDomains := cfg.Network.Process["codex"]
	if len(codexDomains) != 1 {
		t.Errorf("expected 1 domain for codex, got %d", len(codexDomains))
	}
}

func TestParseConfigWithProvision(t *testing.T) {
	tomlContent := `
[vm]
image = "ubuntu-22.04"

[provision]
npm = ["@anthropic-ai/claude-code", "typescript"]
pip = ["aider-chat"]
cargo = ["ripgrep"]
go = ["github.com/junegunn/fzf@latest"]
gem = ["rails"]

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
`
	cfg, err := Parse([]byte(tomlContent))
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if len(cfg.Provision.Npm) != 2 {
		t.Errorf("expected 2 npm packages, got %d", len(cfg.Provision.Npm))
	}
	if cfg.Provision.Npm[0] != "@anthropic-ai/claude-code" {
		t.Errorf("expected first npm package '@anthropic-ai/claude-code', got %q", cfg.Provision.Npm[0])
	}
	if len(cfg.Provision.Pip) != 1 || cfg.Provision.Pip[0] != "aider-chat" {
		t.Errorf("expected pip package 'aider-chat', got %v", cfg.Provision.Pip)
	}
	if len(cfg.Provision.Cargo) != 1 || cfg.Provision.Cargo[0] != "ripgrep" {
		t.Errorf("expected cargo package 'ripgrep', got %v", cfg.Provision.Cargo)
	}
	if len(cfg.Provision.Go) != 1 || cfg.Provision.Go[0] != "github.com/junegunn/fzf@latest" {
		t.Errorf("expected go package, got %v", cfg.Provision.Go)
	}
	if len(cfg.Provision.Gem) != 1 || cfg.Provision.Gem[0] != "rails" {
		t.Errorf("expected gem package 'rails', got %v", cfg.Provision.Gem)
	}
}
