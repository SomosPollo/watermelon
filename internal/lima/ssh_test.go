package lima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGetSSHHost(t *testing.T) {
	vmName := "watermelon-myproject-a1b2c3d4"
	if got, want := GetSSHHost(vmName), "lima-watermelon-myproject-a1b2c3d4"; got != want {
		t.Errorf("GetSSHHost(%q) = %q, want %q", vmName, got, want)
	}
}

func TestEnsureSSHConfigAtCreatesPrivateConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".ssh", "config")
	if err := EnsureSSHConfigAt(configPath); err != nil {
		t.Fatalf("EnsureSSHConfigAt() error = %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading SSH config: %v", err)
	}
	if string(content) != sshConfigHeader {
		t.Fatalf("new SSH config = %q, want %q", content, sshConfigHeader)
	}
	requireMode(t, filepath.Dir(configPath), 0700)
	requireMode(t, configPath, 0600)
}

func TestEnsureSSHConfigAtPreservesContentAndIsIdempotent(t *testing.T) {
	configPath := newSSHConfigFixture(t, "Host example\n  HostName example.com\n", 0600)
	if err := EnsureSSHConfigAt(configPath); err != nil {
		t.Fatalf("first EnsureSSHConfigAt() error = %v", err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(first), sshConfigHeader+"Host example\n  HostName example.com\n"; got != want {
		t.Fatalf("updated SSH config = %q, want %q", got, want)
	}
	firstInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := EnsureSSHConfigAt(configPath); err != nil {
		t.Fatalf("second EnsureSSHConfigAt() error = %v", err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatalf("idempotent call changed content from %q to %q", first, second)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("idempotent call unnecessarily replaced an already-secure config")
	}
	if strings.Count(string(second), "Include "+sshConfigInclude) != 1 {
		t.Fatalf("idempotent config contains duplicate Lima include:\n%s", second)
	}
}

func TestEnsureSSHConfigAtRecognizesValidActiveIncludes(t *testing.T) {
	tests := []string{
		"Include ~/.lima/*/ssh.config\n",
		"  include \"~/.lima/*/ssh.config\" # managed by Lima\n",
		"Include=~/.lima/*/ssh.config\n",
		"Include wrong/path ~/.lima/*/ssh.config\n",
		"Include = ~/.lima/*/ssh.config\r\n",
	}
	for _, existing := range tests {
		t.Run(strings.ReplaceAll(strings.TrimSpace(existing), "/", "_"), func(t *testing.T) {
			configPath := newSSHConfigFixture(t, existing, 0600)
			before, err := os.Stat(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := EnsureSSHConfigAt(configPath); err != nil {
				t.Fatalf("EnsureSSHConfigAt() error = %v", err)
			}
			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != existing {
				t.Fatalf("valid active include was rewritten:\ngot:  %q\nwant: %q", content, existing)
			}
			if !os.SameFile(before, after) {
				t.Fatal("valid active include unnecessarily replaced the config")
			}
		})
	}
}

func TestEnsureSSHConfigAtDoesNotAcceptCommentsOrWrongPaths(t *testing.T) {
	tests := map[string]string{
		"comment":               "# Include ~/.lima/*/ssh.config\n",
		"indented comment":      "   # Include ~/.lima/*/ssh.config\n",
		"arbitrary substring":   "HostName Include ~/.lima/*/ssh.config\n",
		"wrong suffix":          "Include ~/.lima/*/ssh.config.backup\n",
		"wrong Lima directory":  "Include ~/.lima-other/*/ssh.config\n",
		"missing instance glob": "Include ~/.lima/ssh.config\n",
		"escaped glob":          "Include ~/.lima/\\*/ssh.config\n",
		"unterminated quote":    "Include \"~/.lima/*/ssh.config\n",
		"joined comment":        "Include ~/.lima/*/ssh.config#disabled\n",
		"conditional include":   "Host example.com\n  Include ~/.lima/*/ssh.config\n",
		"matched include":       "Match host example.com\n  Include ~/.lima/*/ssh.config\n",
	}
	for name, existing := range tests {
		t.Run(name, func(t *testing.T) {
			configPath := newSSHConfigFixture(t, existing, 0600)
			if err := EnsureSSHConfigAt(configPath); err != nil {
				t.Fatalf("EnsureSSHConfigAt() error = %v", err)
			}
			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(content), sshConfigHeader+existing; got != want {
				t.Fatalf("SSH config = %q, want an active exact include prepended as %q", got, want)
			}
		})
	}
}

func TestEnsureSSHConfigAtRepairsInsecureExistingMode(t *testing.T) {
	existing := "Include ~/.lima/*/ssh.config\n\nHost example\n"
	configPath := newSSHConfigFixture(t, existing, 0666)
	if err := EnsureSSHConfigAt(configPath); err != nil {
		t.Fatalf("EnsureSSHConfigAt() error = %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != existing {
		t.Fatalf("repairing permissions changed content to %q", content)
	}
	requireMode(t, configPath, 0600)
}

func TestEnsureSSHConfigAtRepairsInsecureDirectoryMode(t *testing.T) {
	configPath := newSSHConfigFixture(t, "Include ~/.lima/*/ssh.config\n", 0600)
	sshDir := filepath.Dir(configPath)
	if err := os.Chmod(sshDir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSSHConfigAt(configPath); err != nil {
		t.Fatalf("EnsureSSHConfigAt() error = %v", err)
	}
	requireMode(t, sshDir, 0700)
}

func TestEnsureSSHConfigAtRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	original := []byte("Host must-not-change\n")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sshDir, "config")
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSSHConfigAt(configPath); err == nil {
		t.Fatal("EnsureSSHConfigAt() accepted a symlink config")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(original) {
		t.Fatalf("symlink target changed to %q", content)
	}
}

func TestEnsureSSHConfigAtRejectsSymlinkDirectory(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	if err := os.Mkdir(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.Symlink(targetDir, sshDir); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSSHConfigAt(filepath.Join(sshDir, "config")); err == nil {
		t.Fatal("EnsureSSHConfigAt() accepted a symlink SSH directory")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "config")); !os.IsNotExist(err) {
		t.Fatalf("symlink directory target was modified: %v", err)
	}
}

func TestEnsureSSHConfigAtRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		t.Fatal(err)
	}

	t.Run("directory", func(t *testing.T) {
		configPath := filepath.Join(sshDir, "directory-config")
		if err := os.Mkdir(configPath, 0700); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSSHConfigAt(configPath); err == nil {
			t.Fatal("EnsureSSHConfigAt() accepted a directory config")
		}
	})

	t.Run("fifo", func(t *testing.T) {
		configPath := filepath.Join(sshDir, "fifo-config")
		if err := unix.Mkfifo(configPath, 0600); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSSHConfigAt(configPath); err == nil {
			t.Fatal("EnsureSSHConfigAt() accepted a FIFO config")
		}
	})
}

func TestEnsureSSHConfigAtRejectsHardLinkedConfig(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "other-user-content")
	original := []byte("Host must-not-change\n")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sshDir, "config")
	if err := os.Link(target, configPath); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSSHConfigAt(configPath); err == nil {
		t.Fatal("EnsureSSHConfigAt() accepted a multiply-linked config")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(original) {
		t.Fatalf("hard-link target changed to %q", content)
	}
}

func TestEnsureSSHConfigAtDoesNotOverwriteConcurrentlyCreatedConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".ssh", "config")
	concurrent := []byte("Host concurrent-create\n")
	withSSHConfigBeforePublish(t, func(path string) error {
		return os.WriteFile(path, concurrent, 0600)
	})

	err := EnsureSSHConfigAt(configPath)
	if err == nil || !strings.Contains(err.Error(), "appeared while it was being created") {
		t.Fatalf("EnsureSSHConfigAt() error = %v, want concurrent-create rejection", err)
	}
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != string(concurrent) {
		t.Fatalf("concurrently created config = %q, want %q", content, concurrent)
	}
	requireNoSSHConfigTemps(t, filepath.Dir(configPath))
}

func TestEnsureSSHConfigAtRestoresConcurrentAtomicReplacement(t *testing.T) {
	configPath := newSSHConfigFixture(t, "Host original\n", 0600)
	concurrent := []byte("Host concurrent-save\n")
	withSSHConfigBeforePublish(t, func(path string) error {
		replacement := filepath.Join(filepath.Dir(path), ".editor-save")
		if err := os.WriteFile(replacement, concurrent, 0600); err != nil {
			return err
		}
		return os.Rename(replacement, path)
	})

	err := EnsureSSHConfigAt(configPath)
	if err == nil || !strings.Contains(err.Error(), "concurrent version was restored") {
		t.Fatalf("EnsureSSHConfigAt() error = %v, want concurrent-replacement rejection", err)
	}
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != string(concurrent) {
		t.Fatalf("concurrently replaced config = %q, want %q", content, concurrent)
	}
	requireNoSSHConfigTemps(t, filepath.Dir(configPath))
}

func TestEnsureSSHConfigAtRestoresConcurrentInPlaceWrite(t *testing.T) {
	configPath := newSSHConfigFixture(t, "Host original\n", 0600)
	concurrent := []byte("Host concurrent-in-place-save\n")
	withSSHConfigBeforePublish(t, func(path string) error {
		return os.WriteFile(path, concurrent, 0600)
	})

	err := EnsureSSHConfigAt(configPath)
	if err == nil || !strings.Contains(err.Error(), "concurrent version was restored") {
		t.Fatalf("EnsureSSHConfigAt() error = %v, want in-place-write rejection", err)
	}
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != string(concurrent) {
		t.Fatalf("concurrently written config = %q, want %q", content, concurrent)
	}
	requireNoSSHConfigTemps(t, filepath.Dir(configPath))
}

func withSSHConfigBeforePublish(t *testing.T, hook func(string) error) {
	t.Helper()
	previous := sshConfigBeforePublish
	sshConfigBeforePublish = hook
	t.Cleanup(func() {
		sshConfigBeforePublish = previous
	})
}

func requireNoSSHConfigTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".watermelon-ssh-") {
			t.Fatalf("temporary SSH config %q was not cleaned up", entry.Name())
		}
	}
}

func newSSHConfigFixture(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), ".ssh", "config")
	if err := os.Mkdir(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// os.WriteFile honors the process umask, so force the deliberately insecure
	// fixture modes used by permission-repair tests.
	if err := os.Chmod(configPath, mode); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
