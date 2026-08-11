//go:build linux

package ask

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type terminalCoordinatorState uint8

const maxBufferedGuestInput = 64 * 1024

const (
	terminalCoordinatorIdle terminalCoordinatorState = iota
	terminalCoordinatorStarting
	terminalCoordinatorSeparateInput
	terminalCoordinatorActive
	terminalCoordinatorPromptUnavailable
)

// TerminalCoordinator gives an ask-mode guest command and verdict prompts one
// owner for the host terminal input stream. Interactive guest input is proxied
// through a private PTY so limactl and OpenSSH retain terminal behavior without
// reading the controlling terminal directly. Redirected stdin remains attached
// directly to the guest and is therefore independent of /dev/tty prompts.
type TerminalCoordinator struct {
	dialogMu sync.Mutex
	mu       sync.Mutex
	changed  *sync.Cond
	state    terminalCoordinatorState
	session  *terminalInputSession
}

func NewTerminalCoordinator() *TerminalCoordinator {
	coordinator := &TerminalCoordinator{}
	coordinator.changed = sync.NewCond(&coordinator.mu)
	return coordinator
}

// Dialog implements DialogFunc with controlling-terminal ownership shared with
// Run. It may also be used without Run (for example, by `watermelon code`).
func (c *TerminalCoordinator) Dialog(process, domain string, port int, project string) string {
	c.dialogMu.Lock()
	defer c.dialogMu.Unlock()

	c.mu.Lock()
	for c.state == terminalCoordinatorStarting {
		c.changed.Wait()
	}
	state := c.state
	session := c.session
	c.mu.Unlock()

	switch state {
	case terminalCoordinatorActive:
		if !fdIsForegroundTerminal(session.ttyFD) {
			writeUnavailableTerminalDiagnostic(os.Stderr)
			return VerdictBlock
		}
		return session.prompt(process, domain, port, project)
	case terminalCoordinatorPromptUnavailable:
		writeUnavailableTerminalDiagnostic(os.Stderr)
		return VerdictBlock
	default:
		// With redirected guest stdin, or before an attached command starts,
		// /dev/tty is an independent prompt channel.
		return ShowTerminalPrompt(process, domain, port, project)
	}
}

// Run implements lima.CommandRunner. Only a guest stdin that aliases the
// foreground controlling terminal needs proxying; pipes and files are passed
// through byte-for-byte.
func (c *TerminalCoordinator) Run(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("cannot run a nil guest command")
	}

	// An idle coordinator may already be displaying a startup-time prompt.
	// Finish that direct /dev/tty read before installing the guest input proxy,
	// closing the last possible two-reader transition window.
	c.dialogMu.Lock()
	c.beginRun()
	tty, ttyErr := openControllingTerminal()
	sharesTTY, relationErr := commandInputSharesTerminal(cmd, tty, ttyErr)
	if !sharesTTY {
		if tty != nil {
			_ = tty.Close()
		}
		state := terminalCoordinatorSeparateInput
		if relationErr != nil {
			// An interactive input whose relationship to /dev/tty cannot be
			// verified must never race a prompt. Keep the command usable, but
			// make every prompt fail closed for its duration.
			state = terminalCoordinatorPromptUnavailable
		}
		c.setRunState(state, nil)
		c.dialogMu.Unlock()
		err := cmd.Run()
		c.finishRun()
		return err
	}

	if ttyErr != nil || !fileIsForegroundTerminal(tty) {
		if tty != nil {
			_ = tty.Close()
		}
		c.setRunState(terminalCoordinatorPromptUnavailable, nil)
		c.dialogMu.Unlock()
		err := cmd.Run()
		c.finishRun()
		return err
	}

	session, err := newTerminalInputSession(tty)
	if err != nil {
		_ = tty.Close()
		c.setRunState(terminalCoordinatorPromptUnavailable, nil)
		c.finishRun()
		c.dialogMu.Unlock()
		return fmt.Errorf("preparing exclusive ask-mode terminal input: %w", err)
	}

	originalStdin := cmd.Stdin
	cmd.Stdin = session.slave
	session.start()
	c.setRunState(terminalCoordinatorActive, session)
	c.dialogMu.Unlock()

	startErr := cmd.Start()
	_ = session.slave.Close()
	cmd.Stdin = originalStdin
	if startErr != nil {
		session.stop()
		c.finishRun()
		return startErr
	}

	waitErr := cmd.Wait()
	session.stop()
	c.finishRun()
	return waitErr
}

func (c *TerminalCoordinator) beginRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.state != terminalCoordinatorIdle {
		c.changed.Wait()
	}
	c.state = terminalCoordinatorStarting
	c.session = nil
	c.changed.Broadcast()
}

func (c *TerminalCoordinator) setRunState(state terminalCoordinatorState, session *terminalInputSession) {
	c.mu.Lock()
	c.state = state
	c.session = session
	c.changed.Broadcast()
	c.mu.Unlock()
}

func (c *TerminalCoordinator) finishRun() {
	c.mu.Lock()
	c.state = terminalCoordinatorIdle
	c.session = nil
	c.changed.Broadcast()
	c.mu.Unlock()
}

func commandInputSharesTerminal(cmd *exec.Cmd, controllingTTY *os.File, controllingErr error) (bool, error) {
	input, ok := cmd.Stdin.(*os.File)
	if !ok || !fileIsTerminal(input) {
		return false, nil
	}
	if controllingErr != nil || controllingTTY == nil || !fileIsTerminal(controllingTTY) {
		return false, fmt.Errorf("interactive guest stdin has no usable controlling terminal")
	}

	inputDevice, err := unix.IoctlGetInt(int(input.Fd()), unix.TIOCGDEV)
	if err != nil {
		return false, fmt.Errorf("identifying guest stdin terminal: %w", err)
	}
	controllingDevice, err := unix.IoctlGetInt(int(controllingTTY.Fd()), unix.TIOCGDEV)
	if err != nil {
		return false, fmt.Errorf("identifying controlling terminal: %w", err)
	}
	return inputDevice == controllingDevice, nil
}

type terminalPromptRequest struct {
	process string
	domain  string
	port    int
	project string
	reply   chan string
}

type terminalInputSession struct {
	tty         *os.File
	master      *os.File
	slave       *os.File
	wakeReader  *os.File
	wakeWriter  *os.File
	ttyFD       int
	masterFD    int
	wakeFD      int
	promptState *term.State
	guestState  *term.State

	requests chan terminalPromptRequest
	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}

	termMu     sync.Mutex
	desired    *term.State
	sigCh      chan os.Signal
	sigEnd     chan struct{}
	sigDone    chan struct{}
	sigStop    sync.Once
	resizeEcho atomic.Bool
	quotedNext bool
	// Test-only seam for placing input exactly at the ownership boundary.
	beforePromptInputHandoff func()
}

func newTerminalInputSession(tty *os.File) (*terminalInputSession, error) {
	promptState, err := term.GetState(int(tty.Fd()))
	if err != nil {
		return nil, fmt.Errorf("reading controlling-terminal state: %w", err)
	}
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	cleanupPTY := func() {
		_ = master.Close()
		_ = slave.Close()
	}

	if err := term.Restore(int(slave.Fd()), promptState); err != nil {
		cleanupPTY()
		return nil, fmt.Errorf("initializing guest proxy terminal: %w", err)
	}
	if err := copyTerminalSize(tty, slave); err != nil {
		cleanupPTY()
		return nil, fmt.Errorf("copying terminal size to guest proxy: %w", err)
	}

	wakeReader, wakeWriter, err := os.Pipe()
	if err != nil {
		cleanupPTY()
		return nil, fmt.Errorf("creating terminal-coordinator wake pipe: %w", err)
	}
	cleanupAll := func() {
		cleanupPTY()
		_ = wakeReader.Close()
		_ = wakeWriter.Close()
	}

	ttyFD := int(tty.Fd())
	masterFD := int(master.Fd())
	wakeFD := int(wakeReader.Fd())
	session := &terminalInputSession{
		tty:         tty,
		master:      master,
		slave:       slave,
		wakeReader:  wakeReader,
		wakeWriter:  wakeWriter,
		ttyFD:       ttyFD,
		masterFD:    masterFD,
		wakeFD:      wakeFD,
		promptState: promptState,
		desired:     promptState,
		requests:    make(chan terminalPromptRequest, 1),
		stopCh:      make(chan struct{}),
		done:        make(chan struct{}),
		sigCh:       make(chan os.Signal, 8),
		sigEnd:      make(chan struct{}),
		sigDone:     make(chan struct{}),
	}
	// Install restoration handlers before the first terminal mutation. A
	// signal during setup must not strand the controlling terminal in raw or
	// nonblocking mode.
	session.startSignalWatcher()
	cleanupSession := func() {
		session.stopSignalWatcher()
		cleanupAll()
	}
	session.termMu.Lock()
	if err := unix.SetNonblock(ttyFD, true); err != nil {
		session.termMu.Unlock()
		cleanupSession()
		return nil, fmt.Errorf("making controlling terminal interruptible: %w", err)
	}
	if err := unix.SetNonblock(masterFD, true); err != nil {
		_ = unix.SetNonblock(ttyFD, false)
		session.termMu.Unlock()
		cleanupSession()
		return nil, fmt.Errorf("making guest proxy terminal interruptible: %w", err)
	}
	if err := unix.SetNonblock(wakeFD, true); err != nil {
		_ = unix.SetNonblock(ttyFD, false)
		session.termMu.Unlock()
		cleanupSession()
		return nil, fmt.Errorf("making terminal wake pipe interruptible: %w", err)
	}

	previousState, err := term.MakeRaw(ttyFD)
	if err != nil {
		_ = unix.SetNonblock(ttyFD, false)
		session.termMu.Unlock()
		cleanupSession()
		return nil, fmt.Errorf("enabling guest terminal mode: %w", err)
	}
	guestState, err := term.GetState(ttyFD)
	if err != nil {
		_ = term.Restore(ttyFD, previousState)
		_ = unix.SetNonblock(ttyFD, false)
		session.termMu.Unlock()
		cleanupSession()
		return nil, fmt.Errorf("recording guest terminal mode: %w", err)
	}
	session.guestState = guestState
	session.desired = guestState
	session.termMu.Unlock()

	return session, nil
}

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("opening guest proxy PTY: %w", err)
	}
	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("unlocking guest proxy PTY: %w", err)
	}
	ptyNumber, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("identifying guest proxy PTY: %w", err)
	}
	slavePath := fmt.Sprintf("/dev/pts/%d", ptyNumber)
	slaveFD, err := unix.Open(slavePath, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("opening guest proxy PTY slave: %w", err)
	}
	return master, os.NewFile(uintptr(slaveFD), slavePath), nil
}

func copyTerminalSize(source, target *os.File) error {
	return copyTerminalSizeFD(int(source.Fd()), int(target.Fd()))
}

func copyTerminalSizeFD(sourceFD, targetFD int) error {
	size, err := unix.IoctlGetWinsize(sourceFD, unix.TIOCGWINSZ)
	if err != nil {
		return err
	}
	return unix.IoctlSetWinsize(targetFD, unix.TIOCSWINSZ, size)
}

func (s *terminalInputSession) start() {
	go s.loop()
}

func (s *terminalInputSession) startSignalWatcher() {
	signal.Notify(s.sigCh, os.Interrupt, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGTSTP, syscall.SIGWINCH)
	go s.restoreOnSignal()
}

func (s *terminalInputSession) stopSignalWatcher() {
	s.sigStop.Do(func() {
		signal.Stop(s.sigCh)
		close(s.sigEnd)
		<-s.sigDone
	})
}

func (s *terminalInputSession) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.wake()
		<-s.done
		s.stopSignalWatcher()
		_ = s.wakeReader.Close()
		_ = s.wakeWriter.Close()
		_ = s.master.Close()
		_ = s.tty.Close()
	})
}

func (s *terminalInputSession) restoreOnSignal() {
	defer close(s.sigDone)
	for {
		select {
		case sig := <-s.sigCh:
			if sig == syscall.SIGWINCH {
				_ = copyTerminalSizeFD(s.ttyFD, s.masterFD)
				// limactl and SSH receive the original resize concurrently and
				// may inspect the proxy before the copy completes. Send one
				// follow-up after updating it, suppressing Watermelon's echo.
				if s.resizeEcho.Swap(false) {
					continue
				}
				s.resizeEcho.Store(true)
				_ = syscall.Kill(0, syscall.SIGWINCH)
				continue
			}
			if sig == syscall.SIGTSTP {
				s.suspendTerminalSession()
				continue
			}
			s.restoreAndSignal(sig.(syscall.Signal), os.Getpid())
		case <-s.sigEnd:
			return
		}
	}
}

func (s *terminalInputSession) suspendTerminalSession() {
	s.termMu.Lock()
	desired := s.desired
	_ = term.Restore(s.ttyFD, s.promptState)
	_ = unix.SetNonblock(s.ttyFD, false)
	s.termMu.Unlock()

	// SIGTSTP is intercepted so the terminal can be restored first. SIGSTOP
	// performs the actual suspension and returns only after SIGCONT. Like any
	// terminal-reading job, remain stopped when resumed with `bg`; `fg` first
	// restores this process group as the terminal foreground owner.
	stopTarget := terminalSuspendTarget()
	for {
		_ = syscall.Kill(stopTarget, syscall.SIGSTOP)
		if fdIsForegroundTerminal(s.ttyFD) {
			break
		}
	}

	s.termMu.Lock()
	_ = unix.SetNonblock(s.ttyFD, true)
	if desired != nil {
		_ = term.Restore(s.ttyFD, desired)
	}
	s.termMu.Unlock()
	_ = copyTerminalSizeFD(s.ttyFD, s.masterFD)
}

func terminalSuspendTarget() int {
	processID := os.Getpid()
	processGroup := syscall.Getpgrp()
	parentID := os.Getppid()
	parentGroup, groupErr := unix.Getpgid(parentID)
	processSession, processSessionErr := unix.Getsid(processID)
	parentSession, parentSessionErr := unix.Getsid(parentID)
	return chooseTerminalSuspendTarget(processID, processGroup, processSession, parentGroup, parentSession,
		groupErr == nil && processSessionErr == nil && parentSessionErr == nil)
}

func chooseTerminalSuspendTarget(processID, processGroup, processSession, parentGroup, parentSession int, relationshipKnown bool) int {
	// Only stop the whole job when a distinct parent process group in the
	// same session can resume it. With `set +m` (and some PTY supervisors),
	// Watermelon shares its parent's group; SIGSTOPing that group would also
	// freeze the shell that must bring the command back.
	if relationshipKnown && parentSession == processSession && parentGroup != processGroup {
		// pid 0 targets the caller's process group without the kill(-1)
		// ambiguity that a negated PGID 1 would create in a PID namespace.
		return 0
	}
	return processID
}

func (s *terminalInputSession) restoreAndSignal(sig syscall.Signal, target int) {
	s.termMu.Lock()
	_ = term.Restore(s.ttyFD, s.promptState)
	s.desired = s.promptState
	_ = unix.SetNonblock(s.ttyFD, false)
	signal.Stop(s.sigCh)
	signal.Reset(sig)
	if err := syscall.Kill(target, sig); err != nil && target != os.Getpid() {
		_ = syscall.Kill(os.Getpid(), sig)
	}
	// Keep terminal initialization/restoration serialized until the default
	// signal disposition terminates the process. Releasing termMu here would
	// permit setup to make the terminal raw again in the re-raise window.
	for {
		_ = unix.Pause()
	}
}

func (s *terminalInputSession) prompt(process, domain string, port int, project string) string {
	request := terminalPromptRequest{
		process: process,
		domain:  domain,
		port:    port,
		project: project,
		reply:   make(chan string, 1),
	}
	select {
	case s.requests <- request:
		s.wake()
	case <-s.done:
		return VerdictBlock
	}
	select {
	case verdict := <-request.reply:
		return verdict
	case <-s.done:
		return VerdictBlock
	}
}

func (s *terminalInputSession) wake() {
	_, _ = s.wakeWriter.Write([]byte{1})
}

func (s *terminalInputSession) loop() {
	defer close(s.done)
	defer s.restorePromptTerminal()
	defer s.master.Close()

	var toGuest []byte
	buffer := make([]byte, 4096)
	for {
		pollFDs := []unix.PollFd{
			{Fd: int32(s.wakeFD), Events: unix.POLLIN},
			{Fd: int32(s.ttyFD)},
			{Fd: int32(s.masterFD), Events: unix.POLLIN},
		}
		if len(toGuest) < maxBufferedGuestInput {
			pollFDs[1].Events = unix.POLLIN
		}
		if len(toGuest) > 0 {
			pollFDs[2].Events |= unix.POLLOUT
		}
		if _, err := unix.Poll(pollFDs, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}

		if pollFDs[0].Revents&unix.POLLIN != 0 {
			s.drainWake()
			select {
			case <-s.stopCh:
				return
			default:
			}
			select {
			case request := <-s.requests:
				toGuest = append(toGuest, s.drainGuestTypeahead(buffer, maxBufferedGuestInput-len(toGuest))...)
				verdict, stopped := s.handlePrompt(request)
				request.reply <- verdict
				if stopped {
					return
				}
			default:
			}
		}

		if pollFDs[1].Revents&unix.POLLIN != 0 {
			for {
				remaining := maxBufferedGuestInput - len(toGuest)
				if remaining == 0 {
					break
				}
				readBuffer := buffer
				if remaining < len(readBuffer) {
					readBuffer = readBuffer[:remaining]
				}
				n, err := unix.Read(s.ttyFD, readBuffer)
				if n > 0 {
					toGuest = s.routeGuestInput(toGuest, readBuffer[:n])
				}
				if err != nil || n == 0 || n < len(readBuffer) {
					break
				}
			}
		}

		if len(toGuest) > 0 && pollFDs[2].Revents&unix.POLLOUT != 0 {
			n, err := unix.Write(s.masterFD, toGuest)
			if n > 0 {
				toGuest = toGuest[n:]
			}
			if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
				return
			}
		}

		// The slave normally has echo disabled by OpenSSH. Draining any
		// transient line-discipline echo prevents the PTY output queue from
		// filling before OpenSSH switches it to raw mode.
		if pollFDs[2].Revents&unix.POLLIN != 0 {
			n, err := unix.Read(s.masterFD, buffer)
			if n > 0 {
				_, _ = s.tty.Write(buffer[:n])
			}
			if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) && !errors.Is(err, unix.EIO) {
				return
			}
		}
	}
}

func (s *terminalInputSession) drainWake() {
	buffer := make([]byte, 64)
	for {
		_, err := unix.Read(s.wakeFD, buffer)
		if err != nil {
			return
		}
	}
}

func (s *terminalInputSession) drainGuestTypeahead(buffer []byte, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	drained := make([]byte, 0, min(limit, len(buffer)))
	for {
		remaining := limit - len(drained)
		if remaining == 0 {
			return drained
		}
		readBuffer := buffer
		if remaining < len(readBuffer) {
			readBuffer = readBuffer[:remaining]
		}
		n, err := unix.Read(s.ttyFD, readBuffer)
		if n > 0 {
			drained = s.routeGuestInput(drained, readBuffer[:n])
		}
		if err != nil || n == 0 || n < len(readBuffer) {
			return drained
		}
	}
}

func (s *terminalInputSession) routeGuestInput(destination, input []byte) []byte {
	proxyState, err := unix.IoctlGetTermios(s.masterFD, unix.TCGETS)
	if err != nil || proxyState.Lflag&unix.ISIG == 0 {
		s.quotedNext = false
		return append(destination, input...)
	}

	for _, value := range input {
		if s.quotedNext {
			s.quotedNext = false
			destination = append(destination, value)
			continue
		}
		if proxyState.Lflag&unix.IEXTEN != 0 && enabledControlCharacter(proxyState, unix.VLNEXT, value) {
			// Forward both VLNEXT and the quoted byte so the proxy line
			// discipline performs its normal literal-next processing.
			s.quotedNext = true
			destination = append(destination, value)
			continue
		}

		var sig syscall.Signal
		switch {
		case enabledControlCharacter(proxyState, unix.VINTR, value):
			sig = syscall.SIGINT
		case enabledControlCharacter(proxyState, unix.VQUIT, value):
			sig = syscall.SIGQUIT
		case enabledControlCharacter(proxyState, unix.VSUSP, value):
			sig = syscall.SIGTSTP
		default:
			destination = append(destination, value)
			continue
		}

		// A proxy PTY without a controlling process group would consume
		// these bytes without signaling anyone. Reproduce terminal ISIG
		// behavior against Watermelon's already-validated foreground group.
		if proxyState.Lflag&unix.NOFLSH == 0 {
			destination = destination[:0]
			_ = flushTerminalInput(s.masterFD)
		}
		if sig == syscall.SIGTSTP {
			_ = syscall.Kill(terminalSuspendTarget(), sig)
			continue
		}
		// Terminating control signals must not depend on the asynchronous
		// signal watcher: the guest could exit and tear the watcher down before
		// its notification is handled. Restore synchronously, reinstate the
		// default disposition, then signal the entire foreground job.
		s.restoreAndSignal(sig, 0)
	}
	return destination
}

func enabledControlCharacter(state *unix.Termios, index uint32, value byte) bool {
	configured := state.Cc[index]
	// Linux uses NUL as _POSIX_VDISABLE. A disabled control character must
	// never turn an ordinary NUL byte into a host signal.
	return configured != 0 && configured == value
}

func (s *terminalInputSession) handlePrompt(request terminalPromptRequest) (string, bool) {
	if err := s.setTerminalState(s.promptState); err != nil {
		return VerdictBlock, true
	}
	if err := flushTerminalInput(s.ttyFD); err != nil {
		_ = s.setTerminalState(s.guestState)
		return VerdictBlock, true
	}

	var rendered bytes.Buffer
	writeTerminalPromptContext(&rendered, request.process, request.domain, request.port, request.project)
	if _, err := s.tty.Write(rendered.Bytes()); err != nil {
		_ = s.setTerminalState(s.guestState)
		return VerdictBlock, true
	}
	if s.beforePromptInputHandoff != nil {
		s.beforePromptInputHandoff()
	}
	// The context can become visible while guest typeahead is still arriving.
	// Flush once more before displaying the final choice invitation: bytes
	// entered before that explicit ownership handoff can never be a verdict,
	// while an answer entered after the invitation is never flushed away.
	if err := flushTerminalInput(s.ttyFD); err != nil {
		_ = s.setTerminalState(s.guestState)
		return VerdictBlock, true
	}
	var invitation bytes.Buffer
	writeTerminalChoicePrompt(&invitation)
	if _, err := s.tty.Write(invitation.Bytes()); err != nil {
		_ = s.setTerminalState(s.guestState)
		return VerdictBlock, true
	}

	line, stopped := s.readPromptLine()
	_ = flushTerminalInput(s.ttyFD)
	if err := s.setTerminalState(s.guestState); err != nil {
		return VerdictBlock, true
	}
	if stopped {
		return VerdictBlock, true
	}
	return terminalChoiceVerdict(line), false
}

func (s *terminalInputSession) readPromptLine() (string, bool) {
	line := make([]byte, 0, 128)
	buffer := make([]byte, 4096)
	for {
		pollFDs := []unix.PollFd{
			{Fd: int32(s.wakeFD), Events: unix.POLLIN},
			{Fd: int32(s.ttyFD), Events: unix.POLLIN},
		}
		if _, err := unix.Poll(pollFDs, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return "", false
		}
		if pollFDs[0].Revents&unix.POLLIN != 0 {
			s.drainWake()
			select {
			case <-s.stopCh:
				return "", true
			default:
			}
		}
		if pollFDs[1].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(s.ttyFD, buffer)
		if n > 0 {
			line = append(line, buffer[:n]...)
			if newline := bytes.IndexByte(line, '\n'); newline >= 0 {
				return string(line[:newline]), false
			}
			if len(line) > 64*1024 {
				return "", false
			}
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return "", false
		}
		if n == 0 {
			return "", false
		}
	}
}

func (s *terminalInputSession) setTerminalState(state *term.State) error {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	if err := term.Restore(s.ttyFD, state); err != nil {
		return err
	}
	s.desired = state
	return nil
}

func (s *terminalInputSession) restorePromptTerminal() {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	_ = term.Restore(s.ttyFD, s.promptState)
	s.desired = s.promptState
	_ = unix.SetNonblock(s.ttyFD, false)
}

func flushTerminalInput(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
