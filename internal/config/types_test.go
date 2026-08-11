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
	if cfg.Security.Enforcement != EnforcementFail {
		t.Errorf("expected default enforcement %q, got %s", EnforcementFail, cfg.Security.Enforcement)
	}
	if cfg.IDE.Command != "code" {
		t.Errorf("expected default IDE command 'code', got %s", cfg.IDE.Command)
	}
	if cfg.VM.Image != "ubuntu-22.04" {
		t.Errorf("expected default VM image ubuntu-22.04, got %s", cfg.VM.Image)
	}
	if !MountProjectEnabled(&cfg.VM) {
		t.Error("expected project mounting to be enabled by default")
	}
	if got := DefaultWorkdir(cfg); got != "/project" {
		t.Errorf("DefaultWorkdir() = %q, want /project", got)
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
}

func TestMountProjectEnabled(t *testing.T) {
	enabled, disabled := true, false
	tests := []struct {
		name string
		vm   *VMConfig
		want bool
	}{
		{name: "nil config", vm: nil, want: true},
		{name: "unset field", vm: &VMConfig{}, want: true},
		{name: "explicit true", vm: &VMConfig{MountProject: &enabled}, want: true},
		{name: "explicit false", vm: &VMConfig{MountProject: &disabled}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MountProjectEnabled(tt.vm); got != tt.want {
				t.Errorf("MountProjectEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultWorkdir(t *testing.T) {
	disabled := false
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{name: "nil config", cfg: nil, want: ""},
		{name: "mounted default", cfg: NewConfig(), want: "/project"},
		{name: "configured VM workdir", cfg: &Config{VM: VMConfig{Workdir: "/workspace"}}, want: "/workspace"},
		{name: "unmounted guest home", cfg: &Config{VM: VMConfig{MountProject: &disabled}}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultWorkdir(tt.cfg); got != tt.want {
				t.Errorf("DefaultWorkdir() = %q, want %q", got, tt.want)
			}
		})
	}
}
