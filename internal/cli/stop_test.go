package cli

import (
	"os"
	"strings"
	"testing"

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
