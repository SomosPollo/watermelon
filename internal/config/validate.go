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

	// Validate network process names and domains
	for processName, domains := range cfg.Network.Process {
		if err := validateProcessName(processName); err != nil {
			return fmt.Errorf("invalid network process: %w", err)
		}
		for _, domain := range domains {
			if err := validateDomain(domain); err != nil {
				return fmt.Errorf("invalid domain for process %q: %w", processName, err)
			}
		}
	}

	return nil
}

// validateProcessName checks that a process name is safe for shell use
func validateProcessName(name string) error {
	if name == "" {
		return fmt.Errorf("process name cannot be empty")
	}
	// Disallow shell metacharacters and path separators
	if strings.ContainsAny(name, ";|&$`\\ /") {
		return fmt.Errorf("process name %q contains invalid characters", name)
	}
	return nil
}

// shellMetacharacters contains characters that could be used for shell injection
const shellMetacharacters = ";|&$`\\"

// validateDomain checks that a domain string doesn't contain shell metacharacters
func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if strings.ContainsAny(domain, shellMetacharacters) {
		return fmt.Errorf("domain %q contains invalid characters", domain)
	}
	return nil
}
