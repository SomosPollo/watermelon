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
