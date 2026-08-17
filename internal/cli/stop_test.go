package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestStopCommandNoVM(t *testing.T) {
	oldStatus := cliGetVMStatus
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	t.Cleanup(func() { cliGetVMStatus = oldStatus })

	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewStopCmd()
	err = cmd.RunE(cmd, nil)
	if err == nil {
		t.Error("expected error when no VM exists")
	}
}

func TestStopDoesNotWaitForActiveUsageLease(t *testing.T) {
	configureLifecycleLockTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[security]\nenforcement = \"fail\"\n"), 0600); err != nil {
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

	vmName := derivedVMName(dir)
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

	oldStatus, oldProjectMount, oldStop, oldCompatibility := cliGetVMStatus, cliProjectMountSource, cliStopVM, cliRequireCompatibleLima
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	cliProjectMountSource = func(string) (string, error) { return dir, nil }
	compatibilityCalls := 0
	cliRequireCompatibleLima = func() error {
		compatibilityCalls++
		return os.ErrPermission
	}
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliStopVM = oldStop
		cliRequireCompatibleLima = oldCompatibility
	})

	done := make(chan error, 1)
	go func() {
		cmd := NewStopCmd()
		done <- cmd.RunE(cmd, nil)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop with active session: %v", err)
		}
	case <-time.After(time.Second):
		_ = activeSession.Release()
		leaseReleased = true
		<-done
		t.Fatal("stop waited for the active usage lease instead of stopping the VM immediately")
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
	if compatibilityCalls != 0 {
		t.Fatalf("stop ran %d Lima compatibility checks; recovery must remain available on old Lima", compatibilityCalls)
	}
	if err := activeSession.Release(); err != nil {
		t.Fatal(err)
	}
	leaseReleased = true
}

func TestStopRechecksProjectBindingImmediatelyBeforeStop(t *testing.T) {
	dir := t.TempDir()
	replacementProject := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStatus, oldProjectMount, oldStop := cliGetVMStatus, cliProjectMountSource, cliStopVM
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusRunning }
	bindingCalls := 0
	cliProjectMountSource = func(string) (string, error) {
		bindingCalls++
		if bindingCalls == 1 {
			return dir, nil
		}
		return replacementProject, nil
	}
	stopCalls := 0
	cliStopVM = func(string) error {
		stopCalls++
		return nil
	}
	t.Cleanup(func() {
		cliGetVMStatus = oldStatus
		cliProjectMountSource = oldProjectMount
		cliStopVM = oldStop
	})

	cmd := NewStopCmd()
	err = cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to use VM") {
		t.Fatalf("stop error = %v, want replacement refusal", err)
	}
	if bindingCalls != 2 {
		t.Fatalf("project binding checks = %d, want 2", bindingCalls)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", stopCalls)
	}
}
