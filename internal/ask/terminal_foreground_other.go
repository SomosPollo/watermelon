//go:build !linux

package ask

import "os"

// Supported macOS hosts use the native AppleScript dialog. Keep the terminal
// fallback conservative on other platforms while retaining its existing
// behavior for callers that select it explicitly.
func fileIsForegroundTerminal(file *os.File) bool {
	return fileIsTerminal(file)
}
