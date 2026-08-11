package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type testGuestExitError int

func (e testGuestExitError) Error() string      { return fmt.Sprintf("guest exit %d", e) }
func (e testGuestExitError) GuestExitCode() int { return int(e) }

func TestMainBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}
}

func TestExitCodeForErrorPropagatesEveryGuestStatus(t *testing.T) {
	for want := 1; want <= 255; want++ {
		got, isGuest := exitCodeForError(testGuestExitError(want))
		if !isGuest || got != want {
			t.Fatalf("guest status %d mapped to code=%d guest=%v", want, got, isGuest)
		}
	}
}

func TestExitCodeForErrorUsesOneForOwnedFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "ordinary error", err: errors.New("configuration failed")},
		{name: "invalid zero guest status", err: testGuestExitError(0)},
		{name: "invalid negative guest status", err: testGuestExitError(-1)},
		{name: "invalid large guest status", err: testGuestExitError(256)},
		{name: "wrapped guest status", err: fmt.Errorf("cleanup context: %w", testGuestExitError(42))},
		{name: "joined guest status", err: errors.Join(testGuestExitError(42), errors.New("cleanup failed"))},
		{name: "raw subprocess exit", err: rawExitError(t)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, isGuest := exitCodeForError(test.err)
			if isGuest || got != 1 {
				t.Fatalf("exitCodeForError() = (%d, %v), want (1, false)", got, isGuest)
			}
		})
	}
}

func TestExitCodeForErrorNilIsNotGuestFailure(t *testing.T) {
	got, isGuest := exitCodeForError(nil)
	if isGuest || got != 1 {
		t.Fatalf("exitCodeForError(nil) = (%d, %v), want (1, false)", got, isGuest)
	}
}

func TestExecuteCommandPrintsInvocationErrorsOnceWithUsage(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError string
		wantUsage string
	}{
		{
			name:      "unknown command",
			args:      []string{"frobnicate"},
			wantError: `unknown command "frobnicate" for "watermelon"`,
			wantUsage: "watermelon [command]",
		},
		{
			name:      "unknown flag",
			args:      []string{"status", "--definitely-invalid"},
			wantError: "unknown flag: --definitely-invalid",
			wantUsage: "watermelon status [flags]",
		},
		{
			name:      "unexpected positional argument",
			args:      []string{"status", "unexpected"},
			wantError: `unknown command "unexpected" for "watermelon status"`,
			wantUsage: "watermelon status [flags]",
		},
		{
			name:      "missing positional argument",
			args:      []string{"exec"},
			wantError: "requires at least 1 arg(s), only received 0",
			wantUsage: "watermelon exec [command] [args...] [flags]",
		},
		{
			name:      "invalid copy operands",
			args:      []string{"copy", "local-a", "local-b"},
			wantError: "neither src nor dst uses vmname:path syntax",
			wantUsage: "watermelon copy <src> <dest> [flags]",
		},
		{
			name:      "invalid name flag",
			args:      []string{"status", "--name", "bad:name"},
			wantError: `invalid --name "bad:name"`,
			wantUsage: "watermelon status [flags]",
		},
		{
			name:      "invalid workdir flag",
			args:      []string{"run", "--workdir", "relative"},
			wantError: `invalid --workdir "relative"`,
			wantUsage: "watermelon run [flags]",
		},
		{
			name:      "unknown help topic",
			args:      []string{"help", "frobnicate"},
			wantError: `unknown help topic "frobnicate"`,
			wantUsage: "watermelon help [command]",
		},
		{
			name:      "extra help topic argument",
			args:      []string{"help", "status", "extra"},
			wantError: `unknown help topic "status extra"`,
			wantUsage: "watermelon help [command]",
		},
		{
			name:      "unknown completion shell",
			args:      []string{"completion", "nonsense"},
			wantError: `unknown command "nonsense" for "watermelon completion"`,
			wantUsage: "watermelon completion [command]",
		},
		{
			name:      "extra completion argument",
			args:      []string{"completion", "bash", "extra"},
			wantError: `unknown command "extra" for "watermelon completion bash"`,
			wantUsage: "  watermelon completion bash\n\nFlags:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := executeRootForTest(t, newRootCmd(), tt.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if got := strings.Count(stderr, "Error:"); got != 1 {
				t.Fatalf("Error prefix count = %d, want 1; stderr=%q", got, stderr)
			}
			if got := strings.Count(stderr, tt.wantError); got != 1 {
				t.Fatalf("error text count = %d, want 1 for %q; stderr=%q", got, tt.wantError, stderr)
			}
			if got := strings.Count(stderr, "Usage:"); got != 1 {
				t.Fatalf("usage count = %d, want 1; stderr=%q", got, stderr)
			}
			if !strings.Contains(stderr, tt.wantUsage) {
				t.Fatalf("stderr does not contain usage %q: %q", tt.wantUsage, stderr)
			}
		})
	}
}

func TestExecuteCommandPrintsRuntimeErrorOnceWithoutUsage(t *testing.T) {
	rootCmd := newRootCmd()
	statusCmd := findCommand(t, rootCmd, "status")
	statusCmd.RunE = func(*cobra.Command, []string) error {
		return errors.New("configured policy is stale")
	}

	code, stdout, stderr := executeRootForTest(t, rootCmd, "status")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "Error: configured policy is stale\n" {
		t.Fatalf("stderr = %q, want one consistently formatted error", stderr)
	}
}

func TestExecuteCommandKeepsGuestFailureSilent(t *testing.T) {
	rootCmd := newRootCmd()
	execCmd := findCommand(t, rootCmd, "exec")
	execCmd.RunE = func(*cobra.Command, []string) error {
		return testGuestExitError(37)
	}

	code, stdout, stderr := executeRootForTest(t, rootCmd, "exec", "guest-command")
	if code != 37 {
		t.Fatalf("exit code = %d, want 37", code)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("guest failure output = stdout %q, stderr %q; want both empty", stdout, stderr)
	}
}

func TestExecuteCommandPrintsOwnedCleanupFailureWithoutUsage(t *testing.T) {
	rootCmd := newRootCmd()
	execCmd := findCommand(t, rootCmd, "exec")
	execCmd.RunE = func(*cobra.Command, []string) error {
		return errors.Join(testGuestExitError(37), errors.New("releasing usage lease"))
	}

	code, stdout, stderr := executeRootForTest(t, rootCmd, "exec", "guest-command")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got := strings.Count(stderr, "guest exit 37"); got != 1 {
		t.Fatalf("guest error count = %d, want 1; stderr=%q", got, stderr)
	}
	if got := strings.Count(stderr, "releasing usage lease"); got != 1 {
		t.Fatalf("cleanup error count = %d, want 1; stderr=%q", got, stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Fatalf("cleanup failure included usage: %q", stderr)
	}
}

func TestExecuteCommandLeavesHelpOnStdout(t *testing.T) {
	code, stdout, stderr := executeRootForTest(t, newRootCmd(), "status", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := strings.Count(stdout, "Usage:"); got != 1 {
		t.Fatalf("stdout usage count = %d, want 1; stdout=%q", got, stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestExecuteCommandHelpSubcommandShowsTargetHelp(t *testing.T) {
	code, stdout, stderr := executeRootForTest(t, newRootCmd(), "help", "status")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := strings.Count(stdout, "Usage:"); got != 1 {
		t.Fatalf("stdout usage count = %d, want 1; stdout=%q", got, stdout)
	}
	if !strings.Contains(stdout, "watermelon status [flags]") {
		t.Fatalf("stdout does not contain status usage: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func executeRootForTest(t *testing.T, rootCmd *cobra.Command, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	code := executeCommand(rootCmd)
	return code, stdout.String(), stderr.String()
}

func findCommand(t *testing.T, rootCmd *cobra.Command, name string) *cobra.Command {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{name})
	if err != nil {
		t.Fatalf("finding command %q: %v", name, err)
	}
	if cmd == rootCmd {
		t.Fatalf("command %q resolved to root", name)
	}
	return cmd
}

func rawExitError(t *testing.T) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainExitHelper")
	cmd.Env = append(os.Environ(), "GO_TEST_MAIN_EXIT_HELPER=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper error = %T %v, want *exec.ExitError", err, err)
	}
	if exitErr.ExitCode() != 42 {
		t.Fatalf("helper exit code = %d, want 42", exitErr.ExitCode())
	}
	return exitErr
}

func TestMainExitHelper(t *testing.T) {
	if os.Getenv("GO_TEST_MAIN_EXIT_HELPER") != "1" {
		return
	}
	os.Exit(42)
}
