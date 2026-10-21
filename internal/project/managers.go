package project

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
	"gopkg.in/yaml.v3"
)

func detectNodeManager(files map[string][]byte, declaration string, plan *Plan) error {
	has := func(name string) bool { _, ok := files[name]; return ok }
	manager, version, declared := strings.Cut(declaration, "@")
	if !declared && declaration != "" {
		return fmt.Errorf("packageManager must pin npm, pnpm or Yarn to a numeric version")
	}
	if has("bun.lock") || has("bun.lockb") {
		return fmt.Errorf("Bun projects require manual setup")
	}
	if declaration == "" {
		if has("pnpm-lock.yaml") || has("pnpm-workspace.yaml") {
			manager = "pnpm"
			version = config.DefaultPNPMVersion
		}
		if has("yarn.lock") {
			if manager != "" {
				return fmt.Errorf("conflicting pnpm and Yarn lockfiles; declare packageManager")
			}
			manager = "yarn"
			version = "1.22.22"
			if strings.Contains(string(files["yarn.lock"]), "__metadata:") {
				version = config.DefaultYarnVersion
			}
		}
		if has(".yarnrc.yml") {
			var rc struct {
				YarnPath string `yaml:"yarnPath"`
			}
			if err := yaml.Unmarshal(files[".yarnrc.yml"], &rc); err != nil {
				return fmt.Errorf("invalid .yarnrc.yml: %w", err)
			}
			if rc.YarnPath != "" {
				match := regexp.MustCompile(`(?:^|/)yarn-([0-9]+\.[0-9]+\.[0-9]+)\.cjs$`).FindStringSubmatch(rc.YarnPath)
				if len(match) != 2 {
					return fmt.Errorf("yarnPath must identify a numeric Yarn release, or declare packageManager")
				}
				manager = "yarn"
				version = match[1]
			}
		}
	}
	if manager == "" {
		manager = "npm"
	}
	if manager == "npm" {
		if declaration != "" {
			if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
				return fmt.Errorf("packageManager must pin a numeric npm version")
			}
			plan.NPMVersion = version
		}
		if has("pnpm-lock.yaml") || has("yarn.lock") {
			return fmt.Errorf("npm packageManager conflicts with another manager's lockfile")
		}
		return nil
	}
	plan.Manager = manager + "@" + version
	return config.ValidateManager("node", plan.Manager)
}

func nodeInstallCommands(files map[string][]byte, plan *Plan) error {
	has := func(name string) bool { _, ok := files[name]; return ok }
	name, version, _ := strings.Cut(plan.Manager, "@")
	switch name {
	case "pnpm":
		if has("pnpm-workspace.yaml") {
			var workspace struct {
				Packages []string `yaml:"packages"`
			}
			if err := yaml.Unmarshal(files["pnpm-workspace.yaml"], &workspace); err != nil {
				return fmt.Errorf("invalid pnpm-workspace.yaml: %w", err)
			}
		}
		args := []string{"pnpm", "install", "--config.engine-strict=true"}
		if has("pnpm-lock.yaml") {
			args = append(args, "--frozen-lockfile")
		}
		plan.Commands = [][]string{args}
	case "yarn":
		args := []string{"yarn", "install"}
		if has("yarn.lock") {
			flag := "--immutable"
			if strings.HasPrefix(version, "1.") {
				flag = "--frozen-lockfile"
			}
			args = append(args, flag)
		}
		plan.Commands = [][]string{args}
	default:
		install := "install"
		if has("package-lock.json") || has("npm-shrinkwrap.json") {
			install = "ci"
		} else {
			plan.Notes = append(plan.Notes, "No npm lockfile: dependency resolution is not reproducible until a lockfile is recorded.")
		}
		plan.Commands = [][]string{{"npm", install, "--engine-strict", "--no-audit", "--no-fund"}}
	}
	if plan.Manager != "" {
		plan.Notes = append(plan.Notes, "The pinned package manager is downloaded into the persistent guest tool home under the approved network policy.")
	}
	return nil
}

func pythonInstallCommands(files map[string][]byte, plan *Plan) error {
	has := func(name string) bool { _, ok := files[name]; return ok }
	var py struct {
		Project struct {
			RequiresPython string `toml:"requires-python"`
		} `toml:"project"`
		Tool struct {
			UV *struct {
				RequiredVersion string `toml:"required-version"`
			} `toml:"uv"`
			Poetry *struct {
				RequiresPoetry string         `toml:"requires-poetry"`
				Dependencies   map[string]any `toml:"dependencies"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	if has("pyproject.toml") {
		if _, err := toml.Decode(string(files["pyproject.toml"]), &py); err != nil {
			return err
		}
	}
	if has("uv.lock") && (has("poetry.lock") || py.Tool.Poetry != nil) {
		return fmt.Errorf("conflicting uv and Poetry metadata")
	}
	if has("uv.lock") || py.Tool.UV != nil {
		version := config.DefaultUVVersion
		if py.Tool.UV != nil {
			var err error
			version, err = managerVersion(py.Tool.UV.RequiredVersion, version)
			if err != nil {
				return err
			}
		}
		plan.Manager = "uv@" + version
		args := []string{"uv", "sync", "--no-managed-python", "--python", "/usr/local/bin/python", "--all-packages"}
		if has("uv.lock") {
			args = append(args, "--locked")
		}
		plan.Commands = [][]string{args}
	} else if has("poetry.lock") || py.Tool.Poetry != nil {
		version := config.DefaultPoetryVersion
		if py.Tool.Poetry != nil {
			var err error
			version, err = managerVersion(py.Tool.Poetry.RequiresPoetry, version)
			if err != nil {
				return err
			}
		}
		plan.Manager = "poetry@" + version
		// Poetry's own environment is isolated from the project interpreter.
		plan.Commands = [][]string{{"poetry", "env", "use", "/usr/local/bin/python"}, {"poetry", "install", "--no-interaction"}}
		if has("poetry.lock") {
			plan.Commands[1] = []string{"poetry", "sync", "--no-interaction"}
		}
	} else {
		plan.Commands = [][]string{{"/usr/local/bin/python", "-m", "venv", "--clear", ".venv"}}
		if has("requirements.txt") {
			plan.Commands = append(plan.Commands, []string{"python", "-m", "pip", "install", "-r", "requirements.txt"})
		} else {
			plan.Commands = append(plan.Commands, []string{"python", "-m", "pip", "install", "-e", "."})
		}
		plan.Notes = append(plan.Notes, "pip honors declared requirements; unpinned requirements are not reproducible. Use up --refresh after changing included requirement files.")
	}
	return config.ValidateManager("python", plan.Manager)
}

func managerVersion(requirement, fallback string) (string, error) {
	if requirement == "" {
		return fallback, nil
	}
	if ok, err := MatchesRuntime(fallback, requirement); err == nil && ok {
		return fallback, nil
	}
	exact := strings.TrimPrefix(strings.TrimPrefix(requirement, "=="), "=")
	if regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(exact) {
		return exact, nil
	}
	return "", fmt.Errorf("package-manager requirement %q is incompatible with bundled default %s; pin an exact supported manager version", requirement, fallback)
}
