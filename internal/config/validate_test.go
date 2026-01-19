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
