//go:build !linux

package ask

import "os/exec"

// TerminalCoordinator is a no-op command runner on platforms whose prompt UI
// is already independent of terminal stdin (macOS uses AppleScript).
type TerminalCoordinator struct{}

func NewTerminalCoordinator() *TerminalCoordinator { return &TerminalCoordinator{} }

func (c *TerminalCoordinator) Dialog(process, domain string, port int, project string) string {
	return ShowDialog(process, domain, port, project)
}

func (c *TerminalCoordinator) Run(cmd *exec.Cmd) error { return cmd.Run() }
