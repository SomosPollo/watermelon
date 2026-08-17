package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func configureLifecycleLockTest(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	configHome := t.TempDir()
	limaHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("LIMA_HOME", limaHome)
	userConfig, err := effectiveUserConfigDir()
	if err != nil {
		t.Fatalf("effectiveUserConfigDir() error = %v", err)
	}
	if err := os.MkdirAll(userConfig, 0700); err != nil {
		t.Fatalf("creating platform user config directory: %v", err)
	}
	if err := os.Chmod(userConfig, 0700); err != nil {
		t.Fatalf("securing platform user config directory: %v", err)
	}
	return configHome, limaHome
}

func TestVMLifecycleLockCreatesPersistentPrivateFile(t *testing.T) {
	configureLifecycleLockTest(t)

	lock, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("acquireVMLifecycleLock() error = %v", err)
	}
	namespaceDir, _, err := resolveNamedVMIdentityNamespace()
	if err != nil {
		t.Fatalf("resolveNamedVMIdentityNamespace() error = %v", err)
	}
	wantPath := filepath.Join(namespaceDir, "locks", fullPathDigest("watermelon-dev")+".lock")
	if lock.path != wantPath {
		t.Fatalf("lock path = %q, want %q", lock.path, wantPath)
	}
	before, err := os.Lstat(lock.path)
	if err != nil {
		t.Fatalf("Lstat(lock) error = %v", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
		t.Fatalf("lock mode = %v, want regular 0600", before.Mode())
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Lstat(lock.path); err != nil {
		t.Fatalf("persistent lock disappeared after Release(): %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}

	reacquired, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("reacquireVMLifecycleLock() error = %v", err)
	}
	defer reacquired.Release()
	after, err := os.Lstat(reacquired.path)
	if err != nil {
		t.Fatalf("Lstat(reacquired lock) error = %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("reacquiring the lifecycle lock used a different backing file")
	}
}

func TestVMLifecycleLockCreatesMissingUserConfigDirectory(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "missing", "config")
	limaHome := filepath.Join(root, "lima")
	if err := os.Mkdir(limaHome, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("LIMA_HOME", limaHome)

	lock, err := acquireVMLifecycleLock("watermelon-fresh")
	if err != nil {
		t.Fatalf("first lifecycle lock on fresh config home: %v", err)
	}
	defer lock.Release()
	if info, err := os.Stat(configHome); err != nil || !info.IsDir() {
		t.Fatalf("config home was not created: info=%v err=%v", info, err)
	}
}

func TestVMLifecycleLockIsExclusive(t *testing.T) {
	configureLifecycleLockTest(t)

	lock, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("acquireVMLifecycleLock() error = %v", err)
	}
	defer lock.Release()

	fd, err := unix.Open(lock.path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("opening second lock descriptor: %v", err)
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("second nonblocking flock error = %v, want would-block", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("flock after Release() error = %v", err)
	}
	if err := unix.Flock(fd, unix.LOCK_UN); err != nil {
		t.Fatalf("unlocking second descriptor: %v", err)
	}
}

func TestVMLifecycleLockNamespacing(t *testing.T) {
	_, firstLimaHome := configureLifecycleLockTest(t)

	first, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("first lock error = %v", err)
	}
	firstPath := first.path
	if err := first.Release(); err != nil {
		t.Fatalf("releasing first lock: %v", err)
	}

	otherName, err := acquireVMLifecycleLock("watermelon-test")
	if err != nil {
		t.Fatalf("other-name lock error = %v", err)
	}
	otherNamePath := otherName.path
	if err := otherName.Release(); err != nil {
		t.Fatalf("releasing other-name lock: %v", err)
	}
	if firstPath == otherNamePath {
		t.Fatal("different VM names shared one lifecycle lock")
	}

	aliasRoot := t.TempDir()
	alias := filepath.Join(aliasRoot, "lima-alias")
	if err := os.Symlink(firstLimaHome, alias); err != nil {
		t.Fatalf("creating LIMA_HOME alias: %v", err)
	}
	t.Setenv("LIMA_HOME", alias)
	aliased, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("aliased LIMA_HOME lock error = %v", err)
	}
	aliasedPath := aliased.path
	if err := aliased.Release(); err != nil {
		t.Fatalf("releasing aliased lock: %v", err)
	}
	if aliasedPath != firstPath {
		t.Fatalf("canonical LIMA_HOME alias lock = %q, want %q", aliasedPath, firstPath)
	}

	t.Setenv("LIMA_HOME", t.TempDir())
	otherHome, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("other-home lock error = %v", err)
	}
	defer otherHome.Release()
	if otherHome.path == firstPath {
		t.Fatal("different canonical LIMA_HOME values shared one lifecycle lock")
	}
}

func TestVMLifecycleLockRejectsInvalidVMName(t *testing.T) {
	configureLifecycleLockTest(t)
	if _, err := acquireVMLifecycleLock("bad/name"); err == nil || !strings.Contains(err.Error(), "invalid VM name") {
		t.Fatalf("acquireVMLifecycleLock(invalid) error = %v", err)
	}
}

func TestVMLifecycleLockRejectsUnsafeExistingPath(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *testing.T, path string)
		wantErr string
	}{
		{
			name: "symlink",
			mutate: func(t *testing.T, path string) {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "without following symlinks",
		},
		{
			name: "insecure mode",
			mutate: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "insecure mode",
		},
		{
			name: "fifo",
			mutate: func(t *testing.T, path string) {
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "regular file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configureLifecycleLockTest(t)
			path, _, err := prepareVMLifecycleLockPath("watermelon-dev")
			if err != nil {
				t.Fatalf("prepareVMLifecycleLockPath() error = %v", err)
			}
			tt.mutate(t, path)
			if _, err := acquireVMLifecycleLock("watermelon-dev"); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("acquireVMLifecycleLock() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestVMLifecycleLockRejectsSymlinkedLockDirectory(t *testing.T) {
	configureLifecycleLockTest(t)
	namespaceDir, _, err := resolveNamedVMIdentityNamespace()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{
		filepath.Dir(filepath.Dir(namespaceDir)),
		filepath.Dir(namespaceDir),
		namespaceDir,
	} {
		if err := ensurePrivateIdentityDirectory(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(namespaceDir, "locks")); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireVMLifecycleLock("watermelon-dev"); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("acquireVMLifecycleLock() error = %v, want symlink-directory rejection", err)
	}
}

func TestVMUsageLeaseAllowsSharedHoldersAndMakesExclusiveWait(t *testing.T) {
	configureLifecycleLockTest(t)

	first, err := acquireSharedVMUsageLease("watermelon-dev")
	if err != nil {
		t.Fatalf("first shared usage lease error = %v", err)
	}
	firstReleased := false
	defer func() {
		if !firstReleased {
			_ = first.Release()
		}
	}()
	if filepath.Ext(first.path) != usageLeaseSuffix {
		t.Fatalf("usage lease path = %q, want %s suffix", first.path, usageLeaseSuffix)
	}

	type leaseResult struct {
		lease *vmUsageLease
		err   error
	}
	secondResult := make(chan leaseResult, 1)
	go func() {
		lease, err := acquireSharedVMUsageLease("watermelon-dev")
		secondResult <- leaseResult{lease: lease, err: err}
	}()

	var second *vmUsageLease
	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("second shared usage lease error = %v", result.err)
		}
		second = result.lease
	case <-time.After(2 * time.Second):
		_ = first.Release()
		firstReleased = true
		result := <-secondResult
		if result.lease != nil {
			_ = result.lease.Release()
		}
		t.Fatal("second shared usage lease blocked behind the first shared holder")
	}
	secondReleased := false
	defer func() {
		if !secondReleased {
			_ = second.Release()
		}
	}()

	exclusiveResult := make(chan leaseResult, 1)
	go func() {
		lease, err := acquireExclusiveVMUsageLease("watermelon-dev")
		exclusiveResult <- leaseResult{lease: lease, err: err}
	}()

	select {
	case result := <-exclusiveResult:
		if result.lease != nil {
			_ = result.lease.Release()
		}
		t.Fatalf("exclusive usage lease did not wait for shared holders: %v", result.err)
	case <-time.After(100 * time.Millisecond):
		// Expected: both shared leases are still active.
	}

	if err := first.Release(); err != nil {
		t.Fatalf("releasing first shared usage lease: %v", err)
	}
	firstReleased = true
	select {
	case result := <-exclusiveResult:
		if result.lease != nil {
			_ = result.lease.Release()
		}
		t.Fatalf("exclusive usage lease ignored the second shared holder: %v", result.err)
	case <-time.After(100 * time.Millisecond):
		// Expected: the second shared lease still excludes the writer.
	}

	if err := second.Release(); err != nil {
		t.Fatalf("releasing second shared usage lease: %v", err)
	}
	secondReleased = true
	select {
	case result := <-exclusiveResult:
		if result.err != nil {
			t.Fatalf("exclusive usage lease after shared releases: %v", result.err)
		}
		if result.lease == nil {
			t.Fatal("exclusive usage lease returned nil without an error")
		}
		if err := result.lease.Release(); err != nil {
			t.Fatalf("releasing exclusive usage lease: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive usage lease remained blocked after all shared holders released")
	}

	if _, err := os.Lstat(first.path); err != nil {
		t.Fatalf("persistent usage lease disappeared after Release(): %v", err)
	}
}

func TestVMUsageLeaseIsIndependentFromLifecycleLock(t *testing.T) {
	configureLifecycleLockTest(t)

	lifecycle, err := acquireVMLifecycleLock("watermelon-dev")
	if err != nil {
		t.Fatalf("lifecycle lock error = %v", err)
	}
	lifecycleReleased := false
	defer func() {
		if !lifecycleReleased {
			_ = lifecycle.Release()
		}
	}()

	type leaseResult struct {
		lease *vmUsageLease
		err   error
	}
	resultChannel := make(chan leaseResult, 1)
	go func() {
		lease, err := acquireExclusiveVMUsageLease("watermelon-dev")
		resultChannel <- leaseResult{lease: lease, err: err}
	}()

	select {
	case result := <-resultChannel:
		if result.err != nil {
			t.Fatalf("exclusive usage lease while lifecycle lock held: %v", result.err)
		}
		if result.lease.path == lifecycle.path {
			_ = result.lease.Release()
			t.Fatal("usage lease and lifecycle mutex share one backing file")
		}
		if err := result.lease.Release(); err != nil {
			t.Fatalf("releasing independent usage lease: %v", err)
		}
	case <-time.After(2 * time.Second):
		// Ensure a broken implementation does not leave its acquisition goroutine
		// behind after the test reports the dependency.
		if err := lifecycle.Release(); err != nil {
			t.Fatalf("releasing lifecycle lock after timeout: %v", err)
		}
		lifecycleReleased = true
		result := <-resultChannel
		if result.lease != nil {
			_ = result.lease.Release()
		}
		t.Fatal("usage lease blocked on the independent lifecycle lock")
	}

	if err := lifecycle.Release(); err != nil {
		t.Fatalf("releasing lifecycle lock: %v", err)
	}
	lifecycleReleased = true
}
