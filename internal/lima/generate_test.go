package lima

import (
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestValidateDomain(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{"valid domain", "github.com", false},
		{"valid subdomain", "registry.npmjs.org", false},
		{"valid with port", "example.com:443", false},
		{"valid IP", "192.168.1.1", false},
		{"empty domain", "", true},
		{"semicolon injection", "github.com; rm -rf /", true},
		{"pipe injection", "github.com | cat /etc/passwd", true},
		{"ampersand injection", "github.com && malicious", true},
		{"dollar injection", "github.com$HOME", true},
		{"backtick injection", "github.com`whoami`", true},
		{"backslash injection", "github.com\\test", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDomain(tt.domain)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDomain(%q) error = %v, wantErr %v", tt.domain, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid port 80", 80, false},
		{"valid port 443", 443, false},
		{"valid port 3000", 3000, false},
		{"valid port 1", 1, false},
		{"valid port 65535", 65535, false},
		{"invalid port 0", 0, true},
		{"invalid port negative", -1, true},
		{"invalid port too high", 65536, true},
		{"invalid port very high", 100000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestGenerateConfigValidation(t *testing.T) {
	t.Run("rejects invalid domain", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Network.Allow = []string{"github.com", "evil.com; rm -rf /"}

		_, err := GenerateConfig(cfg, "/test")
		if err == nil {
			t.Error("expected error for invalid domain, got nil")
		}
		if !strings.Contains(err.Error(), "invalid network allow domain") {
			t.Errorf("expected 'invalid network allow domain' in error, got: %v", err)
		}
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Ports.Forward = []int{80, 0}

		_, err := GenerateConfig(cfg, "/test")
		if err == nil {
			t.Error("expected error for invalid port, got nil")
		}
		if !strings.Contains(err.Error(), "invalid port forward") {
			t.Errorf("expected 'invalid port forward' in error, got: %v", err)
		}
	})
}

func TestGenerateLimaConfig(t *testing.T) {
	cfg := config.NewConfig()
	cfg.VM.Image = "ubuntu-22.04"
	cfg.Resources.Memory = "4GB"
	cfg.Resources.CPUs = 2
	cfg.Resources.Disk = "20GB"
	cfg.Network.Allow = []string{"registry.npmjs.org", "github.com"}
	cfg.Ports.Forward = []int{3000, 5173}

	projectDir := "/Users/test/myproject"

	yaml, err := GenerateConfig(cfg, projectDir)
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check key parts are present
	checks := []string{
		"vmType: vz",
		"memory: 4GiB",
		"cpus: 2",
		"disk: 20GiB",
		"/Users/test/myproject",
		"mountPoint: /project",
		"writable: true",
		"iptables",
		"registry.npmjs.org",
	}

	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("expected yaml to contain %q", check)
		}
	}
}

func TestGenerateConfigHasBashrcProvision(t *testing.T) {
	cfg := config.NewConfig()
	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for user-mode provision that sets up /project cd
	if !strings.Contains(yaml, "mode: user") {
		t.Error("expected user-mode provision in yaml")
	}
	if !strings.Contains(yaml, "cd /project") {
		t.Error("expected 'cd /project' in bashrc provision")
	}
}

func TestGenerateConfigWithNetworkProcess(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check that namespace creation is present
	if !strings.Contains(yaml, "watermelon-claude") {
		t.Error("expected yaml to contain namespace name 'watermelon-claude'")
	}
}

func TestGenerateConfigRejectsInvalidProcessDomain(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com", "evil.com; rm -rf /"},
	}

	_, err := GenerateConfig(cfg, "/test")
	if err == nil {
		t.Fatal("expected error for invalid process domain, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected error to mention 'invalid', got: %v", err)
	}
}

func TestGenerateConfigRejectsInvalidProcessName(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude;evil": {"api.anthropic.com"},
	}

	_, err := GenerateConfig(cfg, "/test")
	if err == nil {
		t.Fatal("expected error for invalid process name, got nil")
	}
}

func TestGenerateConfigNetworkNamespaceSetup(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com", "*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for veth pair setup
	if !strings.Contains(yaml, "ip link add") {
		t.Error("expected yaml to contain veth pair creation")
	}

	// Check for namespace network config
	if !strings.Contains(yaml, "ip netns exec watermelon-claude") {
		t.Error("expected yaml to contain namespace execution")
	}

	// Check for iptables in namespace
	if !strings.Contains(yaml, "api.anthropic.com") {
		t.Error("expected yaml to contain process-specific domain")
	}

	// Check that wildcards are NOT passed directly to iptables (iptables doesn't support wildcard syntax)
	if strings.Contains(yaml, "iptables -A OUTPUT -d *.anthropic.com") {
		t.Error("wildcard domain should NOT appear in direct iptables rules")
	}
}

func TestGenerateConfigWrapperScripts(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for wrapper script creation
	if !strings.Contains(yaml, "/usr/local/bin/claude") {
		t.Error("expected yaml to contain wrapper script path")
	}

	// Check wrapper uses namespace
	if !strings.Contains(yaml, "ip netns exec watermelon-claude") {
		t.Error("expected wrapper to use network namespace")
	}
}

func TestGenerateConfigDnsmasqForWildcards(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for dnsmasq config
	if !strings.Contains(yaml, "dnsmasq") {
		t.Error("expected yaml to contain dnsmasq setup")
	}

	// Check for ipset configuration in dnsmasq
	if !strings.Contains(yaml, "ipset=/anthropic.com/") {
		t.Error("expected yaml to contain ipset dnsmasq rule")
	}
}

func TestGenerateConfigNetworkProcessHeredocIndentation(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Heredoc bodies must be indented to remain valid YAML inside script: |
	if !strings.Contains(yaml, "cat > /etc/watermelon/claude-dns.conf << 'DNSCONF'\n      # dnsmasq config for claude") {
		t.Error("expected DNS heredoc body to be indented in generated YAML")
	}
	if !strings.Contains(yaml, "cat > /usr/local/bin/claude << 'WRAPPER'\n      #!/bin/bash") {
		t.Error("expected wrapper heredoc body to be indented in generated YAML")
	}
}

func TestGenerateConfigEmptyNetworkProcess(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	// Network.Process is empty (default)

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should NOT contain namespace setup
	if strings.Contains(yaml, "ip netns add") {
		t.Error("expected yaml to NOT contain namespace setup when NetworkProcess is empty")
	}

	// Should still have regular iptables
	if !strings.Contains(yaml, "registry.npmjs.org") {
		t.Error("expected yaml to contain general network allow rules")
	}
}

func TestGenerateConfigWithProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim":     {"node", "npm"},
		"python:3.12-slim": {"python", "pip"},
	}
	cfg.Provision.Npm = []string{"@anthropic-ai/claude-code", "typescript"}
	cfg.Provision.Pip = []string{"aider-chat"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check custom image build for npm
	if !strings.Contains(yaml, "nerdctl run --name watermelon-npm-build node:20-slim") {
		t.Error("expected yaml to build custom npm image from base image")
	}
	if !strings.Contains(yaml, "npm install -g @anthropic-ai/claude-code typescript") {
		t.Error("expected yaml to install npm packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-npm-build watermelon-npm") {
		t.Error("expected yaml to commit custom npm image")
	}

	// Check custom image build for pip
	if !strings.Contains(yaml, "nerdctl run --name watermelon-pip-build python:3.12-slim") {
		t.Error("expected yaml to build custom pip image from base image")
	}
	if !strings.Contains(yaml, "pip install aider-chat") {
		t.Error("expected yaml to install pip packages in custom image")
	}

	// Check tool wrappers use custom images
	if !strings.Contains(yaml, "watermelon-npm npm") {
		t.Error("expected npm wrapper to use custom watermelon-npm image")
	}
	if !strings.Contains(yaml, "watermelon-pip pip") {
		t.Error("expected pip wrapper to use custom watermelon-pip image")
	}

	// Check binary discovery section exists
	if !strings.Contains(yaml, "grep -vxFf") {
		t.Error("expected yaml to contain binary discovery logic")
	}
	if !strings.Contains(yaml, "wm_nerdctl()") {
		t.Error("expected yaml to define rootless nerdctl helper for provisioning")
	}

	// Ensure wrappers exist for provisioned package commands even if base image already has them
	if !strings.Contains(yaml, "for _bin in claude-code typescript; do") {
		t.Error("expected yaml to ensure wrappers for npm provisioned package commands")
	}
}

func TestNpmPackageToCommand(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"pnpm", "pnpm"},
		{"pnpm@10", "pnpm"},
		{"@scope/name", "name"},
		{"@scope/name@1.2.3", "name"},
		{"", ""},
	}

	for _, tc := range tests {
		got := npmPackageToCommand(tc.in)
		if got != tc.want {
			t.Errorf("npmPackageToCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateConfigSmartWrapperYamlIndentation(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Regression check: smart-wrapper heredoc body must remain indented
	// inside the YAML block scalar.
	if strings.Contains(yaml, "\n#!/bin/bash\n# Ensure custom image exists") {
		t.Error("expected smart wrapper script body to be indented in YAML")
	}
	if !strings.Contains(yaml, "\n      #!/bin/bash\n      # Ensure custom image exists") {
		t.Error("expected indented smart wrapper script body in YAML output")
	}
}

func TestGenerateConfigEmptyProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	// Provision is empty (default)

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should NOT contain any custom image builds or provision commands
	if strings.Contains(yaml, "watermelon-npm") {
		t.Error("expected yaml to NOT contain watermelon-npm when Provision.Npm is empty")
	}
	if strings.Contains(yaml, "watermelon-pip") {
		t.Error("expected yaml to NOT contain watermelon-pip when Provision.Pip is empty")
	}
	if strings.Contains(yaml, "watermelon-cargo") {
		t.Error("expected yaml to NOT contain watermelon-cargo when Provision.Cargo is empty")
	}
	if strings.Contains(yaml, "watermelon-go") {
		t.Error("expected yaml to NOT contain watermelon-go when Provision.Go is empty")
	}
	if strings.Contains(yaml, "watermelon-gem") {
		t.Error("expected yaml to NOT contain watermelon-gem when Provision.Gem is empty")
	}
}

func TestGenerateConfigWithCargoProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"rust:latest": {"cargo", "rustc"},
	}
	cfg.Provision.Cargo = []string{"ripgrep", "fd-find"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "cargo install ripgrep fd-find") {
		t.Error("expected yaml to install cargo packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-cargo-build watermelon-cargo") {
		t.Error("expected yaml to commit custom cargo image")
	}
	// Tool wrappers should use custom image
	if !strings.Contains(yaml, "watermelon-cargo cargo") {
		t.Error("expected cargo wrapper to use custom watermelon-cargo image")
	}
}

func TestGenerateConfigWithGoProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"golang:1.22": {"go"},
	}
	cfg.Provision.Go = []string{"github.com/junegunn/fzf@latest"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "go install github.com/junegunn/fzf@latest") {
		t.Error("expected yaml to install go packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-go-build watermelon-go") {
		t.Error("expected yaml to commit custom go image")
	}
}

func TestGenerateConfigWithGemProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"ruby:3.2": {"ruby", "gem"},
	}
	cfg.Provision.Gem = []string{"rails", "bundler"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "gem install rails bundler") {
		t.Error("expected yaml to install gem packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-gem-build watermelon-gem") {
		t.Error("expected yaml to commit custom gem image")
	}
}

func TestGenerateConfigProvisionRequiresToolImage(t *testing.T) {
	cfg := config.NewConfig()
	// No tools configured, but provision.npm is set
	cfg.Provision.Npm = []string{"pnpm"}

	_, err := GenerateConfig(cfg, "/test/project")
	if err == nil {
		t.Fatal("expected error when provision.npm is set without npm in [tools]")
	}
	if !strings.Contains(err.Error(), "provision.npm requires npm") {
		t.Errorf("expected error about missing npm tool, got: %v", err)
	}
}

func TestGenerateConfigProvisionRejectsInvalidPackageName(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"pnpm; rm -rf /"}

	_, err := GenerateConfig(cfg, "/test/project")
	if err == nil {
		t.Fatal("expected error for invalid package name")
	}
	if !strings.Contains(err.Error(), "invalid npm package") {
		t.Errorf("expected error about invalid package, got: %v", err)
	}
}

func TestGenerateConfigNetworkProcessWithContainerizedTool(t *testing.T) {
	// Reproduces the flip-to-survive config: claude is installed via npm provision
	// and also has a network.process entry. The nerdctl wrapper must use
	// --network=ns:/var/run/netns/watermelon-claude instead of --network=host,
	// because --network=host always uses the root namespace (where containerd runs),
	// ignoring ip netns exec context.
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm", "npx"},
	}
	cfg.Provision.Npm = []string{"pnpm", "@anthropic-ai/claude-code"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// The wrapper should patch --network=host to use the process namespace
	if !strings.Contains(yaml, `--network=host/--network=ns:\/var\/run\/netns\/watermelon-claude`) {
		// Check the sed command is present
		if !strings.Contains(yaml, "sed") || !strings.Contains(yaml, "watermelon-claude") {
			t.Error("expected yaml to contain sed command patching --network=host for claude")
		}
	}

	// The inner wrapper should be saved before overwriting
	if !strings.Contains(yaml, ".watermelon-claude-inner") {
		t.Error("expected yaml to save existing wrapper as .watermelon-claude-inner")
	}

	// apt-get install must appear BEFORE iptables lockdown
	aptGetPos := strings.Index(yaml, "apt-get update && apt-get install -y dnsmasq ipset")
	iptablesRejectPos := strings.Index(yaml, "iptables -A OUTPUT -j REJECT")
	if aptGetPos < 0 {
		t.Fatal("expected yaml to contain apt-get install for dnsmasq")
	}
	if iptablesRejectPos < 0 {
		t.Fatal("expected yaml to contain iptables REJECT rule")
	}
	if aptGetPos > iptablesRejectPos {
		t.Error("apt-get install must appear BEFORE iptables REJECT rule to avoid being blocked by firewall")
	}
}

func TestGenerateConfigSmartWrappers(t *testing.T) {
	t.Run("npm wrapper detects -g/--global", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim": {"node", "npm"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_npm") {
			t.Error("expected yaml to contain smart npm wrapper heredoc")
		}
		if !strings.Contains(yaml, "-g|--global") {
			t.Error("expected npm smart wrapper to detect -g/--global flags")
		}
		if !strings.Contains(yaml, "nerdctl commit") {
			t.Error("expected smart wrapper to use nerdctl commit")
		}
		if !strings.Contains(yaml, `nerdctl tag "node:20-slim" "watermelon-npm"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("pip wrapper detects install subcommand", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"python:3.12-slim": {"python", "pip"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_pip") {
			t.Error("expected yaml to contain smart pip wrapper heredoc")
		}
		if !strings.Contains(yaml, `case "$1" in install)`) {
			t.Error("expected pip smart wrapper to detect install subcommand")
		}
		if !strings.Contains(yaml, `nerdctl tag "python:3.12-slim" "watermelon-pip"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("cargo wrapper with correct bin dirs", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"rust:latest": {"cargo", "rustc"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_cargo") {
			t.Error("expected yaml to contain smart cargo wrapper heredoc")
		}
		if !strings.Contains(yaml, "/usr/local/cargo/bin /usr/local/bin") {
			t.Error("expected cargo wrapper to scan cargo-specific bin dirs")
		}
	})

	t.Run("go wrapper with correct bin dirs", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"golang:1.22": {"go"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_go") {
			t.Error("expected yaml to contain smart go wrapper heredoc")
		}
		if !strings.Contains(yaml, "/go/bin /usr/local/bin") {
			t.Error("expected go wrapper to scan go-specific bin dirs")
		}
	})

	t.Run("gem wrapper is generated", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"ruby:3.2": {"ruby", "gem"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_gem") {
			t.Error("expected yaml to contain smart gem wrapper heredoc")
		}
		if !strings.Contains(yaml, `nerdctl tag "ruby:3.2" "watermelon-gem"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("all package managers get wrappers when present", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim":     {"node", "npm"},
			"python:3.12-slim": {"python", "pip"},
			"rust:latest":      {"cargo", "rustc"},
			"golang:1.22":      {"go"},
			"ruby:3.2":         {"ruby", "gem"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		for _, cmd := range []string{"npm", "pip", "cargo", "go", "gem"} {
			if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_"+cmd) {
				t.Errorf("expected yaml to contain smart wrapper for %s", cmd)
			}
		}
	})

	t.Run("non-package-manager tools do not get smart wrappers", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim": {"node"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if strings.Contains(yaml, "WATERMELON_SMART_WRAPPER") {
			t.Error("expected yaml to NOT contain any smart wrappers for non-package-manager tools")
		}
	})
}

func TestGenerateConfigNoSmartWrapperWithoutPackageManagers(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"alpine:latest": {"sh"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if strings.Contains(yaml, "WATERMELON_SMART_WRAPPER") {
		t.Error("expected yaml to NOT contain smart wrappers when no package managers are in tools")
	}
}
