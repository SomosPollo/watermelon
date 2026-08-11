package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

type controlledPromptReader struct {
	started  chan struct{}
	response chan string
}

func (reader *controlledPromptReader) Read(buffer []byte) (int, error) {
	close(reader.started)
	response := <-reader.response
	return copy(buffer, response), nil
}

func TestDestroyCommandNoVM(t *testing.T) {
	oldStatus := destroyGetStatus
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	t.Cleanup(func() { destroyGetStatus = oldStatus })

	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewDestroyCmd()
	cmd.Flags().Set("force", "true")
	err = cmd.RunE(cmd, nil)
	if err == nil {
		t.Error("expected error when no VM exists")
	}
}

func TestDestroyStopsActiveSessionBeforeWaitingToDelete(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	vmName := lima.VMNameFromPath(dir)
	activeSession, err := acquireSharedVMUsageLease(vmName)
	if err != nil {
		t.Fatal(err)
	}
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			_ = activeSession.Release()
		}
	})

	oldStatus, oldStop, oldDelete, oldProjectMount, oldCompatibility := destroyGetStatus, destroyStop, destroyDelete, cliProjectMountSource, cliRequireCompatibleLima
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	compatibilityCalls := 0
	cliRequireCompatibleLima = func() error {
		compatibilityCalls++
		return os.ErrPermission
	}
	stopCalled := make(chan struct{}, 1)
	destroyStop = func(string) error {
		stopCalled <- struct{}{}
		return nil
	}
	deleteCalled := make(chan struct{}, 1)
	destroyDelete = func(string) error {
		deleteCalled <- struct{}{}
		return nil
	}
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyStop = oldStop
		destroyDelete = oldDelete
		cliProjectMountSource = oldProjectMount
		cliRequireCompatibleLima = oldCompatibility
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("destroy waited for the active lease before stopping the VM")
	}
	select {
	case <-deleteCalled:
		t.Fatal("destroy deleted the VM before the active client released its usage lease")
	default:
	}
	if err := activeSession.Release(); err != nil {
		t.Fatal(err)
	}
	leaseReleased = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("destroy after active session detached: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("destroy did not continue after the active session detached")
	}
	select {
	case <-deleteCalled:
	default:
		t.Fatal("destroy did not delete after the active client released its usage lease")
	}
	if compatibilityCalls != 0 {
		t.Fatalf("destroy ran %d Lima compatibility checks; recovery must remain available on old Lima", compatibilityCalls)
	}
}

func TestDestroyPromptDoesNotBlockStopLifecycle(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	oldDestroyStatus, oldCLIStatus, oldProjectMount, oldStop := destroyGetStatus, cliGetVMStatus, cliProjectMountSource, cliStopVM
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	stopCalled := make(chan struct{}, 1)
	cliStopVM = func(string) error {
		stopCalled <- struct{}{}
		return nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldDestroyStatus
		cliGetVMStatus = oldCLIStatus
		cliProjectMountSource = oldProjectMount
		cliStopVM = oldStop
	})

	reader := &controlledPromptReader{started: make(chan struct{}), response: make(chan string, 1)}
	destroyCmd := NewDestroyCmd()
	destroyCmd.SetIn(reader)
	destroyDone := make(chan error, 1)
	go func() { destroyDone <- destroyCmd.RunE(destroyCmd, nil) }()
	responseSent := false
	t.Cleanup(func() {
		if !responseSent {
			reader.response <- "n\n"
		}
	})

	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("destroy did not reach its confirmation prompt")
	}
	stopDone := make(chan error, 1)
	go func() {
		stopCmd := NewStopCmd()
		stopDone <- stopCmd.RunE(stopCmd, nil)
	}()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("stop while destroy awaited confirmation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("destroy confirmation held the lifecycle lock and blocked stop")
	}
	select {
	case <-stopCalled:
	default:
		t.Fatal("stop did not reach the VM stop operation")
	}

	reader.response <- "n\n"
	responseSent = true
	select {
	case err := <-destroyDone:
		if err != nil {
			t.Fatalf("cancelled destroy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("destroy did not exit after cancellation")
	}
}

func TestDestroyRefusesLegacyVMRecreatedWhilePromptWaits(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	vmName := lima.VMNameFromPath(dir)
	instanceDir := filepath.Join(os.Getenv("LIMA_HOME"), vmName)
	oldStatus, oldProjectMount, oldDelete := destroyGetStatus, cliProjectMountSource, destroyDelete
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	deleteCalls := 0
	destroyDelete = func(string) error {
		deleteCalls++
		return nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		destroyDelete = oldDelete
	})

	reader := &controlledPromptReader{started: make(chan struct{}), response: make(chan string, 1)}
	cmd := NewDestroyCmd()
	cmd.SetIn(reader)
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()
	responseSent := false
	t.Cleanup(func() {
		if !responseSent {
			reader.response <- "n\n"
		}
	})

	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("destroy did not reach its confirmation prompt")
	}
	// The destroy command keeps the old directory inode open but does not hold
	// the lifecycle lock. Replacing the path simulates a force-destroy followed
	// by recreation under the same legacy path-derived public name.
	if err := os.Remove(instanceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(instanceDir, 0700); err != nil {
		t.Fatal(err)
	}
	reader.response <- "yes\n"
	responseSent = true
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "incarnation changed") {
			t.Fatalf("destroy replacement error = %v, want incarnation-change refusal", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("destroy did not finish after replacement confirmation")
	}
	if deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want 0 for replacement incarnation", deleteCalls)
	}
}

func TestDestroyClearsAppliedPolicySnapshotAfterSuccessfulDelete(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	path, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}

	oldStatus, oldDelete, oldProjectMount := destroyGetStatus, destroyDelete, cliProjectMountSource
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	destroyDelete = func(string) error { return nil }
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
		cliProjectMountSource = oldProjectMount
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot remains after successful destroy: %v", err)
	}
}

func TestDestroyPreservesAppliedPolicySnapshotWhenDeleteFails(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	path, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}

	oldStatus, oldDelete, oldProjectMount := destroyGetStatus, destroyDelete, cliProjectMountSource
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	destroyDelete = func(string) error { return errors.New("delete failed") }
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
		cliProjectMountSource = oldProjectMount
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || err.Error() != "delete failed" {
		t.Fatalf("destroy error = %v, want delete failure", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("snapshot was removed after failed destroy: %v", err)
	}
}

func TestDestroyRechecksProjectBindingAfterConfirmation(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)
	replacementProject := t.TempDir()

	oldStatus, oldDelete, oldProjectMount := destroyGetStatus, destroyDelete, cliProjectMountSource
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	deleteCalls := 0
	destroyDelete = func(string) error {
		deleteCalls++
		return nil
	}
	bindingCalls := 0
	cliProjectMountSource = func(string) (string, error) {
		bindingCalls++
		if bindingCalls == 1 {
			return dir, nil
		}
		return replacementProject, nil
	}
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
		cliProjectMountSource = oldProjectMount
	})

	cmd := NewDestroyCmd()
	cmd.SetIn(strings.NewReader("yes\n"))
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to use VM") {
		t.Fatalf("destroy error = %v, want replacement refusal", err)
	}
	if bindingCalls != 2 {
		t.Fatalf("project binding checks = %d, want 2", bindingCalls)
	}
	if deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want 0", deleteCalls)
	}
}

func TestDestroyAllowsBoundUnknownOrBrokenInstance(t *testing.T) {
	dir := prepareDestroySnapshotTest(t)

	oldStatus, oldDelete, oldProjectMount := destroyGetStatus, destroyDelete, cliProjectMountSource
	destroyGetStatus = func(string) lima.VMStatus { return lima.StatusUnknown }
	deleteCalls := 0
	destroyDelete = func(string) error {
		deleteCalls++
		return nil
	}
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	t.Cleanup(func() {
		destroyGetStatus = oldStatus
		destroyDelete = oldDelete
		cliProjectMountSource = oldProjectMount
	})

	cmd := NewDestroyCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("destroy unknown/broken VM: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func prepareDestroySnapshotTest(t *testing.T) string {
	t.Helper()
	oldStop := destroyStop
	destroyStop = func(string) error { return nil }
	t.Cleanup(func() { destroyStop = oldStop })
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	limaHome := t.TempDir()
	t.Setenv("LIMA_HOME", limaHome)
	instanceDir := filepath.Join(limaHome, lima.VMNameFromPath(dir))
	if err := os.Mkdir(instanceDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldInstanceDir := destroyInstanceDir
	destroyInstanceDir = func(string) (string, error) { return instanceDir, nil }
	t.Cleanup(func() { destroyInstanceDir = oldInstanceDir })
	if err := saveAppliedPolicySnapshot(dir, config.NewConfig()); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	return dir
}
