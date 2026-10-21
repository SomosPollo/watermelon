package config

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Lima prefixes filesystem labels with "lima-". ext4 has a 16-byte label
// limit, so the disk name must fit in the remaining eleven bytes.
var workspaceDiskPattern = regexp.MustCompile(`^w[a-f0-9]{10}$`)
var runtimeVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

const DefaultGoRuntime = "1.27"
const DefaultRustRuntime = "1.99"

// SetupCommands is shared by profile validation and guest wrapper generation.
func SetupCommands(profile string) []string {
	switch profile {
	case "node":
		return []string{"node", "npm", "npx"}
	case "python":
		return []string{"python", "python3", "pip"}
	case "go":
		return []string{"go", "gofmt"}
	case "rust":
		return []string{"cargo", "rustc", "rustdoc", "rustfmt"}
	case "java":
		return []string{"java", "javac", "jar", "mvn", "gradle"}
	case "dotnet":
		return []string{"dotnet"}
	case "ruby":
		return []string{"ruby", "gem", "bundle", "bundler"}
	case "php":
		return []string{"php", "composer"}
	case "cpp":
		return []string{"gcc", "g++", "cc", "c++", "clang", "clang++", "cmake", "ctest", "cpack", "ninja", "make", "pkg-config"}
	default:
		return nil
	}
}

// PublicRepositoryURL deliberately accepts only credential-free public GitHub
// HTTPS URLs. SSH agents, URL helpers, local paths and submodules are not an
// implicit credential or execution channel in the first release.
func PublicRepositoryURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", fmt.Errorf("repository must be a public https://github.com/owner/repository URL without credentials, query or fragment")
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "" {
		return "", fmt.Errorf("repository URL must name exactly one owner and repository")
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "-") {
			return "", fmt.Errorf("invalid repository URL path")
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
				return "", fmt.Errorf("invalid repository URL path")
			}
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if strings.TrimSuffix(parts[2], ".git") == "" {
		return "", fmt.Errorf("repository name is empty")
	}
	return u.String(), nil
}

// SetupImage maps a bounded runtime declaration to a maintained full image.
// Full Debian images supply common native-build prerequisites; project code
// never selects a privileged host installation command.
func SetupImage(setup SetupConfig) (string, error) {
	if setup.Profile != "multi" {
		if err := ValidateManager(setup.Profile, setup.Manager); err != nil {
			return "", err
		}
	}

	if setup.Only != "" && SetupCommands(setup.Only) == nil {
		return "", fmt.Errorf("invalid setup.only language")
	}
	if setup.Only != "" && setup.Profile != "multi" && setup.Only != setup.Profile {
		return "", fmt.Errorf("setup.only must match the profile")
	}
	if setup.Profile == "multi" {
		profiles, err := SetupProfiles(setup)
		if err != nil {
			return "", err
		}
		images := []string{}
		for _, p := range profiles {
			image, err := SetupImage(p)
			if err != nil {
				return "", err
			}
			images = append(images, image)
		}
		if len(images) == 1 {
			return images[0], nil
		}
		return fmt.Sprintf("watermelon-project:%x", sha256.Sum256([]byte(strings.Join(images, "\x00"))))[:len("watermelon-project:")+24], nil
	}
	if len(setup.Runtimes) != 0 || len(setup.Projects) != 0 {
		return "", fmt.Errorf("setup.runtimes and setup.projects require setup.profile = multi")
	}
	runtime := setup.Runtime
	switch setup.Profile {
	case "":
		if runtime != "" {
			return "", fmt.Errorf("setup.runtime requires setup.profile")
		}
		return "", nil
	case "node":
		if runtime == "" {
			runtime = "24"
		}
		if !runtimeVersionPattern.MatchString(runtime) || (strings.Split(runtime, ".")[0] != "22" && strings.Split(runtime, ".")[0] != "24") {
			return "", fmt.Errorf("setup.runtime: Node profiles support version 22 or 24 (optionally a numeric minor/patch)")
		}
		return "node:" + runtime + "-bookworm", nil
	case "python":
		if runtime == "" {
			runtime = "3.13"
		}
		parts := strings.Split(runtime, ".")
		if !runtimeVersionPattern.MatchString(runtime) || len(parts) < 2 || parts[0] != "3" || (parts[1] != "12" && parts[1] != "13" && parts[1] != "14") {
			return "", fmt.Errorf("setup.runtime: Python profiles support 3.12, 3.13 or 3.14 (optionally a numeric patch)")
		}
		return "python:" + runtime + "-bookworm", nil
	case "go", "rust":
		if runtime == "" {
			runtime = DefaultGoRuntime
			if setup.Profile == "rust" {
				runtime = DefaultRustRuntime
			}
		}
		parts := strings.Split(runtime, ".")
		if !runtimeVersionPattern.MatchString(runtime) || len(parts) < 2 || parts[0] != "1" {
			return "", fmt.Errorf("setup.runtime: %s requires a numeric stable 1.minor or 1.minor.patch version", setup.Profile)
		}
		image := "golang"
		if setup.Profile == "rust" {
			image = "rust"
		}
		return image + ":" + runtime + "-bookworm", nil
	case "java", "dotnet", "ruby", "php", "cpp":
		return extendedSetupImage(setup)
	default:
		return "", fmt.Errorf("setup.profile %q is unsupported; choose node, python, go, rust, java, dotnet, ruby, php or cpp, or use manual configuration", setup.Profile)
	}
}

var ProfileOrder = []string{"node", "python", "go", "rust", "java", "dotnet", "ruby", "php", "cpp"}

func ValidProjectDirectory(dir string) bool {
	return dir != "" && len(dir) <= 1024 && path.Clean(dir) == dir && !path.IsAbs(dir) && dir != ".." && !strings.HasPrefix(dir, "../") && !strings.ContainsAny(dir, "\\\x00\r\n")
}

// SetupProfiles returns stable, validated per-language image declarations.
func SetupProfiles(setup SetupConfig) ([]SetupConfig, error) {
	if setup.Profile != "multi" {
		if _, err := SetupImage(setup); err != nil {
			return nil, err
		}
		if setup.Profile == "" {
			return nil, nil
		}
		return []SetupConfig{setup}, nil
	}
	if setup.Manager != "" || setup.Runtime != "" || len(setup.Runtimes) == 0 || len(setup.Runtimes) > len(ProfileOrder) || len(setup.Projects) == 0 || len(setup.Projects) > 64 {
		return nil, fmt.Errorf("multi setup requires runtimes and 1–64 projects, without setup.runtime")
	}
	if setup.Only != "" && (len(setup.Runtimes) != 1 || setup.Runtimes[setup.Only] == "") {
		return nil, fmt.Errorf("setup.only must match the sole selected language")
	}
	profiles := []SetupConfig{}
	for _, name := range ProfileOrder {
		if runtime, ok := setup.Runtimes[name]; ok {
			p := SetupConfig{Profile: name, Runtime: runtime}
			if _, err := SetupImage(p); err != nil {
				return nil, err
			}
			profiles = append(profiles, p)
		}
	}
	if len(profiles) != len(setup.Runtimes) {
		return nil, fmt.Errorf("unknown language in setup.runtimes")
	}
	seen, used := map[string]bool{}, map[string]bool{}
	for _, project := range setup.Projects {
		_, known := setup.Runtimes[project.Profile]
		key := project.Directory + "\x00" + project.Profile
		if !ValidProjectDirectory(project.Directory) || !known || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate setup project %q (%s)", project.Directory, project.Profile)
		}
		if err := ValidateManager(project.Profile, project.Manager); err != nil {
			return nil, err
		}
		seen[key], used[project.Profile] = true, true
	}
	if len(used) != len(profiles) {
		return nil, fmt.Errorf("each setup runtime requires a project")
	}
	return profiles, nil
}

func AllSetupCommands(setup SetupConfig) []string {
	profiles, err := SetupProfiles(setup)
	if err != nil {
		return nil
	}
	var commands []string
	for _, profile := range profiles {
		commands = append(commands, SetupCommands(profile.Profile)...)
	}
	seen := map[string]bool{}
	for _, command := range commands {
		seen[command] = true
	}
	managers := []string{setup.Manager}
	for _, project := range setup.Projects {
		managers = append(managers, project.Manager)
	}
	for _, manager := range managers {
		name, _, _ := strings.Cut(manager, "@")
		if name != "" && !seen[name] {
			commands = append(commands, name)
			seen[name] = true
		}
	}
	return commands
}

func SetupHasProfile(setup SetupConfig, profile string) bool {
	if setup.Profile == profile {
		return true
	}
	_, ok := setup.Runtimes[profile]
	return setup.Profile == "multi" && ok
}

func validateProjectSetup(cfg *Config, providers map[string]string) error {
	image, err := SetupImage(cfg.Setup)
	if err != nil {
		return err
	}
	if image != "" {
		if len(cfg.Provision.Npm)+len(cfg.Provision.Pip)+len(cfg.Provision.Cargo)+len(cfg.Provision.Go)+len(cfg.Provision.Gem) != 0 {
			return fmt.Errorf("setup profiles install project dependencies; use manual tools for provision packages")
		}
		if len(cfg.Network.Process) != 0 {
			return fmt.Errorf("setup profiles cannot be combined with network.process; use manual tools for per-command policies")
		}
		for _, command := range AllSetupCommands(cfg.Setup) {
			if providers[command] != image {
				return fmt.Errorf("setup.profile %s requires tool %q from %q", cfg.Setup.Profile, command, image)
			}
		}
		if DefaultWorkdir(cfg) == "" {
			return fmt.Errorf("setup.profile requires an explicit guest workdir or the project mount")
		}
	}
	if cfg.Workspace.Disk == "" {
		if cfg.Workspace.Source != "" {
			return fmt.Errorf("workspace.source requires workspace.disk")
		}
		return nil
	}
	if !workspaceDiskPattern.MatchString(cfg.Workspace.Disk) {
		return fmt.Errorf("workspace.disk must be a Watermelon-managed disk name")
	}
	if cfg.VM.Name != "" || len(cfg.Provision.Scripts) != 0 {
		return fmt.Errorf("managed workspaces use their owning directory's VM name and do not accept root provision scripts")
	}
	if MountProjectEnabled(&cfg.VM) || len(cfg.Mounts) != 0 || cfg.VM.Workdir != "/workspace/project" {
		return fmt.Errorf("managed workspaces require vm.mount_project = false, vm.workdir = /workspace/project and no additional host mounts")
	}
	if _, err := PublicRepositoryURL(cfg.Workspace.Source); err != nil {
		return fmt.Errorf("workspace.source: %w", err)
	}
	return nil
}
