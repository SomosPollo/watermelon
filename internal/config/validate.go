package config

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// NetworkRule is a validated network allow-list entry.
type NetworkRule struct {
	Raw      string
	Host     string
	Port     int
	Wildcard bool
}

// Validate checks config for errors
func Validate(cfg *Config) error {
	// Validate enforcement
	switch cfg.Security.Enforcement {
	case "log", "fail", "silent", "ask":
		// valid
	default:
		return fmt.Errorf("invalid enforcement %q: must be log, fail, silent, or ask", cfg.Security.Enforcement)
	}

	if cfg.VM.Image != "ubuntu-22.04" {
		return fmt.Errorf("unsupported vm.image %q: only ubuntu-22.04 is supported", cfg.VM.Image)
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
	if strings.ContainsAny(cfg.IDE.Command, ShellMetacharacters) {
		return fmt.Errorf("IDE command contains invalid characters")
	}

	for image, commands := range cfg.Tools {
		if err := ValidateToolImage(image); err != nil {
			return fmt.Errorf("invalid tool image: %w", err)
		}
		for _, command := range commands {
			if err := ValidateCommandName(command); err != nil {
				return fmt.Errorf("invalid tool command for image %q: %w", image, err)
			}
		}
	}

	for source, mount := range cfg.Mounts {
		if err := ValidateMountSource(source); err != nil {
			return fmt.Errorf("invalid mount source: %w", err)
		}
		if err := ValidateMountTarget(mount.Target); err != nil {
			return fmt.Errorf("invalid mount target for %q: %w", source, err)
		}
		switch mount.Mode {
		case "", "ro", "rw":
			// valid; empty defaults to read-only
		default:
			return fmt.Errorf("invalid mount mode %q for %q: must be ro or rw", mount.Mode, source)
		}
	}

	// Validate network allow domains
	for _, domain := range cfg.Network.Allow {
		if err := ValidateDomain(domain); err != nil {
			return fmt.Errorf("invalid network allow domain: %w", err)
		}
	}

	// Validate network process names and domains
	for processName, domains := range cfg.Network.Process {
		if err := validateProcessName(processName); err != nil {
			return fmt.Errorf("invalid network process: %w", err)
		}
		for _, domain := range domains {
			if err := ValidateDomain(domain); err != nil {
				return fmt.Errorf("invalid domain for process %q: %w", processName, err)
			}
		}
	}

	// Validate provision package names
	for _, pkg := range cfg.Provision.Npm {
		if err := ValidatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid npm package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Pip {
		if err := ValidatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid pip package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Cargo {
		if err := ValidatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid cargo package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Go {
		if err := ValidatePackageName(pkg); err != nil {
			return fmt.Errorf("invalid go package: %w", err)
		}
	}
	for _, pkg := range cfg.Provision.Gem {
		if err := ValidatePackageName(pkg); err != nil {
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

// ShellMetacharacters contains characters that could be used for shell injection
const ShellMetacharacters = ";|&$`\\"

const safePathDisallowed = ShellMetacharacters + "\"'\n\r\t"

// PackageNameDangerous contains characters that are invalid in package names
const PackageNameDangerous = ";|&$`\\(){}!~'\" \t\n"

// ValidateDomain checks that a network rule is syntactically valid and safe for rendering.
func ValidateDomain(domain string) error {
	_, err := ParseNetworkRule(domain)
	return err
}

// ValidatePackageName checks that a package name doesn't contain dangerous characters
func ValidatePackageName(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	if strings.ContainsAny(pkg, PackageNameDangerous) {
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

// ParseNetworkRule validates and normalizes a network allow-list entry.
func ParseNetworkRule(input string) (NetworkRule, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return NetworkRule{}, fmt.Errorf("domain cannot be empty")
	}
	if raw != input {
		return NetworkRule{}, fmt.Errorf("domain %q contains leading or trailing whitespace", input)
	}
	for _, r := range raw {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '.' && r != '-' && r != '*' && r != ':' {
			return NetworkRule{}, fmt.Errorf("domain %q contains invalid character %q", input, r)
		}
	}

	host := raw
	port := 0
	if colon := strings.LastIndex(raw, ":"); colon >= 0 {
		if strings.Count(raw, ":") > 1 {
			return NetworkRule{}, fmt.Errorf("domain %q contains unsupported IPv6 or multiple ports", input)
		}
		host = raw[:colon]
		portText := raw[colon+1:]
		if host == "" || portText == "" {
			return NetworkRule{}, fmt.Errorf("domain %q has invalid host or port", input)
		}
		parsedPort, err := strconv.Atoi(portText)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return NetworkRule{}, fmt.Errorf("domain %q has invalid port", input)
		}
		port = parsedPort
	}

	wildcard := false
	if strings.Contains(host, "*") {
		if !strings.HasPrefix(host, "*.") || strings.Count(host, "*") != 1 {
			return NetworkRule{}, fmt.Errorf("domain %q has invalid wildcard placement", input)
		}
		if port != 0 {
			return NetworkRule{}, fmt.Errorf("wildcard domain %q cannot include a port", input)
		}
		wildcard = true
		host = strings.TrimPrefix(host, "*.")
	}

	if err := validateHost(host); err != nil {
		return NetworkRule{}, err
	}

	return NetworkRule{
		Raw:      raw,
		Host:     strings.ToLower(host),
		Port:     port,
		Wildcard: wildcard,
	}, nil
}

func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if strings.Contains(host, "..") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return fmt.Errorf("domain %q is malformed", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !addr.Is4() {
			return fmt.Errorf("domain %q uses unsupported IPv6 address", host)
		}
		return nil
	}

	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain %q has an empty label", host)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain %q has a label starting or ending with '-'", host)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') &&
				!(r >= 'A' && r <= 'Z') &&
				!(r >= '0' && r <= '9') &&
				r != '-' {
				return fmt.Errorf("domain %q contains invalid character %q", host, r)
			}
		}
	}
	return nil
}

// ValidateToolImage checks that a container image reference is safe for shell rendering.
func ValidateToolImage(image string) error {
	if image == "" {
		return fmt.Errorf("image cannot be empty")
	}
	for _, r := range image {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '.' && r != '_' && r != '-' && r != '/' && r != ':' && r != '@' {
			return fmt.Errorf("image %q contains invalid character %q", image, r)
		}
	}
	return nil
}

// ValidateCommandName checks that a tool command can safely become /usr/local/bin/<command>.
func ValidateCommandName(command string) error {
	if command == "" {
		return fmt.Errorf("command cannot be empty")
	}
	if command == "." || command == ".." {
		return fmt.Errorf("command %q is invalid", command)
	}
	for _, r := range command {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '.' && r != '_' && r != '-' && r != '+' {
			return fmt.Errorf("command %q contains invalid character %q", command, r)
		}
	}
	return nil
}

func ValidateMountSource(source string) error {
	if source == "" {
		return fmt.Errorf("source cannot be empty")
	}
	if strings.ContainsAny(source, safePathDisallowed) {
		return fmt.Errorf("source %q contains invalid characters", source)
	}
	if source != "~" && !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, "~/") {
		return fmt.Errorf("source %q must be absolute or start with ~/", source)
	}
	return nil
}

func ValidateMountTarget(target string) error {
	if target == "" {
		return fmt.Errorf("target cannot be empty")
	}
	if strings.ContainsAny(target, safePathDisallowed) {
		return fmt.Errorf("target %q contains invalid characters", target)
	}
	if !strings.HasPrefix(target, "/") {
		return fmt.Errorf("target %q must be absolute", target)
	}
	if target == "/project" || strings.HasPrefix(target, "/project/") {
		return fmt.Errorf("target %q conflicts with project mount", target)
	}
	return nil
}
