package ask

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// DialogFunc is the function signature for showing a verdict dialog.
type DialogFunc func(process, domain string, port int, project string) string

// ShowDialog displays the best available host prompt and returns the verdict.
func ShowDialog(process, domain string, port int, project string) string {
	return defaultDialogForGOOS(runtime.GOOS)(process, domain, port, project)
}

func defaultDialogForGOOS(goos string) DialogFunc {
	if goos == "darwin" {
		return showAppleScriptDialog
	}
	return ShowTerminalPrompt
}

func showAppleScriptDialog(process, domain string, port int, project string) string {
	script := buildAppleScript(process, domain, port, project)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		// User cancelled or osascript failed; default to block.
		return VerdictBlock
	}
	return parseDialogResult(strings.TrimSpace(string(out)))
}

// ShowTerminalPrompt asks for a verdict in the terminal. It is used on hosts
// without a native dialog backend.
func ShowTerminalPrompt(process, domain string, port int, project string) string {
	return showTerminalPromptWith(openControllingTerminal, os.Stderr, process, domain, port, project)
}

func openControllingTerminal() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

func showTerminalPromptWith(openTTY func() (*os.File, error), diagnostic io.Writer, process, domain string, port int, project string) string {
	tty, err := openTTY()
	if err != nil {
		writeUnavailableTerminalDiagnostic(diagnostic)
		return VerdictBlock
	}
	defer tty.Close()

	if !fileIsTerminal(tty) || !fileIsForegroundTerminal(tty) {
		writeUnavailableTerminalDiagnostic(diagnostic)
		return VerdictBlock
	}
	return readTerminalPrompt(tty, tty, process, domain, port, project)
}

func writeUnavailableTerminalDiagnostic(out io.Writer) {
	fmt.Fprintln(out, "Watermelon network prompt requires a foreground controlling terminal; blocking by default")
}

func fileIsTerminal(file *os.File) bool {
	return file != nil && term.IsTerminal(int(file.Fd()))
}

func readTerminalPrompt(in io.Reader, out io.Writer, process, domain string, port int, project string) string {
	writeTerminalPrompt(out, process, domain, port, project)

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		fmt.Fprintln(out)
		return VerdictBlock
	}
	return terminalChoiceVerdict(scanner.Text())
}

func writeTerminalPrompt(out io.Writer, process, domain string, port int, project string) {
	writeTerminalPromptContext(out, process, domain, port, project)
	writeTerminalChoicePrompt(out)
}

func writeTerminalPromptContext(out io.Writer, process, domain string, port int, project string) {
	if process == "" {
		process = "A process"
	}

	fmt.Fprintf(out, "\nWatermelon network prompt\n")
	fmt.Fprintf(out, "%q is trying to connect to %s:%d\n", process, domain, port)
	if project != "" {
		fmt.Fprintf(out, "Project: %s\n", project)
	}
}

func writeTerminalChoicePrompt(out io.Writer) {
	fmt.Fprint(out, "Choose: block for session [b], allow once [o], always allow and save [a]: ")
}

func terminalChoiceVerdict(input string) string {
	switch normalizeTerminalChoice(input) {
	case "once":
		return VerdictAllowOnce
	case "always":
		return VerdictAlwaysAllow
	default:
		return VerdictBlock
	}
}

func normalizeTerminalChoice(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "o", "once", "allow once", "allow-once", "2":
		return "once"
	case "a", "always", "always allow", "always-allow", "3":
		return "always"
	default:
		return "block"
	}
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
		`display dialog (quote & "%s" & quote & " is trying to connect to\n%s:%d\n\nProject: %s\n\nBlock is remembered for this session.\nAlways Allow saves this domain to .watermelon.toml.") `+
			`with title "Watermelon" `+
			`buttons {"Block for Session", "Allow Once", "Always Allow and Save"} `+
			`default button "Block for Session" `+
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
