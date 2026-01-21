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
	if cfg.Security.OnViolation != "log" {
		t.Errorf("expected default on_violation 'log', got %s", cfg.Security.OnViolation)
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
}
