package lima

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/saeta-eth/watermelon/internal/config"
)

// validatePort checks that a port is within the valid range 1-65535
func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of valid range (1-65535)", port)
	}
	return nil
}

// processKernelID returns a deterministic identifier short enough for Linux
// interfaces (IFNAMSIZ is 16 including the trailing NUL) and other kernel names.
func processKernelID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x", sum[:6])
}

func buildProcessKernelIDs(processNames []string, identifier func(string) string) (map[string]string, error) {
	ids := make(map[string]string, len(processNames))
	owners := make(map[string]string, len(processNames))
	for _, processName := range processNames {
		id := identifier(processName)
		if owner, exists := owners[id]; exists {
			return nil, fmt.Errorf("network process names %q and %q have colliding kernel identifiers", owner, processName)
		}
		owners[id] = processName
		ids[processName] = id
	}
	return ids, nil
}

var (
	hostGOOS         = runtime.GOOS
	hostEffectiveUID = func() int64 { return int64(os.Geteuid()) }
)

const (
	maxProvisionScriptSize  = 1 << 20
	maxProvisionScriptsSize = 4 << 20
	nfqdGuestDir            = "/mnt/watermelon/bootstrap"
	logStateGuestDir        = "/mnt/watermelon/state"
	verdictAuthKeyGuestPath = "/etc/watermelon/verdict-auth-key"
)

// GenerateOptions contains host paths and values bound to a specific Lima
// instance at creation time. BootstrapHostDir is mounted read-only at the
// fixed bootstrap path and is required with NfqdSHA256 and VerdictAuthKey in
// ask mode. It may also carry immutable instance identity in other enforcement
// modes. LogHostDir, when set, is mounted read-write at the fixed instance-state
// path.
type GenerateOptions struct {
	// Initialization is explicitly allowed only for a newly owned empty disk.
	// Replacement VMs must never format retained workspace data.
	FormatWorkspaceDisk bool
	VerdictServerPort   int
	VerdictAuthKey      string
	NfqdSHA256          string
	BootstrapHostDir    string
	// NfqdHostDir is a deprecated compatibility alias for BootstrapHostDir.
	NfqdHostDir string
	LogHostDir  string
	// PreparedProvisionScripts supplies the exact bytes already audited by the
	// CLI. The generator verifies every digest before embedding them.
	PreparedProvisionScripts *PreparedProvisionScripts
}

// GenerateConfig creates Lima YAML from watermelon config. Mount sources are
// expanded from the config for direct package callers; the CLI uses
// GenerateConfigWithMountSources so the paths it audited are exactly the paths
// recorded in Lima's immutable instance config.
func GenerateConfig(cfg *config.Config, projectDir string, verdictServerPort ...int) (string, error) {
	opts := GenerateOptions{}
	if len(verdictServerPort) > 0 {
		opts.VerdictServerPort = verdictServerPort[0]
	}
	return generateConfig(cfg, projectDir, nil, opts)
}

// GenerateConfigWithMountSources creates Lima YAML using canonical host mount
// paths keyed by their original config spelling.
func GenerateConfigWithMountSources(cfg *config.Config, projectDir string, mountSources map[string]string, verdictServerPort ...int) (string, error) {
	opts := GenerateOptions{}
	if len(verdictServerPort) > 0 {
		opts.VerdictServerPort = verdictServerPort[0]
	}
	return generateConfig(cfg, projectDir, mountSources, opts)
}

// GenerateConfigForInstance creates the immutable Lima config using canonical
// host mount sources and instance-bound security inputs.
func GenerateConfigForInstance(cfg *config.Config, projectDir string, mountSources map[string]string, opts GenerateOptions) (string, error) {
	return generateConfig(cfg, projectDir, mountSources, opts)
}

func generateConfig(cfg *config.Config, projectDir string, mountSources map[string]string, opts GenerateOptions) (string, error) {
	if err := config.Validate(cfg); err != nil {
		return "", err
	}
	toolProviders, err := config.BuildToolCommandProviders(cfg.Tools)
	if err != nil {
		return "", err
	}
	if err := validateProjectDir(projectDir); err != nil {
		return "", err
	}

	// Validate port forwards
	for _, port := range cfg.Ports.Forward {
		if err := validatePort(port); err != nil {
			return "", fmt.Errorf("invalid port forward: %w", err)
		}
	}

	networkRules, globalWildcardRules, err := buildNetworkRules(cfg.Network.Allow)
	if err != nil {
		return "", fmt.Errorf("invalid network allow domain: %w", err)
	}

	networkProcessRules := make(map[string][]config.NetworkRule)
	for processName, domains := range cfg.Network.Process {
		rules, _, err := buildNetworkRules(domains)
		if err != nil {
			return "", fmt.Errorf("invalid domain for process %q: %w", processName, err)
		}
		networkProcessRules[processName] = rules
	}

	dnsExactHosts, dnsWildcardHosts := dnsHostsForRules(networkRules)
	processDNSExactHosts := make(map[string][]string, len(networkProcessRules))
	processDNSWildcards := make(map[string][]string, len(networkProcessRules))
	for processName, rules := range networkProcessRules {
		processExact, processWildcards := dnsHostsForRules(networkRules, rules)
		processDNSExactHosts[processName] = processExact
		processDNSWildcards[processName] = processWildcards
		processOnlyExact, _ := dnsHostsForRules(rules)
		dnsExactHosts = uniqueSortedStrings(append(dnsExactHosts, processOnlyExact...))
	}

	mounts, err := buildMounts(cfg.Mounts, mountSources)
	if err != nil {
		return "", err
	}
	mountProject := config.MountProjectEnabled(&cfg.VM)
	if err := validateInternalMountTargets(mounts, mountProject); err != nil {
		return "", err
	}

	vmType, err := hostVMType(hostGOOS)
	if err != nil {
		return "", err
	}
	// Lima's writable mounts preserve host ownership. Bind the fixed guest
	// account to the effective host UID so a project owned by a non-1000 macOS
	// or Linux user remains writable from the sandbox. The account name and
	// home stay fixed and are still verified by the root provisioning script.
	hostUID := hostEffectiveUID()
	if hostUID < 0 {
		return "", fmt.Errorf("effective host UID %d is invalid", hostUID)
	}
	if hostUID == 0 {
		return "", errors.New("watermelon must run as an unprivileged host user to create a Lima VM")
	}
	// Linux and Darwin expose 32-bit uid_t values. The all-ones value represents
	// an invalid/unset identity and must not be assigned to the guest account.
	const maxVMUserUID = int64(1<<32 - 2)
	if hostUID > maxVMUserUID {
		return "", fmt.Errorf("effective host UID %d is outside the supported guest UID range 1..%d", hostUID, maxVMUserUID)
	}
	vmUserUID := uint32(hostUID)

	// Build provision data: custom images and tool image overrides
	type provSpec struct {
		pkgs        []string
		cmd         string
		installArgs []string
		tag         string
		binDirs     string
	}
	specs := []provSpec{
		{cfg.Provision.Npm, "npm", []string{"npm", "install", "-g"}, "watermelon-npm", "/usr/local/bin"},
		{cfg.Provision.Pip, "pip", []string{"pip", "install"}, "watermelon-pip", "/usr/local/bin"},
		{cfg.Provision.Cargo, "cargo", []string{"cargo", "install"}, "watermelon-cargo", "/usr/local/cargo/bin /usr/local/bin"},
		{cfg.Provision.Go, "go", []string{"go", "install"}, "watermelon-go", "/go/bin /usr/local/bin"},
		{cfg.Provision.Gem, "gem", []string{"gem", "install"}, "watermelon-gem", "/usr/local/bin"},
	}

	var provisionBuilds []provisionBuild
	imageOverrides := make(map[string]string)

	for _, spec := range specs {
		if len(spec.pkgs) == 0 {
			continue
		}
		for _, pkg := range spec.pkgs {
			if err := config.ValidatePackageName(pkg); err != nil {
				return "", fmt.Errorf("invalid %s package: %w", spec.cmd, err)
			}
		}
		img := toolProviders[spec.cmd]
		if img == "" {
			return "", fmt.Errorf("provision.%s requires %s in [tools]; add a container image that provides %s", spec.cmd, spec.cmd, spec.cmd)
		}
		exposeBins, err := provisionExposeBins(spec.cmd, spec.pkgs)
		if err != nil {
			return "", fmt.Errorf("invalid exposed %s command: %w", spec.cmd, err)
		}
		imageOverrides[img] = spec.tag
		installArgs := append([]string(nil), spec.installArgs...)
		installArgs = append(installArgs, spec.pkgs...)
		provisionBuilds = append(provisionBuilds, provisionBuild{
			BaseImage:   img,
			CustomTag:   spec.tag,
			InstallArgs: installArgs,
			BinDirs:     spec.binDirs,
			ExposeBins:  exposeBins,
		})
	}

	// Apply image overrides so tool wrappers use custom images with packages pre-installed
	tools := make(map[string][]string)
	for image, cmds := range cfg.Tools {
		if override, ok := imageOverrides[image]; ok {
			tools[override] = cmds
		} else {
			tools[image] = cmds
		}
	}

	funcMap := template.FuncMap{
		"add":        func(a, b int) int { return a + b },
		"yamlQuote":  strconv.Quote,
		"shellQuote": shellQuote,
		"indent": func(n int, s string) string {
			pad := strings.Repeat(" ", n)
			return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
		},
		"join": strings.Join,
	}
	tmpl, err := template.New("lima").Funcs(funcMap).Parse(limaTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	// Generate smart wrappers for package manager commands found in tools
	type wrapperSpec struct {
		cmd         string
		customTag   string
		binDirs     string
		globalCheck string
	}
	wrapperSpecs := []wrapperSpec{
		{"npm", "watermelon-npm", "/usr/local/bin",
			`for _arg in "$@"; do` + "\n" + `  case "$_arg" in -g|--global) _wm_global=true ;; esac` + "\n" + `done`},
		{"pip", "watermelon-pip", "/usr/local/bin",
			`case "$1" in install) _wm_global=true ;; esac`},
		{"cargo", "watermelon-cargo", "/usr/local/cargo/bin /usr/local/bin",
			`case "$1" in install) _wm_global=true ;; esac`},
		{"go", "watermelon-go", "/go/bin /usr/local/bin",
			`case "$1" in install) _wm_global=true ;; esac`},
		{"gem", "watermelon-gem", "/usr/local/bin",
			`case "$1" in install) _wm_global=true ;; esac`},
	}

	var smartWrappers []smartWrapper
	for _, ws := range wrapperSpecs {
		if img := toolProviders[ws.cmd]; img != "" {
			smartWrappers = append(smartWrappers, smartWrapper{
				Cmd:         ws.cmd,
				BaseImage:   img,
				CustomTag:   ws.customTag,
				BinDirs:     ws.binDirs,
				GlobalCheck: ws.globalCheck,
			})
		}
	}

	effectiveToolProviders := make(map[string]string, len(toolProviders))
	for command, image := range toolProviders {
		if override, ok := imageOverrides[image]; ok {
			effectiveToolProviders[command] = override
		} else {
			effectiveToolProviders[command] = image
		}
	}
	networkProcessImages := buildNetworkProcessImages(effectiveToolProviders, cfg.Network.Process)

	// Collect container images that need to be copied to rootful store
	// when network process namespaces are in use.
	var uniqueImages []string
	if len(networkProcessRules) > 0 {
		uniqueImages = collectUniqueImages(tools)
	}

	image := cfg.VM.Image
	if image == "" {
		image = "ubuntu-22.04"
	}
	workdir := config.DefaultWorkdir(cfg)
	containerWorkdirArgs := ""
	helperWorkdirArgs := ""
	if workdir == "" {
		containerWorkdirArgs = `-v "$_WM_WORKDIR:$_WM_WORKDIR" -w "$_WM_WORKDIR"`
		helperWorkdirArgs = `-v "\$_WM_WORKDIR:\$_WM_WORKDIR" -w "\$_WM_WORKDIR"`
	} else {
		if workdir == "/project" {
			containerWorkdirArgs = "-v /project:/project -w /project"
		} else {
			// These arguments are embedded both in generated scripts and inside
			// single-quoted printf operands that create tool wrappers. Validated
			// guest paths cannot contain a double quote, backslash, dollar, or
			// backtick, so strconv.Quote remains safe in both contexts and keeps
			// spaces within one argument.
			containerWorkdirArgs = "-v " + strconv.Quote(workdir+":"+workdir) + " -w " + strconv.Quote(workdir)
		}
		helperWorkdirArgs = containerWorkdirArgs
	}

	preparedScripts := opts.PreparedProvisionScripts
	if preparedScripts == nil {
		prepared, err := PrepareProvisionScripts(projectDir, cfg.Provision.Scripts)
		if err != nil {
			return "", err
		}
		preparedScripts = &prepared
	}
	if err := validatePreparedProvisionScripts(cfg, preparedScripts); err != nil {
		return "", err
	}
	provisionScripts := make([]provisionScript, len(preparedScripts.Contents))
	for index, content := range preparedScripts.Contents {
		provisionScripts[index] = provisionScript{
			Name:   fmt.Sprintf("script-%08d", index),
			Base64: encodeProvisionScript(content),
		}
	}

	bootstrapHostDir := opts.BootstrapHostDir
	if opts.NfqdHostDir != "" {
		if bootstrapHostDir != "" && bootstrapHostDir != opts.NfqdHostDir {
			return "", fmt.Errorf("bootstrap host directory and deprecated nfqd host directory disagree")
		}
		bootstrapHostDir = opts.NfqdHostDir
	}
	if bootstrapHostDir != "" {
		if err := validateProjectDir(bootstrapHostDir); err != nil {
			return "", fmt.Errorf("invalid bootstrap host directory: %w", err)
		}
		if hostPathsOverlap(bootstrapHostDir, projectDir) {
			return "", fmt.Errorf("bootstrap host directory must not overlap the project directory")
		}
		for _, mount := range mounts {
			if mount.Writable && hostPathsOverlap(bootstrapHostDir, mount.Location) {
				return "", fmt.Errorf("bootstrap host directory must not overlap writable mount %q", mount.Location)
			}
		}
	}

	verdictPort := 0
	verdictAuthKey := ""
	nfqdPath := ""
	nfqdSHA256 := ""
	if cfg.Security.Enforcement == "ask" {
		if opts.VerdictServerPort > 0 {
			if err := validatePort(opts.VerdictServerPort); err != nil {
				return "", fmt.Errorf("invalid verdict server port: %w", err)
			}
			verdictPort = opts.VerdictServerPort
		}
		if verdictPort == 0 {
			verdictPort = 39285 // fallback
		}
		for _, forwardedPort := range cfg.Ports.Forward {
			if verdictPort == forwardedPort {
				return "", fmt.Errorf("verdict server port %d conflicts with configured host port forward", verdictPort)
			}
		}
		if !validLowerSHA256(opts.NfqdSHA256) {
			return "", fmt.Errorf("ask enforcement requires a lowercase SHA-256 digest for watermelon-nfqd")
		}
		if !validLowerSHA256(opts.VerdictAuthKey) {
			return "", fmt.Errorf("ask enforcement requires a 32-byte lowercase hexadecimal verdict authentication key")
		}
		if bootstrapHostDir == "" {
			return "", fmt.Errorf("ask enforcement requires a dedicated watermelon-nfqd host bootstrap directory")
		}
		nfqdPath = path.Join(nfqdGuestDir, "watermelon-nfqd")
		nfqdSHA256 = opts.NfqdSHA256
		verdictAuthKey = opts.VerdictAuthKey
	} else if opts.NfqdSHA256 != "" || opts.VerdictAuthKey != "" {
		return "", fmt.Errorf("watermelon-nfqd digest and verdict authentication key are only valid with ask enforcement")
	}

	logHostDir := opts.LogHostDir
	if cfg.Workspace.Disk != "" && logHostDir != "" {
		return "", fmt.Errorf("managed workspaces keep logs on the guest disk and cannot mount writable host log state")
	}
	if logHostDir != "" {
		if err := validateProjectDir(logHostDir); err != nil {
			return "", fmt.Errorf("invalid log host directory: %w", err)
		}
		if hostPathsOverlap(logHostDir, projectDir) {
			return "", fmt.Errorf("log host directory must not overlap the project directory")
		}
		if bootstrapHostDir != "" && hostPathsOverlap(logHostDir, bootstrapHostDir) {
			return "", fmt.Errorf("log host directory must not overlap the read-only watermelon-nfqd bootstrap directory")
		}
		for _, mount := range mounts {
			if hostPathsOverlap(logHostDir, mount.Location) {
				return "", fmt.Errorf("log host directory must not overlap configured mount %q", mount.Location)
			}
		}
	}
	logDir := logStateGuestDir
	logInGuest := false
	if cfg.Workspace.Disk != "" {
		logDir = "/mnt/lima-" + cfg.Workspace.Disk + "/logs"
	} else if logHostDir == "" {
		if mountProject {
			logDir = "/project/.watermelon"
		} else {
			logDir = "/var/log/watermelon"
			logInGuest = true
		}
	}
	logPath := path.Join(logDir, "logs.log")

	sortedProcessNames := make([]string, 0, len(networkProcessRules))
	for proc := range networkProcessRules {
		sortedProcessNames = append(sortedProcessNames, proc)
	}
	sort.Strings(sortedProcessNames)
	processKernelIDs, err := buildProcessKernelIDs(sortedProcessNames, processKernelID)
	if err != nil {
		return "", err
	}

	logUnknown := cfg.Security.Enforcement == "log" || cfg.Security.Enforcement == "fail"
	rejectUnknown := cfg.Security.Enforcement == "fail" || cfg.Security.Enforcement == "silent" || cfg.Security.Enforcement == "ask"

	data := templateData{
		WorkspaceDisk:        cfg.Workspace.Disk,
		FormatWorkspaceDisk:  opts.FormatWorkspaceDisk,
		MinimumLimaVersion:   MinimumSupportedVersion,
		VMType:               vmType,
		VMUser:               "watermelon",
		VMUserUID:            vmUserUID,
		VMUserHome:           "/home/watermelon",
		CPUs:                 cfg.Resources.CPUs,
		Memory:               convertMemory(cfg.Resources.Memory),
		Disk:                 convertDisk(cfg.Resources.Disk),
		Image:                image,
		MountProject:         mountProject,
		ProjectDir:           projectDir,
		Workdir:              workdir,
		DynamicWorkdir:       workdir == "",
		ContainerWorkdirArgs: containerWorkdirArgs,
		HelperWorkdirArgs:    helperWorkdirArgs,
		BootstrapHostDir:     bootstrapHostDir,
		LogHostDir:           logHostDir,
		LogDir:               logDir,
		LogPath:              logPath,
		LogInGuest:           logInGuest,
		Mounts:               mounts,
		NetworkRules:         networkRules,
		NetworkProcessRules:  networkProcessRules,
		NetworkProcessImages: networkProcessImages,
		ProcessKernelIDs:     processKernelIDs,
		SortedProcessNames:   sortedProcessNames,
		PortForwards:         cfg.Ports.Forward,
		Tools:                tools,
		BootstrapImages:      collectUniqueImages(cfg.Tools),
		ProvisionBuilds:      provisionBuilds,
		SmartWrappers:        smartWrappers,
		UniqueImages:         uniqueImages,
		Enforcement:          cfg.Security.Enforcement,
		LogUnknown:           logUnknown,
		RejectUnknown:        rejectUnknown,
		VerdictServerPort:    verdictPort,
		VerdictAuthKey:       verdictAuthKey,
		VerdictAuthKeyPath:   verdictAuthKeyGuestPath,
		NfqdBinaryPath:       nfqdPath,
		NfqdSHA256:           nfqdSHA256,
		GlobalWildcardRules:  globalWildcardRules,
		DNSExactHosts:        dnsExactHosts,
		DNSWildcardHosts:     dnsWildcardHosts,
		ProcessDNSExactHosts: processDNSExactHosts,
		ProcessDNSWildcards:  processDNSWildcards,
		DNSAllowlistOnly:     cfg.Security.Enforcement == "fail" || cfg.Security.Enforcement == "silent",
		ProvisionScripts:     provisionScripts,
	}
	data.SetupImage, err = config.SetupImage(cfg.Setup)
	if err != nil {
		return "", err
	}
	if data.SetupImage != "" {
		data.SmartWrappers = nil
		data.SetupCommands = config.AllSetupCommands(cfg.Setup)
		data.SetupPython = config.SetupHasProfile(cfg.Setup, "python")
		data.SetupGo = config.SetupHasProfile(cfg.Setup, "go")
		data.SetupRust = config.SetupHasProfile(cfg.Setup, "rust")
		profiles, err := config.SetupProfiles(cfg.Setup)
		if err != nil {
			return "", err
		}
		images := map[string]string{}
		for _, profile := range profiles {
			images[profile.Profile], _ = config.SetupImage(profile)
		}
		data.SetupRustImage = images["rust"]
		data.SetupRuntimeAssets, err = prepareRuntimeAssets(cfg, workdir)
		if err != nil {
			return "", err
		}
		data.SetupDefaultVenv = path.Join(workdir, ".venv")
		var pythonDirs []string
		for _, project := range cfg.Setup.Projects {
			if project.Profile == "python" {
				pythonDirs = append(pythonDirs, project.Directory)
			}
		}
		if len(pythonDirs) == 1 {
			data.SetupDefaultVenv = path.Join(workdir, pythonDirs[0], ".venv")
		}
		if len(profiles) > 1 {
			for _, profile := range []string{"python", "rust", "node", "go"} {
				if images[profile] != "" {
					data.SetupBaseImage = images[profile]
					break
				}
			}
			filtered := []string{}
			for _, image := range data.BootstrapImages {
				if image != data.SetupImage {
					filtered = append(filtered, image)
				}
			}
			for _, profile := range profiles {
				image := images[profile.Profile]
				filtered = append(filtered, image)
				if image != data.SetupBaseImage {
					data.SetupComposeSources = append(data.SetupComposeSources, image)
				}
			}
			data.BootstrapImages = filtered
		}
		if err := configureExtendedRuntime(&data, profiles); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

func hostVMType(goos string) (string, error) {
	switch goos {
	case "darwin":
		return "vz", nil
	case "linux":
		return "qemu", nil
	default:
		return "", fmt.Errorf("unsupported host OS %q: watermelon supports macOS and Linux hosts", goos)
	}
}

func validateProjectDir(projectDir string) error {
	if !utf8.ValidString(projectDir) {
		return fmt.Errorf("project directory must be valid UTF-8")
	}
	if strings.IndexByte(projectDir, 0) >= 0 {
		return fmt.Errorf("project directory contains a NUL byte")
	}
	if !filepath.IsAbs(projectDir) {
		return fmt.Errorf("project directory %q must be absolute", projectDir)
	}
	if filepath.Clean(projectDir) != projectDir {
		return fmt.Errorf("project directory %q must be a clean path", projectDir)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func validateInternalMountTargets(mounts []mountData, mountProject bool) error {
	for _, mount := range mounts {
		if mountProject && path.Clean(mount.Target) == "/project" {
			return fmt.Errorf("mount target %q duplicates Watermelon's project mount", mount.Target)
		}
		for _, internal := range []string{nfqdGuestDir, logStateGuestDir} {
			if guestPathsOverlap(mount.Target, internal) {
				return fmt.Errorf("mount target %q overlaps Watermelon-managed guest path %q", mount.Target, internal)
			}
		}
	}
	return nil
}

func guestPathsOverlap(first, second string) bool {
	first = path.Clean(first)
	second = path.Clean(second)
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}

func hostPathsOverlap(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	return first == second || strings.HasPrefix(first, second+string(filepath.Separator)) || strings.HasPrefix(second, first+string(filepath.Separator))
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func buildNetworkRules(values []string) ([]config.NetworkRule, []config.NetworkRule, error) {
	rules := make([]config.NetworkRule, 0, len(values))
	wildcards := make([]config.NetworkRule, 0)
	for _, value := range values {
		rule, err := config.ParseNetworkRule(value)
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, rule)
		if rule.Wildcard {
			wildcards = append(wildcards, rule)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Raw < rules[j].Raw
	})
	sort.Slice(wildcards, func(i, j int) bool {
		return wildcards[i].Raw < wildcards[j].Raw
	})
	return rules, wildcards, nil
}

// dnsHostsForRules separates exact DNS names from wildcard suffixes. Literal
// IP rules do not need resolver policy entries.
func dnsHostsForRules(ruleSets ...[]config.NetworkRule) ([]string, []string) {
	var exact []string
	var wildcards []string
	for _, rules := range ruleSets {
		for _, rule := range rules {
			if _, err := netip.ParseAddr(rule.Host); err == nil {
				continue
			}
			if rule.Wildcard {
				wildcards = append(wildcards, rule.Host)
			} else {
				exact = append(exact, rule.Host)
			}
		}
	}
	return uniqueSortedStrings(exact), uniqueSortedStrings(wildcards)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func buildMounts(mounts map[string]config.Mount, canonicalSources map[string]string) ([]mountData, error) {
	if canonicalSources != nil && len(canonicalSources) != len(mounts) {
		return nil, fmt.Errorf("canonical mount sources do not match configured mounts")
	}
	sources := make([]string, 0, len(mounts))
	for source := range mounts {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	result := make([]mountData, 0, len(sources))
	for _, source := range sources {
		mount := mounts[source]
		location := canonicalSources[source]
		if canonicalSources == nil {
			var err error
			location, err = expandHostPath(source)
			if err != nil {
				return nil, err
			}
		}
		if location == "" || !utf8.ValidString(location) || strings.IndexByte(location, 0) >= 0 || !filepath.IsAbs(location) || filepath.Clean(location) != location {
			return nil, fmt.Errorf("canonical mount source for %q must be a clean absolute path", source)
		}
		result = append(result, mountData{
			Location: location,
			Target:   filepath.Clean(mount.Target),
			Writable: mount.Mode == "rw",
		})
	}
	return result, nil
}

func expandHostPath(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding %q: %w", path, err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding %q: %w", path, err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

// provisionExposeBins returns best-effort CLI command names that should get wrappers
// for provisioned packages even when they already exist in the base image.
func provisionExposeBins(manager string, pkgs []string) ([]string, error) {
	if manager != "npm" {
		return nil, nil
	}

	seen := make(map[string]bool)
	var bins []string
	for _, pkg := range pkgs {
		bin := npmPackageToCommand(pkg)
		if bin == "" || seen[bin] {
			continue
		}
		if err := config.ValidateCommandName(bin); err != nil {
			return nil, fmt.Errorf("derived from package %q: %w", pkg, err)
		}
		seen[bin] = true
		bins = append(bins, bin)
	}
	return bins, nil
}

// npmPackageToCommand converts npm package specs to likely command names:
// - "pnpm" -> "pnpm"
// - "pnpm@10" -> "pnpm"
// - "@scope/name" -> "name"
// - "@scope/name@1.2.3" -> "name"
func npmPackageToCommand(pkg string) string {
	if pkg == "" {
		return ""
	}

	name := pkg
	if strings.HasPrefix(name, "@") {
		if slash := strings.LastIndex(name, "/"); slash >= 0 && slash+1 < len(name) {
			name = name[slash+1:]
		}
	}

	if at := strings.LastIndex(name, "@"); at > 0 {
		name = name[:at]
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.ContainsAny(name, config.ShellMetacharacters+" /") {
		return ""
	}
	return name
}

// convertMemory converts Watermelon's decimal-looking resource syntax to the
// binary unit spelling Lima expects.
func convertMemory(mem string) string {
	return convertResourceSize(mem)
}

// convertDisk converts Watermelon's decimal-looking resource syntax to the
// binary unit spelling Lima expects.
func convertDisk(disk string) string {
	return convertResourceSize(disk)
}

func convertResourceSize(size string) string {
	for _, unit := range []string{"MB", "GB", "TB"} {
		if strings.HasSuffix(size, unit) {
			return strings.TrimSuffix(size, unit) + string(unit[0]) + "iB"
		}
	}
	return size
}

func buildNetworkProcessImages(providers map[string]string, processes map[string][]string) map[string]string {
	images := make(map[string]string)
	for proc := range processes {
		if img := providers[proc]; img != "" {
			images[proc] = img
		}
	}
	return images
}

func collectUniqueImages(tools map[string][]string) []string {
	images := make([]string, 0, len(tools))
	for image := range tools {
		images = append(images, image)
	}
	sort.Strings(images)
	return images
}
