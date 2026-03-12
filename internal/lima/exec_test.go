package lima

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fakeExecCommand returns a function that creates exec.Cmd objects which
// re-invoke the test binary as a helper process, producing the given output.
func fakeExecCommand(output string, exitCode int) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestExecHelper", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_EXEC_HELPER=1",
			"GO_TEST_EXEC_OUTPUT="+output,
			"GO_TEST_EXEC_EXIT="+fmt.Sprint(exitCode),
		)
		return cmd
	}
}

// withFakeExec replaces execCommand for the duration of a test.
func withFakeExec(t *testing.T, output string, exitCode int) {
	t.Helper()
	old := execCommand
	execCommand = fakeExecCommand(output, exitCode)
	t.Cleanup(func() { execCommand = old })
}

// TestExecHelper is invoked by fakeExecCommand. It is NOT a real test.
func TestExecHelper(t *testing.T) {
	if os.Getenv("GO_TEST_EXEC_HELPER") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_TEST_EXEC_OUTPUT"))
	exitCode := 0
	if code := os.Getenv("GO_TEST_EXEC_EXIT"); code != "" {
		fmt.Sscan(code, &exitCode)
	}
	os.Exit(exitCode)
}

// fakeExecCommandCapture returns a mock that records args AND produces output.
func fakeExecCommandCapture(captured *[]string, output string) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		*captured = append(*captured, command+" "+strings.Join(args, " "))
		cs := []string{"-test.run=TestExecHelper", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_EXEC_HELPER=1",
			"GO_TEST_EXEC_OUTPUT="+output,
			"GO_TEST_EXEC_EXIT=0",
		)
		return cmd
	}
}
