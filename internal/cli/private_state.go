package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("state file contains trailing JSON data")
	}
	return fmt.Errorf("invalid trailing state data: %w", err)
}

// Private state is host authority, outside guest-writable mounts. Callers
// validate the directory hierarchy; these helpers enforce the file boundary.
// Opening without following links or blocking also makes FIFOs fail closed.
func readPrivateStateFile(path, label string, maxBytes int64) ([]byte, error) {
	return readPrivateStateFileAt(unix.AT_FDCWD, path, label, maxBytes)
}

func readPrivateStateFileAt(dirFD int, name, label string, maxBytes int64) ([]byte, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s %q without following symlinks: %w", label, name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("opening %s: invalid file descriptor", label)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q must be a regular file", label, name)
	}
	if !ownedByCurrentUser(info) {
		return nil, fmt.Errorf("%s %q is not owned by the current user", label, name)
	}
	if info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("%s %q has insecure mode %04o; want 0600", label, name, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return nil, fmt.Errorf("%s %q must have exactly one hard link", label, name)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s %q exceeds %d bytes", label, name, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	// The second check catches a file growing after Stat. Never parse a prefix.
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s %q exceeds %d bytes", label, name, maxBytes)
	}
	return data, nil
}

// replacePrivateStateFile publishes a complete, durable record by rename.
// All operations use one validated directory descriptor, so changing a lexical
// parent cannot redirect publication or temporary-file cleanup. Readers see
// either the old inode or the complete replacement, with mode 0600 throughout.
func replacePrivateStateFile(path, label string, data []byte, maxBytes int64) error {
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("%s %q exceeds %d bytes", label, path, maxBytes)
	}
	dirFD, err := openPrivateRuntimeDir(filepath.Dir(path), label+" directory")
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	name := filepath.Base(path)
	if _, err := readPrivateStateFileAt(dirFD, name, label, maxBytes); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tempName, fd, err := createPrivateTempFileAt(dirFD, ".state-")
	if err != nil {
		return err
	}
	defer unix.Unlinkat(dirFD, tempName, 0)
	file := os.NewFile(uintptr(fd), tempName)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("writing %s: invalid file descriptor", label)
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(dirFD, tempName, dirFD, name); err != nil {
		return err
	}
	return unix.Fsync(dirFD)
}
