package project

import (
	"fmt"
	"path"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/mod/modfile"
)

func detectGo(files map[string][]byte, runtime string, plan *Plan) (string, error) {
	var minimum, preferred string
	addMinimum := func(version string) {
		if minimum == "" {
			minimum = version
			return
		}
		a, _ := versionParts(version)
		b, _ := versionParts(minimum)
		if compareVersion(a, b) > 0 {
			minimum = version
		}
	}
	if data, ok := files["go.mod"]; ok {
		mod, err := modfile.Parse("go.mod", data, nil)
		if err != nil {
			return "", fmt.Errorf("invalid go.mod: %w", err)
		}
		if mod.Module == nil {
			return "", fmt.Errorf("go.mod requires a module directive")
		}
		if mod.Go != nil {
			addMinimum(mod.Go.Version)
		}
		if mod.Toolchain != nil {
			preferred = mod.Toolchain.Name
		}
	}
	if data, ok := files["go.work"]; ok {
		work, err := modfile.ParseWork("go.work", data, nil)
		if err != nil {
			return "", fmt.Errorf("invalid go.work: %w", err)
		}
		if len(work.Use) == 0 {
			return "", fmt.Errorf("go.work must declare at least one workspace module")
		}
		for _, use := range work.Use {
			if !projectRelativePath(use.Path) {
				return "", fmt.Errorf("go.work module %q is outside the project; use a workspace containing its modules or manual configuration", use.Path)
			}
		}
		if work.Go != nil {
			addMinimum(work.Go.Version)
		}
		// Workspace toolchain selection takes precedence over go.mod.
		preferred = ""
		if work.Toolchain != nil {
			preferred = work.Toolchain.Name
		}
		plan.Notes = append(plan.Notes, "Go workspace members must stay inside the project. Use up --refresh after changing member manifests or local replacements.")
	} else if _, ok := files["go.mod"]; !ok {
		return "", fmt.Errorf("go setup requires go.mod or go.work")
	}
	if minimum != "" {
		plan.Requirement = ">=" + minimum
	}
	if runtime == "" {
		if data, ok := files[".go-version"]; ok {
			runtime = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(string(data)), "go"), "v")
			plan.Pinned = true
		} else if preferred != "" && preferred != "default" {
			runtime = strings.TrimPrefix(preferred, "go")
			plan.Pinned = true
		} else {
			runtime = config.DefaultGoRuntime
			// A newer declared minimum selects that series, never a downloaded
			// toolchain chosen later by untrusted project execution.
			if minimum != "" {
				a, _ := versionParts(minimum)
				b, _ := versionParts(runtime)
				if compareVersion(a, b) > 0 {
					runtime = minimum
				}
			}
		}
	}
	plan.Commands = [][]string{{"go", "mod", "download", "all"}}
	plan.Notes = append(plan.Notes, "Go modules use proxy.golang.org and checksum verification. Automatic toolchain downloads and direct VCS fallback are disabled; build/test commands run separately inside the sandbox.")
	return runtime, nil
}

func projectRelativePath(value string) bool {
	clean := path.Clean(value)
	return value != "" && !strings.ContainsAny(value, "\\\x00") && !path.IsAbs(value) && clean != ".." && !strings.HasPrefix(clean, "../")
}

func detectRust(files map[string][]byte, runtime string, plan *Plan) (string, error) {
	data, ok := files["Cargo.toml"]
	if !ok {
		return "", fmt.Errorf("rust setup requires Cargo.toml")
	}
	var manifest struct {
		Package *struct {
			RustVersion any `toml:"rust-version"`
		} `toml:"package"`
		Workspace *struct {
			Members []string `toml:"members"`
			Package struct {
				RustVersion string `toml:"rust-version"`
			} `toml:"package"`
		} `toml:"workspace"`
	}
	if _, err := toml.Decode(string(data), &manifest); err != nil {
		return "", fmt.Errorf("invalid Cargo.toml: %w", err)
	}
	if manifest.Package == nil && manifest.Workspace == nil {
		return "", fmt.Errorf("Cargo.toml requires a package or workspace")
	}
	minimum := ""
	if manifest.Workspace != nil {
		minimum = manifest.Workspace.Package.RustVersion
		if minimum != "" {
			if _, err := config.SetupImage(config.SetupConfig{Profile: "rust", Runtime: minimum}); err != nil {
				return "", fmt.Errorf("invalid workspace.package.rust-version: %w", err)
			}
		}
		for _, member := range manifest.Workspace.Members {
			if !projectRelativePath(member) {
				return "", fmt.Errorf("Cargo workspace member %q is outside the project; use manual configuration", member)
			}
		}
		plan.Notes = append(plan.Notes, "Use up --refresh after changing workspace member manifests, local path dependencies, or Cargo configuration.")
	}
	if manifest.Package != nil && manifest.Package.RustVersion != nil {
		switch value := manifest.Package.RustVersion.(type) {
		case string:
			if _, err := config.SetupImage(config.SetupConfig{Profile: "rust", Runtime: value}); err != nil {
				return "", fmt.Errorf("invalid package.rust-version: %w", err)
			}
			a, _ := versionParts(value)
			b, _ := versionParts(minimum)
			if minimum == "" || compareVersion(a, b) > 0 {
				minimum = value
			}
		case map[string]any:
			if value["workspace"] != true || minimum == "" {
				return "", fmt.Errorf("package.rust-version.workspace requires workspace.package.rust-version in the root manifest")
			}
		default:
			return "", fmt.Errorf("package.rust-version must be a numeric version or workspace inheritance")
		}
	}
	if minimum != "" {
		if _, err := config.SetupImage(config.SetupConfig{Profile: "rust", Runtime: minimum}); err != nil {
			return "", fmt.Errorf("invalid rust-version: %w", err)
		}
		plan.Requirement = ">=" + minimum
	}
	channel := ""
	if legacy, ok := files["rust-toolchain"]; ok {
		// Rustup also permits TOML in the extensionless file.
		if strings.HasPrefix(strings.TrimSpace(string(legacy)), "[") || strings.HasPrefix(strings.TrimSpace(string(legacy)), "#") {
			var err error
			channel, err = rustToolchain(legacy)
			if err != nil {
				return "", err
			}
		} else {
			channel = strings.TrimSpace(string(legacy))
		}
		if channel == "" {
			return "", fmt.Errorf("rust-toolchain must declare a channel")
		}
	}
	if modern, ok := files["rust-toolchain.toml"]; ok {
		candidate, err := rustToolchain(modern)
		if err != nil {
			return "", err
		}
		if channel != "" && candidate != channel {
			return "", fmt.Errorf("conflicting Rust toolchain files; keep one toolchain declaration")
		}
		channel = candidate
	}
	if channel != "" && channel != "stable" {
		if _, err := config.SetupImage(config.SetupConfig{Profile: "rust", Runtime: channel}); err != nil {
			return "", fmt.Errorf("Rust automatic setup supports stable numeric toolchains; nightly/beta, dated and custom toolchains require manual configuration: %w", err)
		}
	}
	if runtime == "" {
		runtime = config.DefaultRustRuntime
		if channel != "" && channel != "stable" {
			runtime = channel
			plan.Pinned = true
			// Rustup's 1.minor shorthand denotes its .0 release.
			if strings.Count(runtime, ".") == 1 {
				runtime += ".0"
			}
		} else if minimum != "" {
			a, _ := versionParts(minimum)
			b, _ := versionParts(runtime)
			if compareVersion(a, b) > 0 {
				runtime = minimum
			}
		}
	}
	plan.Commands = [][]string{{"cargo", "fetch"}}
	if _, locked := files["Cargo.lock"]; locked {
		plan.Commands[0] = append(plan.Commands[0], "--locked")
	} else {
		plan.Notes = append(plan.Notes, "No Cargo.lock: cargo fetch will generate a lockfile; existing lockfiles are fetched with --locked.")
	}
	plan.Notes = append(plan.Notes, "Cargo fetch prepares dependencies without compiling project build scripts. Run cargo build/test inside the sandbox; the selected image's bundled toolchain is used without rustup downloads.")
	return runtime, nil
}

func rustToolchain(data []byte) (string, error) {
	var file struct {
		Toolchain struct {
			Channel    string   `toml:"channel"`
			Components []string `toml:"components"`
			Targets    []string `toml:"targets"`
			Path       string   `toml:"path"`
		} `toml:"toolchain"`
	}
	if _, err := toml.Decode(string(data), &file); err != nil {
		return "", fmt.Errorf("invalid Rust toolchain file: %w", err)
	}
	if file.Toolchain.Channel == "" || file.Toolchain.Path != "" || len(file.Toolchain.Targets) != 0 {
		return "", fmt.Errorf("automatic Rust setup requires a stable channel without custom paths or extra targets; use manual tools for cross compilation")
	}
	for _, component := range file.Toolchain.Components {
		if component != "rustfmt" && component != "clippy" && component != "cargo" && component != "rustc" && component != "rust-std" {
			return "", fmt.Errorf("Rust component %q requires manual toolchain configuration; automatic setup includes cargo, rustc, rustdoc, rustfmt and clippy", component)
		}
	}
	return file.Toolchain.Channel, nil
}
