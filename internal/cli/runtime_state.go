package cli

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func preparePrivateRuntimeDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating private runtime directory %q: %w", path, err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("runtime path %q must be a real directory, not a symlink or non-directory: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("opening private runtime directory %q: invalid file descriptor", path)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspecting private runtime directory %q: %w", path, err)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("private runtime directory %q is not owned by the current user", path)
	}
	if err := file.Chmod(0700); err != nil {
		return fmt.Errorf("securing private runtime directory %q: %w", path, err)
	}
	return nil
}

func openPrivateRuntimeDir(path, label string) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("opening private %s %q without following symlinks: %w", label, path, err)
	}
	if err := validatePrivateRuntimeDirFD(fd, label); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func validatePrivateRuntimeDirFD(fd int, label string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspecting private %s: %w", label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) || os.FileMode(stat.Mode).Perm() != 0700 {
		return fmt.Errorf("%s must be a real, current-user-owned 0700 directory", label)
	}
	return nil
}

func createPrivateTempFileAt(dirFD int, prefix string) (string, int, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var random [12]byte
		if _, err := cryptorand.Read(random[:]); err != nil {
			return "", -1, err
		}
		name := prefix + hex.EncodeToString(random[:])
		fd, err := unix.Openat(dirFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err == nil {
			return name, fd, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", -1, err
		}
	}
	return "", -1, errors.New("could not allocate a unique temporary file")
}
