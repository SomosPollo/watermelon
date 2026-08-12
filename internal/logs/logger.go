package logs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maxLogReadBytes int64 = 4 << 20

// LogPath returns the log path for a project
func LogPath(projectDir string) string {
	return filepath.Join(projectDir, ".watermelon", "logs.log")
}

// Read returns recent log entries for a project
func Read(projectDir string) ([]string, error) {
	return ReadPath(LogPath(projectDir))
}

// ReadPath returns recent entries from an explicitly resolved VM log path.
// Callers are responsible for resolving that path from trusted VM metadata.
func ReadPath(logPath string) ([]string, error) {
	parent, file, base, err := openLogFileNoFollow(logPath, unix.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	defer file.Close()
	if err := verifyOpenedLog(parent, file, base, logPath); err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), info.Size()-maxLogReadBytes)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(io.LimitReader(file, maxLogReadBytes))
	if start > 0 {
		// The bounded tail normally begins in the middle of a line. Drop that
		// partial entry so callers never mistake a fragment for a policy event.
		if _, err := reader.ReadString('\n'); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
	}

	var lines []string
	scanner := bufio.NewScanner(reader)
	// A guest can write the mounted log directly. Bound memory while still
	// accepting any complete entry within the total tail budget.
	scanner.Buffer(make([]byte, 64<<10), int(maxLogReadBytes)+1)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// Clear truncates the log file.
func Clear(projectDir string) error {
	return ClearPath(LogPath(projectDir))
}

// ClearPath truncates an explicitly resolved VM log without following symlinks.
// Keeping the inode in place ensures that a long-running logger which already
// has the file open continues writing to the visible log after it is cleared.
func ClearPath(logPath string) error {
	parent, file, base, err := openLogFileNoFollow(logPath, unix.O_WRONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	defer file.Close()
	if err := verifyOpenedLog(parent, file, base, logPath); err != nil {
		return err
	}

	// Recheck the descriptor and its name in the already authenticated parent
	// immediately before truncation. In particular, refuse a hard link that
	// could otherwise make clearing the log truncate an unrelated host file.
	if err := verifyOpenedLog(parent, file, base, logPath); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if err := verifyOpenedLog(parent, file, base, logPath); err != nil {
		return err
	}
	fmt.Println("Log cleared")
	return nil
}

// openLogFileNoFollow walks every directory component with openat and
// O_NOFOLLOW before opening the final entry. O_NOFOLLOW on a single absolute
// open protects only the basename and would still allow an untrusted guest to
// replace a parent such as .watermelon with a symlink.
func openLogFileNoFollow(logPath string, access int) (*os.File, *os.File, string, error) {
	if !filepath.IsAbs(logPath) || filepath.Clean(logPath) != logPath {
		return nil, nil, "", fmt.Errorf("network log path %q must be clean and absolute", logPath)
	}
	parentPath, base := filepath.Split(logPath)
	parentPath = filepath.Clean(parentPath)
	if base == "" || base == "." || base == ".." {
		return nil, nil, "", fmt.Errorf("network log path %q has an invalid filename", logPath)
	}

	const directoryFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_DIRECTORY | unix.O_NOFOLLOW
	rootFD, err := unix.Open(string(filepath.Separator), directoryFlags, 0)
	if err != nil {
		return nil, nil, "", fmt.Errorf("opening filesystem root for network log %q: %w", logPath, err)
	}
	parent := os.NewFile(uintptr(rootFD), string(filepath.Separator))
	if parent == nil {
		_ = unix.Close(rootFD)
		return nil, nil, "", fmt.Errorf("opening filesystem root for network log %q returned an invalid descriptor", logPath)
	}

	trimmed := strings.TrimPrefix(parentPath, string(filepath.Separator))
	if trimmed != "" && trimmed != "." {
		for _, component := range strings.Split(trimmed, string(filepath.Separator)) {
			nextFD, openErr := unix.Openat(int(parent.Fd()), component, directoryFlags, 0)
			if openErr != nil {
				_ = parent.Close()
				return nil, nil, "", fmt.Errorf("opening parent of network log %q without following symlinks: %w", logPath, openErr)
			}
			next := os.NewFile(uintptr(nextFD), component)
			if next == nil {
				_ = unix.Close(nextFD)
				_ = parent.Close()
				return nil, nil, "", fmt.Errorf("opening parent of network log %q returned an invalid descriptor", logPath)
			}
			_ = parent.Close()
			parent = next
		}
	}

	fd, err := unix.Openat(int(parent.Fd()), base, access|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		_ = parent.Close()
		return nil, nil, "", fmt.Errorf("opening network log %q without following symlinks: %w", logPath, err)
	}
	file := os.NewFile(uintptr(fd), logPath)
	if file == nil {
		_ = unix.Close(fd)
		_ = parent.Close()
		return nil, nil, "", fmt.Errorf("opening network log %q returned an invalid descriptor", logPath)
	}
	return parent, file, base, nil
}

func verifyOpenedLog(parent, file *os.File, base, logPath string) error {
	var opened, named unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &opened); err != nil {
		return fmt.Errorf("inspecting open network log %q: %w", logPath, err)
	}
	if err := unix.Fstatat(int(parent.Fd()), base, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("rechecking network log %q: %w", logPath, err)
	}
	if opened.Mode&unix.S_IFMT != unix.S_IFREG || named.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("network log %q must be a regular file, not a symlink or non-regular file", logPath)
	}
	if opened.Nlink != 1 || named.Nlink != 1 {
		return fmt.Errorf("network log %q must not have multiple hard links", logPath)
	}
	if opened.Dev != named.Dev || opened.Ino != named.Ino {
		return fmt.Errorf("network log %q changed while it was being accessed", logPath)
	}
	return nil
}
