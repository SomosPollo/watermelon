package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

var errNfqdBinaryNotFound = errors.New("packaged network interceptor not found")

func ensureNfqdBinary(projectDir string) error {
	binDir, err := prepareNfqdRuntimeDirectories(projectDir)
	if err != nil {
		return err
	}
	return ensureNfqdBinaryAtPath(filepath.Join(binDir, "watermelon-nfqd"))
}

func ensureNfqdBinaryAtPath(nfqdPath string) error {
	if err := preparePrivateRuntimeDirectory(filepath.Dir(nfqdPath)); err != nil {
		return err
	}

	version, officialRelease := goInstallReleaseVersion(cliReadBuildInfo)
	arch := runtime.GOARCH
	// An explicit override is sufficient for an unreleased developer build.
	// Official builds and packaged-sidecar discovery must follow Lima's resolved
	// default guest architecture, which can differ from the CLI architecture
	// when the CLI runs through Rosetta or another emulation layer.
	if os.Getenv("WATERMELON_NFQD_BINARY") == "" || officialRelease {
		var err error
		arch, err = cliNfqdHostArch()
		if err != nil {
			return err
		}
	}

	releaseSource := cliNewNfqdRelease()
	source, err := findNfqdBinary(arch)
	if err == nil {
		if officialRelease {
			if validationErr := releaseSource.validateFile(source, version, arch); validationErr != nil {
				if os.Getenv("WATERMELON_NFQD_BINARY") != "" {
					return fmt.Errorf("validating network interceptor %q for Watermelon %s: %w", source, version, validationErr)
				}
				fmt.Fprintf(os.Stderr, "Warning: ignoring packaged network interceptor %q because it does not match Watermelon %s: %v\n", source, version, validationErr)
				err = errNfqdBinaryNotFound
			}
		}
		if err == nil {
			fmt.Println("Installing network interceptor for VM...")
			return copyExecutable(source, nfqdPath)
		}
	}
	if !errors.Is(err, errNfqdBinaryNotFound) {
		return err
	}

	if !officialRelease {
		return errors.New("watermelon-nfqd sidecar not found; install Watermelon with the release installer, place the matching sidecar beside the CLI, or set WATERMELON_NFQD_BINARY")
	}
	fmt.Printf("Downloading matching network interceptor for Watermelon %s...\n", version)
	if err := releaseSource.install(nfqdPath, version, arch); err != nil {
		return fmt.Errorf("obtaining the matching network interceptor for Watermelon %s: %w; retry, use the release installer, or set WATERMELON_NFQD_BINARY", version, err)
	}
	return nil
}

func verifyRegisteredNfqdBinary(path string) error {
	if _, err := hashPreparedNfqdBinaryAtPath(path); err != nil {
		return err
	}
	version, officialRelease := goInstallReleaseVersion(cliReadBuildInfo)
	if !officialRelease {
		return nil
	}
	arch, err := cliNfqdHostArch()
	if err != nil {
		return err
	}
	if err := cliNewNfqdRelease().validateFile(path, version, arch); err != nil {
		return fmt.Errorf("registered network interceptor does not match Watermelon %s: %w", version, err)
	}
	return nil
}

func hashPreparedNfqdBinaryAtPath(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory %q without following symlinks: %w", dir, err)
	}
	defer unix.Close(dirFD)
	fileFD, err := unix.Openat(dirFD, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("opening prepared network interceptor without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fileFD), path)
	if file == nil {
		_ = unix.Close(fileFD)
		return "", errors.New("opening prepared network interceptor: invalid file descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspecting prepared network interceptor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("prepared network interceptor must be a regular file, got %s", info.Mode().Type())
	}
	if !ownedByCurrentUser(info) {
		return "", fmt.Errorf("prepared network interceptor %q is not owned by the current user", path)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hashing prepared network interceptor: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func prepareNfqdRuntimeDirectories(projectDir string) (string, error) {
	watermelonDir := filepath.Join(projectDir, ".watermelon")
	if err := preparePrivateRuntimeDirectory(watermelonDir); err != nil {
		return "", err
	}
	binDir := filepath.Join(watermelonDir, "bin")
	if err := preparePrivateRuntimeDirectory(binDir); err != nil {
		return "", err
	}
	return binDir, nil
}

func hashPreparedNfqdBinary(projectDir string) (string, error) {
	const directoryFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_DIRECTORY | unix.O_NOFOLLOW
	projectFD, err := unix.Open(projectDir, directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening project directory for network interceptor verification: %w", err)
	}
	defer unix.Close(projectFD)

	watermelonFD, err := unix.Openat(projectFD, ".watermelon", directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory component %q without following symlinks: %w", ".watermelon", err)
	}
	defer unix.Close(watermelonFD)
	binFD, err := unix.Openat(watermelonFD, "bin", directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory component %q without following symlinks: %w", "bin", err)
	}
	defer unix.Close(binFD)

	fileFD, err := unix.Openat(binFD, "watermelon-nfqd", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("opening prepared network interceptor without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fileFD), filepath.Join(projectDir, ".watermelon", "bin", "watermelon-nfqd"))
	if file == nil {
		_ = unix.Close(fileFD)
		return "", errors.New("opening prepared network interceptor: invalid file descriptor")
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspecting prepared network interceptor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("prepared network interceptor must be a regular file, got %s", info.Mode().Type())
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hashing prepared network interceptor: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func installBuiltNfqd(dest string, build func(outputPath string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".watermelon-nfqd-build-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := build(tmpPath); err != nil {
		return err
	}
	info, err := os.Lstat(tmpPath)
	if err != nil {
		return fmt.Errorf("inspecting built network interceptor: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("built network interceptor %q is not a regular file", tmpPath)
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func findNfqdBinary(arch string) (string, error) {
	if override := os.Getenv("WATERMELON_NFQD_BINARY"); override != "" {
		info, err := os.Lstat(override)
		if err != nil {
			return "", fmt.Errorf("WATERMELON_NFQD_BINARY %q cannot be used: %w", override, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("WATERMELON_NFQD_BINARY %q must be a regular, non-symlink file", override)
		}
		return override, nil
	}

	exe, err := cliExecutable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{
		"watermelon-nfqd-linux-" + arch,
		"watermelon-nfqd",
	} {
		candidate := filepath.Join(dir, name)
		info, err := os.Lstat(candidate)
		if err == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("packaged network interceptor %q must be a regular, non-symlink file", candidate)
			}
			return candidate, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspecting packaged network interceptor %q: %w", candidate, err)
		}
	}
	return "", errNfqdBinaryNotFound
}

func copyExecutable(source, dest string) error {
	fd, err := unix.Open(source, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("opening network interceptor source without following symlinks: %w", err)
	}
	src := os.NewFile(uintptr(fd), source)
	if src == nil {
		_ = unix.Close(fd)
		return errors.New("opening network interceptor source: invalid file descriptor")
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("inspecting network interceptor source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("network interceptor source %q must be a regular file", source)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".watermelon-nfqd-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}
