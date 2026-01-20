package lima

import (
	"strings"
	"testing"

	"github.com/saeta/watermelon/internal/config"
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
