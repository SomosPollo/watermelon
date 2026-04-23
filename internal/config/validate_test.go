package config

import (
	"strings"
	"testing"
)

func TestValidateVMName(t *testing.T) {
	tests := []struct {
		name    string
		vmName  string
		wantErr bool
	}{
		{"empty name is valid", "", false},
		{"simple name", "somospollo-vm", false},
		{"alphanumeric with dash", "my-dev-vm", false},
		{"space in name", "my vm", true},
		{"slash in name", "my/vm", true},
		{"semicolon injection", "vm;evil", true},
		{"pipe injection", "vm|evil", true},
		{"dollar injection", "vm$HOME", true},
		{"backtick injection", "vm`whoami`", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.VM.Name = tt.vmName
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with vm.name=%q error = %v, wantErr %v", tt.vmName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateVMImage(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{"ubuntu-22.04 valid", "ubuntu-22.04", false},
		{"ubuntu-24.04 valid", "ubuntu-24.04", false},
		{"empty uses default", "", false},
		{"debian invalid", "debian-12", true},
		{"arbitrary string invalid", "my-custom-image", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.VM.Image = tt.image
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with vm.image=%q error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
		})
	}
}

func TestValidateProvisionScripts(t *testing.T) {
	tests := []struct {
		name    string
		scripts []string
		wantErr bool
		errMsg  string
	}{
		{"empty list valid", []string{}, false, ""},
		{"relative path valid", []string{"./vm/src/setup.sh"}, false, ""},
		{"absolute path valid", []string{"/home/user/setup.sh"}, false, ""},
		{"empty string invalid", []string{""}, true, "cannot be empty"},
		{"semicolon injection", []string{"setup.sh; rm -rf /"}, true, "invalid characters"},
		{"pipe injection", []string{"setup.sh|evil"}, true, "invalid characters"},
		{"dollar injection", []string{"$HOME/setup.sh"}, true, "invalid characters"},
		{"multiple valid", []string{"./setup.sh", "./config.sh"}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Provision.Scripts = tt.scripts
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with scripts=%v error = %v, wantErr %v", tt.scripts, err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestValidateEnforcement(t *testing.T) {
	cfg := NewConfig()

	// Valid values
	for _, v := range []string{"log", "fail", "silent", "ask"} {
		cfg.Security.Enforcement = v
		if err := Validate(cfg); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", v, err)
		}
	}

	// Invalid value
	cfg.Security.Enforcement = "invalid"
	err := Validate(cfg)
	if err == nil {
		t.Error("expected error for invalid enforcement")
	}
	if !strings.Contains(err.Error(), "enforcement") {
		t.Errorf("error should mention enforcement: %v", err)
	}
}

func TestValidateResources(t *testing.T) {
	// Test zero CPUs
	cfg := NewConfig()
	cfg.Resources.CPUs = 0
	err := Validate(cfg)
	if err == nil {
		t.Error("expected error for zero CPUs")
	}
	if !strings.Contains(err.Error(), "cpus") {
		t.Errorf("error should mention cpus: %v", err)
	}

	// Test empty Memory
	cfg = NewConfig()
	cfg.Resources.Memory = ""
	err = Validate(cfg)
	if err == nil {
		t.Error("expected error for empty memory")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error should mention memory: %v", err)
	}

	// Test empty Disk
	cfg = NewConfig()
	cfg.Resources.Disk = ""
	err = Validate(cfg)
	if err == nil {
		t.Error("expected error for empty disk")
	}
	if !strings.Contains(err.Error(), "disk") {
		t.Errorf("error should mention disk: %v", err)
	}
}

func TestValidateIDECommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"valid code", "code", false},
		{"valid cursor", "cursor", false},
		{"valid with dash", "code-insiders", false},
		{"empty command", "", true},
		{"semicolon injection", "code; rm -rf /", true},
		{"pipe injection", "code | cat", true},
		{"ampersand injection", "code && evil", true},
		{"dollar injection", "code$PATH", true},
		{"backtick injection", "code`whoami`", true},
		{"backslash injection", "code\\test", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.IDE.Command = tt.command
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with IDE.Command=%q error = %v, wantErr %v", tt.command, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNetworkAllowDomains(t *testing.T) {
	tests := []struct {
		name    string
		allow   []string
		wantErr bool
	}{
		{"valid domains", []string{"registry.npmjs.org", "github.com"}, false},
		{"valid wildcard", []string{"*.anthropic.com"}, false},
		{"valid IP", []string{"192.168.1.1"}, false},
		{"empty list", []string{}, false},
		{"injection in domain", []string{"evil.com; rm -rf /"}, true},
		{"empty domain in list", []string{"good.com", ""}, true},
		{"backtick injection", []string{"evil.com`whoami`"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Network.Allow = tt.allow
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with allow %v error = %v, wantErr %v", tt.allow, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNetworkProcessNames(t *testing.T) {
	tests := []struct {
		name        string
		processName string
		wantErr     bool
	}{
		{"valid simple", "claude", false},
		{"valid with dash", "code-insiders", false},
		{"valid with underscore", "my_tool", false},
		{"empty name", "", true},
		{"semicolon injection", "claude;rm", true},
		{"pipe injection", "claude|cat", true},
		{"ampersand injection", "claude&&evil", true},
		{"dollar injection", "claude$PATH", true},
		{"backtick injection", "claude`whoami`", true},
		{"backslash injection", "claude\\test", true},
		{"space in name", "my tool", true},
		{"slash in name", "my/tool", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Network.Process = map[string][]string{
				tt.processName: {"example.com"},
			}
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with process name %q error = %v, wantErr %v", tt.processName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNetworkProcessDomains(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		wantErr bool
	}{
		{"valid domains", []string{"api.anthropic.com", "example.com"}, false},
		{"valid wildcard", []string{"*.anthropic.com"}, false},
		{"empty list", []string{}, false},
		{"injection in domain", []string{"evil.com; rm -rf /"}, true},
		{"empty domain in list", []string{"good.com", ""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Network.Process = map[string][]string{
				"testprocess": tt.domains,
			}
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with domains %v error = %v, wantErr %v", tt.domains, err, tt.wantErr)
			}
		})
	}
}

func TestValidateProvisionPackageNames(t *testing.T) {
	tests := []struct {
		name    string
		npm     []string
		wantErr bool
	}{
		{"valid simple package", []string{"typescript"}, false},
		{"valid scoped package", []string{"@anthropic-ai/claude-code"}, false},
		{"valid with version", []string{"typescript@5.0.0"}, false},
		{"empty list", []string{}, false},
		{"empty package name", []string{""}, true},
		{"semicolon injection", []string{"pkg; rm -rf /"}, true},
		{"pipe injection", []string{"pkg | cat"}, true},
		{"ampersand injection", []string{"pkg && evil"}, true},
		{"dollar injection", []string{"pkg$HOME"}, true},
		{"backtick injection", []string{"pkg`whoami`"}, true},
		{"parentheses injection", []string{"pkg(evil)"}, true},
		{"braces injection", []string{"pkg{evil}"}, true},
		{"quote injection", []string{"pkg\"evil"}, true},
		{"single quote injection", []string{"pkg'evil"}, true},
		{"space in name", []string{"pkg name"}, true},
		{"tab in name", []string{"pkg\tname"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Provision.Npm = tt.npm
			// Add node tool so tool dependency validation passes for valid package names
			if len(tt.npm) > 0 {
				cfg.Tools = map[string][]string{"node:20-slim": {"node", "npm"}}
			}
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with npm=%v error = %v, wantErr %v", tt.npm, err, tt.wantErr)
			}
		})
	}
}

func TestValidateProvisionRequiresTool(t *testing.T) {
	tests := []struct {
		name    string
		npm     []string
		pip     []string
		cargo   []string
		goPkgs  []string
		gem     []string
		tools   map[string][]string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "npm without node tool",
			npm:     []string{"typescript"},
			tools:   map[string][]string{},
			wantErr: true,
			errMsg:  "node",
		},
		{
			name:    "npm with node tool",
			npm:     []string{"typescript"},
			tools:   map[string][]string{"node:20-slim": {"node", "npm"}},
			wantErr: false,
		},
		{
			name:    "pip without python tool",
			pip:     []string{"requests"},
			tools:   map[string][]string{},
			wantErr: true,
			errMsg:  "python",
		},
		{
			name:    "pip with python tool",
			pip:     []string{"requests"},
			tools:   map[string][]string{"python:3.12-slim": {"python", "pip"}},
			wantErr: false,
		},
		{
			name:    "cargo without rust tool",
			cargo:   []string{"ripgrep"},
			tools:   map[string][]string{},
			wantErr: true,
			errMsg:  "rust",
		},
		{
			name:    "cargo with rust tool",
			cargo:   []string{"ripgrep"},
			tools:   map[string][]string{"rust:latest": {"cargo", "rustc"}},
			wantErr: false,
		},
		{
			name:    "go without golang tool",
			goPkgs:  []string{"github.com/junegunn/fzf@latest"},
			tools:   map[string][]string{},
			wantErr: true,
			errMsg:  "go",
		},
		{
			name:    "go with golang tool",
			goPkgs:  []string{"github.com/junegunn/fzf@latest"},
			tools:   map[string][]string{"golang:1.22": {"go"}},
			wantErr: false,
		},
		{
			name:    "gem without ruby tool",
			gem:     []string{"rails"},
			tools:   map[string][]string{},
			wantErr: true,
			errMsg:  "ruby",
		},
		{
			name:    "gem with ruby tool",
			gem:     []string{"rails"},
			tools:   map[string][]string{"ruby:3.2": {"ruby", "gem"}},
			wantErr: false,
		},
		{
			name:    "empty provision is valid",
			npm:     []string{},
			pip:     []string{},
			tools:   map[string][]string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Provision.Npm = tt.npm
			cfg.Provision.Pip = tt.pip
			cfg.Provision.Cargo = tt.cargo
			cfg.Provision.Go = tt.goPkgs
			cfg.Provision.Gem = tt.gem
			cfg.Tools = tt.tools
			err := Validate(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error to contain %q, got: %v", tt.errMsg, err)
				}
			}
		})
	}
}
