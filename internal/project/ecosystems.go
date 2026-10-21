package project

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
)

func ProfilesForFiles(files map[string][]byte) map[string]bool {
	result := map[string]bool{}
	for name := range files {
		for _, profile := range config.ProfileOrder {
			if manifestForProfile(path.Base(name), profile) {
				result[profile] = true
			}
		}
	}
	return result
}

func manifestForProfile(name, profile string) bool {
	switch profile {
	case "node":
		return name == "package.json"
	case "python":
		return name == "requirements.txt" || name == "pyproject.toml"
	case "go":
		return name == "go.mod" || name == "go.work"
	case "rust":
		return name == "Cargo.toml"
	case "java":
		return name == "pom.xml" || name == "build.gradle" || name == "build.gradle.kts" || name == "settings.gradle" || name == "settings.gradle.kts"
	case "dotnet":
		return strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".fsproj") || strings.HasSuffix(name, ".vbproj")
	case "ruby":
		return name == "Gemfile"
	case "php":
		return name == "composer.json"
	case "cpp":
		return name == "CMakeLists.txt" || name == "CMakePresets.json"
	}
	return false
}

func detectExtended(files map[string][]byte, profile, runtime string, plan *Plan) (string, error) {
	has := func(name string) bool { _, ok := files[name]; return ok }
	switch profile {
	case "java":
		plan.Runtime = config.DefaultJavaRuntime
		if has(".java-version") {
			plan.Runtime = strings.TrimSpace(string(files[".java-version"]))
			plan.Pinned = true
		}
		if has("pom.xml") {
			var pom struct {
				Properties struct {
					Release string `xml:"maven.compiler.release"`
					Target  string `xml:"maven.compiler.target"`
					Java    string `xml:"java.version"`
				} `xml:"properties"`
			}
			if err := xml.Unmarshal(files["pom.xml"], &pom); err != nil {
				return "", fmt.Errorf("invalid pom.xml: %w", err)
			}
			for _, v := range []string{pom.Properties.Release, pom.Properties.Target, pom.Properties.Java} {
				if v != "" && !strings.Contains(v, "${") {
					plan.Requirement = ">=" + strings.TrimPrefix(v, "1.")
					if runtime == "" && !plan.Pinned {
						plan.Runtime = chooseRuntime(plan.Requirement, []string{"21", "25", "17"})
					}
					break
				}
			}
			executable := "mvn"
			if has("mvnw") {
				plan.Commands = [][]string{{"sh", "./mvnw", "-B", "-ntp", "-DskipTests", "dependency:go-offline", "test-compile"}}
			} else {
				plan.Commands = [][]string{{executable, "-B", "-ntp", "-DskipTests", "dependency:go-offline", "test-compile"}}
			}
			plan.Notes = append(plan.Notes, "Maven prepares dependencies and compiles main/test classes without running tests so lifecycle-plugin dependencies are cached for subsequent builds.")
		} else {
			if runtime == "" && !plan.Pinned {
				pattern := regexp.MustCompile(`(?:jvmToolchain|JavaLanguageVersion\.of)\s*\(\s*([0-9]+)\s*\)`)
				for _, name := range []string{"build.gradle", "build.gradle.kts"} {
					if match := pattern.FindSubmatch(files[name]); len(match) == 2 {
						plan.Runtime = string(match[1])
						plan.Pinned = true
					}
				}
			}
			command := []string{"gradle"}
			if has("gradlew") {
				command = []string{"sh", "./gradlew"}
			}
			plan.Commands = [][]string{append(command, "--no-daemon", "--console=plain", "classes", "testClasses")}
			plan.Notes = append(plan.Notes, "Gradle configuration and compilation run inside the sandbox to prepare build artifacts and dependencies. Android SDKs and platform-specific toolchains require manual configuration.")
		}
	case "dotnet":
		plan.Runtime = config.DefaultDotnetRuntime
		if has("global.json") {
			var global struct {
				SDK struct {
					Version     string `json:"version"`
					RollForward string `json:"rollForward"`
				} `json:"sdk"`
			}
			if err := json.Unmarshal(files["global.json"], &global); err != nil {
				return "", fmt.Errorf("invalid global.json: %w", err)
			}
			if global.SDK.Version != "" {
				plan.Runtime = global.SDK.Version
				plan.Pinned = true
			}
		}
		var names []string
		for name := range files {
			if manifestForProfile(name, "dotnet") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			// XML is parsed as data, never through MSBuild during host inspection.
			decoder := xml.NewDecoder(bytes.NewReader(files[name]))
			for {
				token, err := decoder.Token()
				if err != nil {
					if err != io.EOF {
						return "", fmt.Errorf("invalid %s: %w", name, err)
					}
					break
				}
				if start, ok := token.(xml.StartElement); ok && (start.Name.Local == "TargetFramework" || start.Name.Local == "TargetFrameworks") {
					var text string
					if err := decoder.DecodeElement(&text, &start); err != nil {
						return "", err
					}
					for _, framework := range strings.Split(text, ";") {
						framework = strings.TrimSpace(framework)
						if strings.Contains(framework, "-android") || strings.Contains(framework, "-ios") || strings.Contains(framework, "-maccatalyst") || strings.Contains(framework, "-tvos") || strings.Contains(framework, "-browser") {
							return "", fmt.Errorf("%s targets %q; platform workloads require manual setup", name, framework)
						}
						if strings.HasPrefix(framework, "netcoreapp") || regexp.MustCompile(`^net[5-7]\.`).MatchString(framework) {
							return "", fmt.Errorf("%s targets %q; automatic setup supplies .NET 8, 9 and 10 runtimes; migrate the target or use manual tools", name, framework)
						}
						if match := regexp.MustCompile(`^net([0-9]+)\.([0-9]+)$`).FindStringSubmatch(framework); len(match) == 3 {
							requirement := match[1] + "." + match[2]
							if plan.Requirement == "" || chooseRuntime(plan.Requirement, []string{requirement}) != "" {
								plan.Requirement = ">=" + requirement
							}
						}

						if strings.Contains(framework, "windows") || strings.HasPrefix(framework, "net4") {
							return "", fmt.Errorf("%s targets %q; Windows/.NET Framework projects cannot run in this Linux sandbox", name, framework)
						}
					}
				}
			}
			args := []string{"dotnet", "restore", name, "--nologo"}
			if has("packages.lock.json") {
				args = append(args, "--locked-mode")
			}
			plan.Commands = append(plan.Commands, args)
		}
	case "ruby":
		plan.Runtime = config.DefaultRubyRuntime
		if has(".ruby-version") {
			plan.Runtime = strings.TrimPrefix(strings.TrimSpace(string(files[".ruby-version"])), "ruby-")
			plan.Pinned = true
		}
		// Gemfiles are Ruby programs: only an obvious literal version is inspected.
		re := regexp.MustCompile(`(?m)^\s*ruby\s+((?:["'][^"'\r\n]+["'][ \t]*,?[ \t]*)+)`)
		if match := re.FindSubmatch(files["Gemfile"]); len(match) > 1 {
			var requirements []string
			for _, literal := range regexp.MustCompile(`["']([^"']+)["']`).FindAllStringSubmatch(string(match[1]), -1) {
				requirements = append(requirements, rubyRequirement(literal[1]))
			}
			plan.Requirement = strings.Join(requirements, ",")
			if runtime == "" && !plan.Pinned {
				plan.Runtime = selectRuntime(plan.Requirement, []string{"3.4", "4.0", "3.3", "3.2"})
			}
		}
		args := []string{"bundle", "install", "--jobs", "2", "--retry", "2"}
		if has("Gemfile.lock") {
			args = append([]string{"env", "BUNDLE_FROZEN=true"}, args...)
		}
		plan.Commands = [][]string{args}
		plan.Notes = append(plan.Notes, "Bundler evaluates Gemfile only inside the approved sandbox. Native gems may require additional system libraries.")
	case "php":
		plan.Runtime = config.DefaultPHPRuntime
		var composer struct {
			Require map[string]string `json:"require"`
		}
		if err := json.Unmarshal(files["composer.json"], &composer); err != nil {
			return "", fmt.Errorf("invalid composer.json: %w", err)
		}
		plan.Requirement = regexp.MustCompile(`\s*\|+\s*`).ReplaceAllString(composer.Require["php"], " || ")
		plan.Requirement = regexp.MustCompile(`~([0-9]+\.[0-9]+)(?:$| )`).ReplaceAllString(plan.Requirement, "^$1 ")
		if plan.Requirement != "" && runtime == "" {
			plan.Runtime = selectRuntime(plan.Requirement, []string{"8.4", "8.5", "8.3", "8.2"})
		}
		plan.Commands = [][]string{{"composer", "install", "--no-interaction", "--prefer-dist", "--no-progress"}}
		plan.Notes = append(plan.Notes, "Composer verifies PHP and extension requirements; unsupported extensions fail explicitly rather than being ignored. Common intl, zip, bcmath and database extensions are bundled.")
	case "cpp":
		if has("conanfile.py") || has("conanfile.txt") || has("vcpkg.json") {
			return "", fmt.Errorf("Conan/vcpkg manifests require manual dependency provisioning; automatic native setup currently supports CMake defaults and approved FetchContent sources")
		}
		plan.Runtime = config.DefaultCPPRuntime
		if has("CMakePresets.json") {
			var presets map[string]any
			if err := json.Unmarshal(files["CMakePresets.json"], &presets); err != nil {
				return "", fmt.Errorf("invalid CMakePresets.json: %w", err)
			}
		}
		if !has("CMakeLists.txt") {
			return "", fmt.Errorf("C/C++ automatic setup requires CMakeLists.txt")
		}
		plan.Commands = [][]string{{"cmake", "--fresh", "-S", ".", "-B", ".watermelon-build", "-G", "Ninja"}}
		plan.Notes = append(plan.Notes, "CMake configures the project inside the sandbox with GCC, Clang and Ninja available. Add explicit network rules for FetchContent sources and manual system libraries as needed; configuration never runs on the host.")
	}
	if runtime != "" {
		plan.Runtime = runtime
	}
	if plan.Runtime == "" {
		return "", fmt.Errorf("no supported %s runtime satisfies %q", profile, plan.Requirement)
	}
	return plan.Runtime, nil
}

func RuntimeVersionCommand(profile string) []string {
	switch profile {
	case "java":
		return []string{"/usr/local/java/bin/javac", "--version"}
	case "dotnet":
		return []string{"/usr/local/share/dotnet/dotnet", "--version"}
	case "ruby":
		return []string{"/usr/local/bin/ruby", "-e", "puts RUBY_VERSION"}
	case "php":
		return []string{"/usr/local/bin/php", "-r", "echo PHP_VERSION;"}
	case "cpp":
		return []string{"/usr/local/bin/gcc", "-dumpfullversion"}
	}
	return nil
}

func ParseRuntimeVersion(profile, output string) (string, error) {
	if profile == "java" {
		output = strings.TrimPrefix(output, "javac ")
		parts := strings.Split(output, ".")
		if len(parts) == 4 && regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){3}$`).MatchString(output) {
			output = strings.Join(parts[:3], ".")
		}
	}
	if _, err := versionParts(output); err != nil {
		return "", fmt.Errorf("unexpected %s version %q: %w", profile, output, err)
	}
	return output, nil
}

// A declared exact patch should select that image, rather than today's patch
// for the same series. Candidate validation still uses chooseRuntime unchanged.
func selectRuntime(requirement string, candidates []string) string {
	match := regexp.MustCompile(`^(?:==|=)?([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(strings.TrimSpace(requirement))
	if len(match) == 2 {
		for _, candidate := range candidates {
			if strings.HasPrefix(match[1], candidate+".") || match[1] == candidate {
				return match[1]
			}
		}
	}
	return chooseRuntime(requirement, candidates)
}

func rubyRequirement(value string) string {
	if !strings.HasPrefix(value, "~>") {
		return value
	}
	version := strings.TrimSpace(strings.TrimPrefix(value, "~>"))
	parts, err := versionParts(version)
	if err != nil {
		return value
	}
	upper := fmt.Sprintf("%d.0.0", parts[0]+1)
	if strings.Count(version, ".") >= 2 {
		upper = fmt.Sprintf("%d.%d.0", parts[0], parts[1]+1)
	}
	return ">=" + version + ",<" + upper
}
