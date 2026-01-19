package config

import (
	"fmt"
	"strings"
)

// Validate checks config for errors
func Validate(cfg *Config) error {
	// Validate on_violation
	switch cfg.Security.OnViolation {
	case "log", "fail", "silent":
		// valid
	default:
		return fmt.Errorf("invalid on_violation %q: must be log, fail, or silent", cfg.Security.OnViolation)
	}

	// Validate resources
	if cfg.Resources.CPUs < 1 {
		return fmt.Errorf("cpus must be at least 1")
	}
	if cfg.Resources.Memory == "" {
		return fmt.Errorf("memory is required")
	}
	if cfg.Resources.Disk == "" {
		return fmt.Errorf("disk is required")
	}

	// Validate IDE command
	if cfg.IDE.Command == "" {
		return fmt.Errorf("IDE command cannot be empty")
	}
	if strings.ContainsAny(cfg.IDE.Command, ";|&$`\\") {
		return fmt.Errorf("IDE command contains invalid characters")
	}

	return nil
}
