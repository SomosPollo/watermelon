package lima

import (
	"testing"
)

func TestVMNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/Users/test/myproject", "watermelon-myproject"},
		{"/Users/test/my-project", "watermelon-my-project"},
		{"/Users/test/My Project", "watermelon-my-project"},
	}

	for _, tc := range tests {
		got := VMNameFromPath(tc.path)
		// Should start with watermelon-
		if got[:11] != "watermelon-" {
			t.Errorf("VMNameFromPath(%q) = %q, expected prefix 'watermelon-'", tc.path, got)
		}
	}
}

func TestVMStatus(t *testing.T) {
	// Test status parsing
	status := parseStatus("Running")
	if status != StatusRunning {
		t.Errorf("expected StatusRunning, got %v", status)
	}

	status = parseStatus("Stopped")
	if status != StatusStopped {
		t.Errorf("expected StatusStopped, got %v", status)
	}

	status = parseStatus("")
	if status != StatusNotFound {
		t.Errorf("expected StatusNotFound, got %v", status)
	}
}

func TestGetStatusRunning(t *testing.T) {
	withFakeExec(t, "Running", 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusRunning {
		t.Errorf("GetStatus() = %v, want StatusRunning", status)
	}
}

func TestGetStatusStopped(t *testing.T) {
	withFakeExec(t, "Stopped", 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusStopped {
		t.Errorf("GetStatus() = %v, want StatusStopped", status)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	withFakeExec(t, "", 1)
	status := GetStatus("watermelon-nonexistent")
	if status != StatusNotFound {
		t.Errorf("GetStatus() = %v, want StatusNotFound", status)
	}
}

func TestStopCallsLimactl(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Stop("watermelon-test-12345678")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	if captured[0] != "limactl stop watermelon-test-12345678" {
		t.Errorf("Stop() command = %q, want %q", captured[0], "limactl stop watermelon-test-12345678")
	}
}

func TestDeleteCallsLimactl(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Delete("watermelon-test-12345678")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	if captured[0] != "limactl delete --force watermelon-test-12345678" {
		t.Errorf("Delete() command = %q, want %q", captured[0], "limactl delete --force watermelon-test-12345678")
	}
}

func TestShellPassesWorkdir(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	Shell("watermelon-test-12345678", "/custom/workdir")
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl shell --workdir /custom/workdir watermelon-test-12345678"
	if captured[0] != want {
		t.Errorf("Shell() command = %q, want %q", captured[0], want)
	}
}

func TestShellOmitsWorkdirWhenEmpty(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	Shell("watermelon-test-12345678", "")
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl shell watermelon-test-12345678"
	if captured[0] != want {
		t.Errorf("Shell() command = %q, want %q", captured[0], want)
	}
}

func TestExecPassesWorkdir(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	Exec("watermelon-test-12345678", []string{"docker", "ps"}, "/custom/workdir")
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl shell --workdir /custom/workdir watermelon-test-12345678 -- docker ps"
	if captured[0] != want {
		t.Errorf("Exec() command = %q, want %q", captured[0], want)
	}
}

func TestExecOmitsWorkdirWhenEmpty(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	Exec("watermelon-test-12345678", []string{"docker", "ps"}, "")
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl shell watermelon-test-12345678 -- docker ps"
	if captured[0] != want {
		t.Errorf("Exec() command = %q, want %q", captured[0], want)
	}
}

func TestCopyHostToVM(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Copy("./file.txt", "somospollo-vm:/tmp/", false)
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl copy ./file.txt somospollo-vm:/tmp/"
	if captured[0] != want {
		t.Errorf("Copy() command = %q, want %q", captured[0], want)
	}
}

func TestCopyRecursive(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Copy("./dir/", "somospollo-vm:/tmp/", true)
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}
	want := "limactl copy --recursive ./dir/ somospollo-vm:/tmp/"
	if captured[0] != want {
		t.Errorf("Copy() command = %q, want %q", captured[0], want)
	}
}

func TestCopyVMToHost(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Copy("somospollo-vm:/tmp/output.log", "./", false)
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	want := "limactl copy somospollo-vm:/tmp/output.log ./"
	if captured[0] != want {
		t.Errorf("Copy() command = %q, want %q", captured[0], want)
	}
}

func TestVMStatusString(t *testing.T) {
	tests := []struct {
		status VMStatus
		want   string
	}{
		{StatusRunning, "Running"},
		{StatusStopped, "Stopped"},
		{StatusNotFound, "Not found"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("VMStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
