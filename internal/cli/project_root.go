package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	projectConfigName    = ".watermelon.toml"
	maxProjectConfigSize = 4 << 20
)

// discoverProjectRoot walks physical parent directories for the nearest
// Watermelon configuration. The invocation directory remains the fallback for
// configless management commands, preserving their legacy path-derived VM
// lookup without allowing a search to escape a repository, filesystem, or
// trusted directory hierarchy.
func discoverProjectRoot(dir string) (string, bool, error) {
	return discoverProjectRootWithDevice(dir, projectFilesystemDevice)
}

func discoverProjectRootWithDevice(dir string, device func(string) (uint64, error)) (string, bool, error) {
	start, err := canonicalProjectRoot(dir)
	if err != nil {
		return "", false, err
	}

	current := start
	for {
		hasConfig, err := projectMarkerExists(filepath.Join(current, projectConfigName))
		if err != nil {
			return "", false, fmt.Errorf("checking for %s in %q: %w", projectConfigName, current, err)
		}
		if hasConfig {
			return current, true, nil
		}

		// A .git marker may be either a directory or a file (for example, in a
		// linked worktree). Search the repository root itself, but never adopt a
		// configuration belonging to one of its parents.
		atVCSBoundary, err := projectMarkerExists(filepath.Join(current, ".git"))
		if err != nil {
			return "", false, fmt.Errorf("checking for VCS boundary in %q: %w", current, err)
		}
		if atVCSBoundary {
			return start, false, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return start, false, nil
		}
		trusted, err := trustedProjectSearchParent(parent)
		if err != nil {
			return "", false, fmt.Errorf("checking project-search boundary at %q: %w", parent, err)
		}
		if !trusted {
			return start, false, nil
		}
		currentDevice, err := device(current)
		if err != nil {
			return "", false, fmt.Errorf("identifying filesystem for %q: %w", current, err)
		}
		parentDevice, err := device(parent)
		if err != nil {
			return "", false, fmt.Errorf("identifying filesystem for parent %q: %w", parent, err)
		}
		if currentDevice != parentDevice {
			return start, false, nil
		}
		current = parent
	}
}

// trustedProjectSearchParent prevents an ancestor writable by another user
// from injecting trusted host policy into an otherwise unconfigured project.
// The exact invocation directory is still checked for backward compatibility;
// this guard applies only before discovery enters a parent.
func trustedProjectSearchParent(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("parent is not a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("ownership metadata is unavailable")
	}
	if stat.Uid == 0 {
		return info.Mode().Perm()&0022 == 0, nil
	}
	if stat.Uid == uint32(os.Geteuid()) {
		return info.Mode().Perm()&0002 == 0, nil
	}
	return false, nil
}

func trustedProjectConfigFile(info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("ownership metadata is unavailable")
	}
	if stat.Uid == 0 {
		return info.Mode().Perm()&0022 == 0, nil
	}
	if stat.Uid == uint32(os.Geteuid()) {
		return info.Mode().Perm()&0002 == 0, nil
	}
	return false, nil
}

func projectMarkerExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func projectFilesystemDevice(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("filesystem metadata is unavailable")
	}
	return uint64(stat.Dev), nil
}
