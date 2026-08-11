package logs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

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
	before, err := os.Lstat(logPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("network log %q must be a regular file, not a symlink or non-regular file", logPath)
	}
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("network log %q changed while it was being opened", logPath)
	}

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// Clear removes the log file
func Clear(projectDir string) error {
	return ClearPath(LogPath(projectDir))
}

// ClearPath removes an explicitly resolved VM log without following symlinks.
func ClearPath(logPath string) error {
	if info, err := os.Lstat(logPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("network log %q must be a regular file, not a symlink or non-regular file", logPath)
		}
	} else if os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
	if err := os.Remove(logPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	fmt.Println("Log cleared")
	return nil
}
