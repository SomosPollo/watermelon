package config

import (
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg := NewConfig()

	if cfg.Resources.Memory != "2GB" {
		t.Errorf("expected default memory 2GB, got %s", cfg.Resources.Memory)
	}
	if cfg.Resources.CPUs != 1 {
		t.Errorf("expected default cpus 1, got %d", cfg.Resources.CPUs)
	}
	if cfg.Resources.Disk != "10GB" {
		t.Errorf("expected default disk 10GB, got %s", cfg.Resources.Disk)
	}
	if cfg.Security.Enforcement != "log" {
		t.Errorf("expected default enforcement 'log', got %s", cfg.Security.Enforcement)
	}
	if cfg.IDE.Command != "code" {
		t.Errorf("expected default IDE command 'code', got %s", cfg.IDE.Command)
	}
}

func TestNewConfigHasEmptyNetworkProcess(t *testing.T) {
	cfg := NewConfig()
	if cfg.Network.Process == nil {
		t.Error("expected Network.Process to be initialized, got nil")
	}
	if len(cfg.Network.Process) != 0 {
		t.Errorf("expected Network.Process to be empty, got %d entries", len(cfg.Network.Process))
	}
}

func TestNewConfigHasEmptyProvision(t *testing.T) {
	cfg := NewConfig()
	if cfg.Provision.Npm == nil {
		t.Error("expected Provision.Npm to be initialized, got nil")
	}
	if len(cfg.Provision.Npm) != 0 {
		t.Errorf("expected Provision.Npm to be empty, got %d entries", len(cfg.Provision.Npm))
	}
	if cfg.Provision.Pip == nil {
		t.Error("expected Provision.Pip to be initialized, got nil")
	}
	if cfg.Provision.Cargo == nil {
		t.Error("expected Provision.Cargo to be initialized, got nil")
	}
	if cfg.Provision.Go == nil {
		t.Error("expected Provision.Go to be initialized, got nil")
	}
	if cfg.Provision.Gem == nil {
		t.Error("expected Provision.Gem to be initialized, got nil")
	}
	if cfg.Provision.Scripts == nil {
		t.Error("expected Provision.Scripts to be initialized, got nil")
	}
	if len(cfg.Provision.Scripts) != 0 {
		t.Errorf("expected Provision.Scripts to be empty, got %d entries", len(cfg.Provision.Scripts))
	}
}

func TestNewConfigMountProjectDefault(t *testing.T) {
	cfg := NewConfig()
	if !MountProjectEnabled(&cfg.VM) {
		t.Error("expected MountProjectEnabled to be true by default")
	}
}

func TestMountProjectEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name string
		vm   VMConfig
		want bool
	}{
		{"nil pointer defaults to true", VMConfig{MountProject: nil}, true},
		{"explicit true", VMConfig{MountProject: &trueVal}, true},
		{"explicit false", VMConfig{MountProject: &falseVal}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MountProjectEnabled(&tt.vm); got != tt.want {
				t.Errorf("MountProjectEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultWorkdir(t *testing.T) {
	falseVal := false

	cfg := NewConfig()
	if got := DefaultWorkdir(cfg); got != "/project" {
		t.Errorf("DefaultWorkdir() = %q, want /project for default config", got)
	}

	cfg.VM.MountProject = &falseVal
	if got := DefaultWorkdir(cfg); got != "" {
		t.Errorf("DefaultWorkdir() = %q, want empty string when mount_project=false", got)
	}
}
