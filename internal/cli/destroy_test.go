package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

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
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", privateTempDir(t))
	t.Setenv("LIMA_HOME", t.TempDir())
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
