package lima

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	sshConfigInclude = "~/.lima/*/ssh.config"
	sshConfigHeader  = "# Added by watermelon\nInclude " + sshConfigInclude + "\n\n"
)

// sshConfigBeforePublish is a test seam for exercising races with editors
// that publish their own atomic replacement at the last possible moment.
// Production callers leave it as the no-op below.
var sshConfigBeforePublish = func(string) error { return nil }

// GetSSHHost returns the SSH hostname for a VM.
func GetSSHHost(vmName string) string {
	return "lima-" + vmName
}

// EnsureSSHConfig adds the Lima Include directive to ~/.ssh/config if needed.
func EnsureSSHConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}
	configPath := filepath.Join(home, ".ssh", "config")
	return EnsureSSHConfigAt(configPath)
}

// EnsureSSHConfigAt adds the Lima Include directive to the specified SSH
// config. The containing directory and existing file must be real entries
// owned by the current user. Updates use a private temporary file and an
// atomic same-directory compare-and-swap publication, so a failed write or a
// concurrent replacement is detected without silently overwriting the user's
// version. The file lock coordinates cooperating in-place writers; as with any
// atomic file replacement, a writer that ignores that advisory lock and keeps
// writing an already-displaced inode cannot be coordinated reliably.
func EnsureSSHConfigAt(configPath string) error {
	configPath = filepath.Clean(configPath)
	base := filepath.Base(configPath)
	if base == "" || base == "." || base == ".." {
		return fmt.Errorf("ssh config path %q has an invalid filename", configPath)
	}

	sshDir := filepath.Dir(configPath)
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("creating SSH config directory: %w", err)
	}
	dir, err := openSecureSSHDirectory(sshDir)
	if err != nil {
		return err
	}
	defer dir.Close()

	existing, err := openSSHConfigAt(dir, base, configPath)
	if err != nil {
		return err
	}
	if existing != nil {
		defer func() {
			_ = unix.Flock(int(existing.Fd()), unix.LOCK_UN)
			_ = existing.Close()
		}()
	}

	var original []byte
	if existing != nil {
		original, err = io.ReadAll(existing)
		if err != nil {
			return fmt.Errorf("reading SSH config %q: %w", configPath, err)
		}
	}

	includePresent := hasActiveLimaInclude(original)
	modeSecure := existing != nil && sshConfigModeSecure(existing)
	if includePresent && modeSecure {
		return verifySSHConfigAt(dir, existing, base, configPath)
	}

	updated := original
	if !includePresent {
		updated = make([]byte, 0, len(sshConfigHeader)+len(original))
		updated = append(updated, sshConfigHeader...)
		updated = append(updated, original...)
	}
	if err := replaceSSHConfigAt(dir, existing, base, configPath, original, updated); err != nil {
		return err
	}
	return nil
}

func openSecureSSHDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("opening SSH config directory %q without following symlinks: %w", path, err)
	}
	dir := os.NewFile(uintptr(fd), path)
	if dir == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("opening SSH config directory %q returned an invalid descriptor", path)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("inspecting SSH config directory %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = dir.Close()
		return nil, fmt.Errorf("SSH config directory %q must be a real directory", path)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		_ = dir.Close()
		return nil, fmt.Errorf("SSH config directory %q is not owned by the current user", path)
	}
	if err := dir.Chmod(0700); err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("securing SSH config directory %q: %w", path, err)
	}
	opened, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("re-inspecting SSH config directory %q: %w", path, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("re-inspecting SSH config directory %q: %w", path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(opened, current) {
		_ = dir.Close()
		return nil, fmt.Errorf("SSH config directory %q changed while it was being opened", path)
	}
	return dir, nil
}

func openSSHConfigAt(dir *os.File, base, configPath string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening SSH config %q without following symlinks: %w", configPath, err)
	}
	file := os.NewFile(uintptr(fd), configPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("opening SSH config %q returned an invalid descriptor", configPath)
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("locking SSH config %q: %w", configPath, err)
	}
	if err := verifySSHConfigAt(dir, file, base, configPath); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func verifySSHConfigAt(dir, file *os.File, base, configPath string) error {
	var opened, named unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &opened); err != nil {
		return fmt.Errorf("inspecting open SSH config %q: %w", configPath, err)
	}
	if err := unix.Fstatat(int(dir.Fd()), base, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("rechecking SSH config %q: %w", configPath, err)
	}
	if opened.Mode&unix.S_IFMT != unix.S_IFREG || named.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("SSH config %q must be a regular file, not a symlink or non-regular file", configPath)
	}
	if opened.Nlink != 1 || named.Nlink != 1 {
		return fmt.Errorf("SSH config %q must not have multiple hard links", configPath)
	}
	if opened.Uid != uint32(os.Geteuid()) || named.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("SSH config %q is not owned by the current user", configPath)
	}
	if opened.Dev != named.Dev || opened.Ino != named.Ino {
		return fmt.Errorf("SSH config %q changed while it was being accessed", configPath)
	}
	return nil
}

func sshConfigModeSecure(file *os.File) bool {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return false
	}
	const specialAndPermissionBits = unix.S_ISUID | unix.S_ISGID | unix.S_ISVTX | 0777
	return stat.Mode&specialAndPermissionBits == 0600
}

func replaceSSHConfigAt(dir, existing *os.File, base, configPath string, original, updated []byte) (err error) {
	tmp, tmpName, err := createSSHConfigTempAt(dir, configPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		if tmpName != "" {
			_ = unix.Unlinkat(int(dir.Fd()), tmpName, 0)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("securing temporary SSH config for %q: %w", configPath, err)
	}
	if _, err := tmp.Write(updated); err != nil {
		return fmt.Errorf("writing temporary SSH config for %q: %w", configPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temporary SSH config for %q: %w", configPath, err)
	}
	if err := verifySSHConfigAt(dir, tmp, tmpName, configPath); err != nil {
		return fmt.Errorf("verifying temporary SSH config: %w", err)
	}

	if existing == nil {
		var named unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), base, &named, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			if err == nil {
				return fmt.Errorf("SSH config %q appeared while it was being created", configPath)
			}
			return fmt.Errorf("rechecking absent SSH config %q: %w", configPath, err)
		}
	} else {
		if err := verifySSHConfigAt(dir, existing, base, configPath); err != nil {
			return err
		}
		if err := verifySSHConfigContents(existing, original, configPath); err != nil {
			return err
		}
	}

	if err := sshConfigBeforePublish(configPath); err != nil {
		return fmt.Errorf("before publishing SSH config %q: %w", configPath, err)
	}

	if existing == nil {
		// A no-replace rename closes the gap between the absence check above and
		// publication. If an editor creates the file in that interval, its file
		// wins and Watermelon's temporary file is removed by the deferred cleanup.
		if err := renameSSHConfigNoReplace(int(dir.Fd()), tmpName, base); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return fmt.Errorf("SSH config %q appeared while it was being created", configPath)
			}
			return fmt.Errorf("atomically creating SSH config %q: %w", configPath, err)
		}
		tmpName = ""
	} else {
		// An atomic exchange gives us the directory entry that was current at the
		// exact publication instant. Only commit when that displaced entry is the
		// same inode we opened and verified; otherwise swap it back so a concurrent
		// editor's replacement remains at the public path.
		if err := exchangeSSHConfigNames(int(dir.Fd()), tmpName, base); err != nil {
			return fmt.Errorf("atomically exchanging SSH config %q: %w", configPath, err)
		}
		verifyErr := verifySSHConfigAt(dir, existing, tmpName, configPath)
		if verifyErr == nil {
			// Exchanging the names removes the public path from an in-place
			// writer. Re-read the displaced inode now as part of the CAS: the
			// inode identity alone cannot detect a truncate/write that happened
			// between the pre-publication comparison and the exchange.
			verifyErr = verifySSHConfigContents(existing, original, configPath)
		}
		if verifyErr != nil {
			recoveryPath := filepath.Join(filepath.Dir(configPath), tmpName)
			if rollbackErr := rollbackSSHConfigExchange(dir, tmp, tmpName, base, configPath); rollbackErr != nil {
				// The recovery name may now contain a second concurrent editor's
				// version. Never unlink an entry we can no longer prove is ours.
				tmpName = ""
				return fmt.Errorf("SSH config %q changed during atomic publication (%v); preserving the displaced concurrent version at %q after rollback failed: %w", configPath, verifyErr, recoveryPath, rollbackErr)
			}
			return fmt.Errorf("SSH config %q changed during atomic publication; the concurrent version was restored: %w", configPath, verifyErr)
		}
		if err := unix.Unlinkat(int(dir.Fd()), tmpName, 0); err != nil {
			return fmt.Errorf("removing replaced SSH config inode for %q: %w", configPath, err)
		}
		tmpName = ""
	}

	if err := verifySSHConfigAt(dir, tmp, base, configPath); err != nil {
		return err
	}
	if !sshConfigModeSecure(tmp) {
		return fmt.Errorf("SSH config %q does not have secure 0600 permissions after update", configPath)
	}
	// Some supported filesystems/platforms do not implement fsync for directory
	// descriptors and report EINVAL. The file itself has already been synced;
	// retain the directory durability barrier wherever it is supported.
	if err := dir.Sync(); err != nil && !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("syncing SSH config directory %q: %w", filepath.Dir(configPath), err)
	}
	return nil
}

func verifySSHConfigContents(file *os.File, original []byte, configPath string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("re-reading SSH config %q before update: %w", configPath, err)
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("re-reading SSH config %q before update: %w", configPath, err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("SSH config %q changed while it was being updated", configPath)
	}
	return nil
}

// rollbackSSHConfigExchange restores the entry displaced by an exchange after
// discovering that it was not the inode originally inspected. It refuses to
// overwrite another last-moment editor: in that case the caller preserves the
// recovery entry under its private temporary name and reports its path.
func rollbackSSHConfigExchange(dir, tmp *os.File, tmpName, base, configPath string) error {
	if err := verifySSHConfigAt(dir, tmp, base, configPath); err != nil {
		return fmt.Errorf("the published temporary file was replaced before rollback: %w", err)
	}
	if err := exchangeSSHConfigNames(int(dir.Fd()), tmpName, base); err != nil {
		return fmt.Errorf("restoring the displaced SSH config: %w", err)
	}
	if err := verifySSHConfigAt(dir, tmp, tmpName, configPath); err != nil {
		return fmt.Errorf("another SSH config edit raced with rollback: %w", err)
	}
	return nil
}

func createSSHConfigTempAt(dir *os.File, configPath string) (*os.File, string, error) {
	for attempts := 0; attempts < 128; attempts++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", fmt.Errorf("generating temporary SSH config name for %q: %w", configPath, err)
		}
		name := fmt.Sprintf(".watermelon-ssh-%x.tmp", random[:])
		fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("creating temporary SSH config for %q: %w", configPath, err)
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(int(dir.Fd()), name, 0)
			return nil, "", fmt.Errorf("creating temporary SSH config for %q returned an invalid descriptor", configPath)
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("creating a unique temporary SSH config for %q", configPath)
}

// hasActiveLimaInclude recognizes the actual OpenSSH Include directive rather
// than a comment or an arbitrary occurrence of the same bytes. The keyword is
// case-insensitive, supports OpenSSH's optional equals separator, and may list
// multiple paths. Escaped paths are deliberately not treated as equivalent to
// the glob Watermelon installs.
func hasActiveLimaInclude(config []byte) bool {
	for _, line := range strings.Split(string(config), "\n") {
		keyword, arguments, ok := parseSSHDirective(strings.TrimSuffix(line, "\r"))
		if !ok {
			continue
		}
		// OpenSSH Host and Match sections extend to the next section (or end of
		// file). An Include below either one is conditional and cannot guarantee
		// that Lima's hosts load their generated configuration.
		if strings.EqualFold(keyword, "Host") || strings.EqualFold(keyword, "Match") {
			return false
		}
		if !strings.EqualFold(keyword, "Include") {
			continue
		}
		for _, argument := range arguments {
			if !argument.escaped && argument.value == sshConfigInclude {
				return true
			}
		}
	}
	return false
}

type sshArgument struct {
	value   string
	escaped bool
}

func parseSSHDirective(line string) (string, []sshArgument, bool) {
	i := 0
	for i < len(line) && isSSHSpace(line[i]) {
		i++
	}
	if i == len(line) || line[i] == '#' {
		return "", nil, false
	}
	keywordStart := i
	for i < len(line) && !isSSHSpace(line[i]) && line[i] != '=' {
		i++
	}
	keyword := line[keywordStart:i]
	if keyword == "" {
		return "", nil, false
	}
	hadSeparator := false
	for i < len(line) && isSSHSpace(line[i]) {
		hadSeparator = true
		i++
	}
	if i < len(line) && line[i] == '=' {
		hadSeparator = true
		i++
		for i < len(line) && isSSHSpace(line[i]) {
			i++
		}
	}
	if !hadSeparator {
		return "", nil, false
	}

	var arguments []sshArgument
	for i < len(line) {
		for i < len(line) && isSSHSpace(line[i]) {
			i++
		}
		if i == len(line) || line[i] == '#' {
			break
		}

		var value strings.Builder
		quoted := false
		escaped := false
		for i < len(line) {
			c := line[i]
			if c == '\\' {
				escaped = true
				i++
				if i == len(line) {
					return "", nil, false
				}
				value.WriteByte(line[i])
				i++
				continue
			}
			if c == '"' {
				quoted = !quoted
				i++
				continue
			}
			if !quoted && isSSHSpace(c) {
				break
			}
			value.WriteByte(c)
			i++
		}
		if quoted || value.Len() == 0 {
			return "", nil, false
		}
		arguments = append(arguments, sshArgument{value: value.String(), escaped: escaped})
	}
	return keyword, arguments, len(arguments) > 0
}

func isSSHSpace(c byte) bool {
	return c == ' ' || c == '\t'
}
