package lima

import (
	"errors"
	"os/exec"
	"strings"
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
	if status != StatusUnknown {
		t.Errorf("expected StatusUnknown, got %v", status)
	}
}

func TestGetStatusRunning(t *testing.T) {
	withFakeExec(t, `{"name":"watermelon-other","status":"Stopped"}
{"name":"watermelon-test-12345678","status":"Running"}`, 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusRunning {
		t.Errorf("GetStatus() = %v, want StatusRunning", status)
	}
}

func TestGetStatusStopped(t *testing.T) {
	withFakeExec(t, `{"name":"watermelon-test-12345678","status":"Stopped"}`, 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusStopped {
		t.Errorf("GetStatus() = %v, want StatusStopped", status)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	withFakeExec(t, `{"name":"watermelon-other","status":"Running"}`, 0)
	status := GetStatus("watermelon-nonexistent")
	if status != StatusNotFound {
		t.Errorf("GetStatus() = %v, want StatusNotFound", status)
	}
}

func TestGetStatusTreatsTransitionalBrokenAndCommandErrorsAsUnknown(t *testing.T) {
	for _, limaStatus := range []string{"Starting", "Stopping", "Broken", "Unknown"} {
		t.Run(limaStatus, func(t *testing.T) {
			withFakeExec(t, `{"name":"watermelon-test-12345678","status":"`+limaStatus+`"}`, 0)
			if got := GetStatus("watermelon-test-12345678"); got != StatusUnknown {
				t.Fatalf("GetStatus() = %v, want StatusUnknown", got)
			}
		})
	}

	t.Run("command error", func(t *testing.T) {
		withFakeExec(t, "", 1)
		if got := GetStatus("watermelon-test-12345678"); got != StatusUnknown {
			t.Fatalf("GetStatus() = %v, want StatusUnknown", got)
		}
	})

	t.Run("malformed output", func(t *testing.T) {
		withFakeExec(t, "not-json", 0)
		if got := GetStatus("watermelon-test-12345678"); got != StatusUnknown {
			t.Fatalf("GetStatus() = %v, want StatusUnknown", got)
		}
	})
}

func TestProjectMountSource(t *testing.T) {
	withFakeExec(t, `{"name":"watermelon-test-12345678","config":{"mounts":[{"location":"/host/project","mountPoint":"/project","writable":true}]}}`, 0)

	source, err := ProjectMountSource("watermelon-test-12345678")
	if err != nil {
		t.Fatalf("ProjectMountSource() error = %v", err)
	}
	if source != "/host/project" {
		t.Fatalf("ProjectMountSource() = %q, want /host/project", source)
	}
}

func TestProjectMountSourceRejectsWrongOrAmbiguousInstance(t *testing.T) {
	t.Run("wrong instance", func(t *testing.T) {
		withFakeExec(t, `{"name":"watermelon-other","config":{"mounts":[{"location":"/host/project","mountPoint":"/project"}]}}`, 0)
		if _, err := ProjectMountSource("watermelon-test-12345678"); err == nil || !strings.Contains(err.Error(), "watermelon-other") {
			t.Fatalf("ProjectMountSource() error = %v, want wrong-instance error", err)
		}
	})

	t.Run("missing project mount", func(t *testing.T) {
		withFakeExec(t, `{"name":"watermelon-test-12345678","config":{"mounts":[]}}`, 0)
		if _, err := ProjectMountSource("watermelon-test-12345678"); err == nil || !strings.Contains(err.Error(), "no /project mount") {
			t.Fatalf("ProjectMountSource() error = %v, want missing-mount error", err)
		}
	})

	t.Run("duplicate project mount", func(t *testing.T) {
		withFakeExec(t, `{"name":"watermelon-test-12345678","config":{"mounts":[{"location":"/one","mountPoint":"/project"},{"location":"/two","mountPoint":"/project"}]}}`, 0)
		if _, err := ProjectMountSource("watermelon-test-12345678"); err == nil || !strings.Contains(err.Error(), "multiple /project mounts") {
			t.Fatalf("ProjectMountSource() error = %v, want duplicate-mount error", err)
		}
	})
}

func TestStartCreatesVMWithTimeout(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Start("watermelon-test-12345678", "/tmp/watermelon.yaml")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected create and start commands, got %d commands", len(captured))
	}

	wants := []string{
		"limactl create --name watermelon-test-12345678 /tmp/watermelon.yaml",
		"limactl start --timeout=30m watermelon-test-12345678",
	}
	for i, want := range wants {
		if captured[i] != want {
			t.Errorf("Start() command %d = %q, want %q", i, captured[i], want)
		}
	}
}

func TestStartWithConfigDoesNotInspectOrReuseExistingVM(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = func(command string, args ...string) *exec.Cmd {
		captured = append(captured, command+" "+strings.Join(args, " "))
		return fakeExecCommand("Running", 1)(command, args...)
	}
	t.Cleanup(func() { execCommand = old })

	err := Start("watermelon-test-12345678", "/tmp/watermelon.yaml")
	if err == nil {
		t.Fatal("Start() unexpectedly reused an existing VM")
	}
	var startErr *StartError
	if !errors.As(err, &startErr) || startErr.Stage != StartStageCreate {
		t.Fatalf("Start() error = %T %v, want create-stage StartError", err, err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected only the create command, got %d commands: %v", len(captured), captured)
	}
	if strings.Contains(captured[0], " list ") {
		t.Fatalf("create-only Start() unexpectedly inspected VM status: %q", captured[0])
	}
}

func TestStartRestartsStoppedVMWithTimeout(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, `{"name":"watermelon-test-12345678","status":"Stopped"}`)
	t.Cleanup(func() { execCommand = old })

	err := Start("watermelon-test-12345678", "")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected status check and start command, got %d commands", len(captured))
	}

	want := "limactl start --timeout=30m watermelon-test-12345678"
	if captured[1] != want {
		t.Errorf("Start() command = %q, want %q", captured[1], want)
	}
}

func TestStartWithoutConfigRejectsMissingVM(t *testing.T) {
	withFakeExec(t, "", 0)

	err := Start("watermelon-missing-12345678", "")
	if err == nil || !strings.Contains(err.Error(), "instance not found") {
		t.Fatalf("Start() error = %v, want actionable not-found error", err)
	}
}

func TestStartWithoutConfigRejectsUnknownVMState(t *testing.T) {
	withFakeExec(t, `{"name":"watermelon-test-12345678","status":"Starting"}`, 0)

	err := Start("watermelon-test-12345678", "")
	if err == nil || !strings.Contains(err.Error(), "state is unknown") {
		t.Fatalf("Start() error = %v, want unknown-state error", err)
	}
	var startErr *StartError
	if !errors.As(err, &startErr) || startErr.Stage != StartStageInspect {
		t.Fatalf("Start() error = %T %v, want inspect-stage StartError", err, err)
	}
}

func TestVerifyPolicyApplied(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	if err := VerifyPolicyApplied("watermelon-test-12345678"); err != nil {
		t.Fatalf("VerifyPolicyApplied() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected one quiet marker check, got %d commands", len(captured))
	}
	for _, want := range []string{"limactl shell watermelon-test-12345678 -- sh -c", "/run/watermelon-policy-applied", "stat -c %u"} {
		if !strings.Contains(captured[0], want) {
			t.Errorf("marker check %q does not contain %q", captured[0], want)
		}
	}
}

func TestVerifyPolicyAppliedRejectsMissingMarker(t *testing.T) {
	withFakeExec(t, "", 1)

	err := VerifyPolicyApplied("watermelon-test-12345678")
	if err == nil || !strings.Contains(err.Error(), "/run/watermelon-policy-applied") {
		t.Fatalf("VerifyPolicyApplied() error = %v, want marker error", err)
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

func TestExecPassesArgvCommandDirectly(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Exec("watermelon-test-12345678", []string{"npm", "install"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}

	want := "limactl shell --workdir /project watermelon-test-12345678 -- npm install"
	if captured[0] != want {
		t.Errorf("Exec() command = %q, want %q", captured[0], want)
	}
}

func TestExecRunsCompoundSingleStringThroughShell(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	err := Exec("watermelon-test-12345678", []string{"npm install && npm test"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 command, got %d", len(captured))
	}

	want := "limactl shell --workdir /project watermelon-test-12345678 -- sh -lc npm install && npm test"
	if captured[0] != want {
		t.Errorf("Exec() command = %q, want %q", captured[0], want)
	}
}

func TestVMStatusString(t *testing.T) {
	tests := []struct {
		status VMStatus
		want   string
	}{
		{StatusRunning, "Running"},
		{StatusStopped, "Stopped"},
		{StatusUnknown, "Unknown"},
		{StatusNotFound, "Not found"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("VMStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
