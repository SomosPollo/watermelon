package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/sys/unix"
)

const (
	lifecycleLockSuffix = ".lock"
	usageLeaseSuffix    = ".lease"
)

// persistentVMLock is the common secure file and flock implementation used by
// the short-lived lifecycle mutex and the potentially long-lived usage lease.
// Files are never removed: unlinking an advisory lock file can let old and new
// inodes be locked concurrently.
type persistentVMLock struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	description string
}

// vmLifecycleLock serializes host-side lifecycle decisions for one exact VM
// name in one canonical LIMA_HOME namespace.
type vmLifecycleLock struct {
	*persistentVMLock
}

// vmUsageLease prevents deletion, identity cleanup, and public-name reuse from
// racing an active shell, command, or IDE session. Active users hold shared
// leases; destroy takes an exclusive lease after stopping the VM. Stop itself
// deliberately does not wait, so it remains the immediate fail-closed escape
// hatch for terminating active clients.
type vmUsageLease struct {
	*persistentVMLock
}

// acquireVMLifecycleLock obtains an exclusive advisory lifecycle lock for
// vmName. It blocks until an earlier lifecycle operation has released it.
func acquireVMLifecycleLock(vmName string) (*vmLifecycleLock, error) {
	lock, err := acquirePersistentVMLock(vmName, lifecycleLockSuffix, "lifecycle lock", unix.LOCK_EX)
	if err != nil {
		return nil, err
	}
	return &vmLifecycleLock{persistentVMLock: lock}, nil
}

// acquireSharedVMUsageLease obtains a shared lease for an active VM user. Any
// number of shared holders may coexist, while an exclusive holder waits.
func acquireSharedVMUsageLease(vmName string) (*vmUsageLease, error) {
	lock, err := acquirePersistentVMLock(vmName, usageLeaseSuffix, "VM usage lease", unix.LOCK_SH)
	if err != nil {
		return nil, err
	}
	return &vmUsageLease{persistentVMLock: lock}, nil
}

// acquireExclusiveVMUsageLease obtains the lease used before deletion and
// identity cleanup. It blocks until every active shared usage lease is released.
func acquireExclusiveVMUsageLease(vmName string) (*vmUsageLease, error) {
	lock, err := acquirePersistentVMLock(vmName, usageLeaseSuffix, "VM usage lease", unix.LOCK_EX)
	if err != nil {
		return nil, err
	}
	return &vmUsageLease{persistentVMLock: lock}, nil
}

func acquirePersistentVMLock(vmName, suffix, description string, mode int) (*persistentVMLock, error) {
	if err := config.ValidateVMName(vmName); err != nil {
		return nil, fmt.Errorf("invalid VM name for %s: %w", description, err)
	}
	if mode != unix.LOCK_SH && mode != unix.LOCK_EX {
		return nil, fmt.Errorf("invalid internal flock mode for %s", description)
	}

	path, locksDir, err := preparePersistentVMLockPath(vmName, suffix, description)
	if err != nil {
		return nil, err
	}
	file, created, err := openPersistentVMLockFile(path, description)
	if err != nil {
		return nil, err
	}
	closeWithError := func(primary error) (*persistentVMLock, error) {
		return nil, errors.Join(primary, file.Close())
	}

	if created {
		if err := file.Sync(); err != nil {
			return closeWithError(fmt.Errorf("syncing new %s file %q: %w", description, path, err))
		}
		if err := syncIdentityDirectory(locksDir); err != nil {
			return closeWithError(fmt.Errorf("syncing %s directory: %w", description, err))
		}
	}
	if err := validatePersistentVMLockFile(file, path, description); err != nil {
		return closeWithError(err)
	}

	if err := unix.Flock(int(file.Fd()), mode); err != nil {
		return closeWithError(fmt.Errorf("acquiring %s for VM %q: %w", description, vmName, err))
	}
	// Recheck the directory entry after a possibly blocking acquisition. This
	// detects replacement while this process waited and prevents two different
	// inodes from acting as the same lock or lease.
	if err := validatePersistentVMLockPath(file, path, description); err != nil {
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		return nil, errors.Join(err, unlockErr, closeErr)
	}

	return &persistentVMLock{file: file, path: path, description: description}, nil
}

// prepareVMLifecycleLockPath is retained as the lifecycle-specific path helper
// for callers and tests that need to preflight the secure state location.
func prepareVMLifecycleLockPath(vmName string) (path, locksDir string, err error) {
	return preparePersistentVMLockPath(vmName, lifecycleLockSuffix, "lifecycle lock")
}

func prepareVMUsageLeasePath(vmName string) (path, locksDir string, err error) {
	return preparePersistentVMLockPath(vmName, usageLeaseSuffix, "VM usage lease")
}

func preparePersistentVMLockPath(vmName, suffix, description string) (path, locksDir string, err error) {
	if err := config.ValidateVMName(vmName); err != nil {
		return "", "", fmt.Errorf("invalid VM name for %s: %w", description, err)
	}
	if suffix != lifecycleLockSuffix && suffix != usageLeaseSuffix {
		return "", "", fmt.Errorf("invalid internal file suffix for %s", description)
	}
	namespaceDir, _, err := resolveNamedVMIdentityNamespace()
	if err != nil {
		return "", "", err
	}
	locksDir = filepath.Join(namespaceDir, "locks")
	// The platform config directory is allowed to be absent on a fresh
	// account. Create the Watermelon suffix, then authenticate every resulting
	// component before opening a file. This mirrors applied-policy state
	// creation and refuses symlinks or foreign-owned state after creation.
	if err := os.MkdirAll(locksDir, 0700); err != nil {
		return "", "", fmt.Errorf("creating %s directory %q: %w", description, locksDir, err)
	}
	appStateRoot := filepath.Dir(filepath.Dir(namespaceDir))
	userConfigDir := filepath.Dir(appStateRoot)
	if err := validateTrustedDirectoryIfPresent("user config directory", userConfigDir); err != nil {
		return "", "", err
	}
	// The user config directory is resolved and validated by
	// resolveNamedVMIdentityNamespace. Validate the private Watermelon-owned
	// suffix without following a final symlink in any component.
	for _, component := range []string{
		appStateRoot,
		filepath.Dir(namespaceDir),
		namespaceDir,
		locksDir,
	} {
		if err := ensurePrivateIdentityDirectory(component); err != nil {
			return "", "", fmt.Errorf("preparing %s state: %w", description, err)
		}
	}
	return filepath.Join(locksDir, fullPathDigest(vmName)+suffix), locksDir, nil
}

func openPersistentVMLockFile(path, description string) (*os.File, bool, error) {
	const flags = unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	fd, err := unix.Open(path, flags|unix.O_CREAT|unix.O_EXCL, 0600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Open(path, flags, 0)
	}
	if err != nil {
		return nil, false, fmt.Errorf("opening %s file %q without following symlinks: %w", description, path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, fmt.Errorf("opening %s file %q: invalid file descriptor", description, path)
	}
	if created {
		// O_CREAT is subject to the caller's umask, so set and subsequently verify
		// the exact private mode on a file that this call exclusively created.
		if err := file.Chmod(0600); err != nil {
			_ = file.Close()
			return nil, false, fmt.Errorf("securing %s file %q: %w", description, path, err)
		}
	}
	return file, created, nil
}

func validatePersistentVMLockFile(file *os.File, path, description string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspecting %s file %q: %w", description, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s path %q must be a regular file", description, path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("%s file %q is not owned by the current user", description, path)
	}
	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("%s file %q has insecure mode %04o; want 0600", description, path, info.Mode().Perm())
	}
	return nil
}

func validatePersistentVMLockPath(file *os.File, path, description string) error {
	if err := validatePersistentVMLockFile(file, path, description); err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("re-inspecting %s file %q: %w", description, path, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("re-inspecting %s path %q: %w", description, path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return fmt.Errorf("%s path %q changed while it was being acquired", description, path)
	}
	if !ownedByCurrentUser(current) || current.Mode().Perm() != 0600 {
		return fmt.Errorf("%s path %q became insecure while it was being acquired", description, path)
	}
	return nil
}

// release drops an advisory lock and closes its descriptor. The persistent
// file is intentionally left in place. It is safe to call more than once.
func (lock *persistentVMLock) release() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return nil
	}

	file := lock.file
	lock.file = nil
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("releasing %s %q: %w", lock.description, lock.path, unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("closing %s %q: %w", lock.description, lock.path, closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

// Release drops the lifecycle mutex without removing its persistent file.
func (lock *vmLifecycleLock) Release() error {
	if lock == nil {
		return nil
	}
	return lock.persistentVMLock.release()
}

// Release drops a shared or exclusive usage lease without removing its
// persistent file.
func (lease *vmUsageLease) Release() error {
	if lease == nil {
		return nil
	}
	return lease.persistentVMLock.release()
}
