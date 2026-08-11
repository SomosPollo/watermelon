//go:build linux

package ask

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	terminalCoordinatorHelperEnv   = "WATERMELON_TERMINAL_COORDINATOR_HELPER"
	directTerminalPromptHelperEnv  = "WATERMELON_DIRECT_TERMINAL_PROMPT_HELPER"
	terminalCoordinatorHelperReady = byte('r')
	terminalCoordinatorHelperGo    = byte('g')
)

type terminalCoordinatorHelperControl struct {
	toHelper   *os.File
	fromHelper *os.File
}

func (c *terminalCoordinatorHelperControl) signal(t *testing.T, value byte) {
	t.Helper()
	if _, err := c.toHelper.Write([]byte{value}); err != nil {
		t.Fatalf("signaling terminal coordinator helper: %v", err)
	}
}

func (c *terminalCoordinatorHelperControl) waitFor(t *testing.T, want byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for terminal coordinator helper event %q", want)
		}
		milliseconds := int(remaining / time.Millisecond)
		if milliseconds < 1 {
			milliseconds = 1
		}
		poll := []unix.PollFd{{Fd: int32(c.fromHelper.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, milliseconds)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			t.Fatalf("waiting for terminal coordinator helper event: %v", err)
		}
		if ready == 0 {
			continue
		}
		if poll[0].Revents&unix.POLLIN == 0 {
			t.Fatalf("terminal coordinator helper event pipe closed before event %q", want)
		}
		var event [1]byte
		if _, err := c.fromHelper.Read(event[:]); err != nil {
			t.Fatalf("reading terminal coordinator helper event: %v", err)
		}
		if event[0] != want {
			t.Fatalf("terminal coordinator helper event = %q, want %q", event[0], want)
		}
		return
	}
}

func TestTerminalCoordinatorSeparatesSimultaneousGuestAndPromptInput(t *testing.T) {
	transcript := runTerminalCoordinatorHelper(t, "interactive", func(master *os.File, output *bytes.Buffer, _ *terminalCoordinatorHelperControl) {
		readPTYUntil(t, master, output, "always allow and save [a]: ")
		if _, err := master.Write([]byte("o\n")); err != nil {
			t.Fatalf("writing prompt verdict: %v", err)
		}
		readPTYUntil(t, master, output, "VERDICT:allow-once")
		if _, err := master.Write([]byte("guest-data\n")); err != nil {
			t.Fatalf("writing guest input: %v", err)
		}
		readPTYUntil(t, master, output, "GUEST_INPUT:guest-data")
	})

	if strings.Contains(transcript, "GUEST_INPUT:o") {
		t.Fatalf("prompt answer leaked to guest stdin:\n%s", transcript)
	}
}

func TestTerminalPromptUsesControllingTTYInsteadOfRedirectedStandardStreams(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("opening direct-prompt PTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating redirected prompt stdin: %v", err)
	}
	defer stdinReader.Close()
	if _, err := stdinWriter.Write([]byte("a\n")); err != nil {
		t.Fatalf("queuing redirected stdin: %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("closing redirected prompt stdin: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=^TestDirectTerminalPromptHelperProcess$")
	cmd.Env = append(os.Environ(), directTerminalPromptHelperEnv+"=1")
	cmd.Stdin = stdinReader
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.ExtraFiles = []*os.File{slave}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting direct-prompt helper: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	var terminalOutput bytes.Buffer
	readPTYUntil(t, master, &terminalOutput, "always allow and save [a]: ")
	if _, err := master.Write([]byte("o\n")); err != nil {
		t.Fatalf("writing direct-prompt verdict: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("direct-prompt helper failed: %v\nterminal: %s\nstderr: %s", err, terminalOutput.String(), stderr.String())
	}
	if got := stdout.String(); got != "VERDICT:allow-once\n" {
		t.Fatalf("redirected stdout = %q, want verdict only", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("direct prompt wrote an unexpected diagnostic: %q", stderr.String())
	}
	remaining, err := io.ReadAll(stdinReader)
	if err != nil {
		t.Fatalf("reading untouched redirected stdin: %v", err)
	}
	if string(remaining) != "a\n" {
		t.Fatalf("redirected stdin after prompt = %q, want untouched prompt-like bytes", remaining)
	}
}

func TestDirectTerminalPromptHelperProcess(t *testing.T) {
	if os.Getenv(directTerminalPromptHelperEnv) == "" {
		return
	}
	verdict := ShowTerminalPrompt("npm", "example.com", 443, "app")
	fmt.Fprintf(os.Stdout, "VERDICT:%s\n", verdict)
	os.Exit(0)
}

func TestTerminalCoordinatorDoesNotConsumeQueuedGuestInputAsVerdict(t *testing.T) {
	transcript := runTerminalCoordinatorHelper(t, "typeahead", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		control.waitFor(t, terminalCoordinatorHelperReady)
		if _, err := master.Write([]byte("queued-guest-data\n")); err != nil {
			t.Fatalf("writing queued guest input: %v", err)
		}
		control.signal(t, terminalCoordinatorHelperGo)

		readPTYUntil(t, master, output, "always allow and save [a]: ")
		if strings.Contains(output.String(), "VERDICT:") {
			t.Fatalf("queued guest input completed the prompt before a verdict was entered:\n%s", output.String())
		}
		if _, err := master.Write([]byte("o\n")); err != nil {
			t.Fatalf("writing prompt verdict: %v", err)
		}
		readPTYUntil(t, master, output, "VERDICT:allow-once")
		readPTYUntil(t, master, output, "GUEST_INPUT:queued-guest-data")
		if _, err := master.Write([]byte("finish\n")); err != nil {
			t.Fatalf("finishing queued-input guest: %v", err)
		}
	})

	if strings.Contains(transcript, "GUEST_INPUT:o") {
		t.Fatalf("prompt answer leaked to guest stdin:\n%s", transcript)
	}
}

func TestTerminalCoordinatorFlushesInputBeforeChoiceInvitation(t *testing.T) {
	transcript := runTerminalCoordinatorHelper(t, "transition", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		control.waitFor(t, terminalCoordinatorHelperReady)
		if _, err := master.Write([]byte("o\n")); err != nil {
			t.Fatalf("writing transition-window guest input: %v", err)
		}
		control.signal(t, terminalCoordinatorHelperGo)

		readPTYUntil(t, master, output, "always allow and save [a]: ")
		if strings.Contains(output.String(), "VERDICT:") {
			t.Fatalf("transition-window guest input completed the prompt:\n%s", output.String())
		}
		if _, err := master.Write([]byte("o\n")); err != nil {
			t.Fatalf("writing prompt verdict after ownership handoff: %v", err)
		}
		readPTYUntil(t, master, output, "VERDICT:allow-once")
		if _, err := master.Write([]byte("finish\n")); err != nil {
			t.Fatalf("finishing transition-window guest: %v", err)
		}
		readPTYUntil(t, master, output, "GUEST_INPUT:finish")
	})
	if strings.Contains(transcript, "GUEST_INPUT:o") {
		t.Fatalf("prompt-transition input leaked to guest:\n%s", transcript)
	}
}

func TestTerminalCoordinatorPreservesPipedGuestInput(t *testing.T) {
	transcript := runTerminalCoordinatorHelper(t, "piped", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		readPTYUntil(t, master, output, "always allow and save [a]: ")
		control.signal(t, terminalCoordinatorHelperGo)
		readPTYUntil(t, master, output, "GUEST_INPUT:pipe-data")
		if strings.Contains(output.String(), "VERDICT:") {
			t.Fatalf("piped guest input unexpectedly completed the terminal prompt:\n%s", output.String())
		}
		if _, err := master.Write([]byte("o\n")); err != nil {
			t.Fatalf("writing prompt verdict: %v", err)
		}
		readPTYUntil(t, master, output, "VERDICT:allow-once")
	})

	if strings.Contains(transcript, "GUEST_INPUT:o") {
		t.Fatalf("controlling-terminal answer replaced piped guest input:\n%s", transcript)
	}
}

func TestTerminalCoordinatorCancelsPromptWhenGuestExits(t *testing.T) {
	transcript := runTerminalCoordinatorHelper(t, "cancel", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		readPTYUntil(t, master, output, "always allow and save [a]: ")
		control.signal(t, terminalCoordinatorHelperGo)
		readPTYUntil(t, master, output, "VERDICT:block")
	})
	if strings.Contains(transcript, "GUEST_INPUT:") {
		t.Fatalf("cancelled guest unexpectedly consumed terminal input:\n%s", transcript)
	}
}

func TestTerminalCoordinatorForwardsControlByteWhenProxyIsRaw(t *testing.T) {
	runTerminalCoordinatorHelper(t, "raw-control", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		control.waitFor(t, terminalCoordinatorHelperReady)
		if _, err := master.Write([]byte{3}); err != nil {
			t.Fatalf("writing raw proxy control byte: %v", err)
		}
		readPTYUntil(t, master, output, "GUEST_BYTE:3")
	})
}

func TestTerminalCoordinatorPreservesLiteralNextForCanonicalProxy(t *testing.T) {
	runTerminalCoordinatorHelper(t, "literal-control", func(master *os.File, output *bytes.Buffer, control *terminalCoordinatorHelperControl) {
		control.waitFor(t, terminalCoordinatorHelperReady)
		state, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
		if err != nil {
			t.Fatalf("reading literal-next terminal state: %v", err)
		}
		literalNext := state.Cc[unix.VLNEXT]
		interrupt := state.Cc[unix.VINTR]
		if literalNext == 0 || interrupt == 0 {
			t.Skip("terminal literal-next or interrupt character is disabled")
		}
		if _, err := master.Write([]byte{literalNext, interrupt, '\n'}); err != nil {
			t.Fatalf("writing literal interrupt byte: %v", err)
		}
		readPTYUntil(t, master, output, "GUEST_BYTE:3")
	})
}

func TestTerminalCoordinatorTurnsCanonicalProxyInterruptIntoJobSignal(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("opening interrupt helper PTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	initialState, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("reading initial interrupt helper terminal state: %v", err)
	}
	interruptByte := initialState.Cc[unix.VINTR]
	if interruptByte == 0 {
		t.Skip("terminal interrupt character is disabled")
	}

	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating interrupt helper control pipe: %v", err)
	}
	defer controlReader.Close()
	defer controlWriter.Close()
	eventReader, eventWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating interrupt helper event pipe: %v", err)
	}
	defer eventReader.Close()
	defer eventWriter.Close()

	var output bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=^TestTerminalCoordinatorHelperProcess$")
	cmd.Env = append(os.Environ(), terminalCoordinatorHelperEnv+"=interrupt")
	cmd.Stdin = slave
	// Redirecting output models Lima's no-remote-PTY path, where the proxy
	// retains canonical ISIG processing.
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.ExtraFiles = []*os.File{controlReader, eventWriter}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting interrupt helper: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	_ = controlReader.Close()
	_ = eventWriter.Close()

	control := &terminalCoordinatorHelperControl{toHelper: controlWriter, fromHelper: eventReader}
	control.waitFor(t, terminalCoordinatorHelperReady)
	if _, err := master.Write([]byte{interruptByte}); err != nil {
		t.Fatalf("writing terminal interrupt: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("interrupt helper error = %v, want signal termination\n%s", err, output.String())
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
			t.Fatalf("interrupt helper status = %#v, want SIGINT\n%s", exitErr.Sys(), output.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("interrupt helper did not terminate:\n%s", output.String())
	}

	finalState, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("reading restored interrupt helper terminal state: %v", err)
	}
	if !reflect.DeepEqual(finalState, initialState) {
		t.Fatalf("terminal state was not restored before SIGINT\ninitial: %#v\nfinal:   %#v", initialState, finalState)
	}
}

func TestTerminalPromptRejectsTTYThatIsNotControlling(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var diagnostic bytes.Buffer
	verdict := showTerminalPromptWith(func() (*os.File, error) {
		return slave, nil
	}, &diagnostic, "npm", "example.com", 443, "app")
	if verdict != VerdictBlock {
		t.Fatalf("non-controlling PTY verdict = %q, want %q", verdict, VerdictBlock)
	}
	if !strings.Contains(diagnostic.String(), "foreground controlling terminal") {
		t.Fatalf("missing foreground-terminal diagnostic: %q", diagnostic.String())
	}
}

func TestChooseTerminalSuspendTargetDoesNotStopParentShell(t *testing.T) {
	const (
		processID      = 101
		processGroup   = 202
		processSession = 303
	)
	tests := []struct {
		name              string
		parentGroup       int
		parentSession     int
		relationshipKnown bool
		want              int
	}{
		{name: "ordinary job-control group", parentGroup: 201, parentSession: processSession, relationshipKnown: true, want: 0},
		{name: "shared shell group", parentGroup: processGroup, parentSession: processSession, relationshipKnown: true, want: processID},
		{name: "different session supervisor", parentGroup: 201, parentSession: 404, relationshipKnown: true, want: processID},
		{name: "unknown relationship", want: processID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := chooseTerminalSuspendTarget(processID, processGroup, processSession, test.parentGroup, test.parentSession, test.relationshipKnown)
			if got != test.want {
				t.Fatalf("suspend target = %d, want %d", got, test.want)
			}
		})
	}
}

func runTerminalCoordinatorHelper(t *testing.T, mode string, interact func(*os.File, *bytes.Buffer, *terminalCoordinatorHelperControl)) string {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("opening helper PTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	initialState, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("reading initial helper terminal state: %v", err)
	}
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating helper control pipe: %v", err)
	}
	defer controlReader.Close()
	defer controlWriter.Close()
	eventReader, eventWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating helper event pipe: %v", err)
	}
	defer eventReader.Close()
	defer eventWriter.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestTerminalCoordinatorHelperProcess$")
	cmd.Env = append(os.Environ(), terminalCoordinatorHelperEnv+"="+mode)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.ExtraFiles = []*os.File{controlReader, eventWriter}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting terminal coordinator helper: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	_ = controlReader.Close()
	_ = eventWriter.Close()

	var output bytes.Buffer
	interact(master, &output, &terminalCoordinatorHelperControl{toHelper: controlWriter, fromHelper: eventReader})

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("terminal coordinator helper failed: %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("terminal coordinator helper timed out:\n%s", output.String())
	}
	finalState, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("reading restored helper terminal state: %v", err)
	}
	if !reflect.DeepEqual(finalState, initialState) {
		t.Fatalf("terminal state was not restored after guest and prompt session\ninitial: %#v\nfinal:   %#v", initialState, finalState)
	}
	return output.String()
}

func readPTYUntil(t *testing.T, master *os.File, output *bytes.Buffer, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buffer := make([]byte, 4096)
	for !strings.Contains(output.String(), marker) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for %q:\n%s", marker, output.String())
		}
		milliseconds := int(remaining / time.Millisecond)
		if milliseconds < 1 {
			milliseconds = 1
		}
		poll := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, milliseconds)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			t.Fatalf("polling helper PTY: %v", err)
		}
		if ready == 0 {
			continue
		}
		n, err := master.Read(buffer)
		if n > 0 {
			output.Write(buffer[:n])
		}
		if err != nil && !errors.Is(err, unix.EIO) {
			t.Fatalf("reading helper PTY: %v", err)
		}
		if errors.Is(err, unix.EIO) && !strings.Contains(output.String(), marker) {
			t.Fatalf("helper PTY closed before %q:\n%s", marker, output.String())
		}
	}
}

func TestTerminalCoordinatorHelperProcess(t *testing.T) {
	mode := os.Getenv(terminalCoordinatorHelperEnv)
	if mode == "" {
		return
	}
	parentControl := os.NewFile(3, "terminal-coordinator-test-control")
	parentEvents := os.NewFile(4, "terminal-coordinator-test-events")
	if parentControl == nil || parentEvents == nil {
		t.Fatal("terminal coordinator helper control descriptors are unavailable")
	}
	defer parentControl.Close()
	defer parentEvents.Close()

	coordinator := NewTerminalCoordinator()
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyReader.Close()
	defer readyWriter.Close()

	guestScript := `printf x >&3; IFS= read -r line; printf 'GUEST_INPUT:%s\n' "$line"`
	if mode == "typeahead" {
		guestScript = `printf x >&3; IFS= read -r line; printf 'GUEST_INPUT:%s\n' "$line"; IFS= read -r _`
	} else if mode == "transition" {
		guestScript = `printf x >&3; IFS= read -r line; printf 'GUEST_INPUT:%s\n' "$line"`
	} else if mode == "raw-control" {
		guestScript = `stty raw -echo <&0; printf x >&3; value=$(dd bs=1 count=1 2>/dev/null | od -An -tu1 | tr -d ' '); printf 'GUEST_BYTE:%s\n' "$value"`
	} else if mode == "literal-control" {
		guestScript = `printf x >&3; IFS= read -r line; value=$(printf %s "$line" | od -An -tu1 | tr -d ' '); printf 'GUEST_BYTE:%s\n' "$value"`
	} else if mode == "interrupt" {
		guestScript = `printf x >&3; while :; do sleep 10; done`
	}
	guest := exec.Command("sh", "-c", guestScript)
	guest.Stdout = os.Stdout
	guest.Stderr = os.Stderr
	guest.ExtraFiles = []*os.File{readyWriter}

	var pipeWriter *os.File
	var cancelWriter *os.File
	if mode == "interactive" || mode == "typeahead" || mode == "transition" || mode == "raw-control" || mode == "literal-control" || mode == "interrupt" {
		guest.Stdin = os.Stdin
	} else if mode == "piped" {
		pipeReader, writer, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		defer pipeReader.Close()
		defer writer.Close()
		pipeWriter = writer
		guest.Stdin = pipeReader
	} else if mode == "cancel" {
		cancelReader, writer, cancelErr := os.Pipe()
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		defer cancelReader.Close()
		defer writer.Close()
		cancelWriter = writer
		guest.Stdin = os.Stdin
		guest.Args[2] = `printf x >&3; IFS= read -r _ <&4; exit 7`
		guest.ExtraFiles = append(guest.ExtraFiles, cancelReader)
	} else {
		t.Fatalf("unknown helper mode %q", mode)
	}

	guestDone := make(chan error, 1)
	go func() { guestDone <- coordinator.Run(guest) }()
	readyDone := make(chan error, 1)
	go func() {
		ready := make([]byte, 1)
		_, readErr := readyReader.Read(ready)
		readyDone <- readErr
	}()
	select {
	case err := <-readyDone:
		if err != nil {
			t.Fatalf("waiting for guest reader: %v", err)
		}
	case err := <-guestDone:
		t.Fatalf("guest command exited before reading input: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("guest command did not start reading input")
	}
	if mode == "transition" {
		coordinator.mu.Lock()
		session := coordinator.session
		coordinator.mu.Unlock()
		if session == nil {
			t.Fatal("transition test has no active terminal session")
		}
		session.beforePromptInputHandoff = func() {
			if _, err := parentEvents.Write([]byte{terminalCoordinatorHelperReady}); err != nil {
				t.Errorf("notifying parent of prompt transition: %v", err)
				return
			}
			if err := waitForTerminalCoordinatorHelperControl(parentControl, terminalCoordinatorHelperGo); err != nil {
				t.Errorf("waiting to complete prompt transition: %v", err)
			}
		}
	}
	if mode == "raw-control" || mode == "literal-control" || mode == "interrupt" {
		if _, err := parentEvents.Write([]byte{terminalCoordinatorHelperReady}); err != nil {
			t.Fatalf("notifying parent that control-byte guest is ready: %v", err)
		}
		select {
		case err := <-guestDone:
			if err != nil {
				t.Fatalf("control-byte guest failed: %v", err)
			}
			return
		case <-time.After(10 * time.Second):
			t.Fatal("control-byte guest did not finish")
		}
	}

	var controlDone chan error
	switch mode {
	case "typeahead":
		if _, err := parentEvents.Write([]byte{terminalCoordinatorHelperReady}); err != nil {
			t.Fatalf("notifying parent that typeahead guest is ready: %v", err)
		}
		if err := waitForTerminalCoordinatorHelperControl(parentControl, terminalCoordinatorHelperGo); err != nil {
			t.Fatal(err)
		}
	case "piped":
		controlDone = make(chan error, 1)
		go func() {
			if err := waitForTerminalCoordinatorHelperControl(parentControl, terminalCoordinatorHelperGo); err != nil {
				controlDone <- err
				return
			}
			_, err := pipeWriter.Write([]byte("pipe-data\n"))
			if closeErr := pipeWriter.Close(); err == nil {
				err = closeErr
			}
			controlDone <- err
		}()
	case "cancel":
		controlDone = make(chan error, 1)
		go func() {
			if err := waitForTerminalCoordinatorHelperControl(parentControl, terminalCoordinatorHelperGo); err != nil {
				controlDone <- err
				return
			}
			controlDone <- cancelWriter.Close()
		}()
	}

	verdict := coordinator.Dialog("npm", "example.com", 443, "app")
	fmt.Fprintf(os.Stdout, "\nVERDICT:%s\n", verdict)
	if controlDone != nil {
		select {
		case err := <-controlDone:
			if err != nil {
				t.Fatalf("performing coordinated helper action: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for coordinated helper action")
		}
	}

	select {
	case err := <-guestDone:
		if err != nil && mode != "cancel" {
			t.Fatalf("guest command failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("guest command did not finish")
	}
}

func waitForTerminalCoordinatorHelperControl(control *os.File, want byte) error {
	var value [1]byte
	n, err := control.Read(value[:])
	if err != nil {
		return fmt.Errorf("waiting for parent test control: %w", err)
	}
	if n != 1 || value[0] != want {
		return fmt.Errorf("parent test control = %q, want %q", value[:n], want)
	}
	return nil
}
