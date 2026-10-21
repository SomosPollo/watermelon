package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

func (p Plan) Parts() []Plan {
	if len(p.Projects) != 0 {
		return p.Projects
	}
	p.Directory = "."
	return []Plan{p}
}

func (p Plan) Setup() config.SetupConfig {
	if len(p.Projects) == 0 {
		return config.SetupConfig{Profile: p.Profile, Runtime: p.Runtime, Only: p.Only, Manager: p.Manager}
	}
	setup := config.SetupConfig{Profile: "multi", Runtimes: map[string]string{}, Only: p.Only}
	for _, part := range p.Projects {
		setup.Runtimes[part.Profile] = part.Runtime
		setup.Projects = append(setup.Projects, config.SetupProject{Directory: part.Directory, Profile: part.Profile, Manager: part.Manager})
	}
	return setup
}

// Detect automatically composes all supported projects. An explicit template
// narrows detection; a qualified runtime string overrides versions by language.
func Detect(files map[string][]byte, profile, runtime string) (Plan, error) {
	overrides, err := RuntimeOverrides(runtime)
	if err != nil {
		return Plan{}, err
	}
	if !strings.Contains(runtime, "=") && runtime != "" {
		target := profile
		if target == "" || target == "all" || target == "multi" {
			languages := ProfilesForFiles(files)
			if len(languages) != 1 {
				return Plan{}, fmt.Errorf("use qualified runtime overrides (node=22,python=3.13) for multiple languages")
			}
			for name := range languages {
				target = name
			}
		}
		overrides[target] = runtime
	}
	return detectProjects(files, profile, overrides)
}

// RuntimeOverrides validates qualified overrides before clone creates a workspace.
func RuntimeOverrides(runtime string) (map[string]string, error) {
	overrides := map[string]string{}
	if !strings.Contains(runtime, "=") {
		return overrides, nil
	}
	for _, item := range strings.Split(runtime, ",") {
		name, version, ok := strings.Cut(item, "=")
		_, duplicate := overrides[name]
		if !ok || version == "" || duplicate || config.SetupCommands(name) == nil {
			return nil, fmt.Errorf("runtime overrides use language=version, without duplicates: %q", item)
		}
		if _, err := config.SetupImage(config.SetupConfig{Profile: name, Runtime: version}); err != nil {
			return nil, fmt.Errorf("invalid runtime override %q: %w", item, err)
		}
		overrides[name] = version
	}
	return overrides, nil
}

func DetectConfigured(files map[string][]byte, setup config.SetupConfig) (Plan, error) {
	return DetectConfiguredWithRuntime(files, setup, "")
}

// Explicit up overrides replace selected versions while keeping the remaining
// reviewed language choices. A changed result still requires CLI approval.
func DetectConfiguredWithRuntime(files map[string][]byte, setup config.SetupConfig, runtime string) (Plan, error) {
	overrides := setup.Runtimes
	if setup.Profile != "multi" {
		overrides = map[string]string{setup.Profile: setup.Runtime}
	}
	// Retain reviewed version choices for languages still present. Removing the
	// last project of a language removes its toolchain in the next approval plan.
	present := ProfilesForFiles(files)
	filtered := map[string]string{}
	for name, version := range overrides {
		if present[name] {
			filtered[name] = version
		}
	}
	requested, err := RuntimeOverrides(runtime)
	if err != nil {
		return Plan{}, err
	}
	if runtime != "" && !strings.Contains(runtime, "=") {
		if setup.Only != "" && present[setup.Only] {
			requested[setup.Only] = runtime
		} else {
			if len(present) != 1 {
				return Plan{}, fmt.Errorf("use qualified runtime overrides (node=22,python=3.13) for multiple languages")
			}
			for name := range present {
				requested[name] = runtime
			}
		}
	}
	for name, version := range requested {
		filtered[name] = version
	}
	return detectProjects(files, setup.Only, filtered)
}

func detectProjects(files map[string][]byte, selected string, overrides map[string]string) (Plan, error) {
	grouped := map[string]map[string][]byte{}
	for name, data := range files {
		if !ValidManifestPath(name) {
			return Plan{}, fmt.Errorf("unexpected project metadata path %q", name)
		}
		dir := path.Dir(name)
		if grouped[dir] == nil {
			grouped[dir] = map[string][]byte{}
		}
		grouped[dir][path.Base(name)] = data
	}
	dirs := []string{}
	for dir := range grouped {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var jobs []Plan
	for _, dir := range dirs {
		metadata := grouped[dir]
		for _, profile := range config.ProfileOrder {
			if selected != "" && selected != "all" && selected != "multi" && profile != selected {
				continue
			}
			if !ProfilesForFiles(metadata)[profile] {
				continue
			}
			metadata, err := projectMetadata(grouped, dir, profile)
			if err != nil {
				return Plan{}, err
			}
			plan, err := detectSingle(metadata, profile, overrides[profile])
			if err != nil {
				return Plan{}, fmt.Errorf("project %q: %w", dir, err)
			}
			plan.Directory = dir
			plan.Workspace = workspaceOwner(grouped, dir, profile)
			if plan.Workspace != "" {
				plan.Commands = nil
				plan.Notes = append(plan.Notes, fmt.Sprintf("Dependencies are installed by the workspace at %q.", plan.Workspace))
			}
			jobs = append(jobs, plan)
		}
	}
	if len(jobs) == 0 {
		return Plan{}, fmt.Errorf("no supported project manifest found; automatic setup supports node, python, go, rust, java, dotnet, ruby, php and cpp")
	}
	if len(jobs) > 64 {
		return Plan{}, fmt.Errorf("automatic setup supports at most 64 projects")
	}
	for name := range overrides {
		found := false
		for _, p := range jobs {
			found = found || p.Profile == name
		}
		if !found {
			return Plan{}, fmt.Errorf("runtime override for undetected language %q", name)
		}
	}
	for _, profile := range config.ProfileOrder {
		var candidates []string
		for _, job := range jobs {
			if job.Profile == profile {
				candidates = append(candidates, job.Runtime)
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			a, _ := versionParts(candidates[i])
			b, _ := versionParts(candidates[j])
			return compareVersion(a, b) > 0
		})
		chosen := ""
		for _, candidate := range candidates {
			valid := true
			for _, job := range jobs {
				if job.Profile == profile {
					if job.Pinned {
						ok, _ := MatchesRuntime(candidate, job.Runtime)
						valid = valid && ok
					}
					if job.Requirement != "" {
						valid = valid && chooseRuntime(job.Requirement, []string{candidate}) != ""
					}
				}
			}
			if valid {
				chosen = candidate
				break
			}
		}
		if len(candidates) != 0 && chosen == "" {
			return Plan{}, fmt.Errorf("conflicting %s runtime requirements across projects; choose a compatible explicit --runtime %s=version override or use manual configuration", profile, profile)
		}
		for i, job := range jobs {
			if job.Profile == profile {
				metadata, err := projectMetadata(grouped, job.Directory, profile)
				if err != nil {
					return Plan{}, err
				}
				plan, err := detectSingle(metadata, profile, chosen)
				if err != nil {
					return Plan{}, err
				}
				plan.Directory = job.Directory
				plan.Workspace = job.Workspace
				if job.Workspace != "" {
					plan.Commands = nil
					plan.Notes = append(plan.Notes, fmt.Sprintf("Dependencies are installed by the workspace at %q.", job.Workspace))
				}
				jobs[i] = plan
			}
		}
	}
	// Tool-only fetches and Python environments precede npm hooks, whose builds
	// may invoke any of the other languages in the shared image.
	order := map[string]int{"go": 0, "rust": 1, "java": 2, "dotnet": 3, "cpp": 4, "ruby": 5, "php": 6, "python": 7, "node": 8}
	sort.SliceStable(jobs, func(i, j int) bool {
		if order[jobs[i].Profile] != order[jobs[j].Profile] {
			return order[jobs[i].Profile] < order[jobs[j].Profile]
		}
		return jobs[i].Directory < jobs[j].Directory
	})
	encoded, _ := json.Marshal(struct {
		Projects []Plan
		Files    map[string][]byte
	}{jobs, files})
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	if len(jobs) == 1 && jobs[0].Directory == "." {
		jobs[0].Fingerprint = fingerprint
		if selected != "all" && selected != "multi" {
			jobs[0].Only = selected
		}
		return jobs[0], nil
	}
	plan := Plan{Profile: "multi", Projects: jobs}
	if selected != "all" && selected != "multi" {
		plan.Only = selected
	}
	image, err := config.SetupImage(plan.Setup())
	if err != nil {
		return Plan{}, err
	}
	plan.Image = image
	plan.Fingerprint = fingerprint
	return plan, nil
}

func workspaceOwner(groups map[string]map[string][]byte, dir, profile string) string {
	for parent := path.Dir(dir); dir != "."; parent = path.Dir(parent) {
		relative, err := relativeProjectDir(parent, dir)
		if err != nil {
			return ""
		}
		metadata := groups[parent]
		switch profile {
		case "go":
			if _, own := groups[dir]["go.work"]; own {
				return ""
			}
			if data, ok := metadata["go.work"]; ok {
				if work, err := modfile.ParseWork("go.work", data, nil); err == nil {
					for _, use := range work.Use {
						if path.Clean(use.Path) == relative {
							return parent
						}
					}
				}
			}
		case "node":
			if data, ok := metadata["pnpm-workspace.yaml"]; ok {
				var workspace struct {
					Packages []string `yaml:"packages"`
				}
				if yaml.Unmarshal(data, &workspace) == nil && workspacePatterns(workspace.Packages, nil, relative) {
					return parent
				}
			}
			var pkg struct {
				Workspaces json.RawMessage `json:"workspaces"`
			}
			if json.Unmarshal(metadata["package.json"], &pkg) == nil && len(pkg.Workspaces) != 0 {
				var patterns []string
				if json.Unmarshal(pkg.Workspaces, &patterns) != nil {
					var object struct {
						Packages []string `json:"packages"`
					}
					_ = json.Unmarshal(pkg.Workspaces, &object)
					patterns = object.Packages
				}
				for _, pattern := range patterns {
					if workspaceMatch(pattern, relative) {
						return parent
					}
				}
			}
		case "python":
			var py struct {
				Tool struct {
					UV struct {
						Workspace struct{ Members, Exclude []string } `toml:"workspace"`
					} `toml:"uv"`
				} `toml:"tool"`
			}
			if _, err := toml.Decode(string(metadata["pyproject.toml"]), &py); err == nil && workspacePatterns(py.Tool.UV.Workspace.Members, py.Tool.UV.Workspace.Exclude, relative) {
				return parent
			}
		case "java":
			if data, ok := metadata["pom.xml"]; ok {
				var pom struct {
					Modules []string `xml:"modules>module"`
				}
				if xml.Unmarshal(data, &pom) == nil {
					for _, module := range pom.Modules {
						if path.Clean(module) == relative {
							return parent
						}
					}
				}
			} else if _, own := groups[dir]["settings.gradle"]; !own {
				_, ownKotlin := groups[dir]["settings.gradle.kts"]
				_, groovy := metadata["settings.gradle"]
				_, kotlin := metadata["settings.gradle.kts"]
				if !ownKotlin && (groovy || kotlin) && gradleIncludes(metadata, relative) {
					return parent
				}
			}
		case "rust":
			var cargo struct {
				Workspace *struct {
					Exclude []string `toml:"exclude"`
					Members []string `toml:"members"`
				} `toml:"workspace"`
			}
			var child struct {
				Workspace any `toml:"workspace"`
			}
			_, _ = toml.Decode(string(groups[dir]["Cargo.toml"]), &child)
			if _, err := toml.Decode(string(metadata["Cargo.toml"]), &cargo); err == nil && cargo.Workspace != nil && child.Workspace == nil {
				excluded := false
				for _, pattern := range cargo.Workspace.Exclude {
					excluded = excluded || workspaceMatch(pattern, relative)
				}
				for _, pattern := range cargo.Workspace.Members {
					if !excluded && workspaceMatch(pattern, relative) {
						return parent
					}
				}
			}
		}
		if parent == "." {
			break
		}
	}
	return ""
}

// A nested build file is not sufficient evidence of Gradle membership. Only
// literal include declarations suppress its own installation; arbitrary
// settings code is left for Gradle to evaluate under the sandbox policy.
func gradleIncludes(files map[string][]byte, relative string) bool {
	declaration := regexp.MustCompile(`(?m)^[\t ]*include\b[\t ]*(\([^)]*\)|[^\r\n]+)`)
	literal := regexp.MustCompile(`["']([A-Za-z0-9_:/.-]+)["']`)
	arguments := regexp.MustCompile(`^["'][A-Za-z0-9_:/.-]+["'](?:\s*,\s*["'][A-Za-z0-9_:/.-]+["'])*\s*,?$`)
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		for _, match := range declaration.FindAllSubmatch(files[name], -1) {
			args := strings.TrimSpace(string(match[1]))
			if strings.HasPrefix(args, "(") {
				args = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(args, "("), ")"))
			} else if offset := strings.Index(args, "//"); offset >= 0 {
				args = strings.TrimSpace(args[:offset])
			}
			if !arguments.MatchString(args) {
				continue
			}
			for _, member := range literal.FindAllStringSubmatch(args, -1) {
				dir := strings.ReplaceAll(strings.TrimPrefix(member[1], ":"), ":", "/")
				if dir == relative || strings.HasPrefix(dir, relative+"/") {
					return true
				}
			}
		}
	}
	return false
}

func relativeProjectDir(parent, child string) (string, error) {
	if parent == "." {
		return child, nil
	}
	if strings.HasPrefix(child, parent+"/") {
		return strings.TrimPrefix(child, parent+"/"), nil
	}
	return "", fmt.Errorf("unrelated paths")
}

// Version files are inherited from the nearest ancestor, as toolchains normally
// do. Only fixed metadata is copied; repository code is never evaluated here.
func projectMetadata(groups map[string]map[string][]byte, dir, profile string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, value := range groups[dir] {
		files[name] = value
	}
	names := map[string][]string{"node": {".nvmrc", ".node-version"}, "python": {".python-version"}, "go": {".go-version"}, "rust": {"rust-toolchain", "rust-toolchain.toml"}, "java": {".java-version"}, "dotnet": {"global.json"}, "ruby": {".ruby-version"}}[profile]
	for ancestor := dir; ; ancestor = path.Dir(ancestor) {
		found := false
		for _, name := range names {
			if value, ok := groups[ancestor][name]; ok {
				files[name] = value
				found = true
			}
		}
		if found || ancestor == "." {
			break
		}
	}
	if profile == "go" {
		if owner := workspaceOwner(groups, dir, profile); owner != "" {
			// go.work controls toolchain suggestions for all its modules. Member
			// go minimums still contribute to the common runtime requirement.
			files["go.work"] = groups[owner]["go.work"]
		}
	}
	if profile == "rust" {
		var child map[string]any
		if _, err := toml.Decode(string(files["Cargo.toml"]), &child); err != nil {
			return nil, err
		}
		pkg, _ := child["package"].(map[string]any)
		if inherited, ok := pkg["rust-version"].(map[string]any); ok && inherited["workspace"] == true {
			owner := workspaceOwner(groups, dir, profile)
			if owner != "" {
				var parent struct {
					Workspace struct {
						Package struct {
							RustVersion string `toml:"rust-version"`
						} `toml:"package"`
					} `toml:"workspace"`
				}
				if _, err := toml.Decode(string(groups[owner]["Cargo.toml"]), &parent); err != nil {
					return nil, err
				}
				if parent.Workspace.Package.RustVersion != "" {
					pkg["rust-version"] = parent.Workspace.Package.RustVersion
					var err error
					files["Cargo.toml"], err = toml.Marshal(child)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return files, nil
}

func workspaceMatch(pattern, name string) bool {
	p, n := strings.Split(strings.TrimPrefix(pattern, "./"), "/"), strings.Split(name, "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(p) {
			return j == len(n)
		}
		if p[i] == "**" {
			return match(i+1, j) || (j < len(n) && match(i, j+1))
		}
		if j == len(n) {
			return false
		}
		ok, _ := path.Match(p[i], n[j])
		return ok && match(i+1, j+1)
	}
	return len(p) <= MaxDepth+1 && len(n) <= MaxDepth+1 && match(0, 0)
}

func workspacePatterns(patterns, exclude []string, relative string) bool {
	matched := false
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			exclude = append(exclude, strings.TrimPrefix(pattern, "!"))
		} else {
			matched = matched || workspaceMatch(pattern, relative)
		}
	}
	for _, pattern := range exclude {
		if workspaceMatch(pattern, relative) {
			return false
		}
	}
	return matched
}
