package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

type testExecGuestExitError struct {
	code int
}

type testAskTerminalCoordinator struct {
	dialogCalls int
}

func (c *testAskTerminalCoordinator) Run(*exec.Cmd) error { return nil }

func (c *testAskTerminalCoordinator) Dialog(string, string, int, string) string {
	c.dialogCalls++
	return "block"
}

func (e *testExecGuestExitError) Error() string      { return fmt.Sprintf("guest exit %d", e.code) }
func (e *testExecGuestExitError) GuestExitCode() int { return e.code }

func TestFinishExecCommandPreservesOnlyCleanGuestResult(t *testing.T) {
	t.Run("clean guest exit", func(t *testing.T) {
		cmd := NewExecCmd()
		guestErr := &testExecGuestExitError{code: 37}
		got := finishExecCommand(cmd, guestErr, nil)
		if got != guestErr {
			t.Fatalf("finishExecCommand() = %T %v, want exact guest error", got, got)
		}
		if !cmd.SilenceErrors || !cmd.SilenceUsage {
			t.Fatalf("guest result left Cobra noise enabled: errors=%v usage=%v", cmd.SilenceErrors, cmd.SilenceUsage)
		}
	})

	t.Run("guest exit with cleanup failure", func(t *testing.T) {
		cmd := NewExecCmd()
		guestErr := &testExecGuestExitError{code: 37}
		cleanupErr := errors.New("releasing usage lease")
		got := finishExecCommand(cmd, guestErr, cleanupErr)
		if !errors.Is(got, guestErr) || !errors.Is(got, cleanupErr) {
			t.Fatalf("finishExecCommand() = %v, want joined guest and cleanup errors", got)
		}
		if _, ok := got.(guestExitCoder); ok {
			t.Fatalf("cleanup failure retained a top-level guest marker: %T", got)
		}
		if cmd.SilenceErrors || cmd.SilenceUsage {
			t.Fatalf("cleanup failure silenced Cobra: errors=%v usage=%v", cmd.SilenceErrors, cmd.SilenceUsage)
		}
	})

	t.Run("ordinary exec failure", func(t *testing.T) {
		cmd := NewExecCmd()
		execErr := errors.New("launching limactl")
		got := finishExecCommand(cmd, execErr, nil)
		if got != execErr {
			t.Fatalf("finishExecCommand() = %v, want exact ordinary error", got)
		}
		if cmd.SilenceErrors || cmd.SilenceUsage {
			t.Fatalf("ordinary failure silenced Cobra: errors=%v usage=%v", cmd.SilenceErrors, cmd.SilenceUsage)
		}
	})

	t.Run("cleanup after success", func(t *testing.T) {
		cmd := NewExecCmd()
		cleanupErr := errors.New("releasing usage lease")
		got := finishExecCommand(cmd, nil, cleanupErr)
		if !errors.Is(got, cleanupErr) {
			t.Fatalf("finishExecCommand() = %v, want cleanup error", got)
		}
		if _, ok := got.(guestExitCoder); ok {
			t.Fatalf("cleanup failure was marked as guest exit: %T", got)
		}
	})
}

func TestCleanGuestResultProducesNoCobraErrorOrUsage(t *testing.T) {
	cmd := NewExecCmd()
	guestErr := &testExecGuestExitError{code: 37}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return finishExecCommand(cmd, guestErr, nil)
	}
	cmd.SetArgs([]string{"guest-command"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	err := cmd.Execute()
	if err != guestErr {
		t.Fatalf("Execute() error = %T %v, want exact guest error", err, err)
	}
	if output.Len() != 0 {
		t.Fatalf("Cobra printed output for guest result: %q", output.String())
	}
}

func TestExecFlagParsingStopsAtGuestCommand(t *testing.T) {
	tests := []struct {
		name        string
		input       []string
		wantVMName  string
		wantCommand []string
	}{
		{
			name:        "watermelon name before command",
			input:       []string{"--name", "dev", "docker", "run", "--name", "web", "nginx"},
			wantVMName:  "dev",
			wantCommand: []string{"docker", "run", "--name", "web", "nginx"},
		},
		{
			name:        "optional separator",
			input:       []string{"--name=dev", "--", "docker", "run", "--name", "web", "nginx"},
			wantVMName:  "dev",
			wantCommand: []string{"docker", "run", "--name", "web", "nginx"},
		},
		{
			name:        "guest name without watermelon name",
			input:       []string{"docker", "run", "--name", "web", "nginx"},
			wantCommand: []string{"docker", "run", "--name", "web", "nginx"},
		},
		{
			name:        "guest help flag",
			input:       []string{"npm", "install", "--help"},
			wantCommand: []string{"npm", "install", "--help"},
		},
		{
			name:        "guest unknown flag",
			input:       []string{"tool", "--definitely-not-a-watermelon-flag", "value"},
			wantCommand: []string{"tool", "--definitely-not-a-watermelon-flag", "value"},
		},
		{
			name:        "single compound command remains one argument",
			input:       []string{`printf '%s' "$HOME" && echo done`},
			wantCommand: []string{`printf '%s' "$HOME" && echo done`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewExecCmd()
			if err := cmd.ParseFlags(test.input); err != nil {
				t.Fatalf("ParseFlags(%q) error = %v", test.input, err)
			}

			gotVMName, err := cmd.Flags().GetString("name")
			if err != nil {
				t.Fatalf("reading --name: %v", err)
			}
			if gotVMName != test.wantVMName {
				t.Fatalf("--name = %q, want %q", gotVMName, test.wantVMName)
			}

			if got := cmd.Flags().Args(); !reflect.DeepEqual(got, test.wantCommand) {
				t.Fatalf("guest args = %#v, want %#v", got, test.wantCommand)
			}
		})
	}
}

func TestExecWatermelonFlagsMustPrecedeGuestCommand(t *testing.T) {
	cmd := NewExecCmd()
	input := []string{"docker", "run", "--name", "web", "--", "--name", "dev"}
	if err := cmd.ParseFlags(input); err != nil {
		t.Fatalf("ParseFlags(%q) error = %v", input, err)
	}

	gotVMName, err := cmd.Flags().GetString("name")
	if err != nil {
		t.Fatalf("reading --name: %v", err)
	}
	if gotVMName != "" {
		t.Fatalf("Watermelon --name = %q, want empty after guest command starts", gotVMName)
	}
	if got := cmd.Flags().Args(); !reflect.DeepEqual(got, input) {
		t.Fatalf("guest args = %#v, want %#v", got, input)
	}
}

func TestAskExecSharesCoordinatorBetweenServerAndAttachedCommand(t *testing.T) {
	configureLifecycleLockTest(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"ask\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedPolicySnapshot(project, cfg); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	fakeCoordinator := &testAskTerminalCoordinator{}
	oldStatus, oldProjectMount, oldVerify := cliGetVMStatus, cliProjectMountSource, cliVerifyPolicy
	oldExec, oldExecWithRunner := cliExecVM, cliExecVMWithRunner
	oldCoordinator, oldStartServer := cliNewAskCoordinator, cliStartAskServer
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	cliVerifyPolicy = func(string) error { return nil }
	cliNewAskCoordinator = func() askTerminalCoordinator { return fakeCoordinator }
	var serverDialog ask.DialogFunc
	cliStartAskServer = func(dir, vmName string, dialog ask.DialogFunc) (net.Listener, error) {
		if dir != project || vmName != lima.VMNameFromPath(project) {
			t.Fatalf("ask server target = %q %q", dir, vmName)
		}
		serverDialog = dialog
		return net.Listen("tcp", "127.0.0.1:0")
	}
	plainExecCalls := 0
	cliExecVM = func(string, []string, ...string) error {
		plainExecCalls++
		return nil
	}
	runnerExecCalls := 0
	cliExecVMWithRunner = func(_ string, args []string, runner lima.CommandRunner, _ ...string) error {
		runnerExecCalls++
		if !reflect.DeepEqual(args, []string{"true"}) {
			t.Fatalf("guest args = %#v, want [true]", args)
		}
		if runner != fakeCoordinator {
			t.Fatalf("attached command runner = %T %p, want shared coordinator %p", runner, runner, fakeCoordinator)
		}
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliVerifyPolicy = oldVerify
		cliExecVM = oldExec
		cliExecVMWithRunner = oldExecWithRunner
		cliNewAskCoordinator = oldCoordinator
		cliStartAskServer = oldStartServer
	})

	cmd := NewExecCmd()
	if err := cmd.RunE(cmd, []string{"true"}); err != nil {
		t.Fatalf("ask-mode exec error = %v", err)
	}
	if runnerExecCalls != 1 || plainExecCalls != 0 {
		t.Fatalf("runner exec calls = %d, plain exec calls = %d", runnerExecCalls, plainExecCalls)
	}
	if serverDialog == nil {
		t.Fatal("ask-mode exec did not register the coordinator dialog")
	}
	if verdict := serverDialog("npm", "example.com", 443, "app"); verdict != "block" {
		t.Fatalf("registered dialog verdict = %q, want block", verdict)
	}
	if fakeCoordinator.dialogCalls != 1 {
		t.Fatalf("shared coordinator dialog calls = %d, want 1", fakeCoordinator.dialogCalls)
	}
}

func TestExecHandsOffLifecycleLockWhileHoldingUsageLease(t *testing.T) {
	configureLifecycleLockTest(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[security]\nenforcement = \"fail\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedPolicySnapshot(project, cfg); err != nil {
		t.Fatal(err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	vmName := lima.VMNameFromPath(project)
	oldStatus, oldProjectMount, oldVerify, oldExec := cliGetVMStatus, cliProjectMountSource, cliVerifyPolicy, cliExecVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return project, nil }
	cliVerifyPolicy = func(string) error { return nil }
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliVerifyPolicy = oldVerify
		cliExecVM = oldExec
	})

	type execInvocation struct {
		vmName  string
		args    []string
		workdir []string
	}
	execStarted := make(chan execInvocation, 1)
	allowExecExit := make(chan struct{})
	var allowExecExitOnce sync.Once
	cliExecVM = func(name string, args []string, workdir ...string) error {
		execStarted <- execInvocation{
			vmName:  name,
			args:    append([]string(nil), args...),
			workdir: append([]string(nil), workdir...),
		}
		<-allowExecExit
		return nil
	}

	commandDone := make(chan error, 1)
	commandFinished := false
	t.Cleanup(func() {
		allowExecExitOnce.Do(func() { close(allowExecExit) })
		if !commandFinished {
			select {
			case <-commandDone:
			case <-time.After(2 * time.Second):
				t.Errorf("mocked exec command did not exit during cleanup")
			}
		}
	})
	go func() {
		cmd := NewExecCmd()
		commandDone <- cmd.RunE(cmd, []string{"long-running", "--guest-flag"})
	}()

	var invocation execInvocation
	select {
	case invocation = <-execStarted:
	case err := <-commandDone:
		commandFinished = true
		t.Fatalf("exec returned before invoking the guest command: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not reach the mocked guest command")
	}
	if invocation.vmName != vmName {
		t.Fatalf("exec VM = %q, want %q", invocation.vmName, vmName)
	}
	if !reflect.DeepEqual(invocation.args, []string{"long-running", "--guest-flag"}) {
		t.Fatalf("exec args = %#v", invocation.args)
	}
	if !reflect.DeepEqual(invocation.workdir, []string{"/project"}) {
		t.Fatalf("exec workdir = %#v, want [/project]", invocation.workdir)
	}

	// The command has entered cliExecVM, so the handoff must already have
	// released the short lifecycle mutex. Acquiring it here models stop and
	// fail-closed handling while the guest command remains active.
	type lifecycleResult struct {
		lock *vmLifecycleLock
		err  error
	}
	lifecycleAcquired := make(chan lifecycleResult, 1)
	go func() {
		lock, err := acquireVMLifecycleLock(vmName)
		lifecycleAcquired <- lifecycleResult{lock: lock, err: err}
	}()
	select {
	case result := <-lifecycleAcquired:
		if result.err != nil {
			t.Fatalf("acquiring lifecycle lock during exec: %v", result.err)
		}
		if err := result.lock.Release(); err != nil {
			t.Fatalf("releasing lifecycle lock acquired during exec: %v", err)
		}
	case <-time.After(2 * time.Second):
		// Unblock the command so a broken lifecycle handoff cannot strand the
		// contender goroutine after the regression is reported.
		allowExecExitOnce.Do(func() { close(allowExecExit) })
		if err := <-commandDone; err != nil {
			t.Errorf("exec cleanup error = %v", err)
		}
		commandFinished = true
		result := <-lifecycleAcquired
		if result.lock != nil {
			_ = result.lock.Release()
		}
		t.Fatal("lifecycle lock remained held while the guest command was active")
	}

	// The lifecycle mutex is free, but the separate shared usage lease must
	// continue protecting the active command from destructive cleanup.
	leasePath, _, err := prepareVMUsageLeasePath(vmName)
	if err != nil {
		t.Fatal(err)
	}
	leaseFD, err := unix.Open(leasePath, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(leaseFD)
	if err := unix.Flock(leaseFD, unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		if err == nil {
			_ = unix.Flock(leaseFD, unix.LOCK_UN)
		}
		t.Fatalf("exclusive usage lease while guest command active = %v, want would-block", err)
	}

	allowExecExitOnce.Do(func() { close(allowExecExit) })
	select {
	case err := <-commandDone:
		commandFinished = true
		if err != nil {
			t.Fatalf("exec command error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after the mocked guest command exited")
	}

	if err := unix.Flock(leaseFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("usage lease remained held after guest command exit: %v", err)
	}
	if err := unix.Flock(leaseFD, unix.LOCK_UN); err != nil {
		t.Fatalf("releasing post-exec lease probe: %v", err)
	}
}
