package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

type appliedPolicyHostContext struct {
	VMName             string
	ProjectRoot        string
	ProjectRootLexical string
	UserConfig         string
	UserConfigLexical  string
	AppStateRoot       string
	LimaHome           string
	LimaHomeLexical    string
	MountsLexical      map[string]string
	StateDir           string
	SnapshotPath       string
	Recorded           config.AppliedHostContext
}

func canonicalProjectRoot(dir string) (string, error) {
	canonical, err := canonicalizeHostPath("project root", dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspecting canonical project root %q: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("canonical project root %q is not a directory", canonical)
	}
	return canonical, nil
}

// derivedVMName keeps path-derived instance selection stable across lexical
// aliases such as macOS's /var -> /private/var symlink. Commands normally pass
// an already-canonical project root, but recovery and testable helper paths can
// reach instance selection before project discovery has normalized the path.
func derivedVMName(dir string) string {
	if canonical, err := canonicalProjectRoot(dir); err == nil {
		dir = canonical
	}
	return lima.VMNameFromPath(dir)
}

// effectiveUserConfigDir honors an explicit XDG override on every host OS.
// os.UserConfigDir ignores XDG_CONFIG_HOME on macOS, which makes it impossible
// for callers to relocate Watermelon's host-only state in a portable way.
func effectiveUserConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("path in $XDG_CONFIG_HOME is relative")
		}
		return dir, nil
	}
	return os.UserConfigDir()
}

func resolveAppliedPolicyHostContext(dir string, cfg *config.Config) (appliedPolicyHostContext, error) {
	return resolveAppliedPolicyHostContextForVM(dir, cfg, "")
}

func resolveAppliedPolicyHostContextForVM(dir string, cfg *config.Config, vmName string) (appliedPolicyHostContext, error) {
	projectRootLexical, err := cleanAbsoluteHostPath("project root", dir)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	projectRoot, err := canonicalProjectRoot(dir)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}

	userConfigLexical, err := effectiveUserConfigDir()
	if err != nil {
		return appliedPolicyHostContext{}, fmt.Errorf("locating user config directory: %w", err)
	}
	userConfigLexical, err = cleanAbsoluteHostPath("user config directory", userConfigLexical)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	userConfigDir, err := canonicalizeHostPath("user config directory", userConfigLexical)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	if err := validateTrustedDirectoryIfPresent("user config directory", userConfigDir); err != nil {
		return appliedPolicyHostContext{}, err
	}

	limaHomeLexical, limaHome, err := effectiveLimaHome()
	if err != nil {
		return appliedPolicyHostContext{}, err
	}

	appStateRoot := filepath.Join(userConfigDir, "watermelon")
	namespace := "lima-" + fullPathDigest(limaHome)
	stateDir := filepath.Join(appStateRoot, "vms", namespace)
	defaultVMName := lima.VMNameFromPath(projectRoot)
	if vmName == "" {
		vmName = defaultVMName
	}
	if err := config.ValidateVMName(vmName); err != nil {
		return appliedPolicyHostContext{}, fmt.Errorf("invalid VM name %q: %w", vmName, err)
	}
	snapshotKey := fullPathDigest(projectRoot)
	if vmName != defaultVMName {
		snapshotKey = fullPathDigest(projectRoot + "\x00" + vmName)
	}
	snapshotPath := filepath.Join(stateDir, snapshotKey+".json")

	for _, component := range []string{appStateRoot, filepath.Join(appStateRoot, "vms"), stateDir} {
		if err := validateAppStateComponentIfPresent(component); err != nil {
			return appliedPolicyHostContext{}, err
		}
	}

	mountSources := make(map[string]string)
	mountsLexical := make(map[string]string)
	if cfg != nil {
		mountsLexical, mountSources, err = resolveMountSources(cfg.Mounts)
		if err != nil {
			return appliedPolicyHostContext{}, err
		}
	}

	host := appliedPolicyHostContext{
		VMName:             vmName,
		ProjectRoot:        projectRoot,
		ProjectRootLexical: projectRootLexical,
		UserConfig:         userConfigDir,
		UserConfigLexical:  userConfigLexical,
		AppStateRoot:       appStateRoot,
		LimaHome:           limaHome,
		LimaHomeLexical:    limaHomeLexical,
		MountsLexical:      mountsLexical,
		StateDir:           stateDir,
		SnapshotPath:       snapshotPath,
		Recorded: config.AppliedHostContext{
			ProjectRoot:  projectRoot,
			LimaHome:     limaHome,
			EffectiveUID: uint32(os.Geteuid()),
			MountSources: mountSources,
		},
	}
	if err := validateAppliedPolicyHostIsolation(host, cfg); err != nil {
		return appliedPolicyHostContext{}, err
	}
	return host, nil
}

func effectiveLimaHome() (string, string, error) {
	limaHome := os.Getenv("LIMA_HOME")
	if limaHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("locating home directory for default LIMA_HOME: %w", err)
		}
		limaHome = filepath.Join(home, ".lima")
	}
	lexical, err := cleanAbsoluteHostPath("LIMA_HOME", limaHome)
	if err != nil {
		return "", "", err
	}
	canonical, err := canonicalizeHostPath("LIMA_HOME", lexical)
	if err != nil {
		return "", "", err
	}
	return lexical, canonical, nil
}

func resolveMountSources(mounts map[string]config.Mount) (map[string]string, map[string]string, error) {
	lexical := make(map[string]string, len(mounts))
	canonical := make(map[string]string, len(mounts))
	for source := range mounts {
		expanded, err := expandPolicyMountSource(source)
		if err != nil {
			return nil, nil, err
		}
		cleaned, err := cleanAbsoluteHostPath(fmt.Sprintf("mount source %q", source), expanded)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := canonicalizeHostPath(fmt.Sprintf("mount source %q", source), cleaned)
		if err != nil {
			return nil, nil, err
		}
		lexical[source] = cleaned
		canonical[source] = resolved
	}
	return lexical, canonical, nil
}

func expandPolicyMountSource(source string) (string, error) {
	if source != "~" && !strings.HasPrefix(source, "~/") {
		return source, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding mount source %q: %w", source, err)
	}
	if source == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(source, "~/")), nil
}

// canonicalizeHostPath resolves every existing symlink while retaining any
// not-yet-created suffix beneath the longest existing ancestor.
func canonicalizeHostPath(label, path string) (string, error) {
	cleaned, err := cleanAbsoluteHostPath(label, path)
	if err != nil {
		return "", err
	}
	current := cleaned
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolving %s %q: %w", label, path, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspecting %s %q: %w", label, path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolving %s %q: no existing ancestor", label, path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func cleanAbsoluteHostPath(label, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute, got %q", label, path)
	}
	return filepath.Clean(path), nil
}

func validateAppliedPolicyHostIsolation(host appliedPolicyHostContext, cfg *config.Config) error {
	if pathsOverlap(host.AppStateRoot, host.ProjectRoot) {
		return fmt.Errorf("applied-policy host state %q overlaps the guest-writable project %q", host.AppStateRoot, host.ProjectRoot)
	}
	if pathsOverlap(host.AppStateRoot, host.LimaHome) {
		return fmt.Errorf("applied-policy host state %q overlaps LIMA_HOME %q", host.AppStateRoot, host.LimaHome)
	}
	if pathsOverlap(host.ProjectRoot, host.LimaHome) {
		return fmt.Errorf("guest-writable project %q overlaps LIMA_HOME %q", host.ProjectRoot, host.LimaHome)
	}
	if pathVariantsOverlap(
		[]string{host.ProjectRoot, host.ProjectRootLexical},
		[]string{host.LimaHome, host.LimaHomeLexical},
	) {
		return fmt.Errorf("guest-writable project path %q (canonical %q) overlaps lexical LIMA_HOME %q (canonical %q)", host.ProjectRootLexical, host.ProjectRoot, host.LimaHomeLexical, host.LimaHome)
	}
	if host.UserConfigLexical != host.UserConfig && pathVariantsOverlap(
		[]string{host.ProjectRoot, host.ProjectRootLexical},
		[]string{host.UserConfigLexical},
	) {
		return fmt.Errorf("guest-writable project path %q (canonical %q) overlaps lexical user config directory %q", host.ProjectRootLexical, host.ProjectRoot, host.UserConfigLexical)
	}
	if cfg == nil {
		return nil
	}
	for source, mount := range cfg.Mounts {
		canonical := host.Recorded.MountSources[source]
		lexical := host.MountsLexical[source]
		if pathVariantsOverlap(
			[]string{canonical, lexical},
			[]string{host.LimaHome, host.LimaHomeLexical},
		) {
			return fmt.Errorf("mount source %q (lexical %s, canonical %s) overlaps LIMA_HOME (lexical %q, canonical %q)", source, lexical, canonical, host.LimaHomeLexical, host.LimaHome)
		}
		if host.UserConfigLexical != host.UserConfig && pathVariantsOverlap(
			[]string{canonical, lexical},
			[]string{host.UserConfigLexical},
		) {
			return fmt.Errorf("mount source %q (lexical %s, canonical %s) overlaps lexical user config directory %q", source, lexical, canonical, host.UserConfigLexical)
		}
		if mount.Mode == "rw" && pathsOverlap(canonical, host.AppStateRoot) {
			return fmt.Errorf("read-write mount source %q (%s) overlaps applied-policy host state %q", source, canonical, host.AppStateRoot)
		}
	}
	return nil
}

func pathVariantsOverlap(first, second []string) bool {
	for _, firstPath := range first {
		if firstPath == "" {
			continue
		}
		for _, secondPath := range second {
			if secondPath != "" && pathsOverlap(firstPath, secondPath) {
				return true
			}
		}
	}
	return false
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func fullPathDigest(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func validateTrustedDirectoryIfPresent(label, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting %s %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", label, path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("%s %q is not owned by the current user", label, path)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s %q is writable by group or other users (mode %04o)", label, path, info.Mode().Perm())
	}
	return nil
}

func validateAppStateComponentIfPresent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting applied-policy state directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("applied-policy state path %q must be a real directory, not a symlink or non-directory", path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("applied-policy state directory %q is not owned by the current user", path)
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
