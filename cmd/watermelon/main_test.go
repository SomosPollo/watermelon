package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
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
