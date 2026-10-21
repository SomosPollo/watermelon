package config

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const RuntimeAssemblyVersion = "extended-v1"
const DefaultJavaRuntime = "21"
const DefaultDotnetRuntime = "10.0"
const DefaultRubyRuntime = "3.4"
const DefaultPHPRuntime = "8.4"
const DefaultCPPRuntime = "12"
const DefaultPNPMVersion = "10.20.0"
const DefaultYarnVersion = "4.10.3"
const DefaultUVVersion = "0.12.0"
const DefaultPoetryVersion = "2.5.1"
const CorepackVersion = "0.34.4"

func ExtendedProfile(profile string) bool {
	switch profile {
	case "java", "dotnet", "ruby", "php", "cpp":
		return true
	}
	return false
}

// Source images are fixed official repositories. Numeric selectors never
// become arbitrary image names or privileged shell fragments.
func SetupSourceImage(setup SetupConfig) (string, error) {
	version := setup.Runtime
	defaults := map[string]string{"java": DefaultJavaRuntime, "dotnet": DefaultDotnetRuntime, "ruby": DefaultRubyRuntime, "php": DefaultPHPRuntime, "cpp": DefaultCPPRuntime}
	if version == "" {
		version = defaults[setup.Profile]
	}
	if !ExtendedProfile(setup.Profile) {
		return SetupImage(setup)
	}
	if !runtimeVersionPattern.MatchString(version) {
		return "", fmt.Errorf("%s runtime must be numeric", setup.Profile)
	}
	parts := strings.Split(version, ".")
	switch setup.Profile {
	case "java":
		if len(parts) != 1 || (version != "17" && version != "21" && version != "25") {
			return "", fmt.Errorf("Java supports JDK 17, 21 or 25")
		}
		return "eclipse-temurin:" + version + "-jdk-jammy", nil
	case "dotnet":
		if len(parts) < 2 || parts[1] != "0" || (parts[0] != "8" && parts[0] != "9" && parts[0] != "10") {
			return "", fmt.Errorf(".NET supports numeric 8.0, 9.0 or 10.0 SDK selectors")
		}
		suffix := "-noble"
		if parts[0] == "8" {
			suffix = "-jammy"
		}
		return "mcr.microsoft.com/dotnet/sdk:" + version + suffix, nil
	case "ruby":
		if len(parts) < 2 || (parts[0] != "3" && parts[0] != "4") {
			return "", fmt.Errorf("Ruby requires a stable numeric 3.x or 4.x version")
		}
		return "ruby:" + version + "-bookworm", nil
	case "php":
		if len(parts) < 2 || parts[0] != "8" {
			return "", fmt.Errorf("PHP requires a stable numeric 8.x version")
		}
		return "php:" + version + "-cli-bookworm", nil
	case "cpp":
		if len(parts) != 1 || (version != "12" && version != "13" && version != "14" && version != "15") {
			return "", fmt.Errorf("C/C++ supports GCC 12, 13, 14 or 15")
		}
		return "gcc:" + version + "-bookworm", nil
	}
	return "", fmt.Errorf("unknown toolchain")
}

func extendedSetupImage(setup SetupConfig) (string, error) {
	source, err := SetupSourceImage(setup)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(RuntimeAssemblyVersion + "\x00" + source))
	return fmt.Sprintf("watermelon-project:%x", digest[:12]), nil
}

func ValidateManager(profile, manager string) error {
	if manager == "" {
		return nil
	}
	name, version, ok := strings.Cut(manager, "@")
	allowed := map[string]map[string]bool{"node": {"pnpm": true, "yarn": true}, "python": {"uv": true, "poetry": true}}
	validVersion := runtimeVersionPattern.MatchString(version) && strings.Count(version, ".") == 2
	if profile == "node" {
		validVersion = ValidNodeManagerVersion(version)
	}
	if !ok || !allowed[profile][name] || !validVersion {
		return fmt.Errorf("invalid %s package manager %q; use a numeric pinned pnpm/yarn or uv/Poetry version", profile, manager)
	}
	return nil
}

// Corepack's optional digest is part of the approved pin. Never strip it or
// accept a URL/custom executable in place of a registry release.
func ValidNodeManagerVersion(version string) bool {
	numeric, integrity, hashed := strings.Cut(version, "+")
	if !runtimeVersionPattern.MatchString(numeric) || strings.Count(numeric, ".") != 2 {
		return false
	}
	if !hashed {
		return true
	}
	algorithm, digest, ok := strings.Cut(integrity, ".")
	length := map[string]int{"sha224": 56, "sha256": 64, "sha384": 96, "sha512": 128}[algorithm]
	if !ok || length == 0 || len(digest) != length {
		return false
	}
	for _, r := range digest {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
