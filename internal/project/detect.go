// Package project inspects bounded declarative metadata. It never evaluates
// project scripts or imports repository configuration into host policy.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
)

const MaxFileSize = 8 << 20
const MaxMetadataSize = 24 << 20

var ManifestNames = []string{"package.json", "package-lock.json", "npm-shrinkwrap.json", ".nvmrc", ".node-version", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb", "requirements.txt", "pyproject.toml", ".python-version", "uv.lock", "poetry.lock", "go.mod", "go.sum", "go.work", "go.work.sum", ".go-version", "Cargo.toml", "Cargo.lock", "rust-toolchain", "rust-toolchain.toml", "pnpm-workspace.yaml", ".yarnrc.yml", "pom.xml", ".java-version", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.properties", "gradle-wrapper.properties", "maven-wrapper.properties", "mvnw", "gradlew", "global.json", "*.csproj", "*.fsproj", "*.vbproj", "*.sln", "*.slnx", "Directory.Build.props", "Directory.Build.targets", "Directory.Packages.props", "NuGet.Config", "nuget.config", "packages.lock.json", "Gemfile", "Gemfile.lock", ".ruby-version", "composer.json", "composer.lock", "CMakeLists.txt", "CMakePresets.json", "CMakeUserPresets.json", "conanfile.txt", "conanfile.py", "vcpkg.json"}

type Plan struct {
	Manager     string     `json:"manager,omitempty"`
	Workspace   string     `json:"workspace,omitempty"`
	Only        string     `json:"only,omitempty"`
	Directory   string     `json:"directory,omitempty"`
	Projects    []Plan     `json:"projects,omitempty"`
	Pinned      bool       `json:"-"`
	Profile     string     `json:"profile"`
	Runtime     string     `json:"runtime"`
	Image       string     `json:"image"`
	Requirement string     `json:"requirement,omitempty"`
	NPMVersion  string     `json:"npm_version,omitempty"`
	Commands    [][]string `json:"commands"`
	Fingerprint string     `json:"fingerprint"`
	Notes       []string   `json:"notes,omitempty"`
}

func detectSingle(files map[string][]byte, profile, runtime string) (Plan, error) {
	has := func(name string) bool { _, ok := files[name]; return ok }
	node, python := has("package.json"), has("requirements.txt") || has("pyproject.toml")
	plan := Plan{Profile: profile, Pinned: runtime != ""}
	switch profile {
	case "node":
		if !node {
			return Plan{}, fmt.Errorf("node setup requires package.json")
		}
		var pkg struct {
			PackageManager string `json:"packageManager"`
			DevEngines     struct {
				PackageManager struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"packageManager"`
			} `json:"devEngines"`
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		if err := json.Unmarshal(files["package.json"], &pkg); err != nil {
			return Plan{}, fmt.Errorf("invalid package.json: %w", err)
		}
		if pkg.PackageManager == "" && pkg.DevEngines.PackageManager.Name != "" {
			pkg.PackageManager = pkg.DevEngines.PackageManager.Name + "@" + pkg.DevEngines.PackageManager.Version
		}
		if err := detectNodeManager(files, pkg.PackageManager, &plan); err != nil {
			return Plan{}, err
		}
		plan.Requirement = pkg.Engines.Node
		if runtime == "" {
			for _, name := range []string{".nvmrc", ".node-version"} {
				if has(name) {
					candidate := strings.TrimPrefix(strings.TrimSpace(string(files[name])), "v")
					if runtime != "" && candidate != runtime {
						return Plan{}, fmt.Errorf("conflicting Node version files; select --runtime explicitly")
					}
					runtime = candidate
					plan.Pinned = true
				}
			}
		}
		if runtime == "" {
			runtime = "24"
			if plan.Requirement != "" {
				runtime = selectRuntime(plan.Requirement, []string{"24", "22"})
			}
		}
		if runtime == "" {
			return Plan{}, fmt.Errorf("no supported Node runtime satisfies engines.node; select a compatible --runtime or use manual tools")
		}
		if err := nodeInstallCommands(files, &plan); err != nil {
			return Plan{}, err
		}
	case "python":
		if !python {
			return Plan{}, fmt.Errorf("python setup requires requirements.txt or pyproject.toml")
		}
		if has("pyproject.toml") {
			var py struct {
				Project struct {
					RequiresPython string `toml:"requires-python"`
				} `toml:"project"`
				Tool map[string]any `toml:"tool"`
			}
			if _, err := toml.Decode(string(files["pyproject.toml"]), &py); err != nil {
				return Plan{}, fmt.Errorf("invalid pyproject.toml: %w", err)
			}
			plan.Requirement = py.Project.RequiresPython
			if plan.Requirement == "" {
				if poetry, ok := py.Tool["poetry"].(map[string]any); ok {
					if deps, ok := poetry["dependencies"].(map[string]any); ok {
						if requirement, ok := deps["python"].(string); ok {
							plan.Requirement = requirement
						}
					}
				}
			}

		}
		if runtime == "" && has(".python-version") {
			runtime = strings.TrimSpace(string(files[".python-version"]))
			plan.Pinned = true
		}
		if runtime == "" {
			runtime = "3.13"
			if plan.Requirement != "" {
				runtime = selectRuntime(plan.Requirement, []string{"3.13", "3.14", "3.12"})
			}
		}
		if runtime == "" {
			return Plan{}, fmt.Errorf("no supported Python runtime satisfies requires-python; select a compatible --runtime or use manual tools")
		}
		if err := pythonInstallCommands(files, &plan); err != nil {
			return Plan{}, err
		}
	case "go":
		var err error
		runtime, err = detectGo(files, runtime, &plan)
		if err != nil {
			return Plan{}, err
		}
	case "rust":
		var err error
		runtime, err = detectRust(files, runtime, &plan)
		if err != nil {
			return Plan{}, err
		}
	case "java", "dotnet", "ruby", "php", "cpp":
		var err error
		runtime, err = detectExtended(files, profile, runtime, &plan)
		if err != nil {
			return Plan{}, err
		}
	default:
		return Plan{}, fmt.Errorf("unsupported setup profile %q", profile)
	}
	plan.Runtime = runtime
	var err error
	plan.Image, err = config.SetupImage(config.SetupConfig{Profile: profile, Runtime: runtime})
	if err != nil {
		return Plan{}, err
	}
	if plan.Requirement != "" {
		if _, err := MatchesRuntime(runtime, plan.Requirement); err != nil {
			return Plan{}, err
		}
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", profile, runtime)
	for _, name := range keys {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(files[name]))
		h.Write(files[name])
	}
	plan.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return plan, nil
}

func chooseRuntime(requirement string, candidates []string) string {
	for _, candidate := range candidates {
		ok, err := MatchesRuntime(candidate, requirement)
		if err == nil && ok {
			return candidate
		}
		// A series image contains its current patch, not necessarily .0. A
		// lower bound such as >=3.13.1 can still use the 3.13 series. Select a
		// potentially compatible series here; verify the actual image version
		// before running any project installation commands.
		for _, alternative := range strings.Split(requirement, "||") {
			for _, token := range strings.Fields(strings.ReplaceAll(alternative, ",", " ")) {
				match := comparator.FindStringSubmatch(token)
				if match == nil {
					continue
				}
				parts := []string{}
				for _, part := range match[2:] {
					if part == "" || part == "x" || part == "*" {
						break
					}
					parts = append(parts, part)
				}
				boundary := strings.Join(parts, ".")
				if boundary != candidate && !strings.HasPrefix(boundary, candidate+".") {
					continue
				}
				if ok, err := MatchesRuntime(boundary, requirement); err == nil && ok {
					return candidate
				}
			}
		}
	}
	return ""
}
