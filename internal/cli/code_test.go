package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

type recordingCodeLease struct {
	name       string
	events     *[]string
	releaseErr error
	released   bool
}

func (lease *recordingCodeLease) Release() error {
	if lease.released {
		return nil
	}
	lease.released = true
	*lease.events = append(*lease.events, "release-"+lease.name)
	return lease.releaseErr
}

type recordingCodeCloser struct {
	events *[]string
	closed bool
}

func (closer *recordingCodeCloser) Close() error {
	if closer.closed {
		return nil
	}
	closer.closed = true
	*closer.events = append(*closer.events, "close-ask")
	return nil
}

type codeRunHarness struct {
	project   string
	vmName    string
	events    []string
	lifecycle *recordingCodeLease
	usage     *recordingCodeLease
	listener  *recordingCodeCloser
}

func setupCodeRunHarness(t *testing.T, enforcement string) *codeRunHarness {
	t.Helper()
	configureLifecycleLockTest(t)

	project := privateTempDir(t)
	contents := fmt.Sprintf(`[security]
enforcement = %q

[ide]
command = "test-code"
workdir = "/workspace/app"
`, enforcement)
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte(contents), 0600); err != nil {
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

	harness := &codeRunHarness{
		project: project,
		vmName:  derivedVMName(project),
	}
	harness.lifecycle = &recordingCodeLease{name: "lifecycle", events: &harness.events}
	harness.usage = &recordingCodeLease{name: "usage", events: &harness.events}
	harness.listener = &recordingCodeCloser{events: &harness.events}

	oldCompatibility := cliRequireCompatibleLima
	oldStatus, oldProjectMount, oldVerify := cliGetVMStatus, cliProjectMountSource, cliVerifyPolicy
	oldEnsureSSH, oldLookPath, oldRunIDE := cliCodeEnsureSSHConfig, cliCodeLookPath, cliCodeRunIDE
	oldLifecycle, oldUsage, oldAsk := cliCodeAcquireLifecycleLock, cliCodeAcquireUsageLease, cliCodeStartAskServer
	t.Cleanup(func() {
		cliRequireCompatibleLima = oldCompatibility
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliVerifyPolicy = oldVerify
		cliCodeEnsureSSHConfig = oldEnsureSSH
		cliCodeLookPath = oldLookPath
		cliCodeRunIDE = oldRunIDE
		cliCodeAcquireLifecycleLock = oldLifecycle
		cliCodeAcquireUsageLease = oldUsage
		cliCodeStartAskServer = oldAsk
	})

	cliRequireCompatibleLima = func() error { return nil }
	cliGetVMStatus = func(name string) lima.VMStatus {
		if name != harness.vmName {
			t.Fatalf("status VM = %q, want %q", name, harness.vmName)
		}
		return lima.StatusRunning
	}
	cliProjectMountSource = func(name string) (string, error) {
		if name != harness.vmName {
			t.Fatalf("project mount VM = %q, want %q", name, harness.vmName)
		}
		return project, nil
	}
	cliVerifyPolicy = func(name string) error {
		if name != harness.vmName {
			t.Fatalf("policy VM = %q, want %q", name, harness.vmName)
		}
		return nil
	}
	cliCodeAcquireLifecycleLock = func(name string) (codeSessionLease, error) {
		if name != harness.vmName {
			t.Fatalf("lifecycle VM = %q, want %q", name, harness.vmName)
		}
		harness.events = append(harness.events, "acquire-lifecycle")
		return harness.lifecycle, nil
	}
	cliCodeAcquireUsageLease = func(name string) (codeSessionLease, error) {
		if name != harness.vmName {
			t.Fatalf("usage VM = %q, want %q", name, harness.vmName)
		}
		harness.events = append(harness.events, "acquire-usage")
		return harness.usage, nil
	}
	cliCodeEnsureSSHConfig = func() error {
		harness.events = append(harness.events, "ensure-ssh")
		return nil
	}
	cliCodeLookPath = func(name string) (string, error) {
		harness.events = append(harness.events, "look-path")
		return "/test/bin/" + name, nil
	}
	cliCodeRunIDE = func(string, []string) error {
		harness.events = append(harness.events, "run-ide")
		return nil
	}
	cliCodeStartAskServer = func(dir, name string) (io.Closer, error) {
		if dir != project || name != harness.vmName {
			t.Fatalf("ask server target = %q %q, want %q %q", dir, name, project, harness.vmName)
		}
		harness.events = append(harness.events, "start-ask")
		return harness.listener, nil
	}

	return harness
}

func TestBuildIDECommand(t *testing.T) {
	tests := []struct {
		name     string
		ideCmd   string
		sshHost  string
		workdir  string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "vscode",
			ideCmd:   "code",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "/project",
			wantCmd:  "code",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678", "/project"},
		},
		{
			name:     "cursor",
			ideCmd:   "cursor",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "/workspace/app",
			wantCmd:  "cursor",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678", "/workspace/app"},
		},
		{
			name:     "guest home",
			ideCmd:   "code",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "",
			wantCmd:  "code",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := buildIDECommand(tt.ideCmd, tt.sshHost, tt.workdir)
			if cmd != tt.wantCmd {
				t.Errorf("buildIDECommand() cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if len(args) != len(tt.wantArgs) {
				t.Errorf("buildIDECommand() args len = %d, want %d", len(args), len(tt.wantArgs))
				return
			}
			for i, arg := range args {
				if arg != tt.wantArgs[i] {
					t.Errorf("buildIDECommand() args[%d] = %q, want %q", i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}

func TestRunCodeWithNameSuccessfulSession(t *testing.T) {
	harness := setupCodeRunHarness(t, "fail")
	var gotCommand string
	var gotArgs []string
	cliCodeRunIDE = func(command string, args []string) error {
		harness.events = append(harness.events, "run-ide")
		gotCommand = command
		gotArgs = append([]string(nil), args...)
		if !harness.lifecycle.released {
			t.Fatal("IDE launched before the lifecycle lock was released")
		}
		if harness.usage.released {
			t.Fatal("usage lease was released before the IDE exited")
		}
		return nil
	}

	if err := runCodeWithName(""); err != nil {
		t.Fatalf("runCodeWithName() error = %v", err)
	}
	if gotCommand != "test-code" {
		t.Fatalf("IDE command = %q, want test-code", gotCommand)
	}
	wantArgs := []string{
		"--wait",
		"--remote",
		"ssh-remote+" + lima.GetSSHHost(harness.vmName),
		"/workspace/app",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("IDE args = %#v, want %#v", gotArgs, wantArgs)
	}
	wantEvents := []string{
		"acquire-lifecycle",
		"ensure-ssh",
		"look-path",
		"acquire-usage",
		"release-lifecycle",
		"run-ide",
		"release-usage",
	}
	if !reflect.DeepEqual(harness.events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", harness.events, wantEvents)
	}
}

func TestRunCodeWithNameSSHSetupFailureIsNonFatal(t *testing.T) {
	harness := setupCodeRunHarness(t, "fail")
	sshErr := errors.New("ssh setup failed")
	cliCodeEnsureSSHConfig = func() error {
		harness.events = append(harness.events, "ensure-ssh")
		return sshErr
	}

	if err := runCodeWithName(""); err != nil {
		t.Fatalf("runCodeWithName() error = %v, want SSH warning to be non-fatal", err)
	}
	if !harness.lifecycle.released || !harness.usage.released {
		t.Fatalf("leases after SSH warning: lifecycle=%v usage=%v", harness.lifecycle.released, harness.usage.released)
	}
	if !slicesContain(harness.events, "run-ide") {
		t.Fatal("IDE was not launched after the non-fatal SSH setup failure")
	}
}

func TestRunCodeWithNameLookPathFailureCleansUp(t *testing.T) {
	harness := setupCodeRunHarness(t, "ask")
	cliCodeLookPath = func(name string) (string, error) {
		harness.events = append(harness.events, "look-path")
		return "", errors.New("missing executable")
	}

	err := runCodeWithName("")
	if err == nil || !strings.Contains(err.Error(), "test-code not found") {
		t.Fatalf("runCodeWithName() error = %v, want missing IDE guidance", err)
	}
	if !harness.lifecycle.released {
		t.Fatal("lifecycle lock was not released after LookPath failure")
	}
	if harness.usage.released || slicesContain(harness.events, "acquire-usage") {
		t.Fatal("usage lease was acquired after LookPath failure")
	}
	if !harness.listener.closed {
		t.Fatal("ask verdict listener was not closed after LookPath failure")
	}
	if slicesContain(harness.events, "run-ide") {
		t.Fatal("IDE was launched after LookPath failure")
	}
}

func TestRunCodeWithNameLauncherFailureReleasesUsageLease(t *testing.T) {
	harness := setupCodeRunHarness(t, "fail")
	launchErr := errors.New("launcher failed")
	cleanupErr := errors.New("usage cleanup failed")
	harness.usage.releaseErr = cleanupErr
	cliCodeRunIDE = func(string, []string) error {
		harness.events = append(harness.events, "run-ide")
		return launchErr
	}

	err := runCodeWithName("")
	if !errors.Is(err, launchErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("runCodeWithName() error = %v, want launcher and usage cleanup failures", err)
	}
	if !strings.Contains(err.Error(), "launching test-code") {
		t.Fatalf("runCodeWithName() error = %v, want launcher context", err)
	}
	if !harness.lifecycle.released || !harness.usage.released {
		t.Fatalf("leases after launcher failure: lifecycle=%v usage=%v", harness.lifecycle.released, harness.usage.released)
	}
}

func TestRunCodeWithNameLifecycleReleaseFailureAbortsAndReleasesUsageLease(t *testing.T) {
	harness := setupCodeRunHarness(t, "fail")
	releaseErr := errors.New("lifecycle release failed")
	harness.lifecycle.releaseErr = releaseErr

	err := runCodeWithName("")
	if !errors.Is(err, releaseErr) {
		t.Fatalf("runCodeWithName() error = %v, want lifecycle release failure", err)
	}
	if slicesContain(harness.events, "run-ide") {
		t.Fatal("IDE launched after lifecycle handoff failed")
	}
	if !harness.usage.released {
		t.Fatal("usage lease was not released after lifecycle handoff failed")
	}
}

func TestRunCodeWithNameUsageReleaseFailureIsReturned(t *testing.T) {
	harness := setupCodeRunHarness(t, "fail")
	releaseErr := errors.New("usage release failed")
	harness.usage.releaseErr = releaseErr

	err := runCodeWithName("")
	if !errors.Is(err, releaseErr) {
		t.Fatalf("runCodeWithName() error = %v, want usage release failure", err)
	}
	if !slicesContain(harness.events, "run-ide") || !harness.usage.released {
		t.Fatalf("events after usage release failure = %#v", harness.events)
	}
}

func TestRunCodeWithNameAskListenerLivesForEntireIDESession(t *testing.T) {
	harness := setupCodeRunHarness(t, "ask")
	cliCodeRunIDE = func(string, []string) error {
		harness.events = append(harness.events, "run-ide")
		if harness.listener.closed {
			t.Fatal("ask verdict listener closed before the IDE session started")
		}
		if !harness.lifecycle.released {
			t.Fatal("lifecycle lock remained held during the IDE session")
		}
		if harness.usage.released {
			t.Fatal("usage lease was released during the IDE session")
		}
		return nil
	}

	if err := runCodeWithName(""); err != nil {
		t.Fatalf("runCodeWithName() error = %v", err)
	}
	if !harness.listener.closed {
		t.Fatal("ask verdict listener was not closed after the IDE exited")
	}
	if !harness.usage.released {
		t.Fatal("usage lease was not released after the IDE exited")
	}
	wantTail := []string{"release-lifecycle", "run-ide", "release-usage", "close-ask"}
	if len(harness.events) < len(wantTail) {
		t.Fatalf("events = %#v, want tail %#v", harness.events, wantTail)
	}
	gotTail := harness.events[len(harness.events)-len(wantTail):]
	if !reflect.DeepEqual(gotTail, wantTail) {
		t.Fatalf("event tail = %#v, want %#v (all events: %#v)", gotTail, wantTail, harness.events)
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
