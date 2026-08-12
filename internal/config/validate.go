package config

import (
	"fmt"
	"math"
	"net/netip"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxVMNameLength     = 76
	maxNetworkProcesses = 255
)

// Restrict public names to lowercase even though Lima accepts uppercase. Lima
// stores instances by name under LIMA_HOME; on the case-insensitive filesystems
// common on macOS, case variants alias the same directory and cannot be locked
// or owned independently.
var vmNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

var resourceSizePattern = regexp.MustCompile(`^([1-9][0-9]*)(MB|GB|TB)$`)

var reservedProvisionImageNames = map[string]struct{}{
	"watermelon-npm":   {},
	"watermelon-pip":   {},
	"watermelon-cargo": {},
	"watermelon-go":    {},
	"watermelon-gem":   {},
}

// NetworkRule is a validated network allow-list entry.
type NetworkRule struct {
	Raw      string
	Host     string
	Port     int
	Wildcard bool
}

// Validate checks config for errors
func Validate(cfg *Config) error {
	if cfg.VM.Name != "" {
		if err := ValidateVMName(cfg.VM.Name); err != nil {
			return fmt.Errorf("invalid vm.name: %w", err)
		}
	}

	// Validate enforcement against the same descriptors used by the CLI.
	if _, ok := LookupEnforcement(cfg.Security.Enforcement); !ok {
		return fmt.Errorf("invalid enforcement %q: must be log, fail, silent, or ask", cfg.Security.Enforcement)
	}

	switch cfg.VM.Image {
	case "", "ubuntu-22.04", "ubuntu-24.04":
		// An empty value uses the default image during generation.
	default:
		return fmt.Errorf("unsupported vm.image %q: must be ubuntu-22.04 or ubuntu-24.04", cfg.VM.Image)
	}

	if cfg.VM.Workdir != "" {
		if err := ValidateGuestWorkdir(cfg.VM.Workdir); err != nil {
			return fmt.Errorf("invalid vm.workdir: %w", err)
		}
	}

	// Validate resources
	if cfg.Resources.CPUs < 1 {
		return fmt.Errorf("cpus must be at least 1")
	}
	if err := ValidateResourceSize(cfg.Resources.Memory); err != nil {
		return fmt.Errorf("invalid memory: %w", err)
	}
	if err := ValidateResourceSize(cfg.Resources.Disk); err != nil {
		return fmt.Errorf("invalid disk: %w", err)
	}

	// Validate IDE command
	if cfg.IDE.Command == "" {
		return fmt.Errorf("IDE command cannot be empty")
	}
	if strings.ContainsAny(cfg.IDE.Command, ShellMetacharacters) {
		return fmt.Errorf("IDE command contains invalid characters")
	}
	if cfg.IDE.Workdir != "" {
		if err := ValidateGuestWorkdir(cfg.IDE.Workdir); err != nil {
			return fmt.Errorf("invalid ide.workdir: %w", err)
		}
	}

	toolProviders, err := BuildToolCommandProviders(cfg.Tools)
	if err != nil {
		return err
	}

	mountSources := make([]string, 0, len(cfg.Mounts))
	for source := range cfg.Mounts {
		mountSources = append(mountSources, source)
	}
	sort.Strings(mountSources)
	mountTargets := make(map[string]string, len(cfg.Mounts))
	for _, source := range mountSources {
		mount := cfg.Mounts[source]
		if err := ValidateMountSource(source); err != nil {
			return fmt.Errorf("invalid mount source: %w", err)
		}
		if err := ValidateMountTarget(mount.Target); err != nil {
			return fmt.Errorf("invalid mount target for %q: %w", source, err)
		}
		normalizedTarget := filepath.Clean(mount.Target)
		if owner, exists := mountTargets[normalizedTarget]; exists {
			return fmt.Errorf("mount sources %q and %q use the same normalized target %q", owner, source, normalizedTarget)
		}
		mountTargets[normalizedTarget] = source
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

	seenPorts := make(map[int]struct{}, len(cfg.Ports.Forward))
	for _, port := range cfg.Ports.Forward {
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid port forward: port %d is out of valid range (1-65535)", port)
		}
		if _, exists := seenPorts[port]; exists {
			return fmt.Errorf("invalid port forward: port %d is listed more than once", port)
		}
		seenPorts[port] = struct{}{}
	}

	// Validate network process names and domains
	if len(cfg.Network.Process) > maxNetworkProcesses {
		return fmt.Errorf("network.process has %d entries; maximum is %d", len(cfg.Network.Process), maxNetworkProcesses)
	}
	for processName, domains := range cfg.Network.Process {
		if err := ValidateProcessName(processName); err != nil {
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
	for _, script := range cfg.Provision.Scripts {
		if err := ValidateProvisionScript(script); err != nil {
			return fmt.Errorf("invalid provision script: %w", err)
		}
	}
	if len(cfg.Provision.ScriptSHA256) != 0 && len(cfg.Provision.ScriptSHA256) != len(cfg.Provision.Scripts) {
		return fmt.Errorf("internal provision script digest count does not match scripts")
	}
	for _, digest := range cfg.Provision.ScriptSHA256 {
		if !isSHA256(digest) || digest != strings.ToLower(digest) {
			return fmt.Errorf("internal provision script digest must be a lowercase SHA-256 value")
		}
	}

	// Each active provisioner needs its exact package-manager command. The
	// current image builder commits one independent derived image per manager,
	// so sharing a base image between active provisioners would silently make
	// all wrappers use only the last derived image.
	provisioners := []struct {
		name     string
		command  string
		packages []string
	}{
		{name: "provision.npm", command: "npm", packages: cfg.Provision.Npm},
		{name: "provision.pip", command: "pip", packages: cfg.Provision.Pip},
		{name: "provision.cargo", command: "cargo", packages: cfg.Provision.Cargo},
		{name: "provision.go", command: "go", packages: cfg.Provision.Go},
		{name: "provision.gem", command: "gem", packages: cfg.Provision.Gem},
	}
	activeImageProvisioner := make(map[string]string)
	for _, provisioner := range provisioners {
		if len(provisioner.packages) == 0 {
			continue
		}
		image := toolProviders[provisioner.command]
		if image == "" {
			return fmt.Errorf("%s requires command %q in [tools]", provisioner.name, provisioner.command)
		}
		if previous, exists := activeImageProvisioner[image]; exists {
			return fmt.Errorf("%s and %s cannot provision the same tool image %q", previous, provisioner.name, image)
		}
		activeImageProvisioner[image] = provisioner.name
	}

	return nil
}

// ValidateVMName checks a name against Lima's instance-name restrictions.
func ValidateVMName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > maxVMNameLength {
		return fmt.Errorf("name is too long: %d bytes (maximum %d)", len(name), maxVMNameLength)
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
		return fmt.Errorf("name %q must not end in .yaml or .yml", name)
	}
	if !vmNamePattern.MatchString(name) {
		return fmt.Errorf("name %q must match %s", name, vmNamePattern.String())
	}
	return nil
}

// ValidateGuestWorkdir checks a guest directory independently of the host OS.
// Guest paths are always Linux paths, even when Watermelon runs on macOS.
func ValidateGuestWorkdir(workdir string) error {
	if workdir == "" {
		return fmt.Errorf("workdir cannot be empty")
	}
	if !utf8.ValidString(workdir) {
		return fmt.Errorf("workdir must be valid UTF-8")
	}
	if strings.IndexByte(workdir, 0) >= 0 {
		return fmt.Errorf("workdir cannot contain a NUL byte")
	}
	if strings.ContainsAny(workdir, safePathDisallowed) {
		return fmt.Errorf("workdir %q contains invalid characters", workdir)
	}
	if !path.IsAbs(workdir) {
		return fmt.Errorf("workdir %q must be an absolute Linux path", workdir)
	}
	if cleaned := path.Clean(workdir); cleaned != workdir {
		return fmt.Errorf("workdir %q must be a clean path (use %q)", workdir, cleaned)
	}
	return nil
}

// ValidateProvisionScript checks a project-relative script path before it is
// resolved and read by the Lima config generator. Provision files are limited
// to the project so a repository cannot make Watermelon copy an arbitrary host
// file into its VM merely by naming an absolute path or traversing upward.
func ValidateProvisionScript(script string) error {
	if script == "" {
		return fmt.Errorf("script path cannot be empty")
	}
	if !utf8.ValidString(script) {
		return fmt.Errorf("script path must be valid UTF-8")
	}
	if strings.IndexByte(script, 0) >= 0 {
		return fmt.Errorf("script path cannot contain a NUL byte")
	}
	if strings.ContainsAny(script, safePathDisallowed) {
		return fmt.Errorf("script path %q contains invalid characters", script)
	}
	if filepath.IsAbs(script) {
		return fmt.Errorf("script path %q must be relative to the project root", script)
	}
	for _, component := range strings.Split(filepath.ToSlash(script), "/") {
		if component == ".." {
			return fmt.Errorf("script path %q must not traverse outside the project root", script)
		}
	}
	return nil
}

// ValidateProcessName checks that a process name can safely be used as a
// command name and as one host filesystem path component.
func ValidateProcessName(name string) error {
	if name == "" {
		return fmt.Errorf("process name cannot be empty")
	}
	if len(name) > 255 {
		return fmt.Errorf("process name is too long: %d bytes (maximum 255)", len(name))
	}
	for index, r := range name {
		firstAllowed := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_'
		if index == 0 && !firstAllowed {
			return fmt.Errorf("process name %q must start with an ASCII letter, digit, or underscore", name)
		}
		if !firstAllowed && r != '.' && r != '+' && r != '-' {
			return fmt.Errorf("process name %q contains invalid character %q", name, r)
		}
	}
	return nil
}

// ShellMetacharacters contains characters that could be used for shell injection
const ShellMetacharacters = ";|&$`\\"

const safePathDisallowed = ShellMetacharacters + "\"'\n\r\t"

// PackageNameDangerous contains characters that are invalid in package names
const PackageNameDangerous = ";|&$`\\(){}!'\" \t\n"

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
	if !utf8.ValidString(pkg) {
		return fmt.Errorf("package name %q must be valid UTF-8", pkg)
	}
	if strings.IndexByte(pkg, 0) >= 0 {
		return fmt.Errorf("package name cannot contain a NUL byte")
	}
	if strings.HasPrefix(pkg, "-") {
		return fmt.Errorf("package name %q cannot start with '-'", pkg)
	}
	for _, r := range pkg {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("package name %q contains whitespace or control characters", pkg)
		}
	}
	if strings.ContainsAny(pkg, PackageNameDangerous) {
		return fmt.Errorf("package name %q contains invalid characters", pkg)
	}
	return nil
}

// BuildToolCommandProviders validates tool declarations and returns the one
// container image that owns each command. Sorting image names makes duplicate
// diagnostics stable even though TOML tables are represented by a Go map.
func BuildToolCommandProviders(tools map[string][]string) (map[string]string, error) {
	images := make([]string, 0, len(tools))
	for image := range tools {
		images = append(images, image)
	}
	sort.Strings(images)

	providers := make(map[string]string)
	for _, image := range images {
		if err := ValidateToolImage(image); err != nil {
			return nil, fmt.Errorf("invalid tool image: %w", err)
		}
		seenInImage := make(map[string]struct{}, len(tools[image]))
		for _, command := range tools[image] {
			if err := ValidateCommandName(command); err != nil {
				return nil, fmt.Errorf("invalid tool command for image %q: %w", image, err)
			}
			if command == "nerdctl" {
				return nil, fmt.Errorf("tool command %q is reserved by Watermelon", command)
			}
			if _, exists := seenInImage[command]; exists {
				return nil, fmt.Errorf("tool command %q is declared more than once for image %q", command, image)
			}
			seenInImage[command] = struct{}{}
			if owner, exists := providers[command]; exists {
				return nil, fmt.Errorf("tool command %q is declared by multiple images %q and %q", command, owner, image)
			}
			providers[command] = image
		}
	}
	return providers, nil
}

// ValidateResourceSize validates Watermelon's documented resource syntax and
// rejects zero and byte totals that Lima cannot represent as a signed byte count.
func ValidateResourceSize(size string) error {
	matches := resourceSizePattern.FindStringSubmatch(size)
	if matches == nil {
		return fmt.Errorf("size %q must be a positive integer followed by MB, GB, or TB", size)
	}
	amount, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return fmt.Errorf("size %q is too large", size)
	}
	multipliers := map[string]uint64{"MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40}
	if amount > uint64(math.MaxInt64)/multipliers[matches[2]] {
		return fmt.Errorf("size %q is too large", size)
	}
	return nil
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
	first := image[0]
	if !((first >= 'a' && first <= 'z') ||
		(first >= 'A' && first <= 'Z') ||
		(first >= '0' && first <= '9')) {
		return fmt.Errorf("image %q must start with an ASCII letter or digit", image)
	}
	for _, r := range image {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '.' && r != '_' && r != '-' && r != '/' && r != ':' && r != '@' {
			return fmt.Errorf("image %q contains invalid character %q", image, r)
		}
	}
	if isReservedProvisionImage(image) {
		return fmt.Errorf("image %q conflicts with a Watermelon-managed provisioning image", image)
	}
	return nil
}

func isReservedProvisionImage(image string) bool {
	repository := image
	if digest := strings.IndexByte(repository, '@'); digest >= 0 {
		repository = repository[:digest]
	}
	if slash, colon := strings.LastIndexByte(repository, '/'), strings.LastIndexByte(repository, ':'); colon > slash {
		repository = repository[:colon]
	}
	parts := strings.Split(repository, "/")
	switch {
	case len(parts) == 1:
		// An unqualified image is Docker Hub's implicit library repository.
	case len(parts) == 2 && parts[0] == "library":
		// Docker also accepts the explicit short form library/<name>.
		parts = parts[1:]
	case len(parts) == 2 && (parts[0] == "docker.io" || parts[0] == "index.docker.io"):
		parts = parts[1:]
	case len(parts) == 3 && (parts[0] == "docker.io" || parts[0] == "index.docker.io") && parts[1] == "library":
		parts = parts[2:]
	default:
		return false
	}
	_, reserved := reservedProvisionImageNames[parts[0]]
	return reserved
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
	if !utf8.ValidString(source) {
		return fmt.Errorf("source %q must be valid UTF-8", source)
	}
	if strings.IndexByte(source, 0) >= 0 {
		return fmt.Errorf("source %q cannot contain a NUL byte", source)
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
	if !utf8.ValidString(target) {
		return fmt.Errorf("target %q must be valid UTF-8", target)
	}
	if strings.IndexByte(target, 0) >= 0 {
		return fmt.Errorf("target %q cannot contain a NUL byte", target)
	}
	if strings.ContainsAny(target, safePathDisallowed) {
		return fmt.Errorf("target %q contains invalid characters", target)
	}
	if !filepath.IsAbs(target) {
		return fmt.Errorf("target %q must be absolute", target)
	}
	for _, component := range strings.Split(target, string(filepath.Separator)) {
		if component == ".." {
			return fmt.Errorf("target %q cannot contain path traversal", target)
		}
	}
	cleaned := filepath.Clean(target)
	const mountRoot = "/mnt/watermelon"
	if cleaned == mountRoot {
		return fmt.Errorf("target %q cannot replace Watermelon's managed mount root %s", target, mountRoot)
	}
	if !strings.HasPrefix(cleaned, mountRoot+string(filepath.Separator)) {
		return fmt.Errorf("target %q must be a descendant of %s", target, mountRoot)
	}
	for _, managed := range []string{
		mountRoot + "/bootstrap",
		mountRoot + "/state",
	} {
		if cleaned == managed || strings.HasPrefix(cleaned, managed+string(filepath.Separator)) {
			return fmt.Errorf("target %q overlaps Watermelon's managed guest path %s", target, managed)
		}
	}
	return nil
}
