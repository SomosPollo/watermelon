package ask

import (
	"fmt"
	"os/exec"
	"strings"
)

// DialogFunc is the function signature for showing a verdict dialog.
type DialogFunc func(process, domain string, port int, project string) string

// ShowDialog displays a macOS native dialog via osascript and returns the verdict.
func ShowDialog(process, domain string, port int, project string) string {
	script := buildAppleScript(process, domain, port, project)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		// User cancelled or osascript failed — default to block
		return VerdictBlock
	}
	return parseDialogResult(strings.TrimSpace(string(out)))
}

func buildAppleScript(process, domain string, port int, project string) string {
	if process == "" {
		process = "A process"
	}
	// Escape backslashes and quotes for AppleScript string literals
	process = escapeAppleScript(process)
	domain = escapeAppleScript(domain)
	project = escapeAppleScript(project)

	return fmt.Sprintf(
		`display dialog (quote & "%s" & quote & " is trying to connect to\n%s:%d\n\nProject: %s") `+
			`with title "Watermelon" `+
			`buttons {"Block", "Allow Once", "Always Allow"} `+
			`default button "Block" `+
			`with icon caution`,
		process, domain, port, project,
	)
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func parseDialogResult(output string) string {
	switch {
	case strings.Contains(output, "Allow Once"):
		return VerdictAllowOnce
	case strings.Contains(output, "Always Allow"):
		return VerdictAlwaysAllow
	default:
		return VerdictBlock
	}
}
