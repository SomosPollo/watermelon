package config

import (
	"fmt"
	"strings"
)

// Validate checks config for errors
func Validate(cfg *Config) error {
	// Validate enforcement
	switch cfg.Security.Enforcement {
	case "log", "fail", "silent", "ask":
		// valid
	default:
		return fmt.Errorf("invalid enforcement %q: must be log, fail, silent, or ask", cfg.Security.Enforcement)
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

	// Validate provision package names
	for _, pkg := range cfg.Provision.Npm {
		if err := validatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid npm package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Pip {
		if err := validatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid pip package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Cargo {
		if err := validatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid cargo package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Go {
		if err := validatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid go package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Gem {
		if err := validatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid gem package: %w", err)
		}
	}

	// Validate provision tool dependencies
	if len(cfg.Provision.Npm) > 0 && !hasToolImage(cfg.Tools, "node") {
		return fmt.Errorf("provision.npm requires a node image in [tools]")
	}
	if len(cfg.Provision.Pip) > 0 && !hasToolImage(cfg.Tools, "python") {
		return fmt.Errorf("provision.pip requires a python image in [tools]")
	}
	if len(cfg.Provision.Cargo) > 0 && !hasToolImage(cfg.Tools, "rust") {
		return fmt.Errorf("provision.cargo requires a rust image in [tools]")
	}
	if len(cfg.Provision.Go) > 0 && !hasToolImage(cfg.Tools, "go") {
		return fmt.Errorf("provision.go requires a go image in [tools] (golang or go)")
	}
	if len(cfg.Provision.Gem) > 0 && !hasToolImage(cfg.Tools, "ruby") {
		return fmt.Errorf("provision.gem requires a ruby image in [tools]")
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

// validatePackageName checks that a package name doesn't contain shell metacharacters
func validatePackageName(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	if strings.ContainsAny(pkg, shellMetacharacters) {
		return fmt.Errorf("package name %q contains invalid characters", pkg)
	}
	return nil
}

// hasToolImage checks if any tool image key contains the given substring
func hasToolImage(tools map[string][]string, substr string) bool {
	for image := range tools {
		if strings.Contains(image, substr) {
			return true
		}
	}
	return false
}
