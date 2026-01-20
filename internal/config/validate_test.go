package config

import (
	"strings"
	"testing"
)

func TestValidateOnViolation(t *testing.T) {
	cfg := NewConfig()

	// Valid values
	for _, v := range []string{"log", "fail", "silent"} {
		cfg.Security.OnViolation = v
		if err := Validate(cfg); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", v, err)
		}
	}

	// Invalid value
	cfg.Security.OnViolation = "invalid"
	err := Validate(cfg)
	if err == nil {
		t.Error("expected error for invalid on_violation")
	}
	if !strings.Contains(err.Error(), "on_violation") {
		t.Errorf("error should mention on_violation: %v", err)
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
