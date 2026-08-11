package lima

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func limaStringRecord(values ...string) string {
	fields := make([]string, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value) // JSON string marshaling cannot fail.
		fields = append(fields, string(encoded))
	}
	return strings.Join(fields, "\t")
}

func limaMountRecord(name, mountsJSON string) string {
	return limaStringRecord(name) + "\t" + mountsJSON
}

func TestVMNameFromPath(t *testing.T) {
	tests := []struct {
		path string
	}{
		{"/Users/test/myproject"},
		{"/Users/test/my-project"},
		{"/Users/test/My Project"},
	}

	for _, tc := range tests {
		got := VMNameFromPath(tc.path)
		hash := sha256.Sum256([]byte(tc.path))
		base := strings.ReplaceAll(strings.ToLower(filepath.Base(tc.path)), " ", "-")
		want := fmt.Sprintf("watermelon-%s-%s", base, hex.EncodeToString(hash[:])[:8])
		if got != want {
			t.Errorf("VMNameFromPath(%q) = %q, want backwards-compatible name %q", tc.path, got, want)
		}
	}
}

func TestVMNameFromPathAlwaysProducesValidBoundedLimaName(t *testing.T) {
	for _, projectPath := range []string{
		"/tmp/!!!",
		"/tmp/crème brûlée 🚀",
		"/tmp/..multiple___separators--",
		"/tmp/" + strings.Repeat("a", 300),
		"/",
	} {
		t.Run(projectPath, func(t *testing.T) {
			got := VMNameFromPath(projectPath)
			if len(got) > 76 {
				t.Fatalf("VMNameFromPath() length = %d, want <= 76: %q", len(got), got)
			}
			if err := config.ValidateVMName(got); err != nil {
				t.Fatalf("VMNameFromPath(%q) = %q is invalid: %v", projectPath, got, err)
			}
		})
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
	withFakeExec(t, limaStringRecord("watermelon-other", "Stopped")+"\n"+
		limaStringRecord("watermelon-test-12345678", "Running"), 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusRunning {
		t.Errorf("GetStatus() = %v, want StatusRunning", status)
	}
}

func TestGetStatusStopped(t *testing.T) {
	withFakeExec(t, limaStringRecord("watermelon-test-12345678", "Stopped"), 0)
	status := GetStatus("watermelon-test-12345678")
	if status != StatusStopped {
		t.Errorf("GetStatus() = %v, want StatusStopped", status)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	withFakeExec(t, limaStringRecord("watermelon-other", "Running"), 0)
	status := GetStatus("watermelon-nonexistent")
	if status != StatusNotFound {
		t.Errorf("GetStatus() = %v, want StatusNotFound", status)
	}
}

func TestGetStatusUsesNarrowTemplateOutput(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, limaStringRecord("custom-dev", "Running"))
	t.Cleanup(func() { execCommand = old })

	if got := GetStatus("custom-dev"); got != StatusRunning {
		t.Fatalf("GetStatus() = %v, want StatusRunning", got)
	}
	want := "limactl list --format " + statusListFormat
	if len(captured) != 1 || captured[0] != want {
		t.Fatalf("GetStatus() commands = %q, want [%q]", captured, want)
	}
}

func TestGetStatusTreatsTransitionalBrokenAndCommandErrorsAsUnknown(t *testing.T) {
	for _, limaStatus := range []string{"Starting", "Stopping", "Broken", "Unknown"} {
		t.Run(limaStatus, func(t *testing.T) {
			withFakeExec(t, limaStringRecord("watermelon-test-12345678", limaStatus), 0)
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
	withFakeExec(t, limaMountRecord("watermelon-test-12345678", `[{"location":"/host/project","mountPoint":"/project","writable":true}]`), 0)

	source, err := ProjectMountSource("watermelon-test-12345678")
	if err != nil {
		t.Fatalf("ProjectMountSource() error = %v", err)
	}
	if source != "/host/project" {
		t.Fatalf("ProjectMountSource() = %q, want /host/project", source)
	}
}

func TestInstanceDirUsesNarrowTemplateOutput(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, limaStringRecord("custom-dev", "/host/.lima/custom-dev"))
	t.Cleanup(func() { execCommand = old })

	dir, err := InstanceDir("custom-dev")
	if err != nil {
		t.Fatalf("InstanceDir() error = %v", err)
	}
	if dir != "/host/.lima/custom-dev" {
		t.Fatalf("InstanceDir() = %q", dir)
	}
	want := "limactl list --format " + dirListFormat + " custom-dev"
	if len(captured) != 1 || captured[0] != want {
		t.Fatalf("InstanceDir() commands = %q, want [%q]", captured, want)
	}
}

func TestMountSourceReturnsDedicatedBootstrapMount(t *testing.T) {
	withFakeExec(t, limaMountRecord("custom-dev", `[{"location":"/host/runtime/bootstrap","mountPoint":"/mnt/watermelon/bootstrap","writable":false}]`), 0)

	mount, err := InstanceMount("custom-dev", "/mnt/watermelon/bootstrap")
	if err != nil {
		t.Fatalf("InstanceMount() error = %v", err)
	}
	if mount.Location != "/host/runtime/bootstrap" || mount.Writable {
		t.Fatalf("InstanceMount() = %+v, want read-only /host/runtime/bootstrap", mount)
	}
}

func TestInstanceMountUsesNarrowTemplateOutput(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, limaMountRecord("custom-dev", `[{"location":"/host/project","mountPoint":"/project"}]`))
	t.Cleanup(func() { execCommand = old })

	if _, err := InstanceMount("custom-dev", "/project"); err != nil {
		t.Fatalf("InstanceMount() error = %v", err)
	}
	want := "limactl list --format " + mountListFormat + " custom-dev"
	if len(captured) != 1 || captured[0] != want {
		t.Fatalf("InstanceMount() commands = %q, want [%q]", captured, want)
	}
}

func TestInstanceMountParsesRecordLargerThanScannerLimit(t *testing.T) {
	location := "/host/" + strings.Repeat("x", 70*1024)
	mounts, err := json.Marshal([]LimaMount{{Location: location, MountPoint: "/project", Writable: true}})
	if err != nil {
		t.Fatal(err)
	}
	withFakeExec(t, limaMountRecord("custom-dev", string(mounts)), 0)

	mount, err := InstanceMount("custom-dev", "/project")
	if err != nil {
		t.Fatalf("InstanceMount() error = %v", err)
	}
	if mount.Location != location {
		t.Fatalf("InstanceMount() location length = %d, want %d", len(mount.Location), len(location))
	}
}

func TestMountSourceRejectsMissingEmptyAndDuplicateMounts(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "missing",
			json: limaMountRecord("custom-dev", `[]`),
			want: "has no /mnt/watermelon/bootstrap mount",
		},
		{
			name: "empty source",
			json: limaMountRecord("custom-dev", `[{"location":"","mountPoint":"/mnt/watermelon/bootstrap"}]`),
			want: "has an empty source",
		},
		{
			name: "duplicate",
			json: limaMountRecord("custom-dev", `[{"location":"/one","mountPoint":"/mnt/watermelon/bootstrap"},{"location":"/two","mountPoint":"/mnt/watermelon/bootstrap"}]`),
			want: "has multiple /mnt/watermelon/bootstrap mounts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeExec(t, tt.json, 0)
			_, err := MountSource("custom-dev", "/mnt/watermelon/bootstrap")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("MountSource() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestProjectMountSourceRejectsWrongOrAmbiguousInstance(t *testing.T) {
	t.Run("wrong instance", func(t *testing.T) {
		withFakeExec(t, limaMountRecord("watermelon-other", `[{"location":"/host/project","mountPoint":"/project"}]`), 0)
		if _, err := ProjectMountSource("watermelon-test-12345678"); err == nil || !strings.Contains(err.Error(), "watermelon-other") {
			t.Fatalf("ProjectMountSource() error = %v, want wrong-instance error", err)
		}
	})

	t.Run("missing project mount", func(t *testing.T) {
		withFakeExec(t, limaMountRecord("watermelon-test-12345678", `[]`), 0)
		if _, err := ProjectMountSource("watermelon-test-12345678"); err == nil || !strings.Contains(err.Error(), "no /project mount") {
			t.Fatalf("ProjectMountSource() error = %v, want missing-mount error", err)
		}
	})

	t.Run("duplicate project mount", func(t *testing.T) {
		withFakeExec(t, limaMountRecord("watermelon-test-12345678", `[{"location":"/one","mountPoint":"/project"},{"location":"/two","mountPoint":"/project"}]`), 0)
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
	execCommand = fakeExecCommandCapture(&captured, limaStringRecord("watermelon-test-12345678", "Stopped"))
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
	withFakeExec(t, limaStringRecord("watermelon-test-12345678", "Starting"), 0)

	err := Start("watermelon-test-12345678", "")
	if err == nil || !strings.Contains(err.Error(), "state is unknown") {
		t.Fatalf("Start() error = %v, want unknown-state error", err)
	}
	var startErr *StartError
	if !errors.As(err, &startErr) || startErr.Stage != StartStageInspect {
		t.Fatalf("Start() error = %T %v, want inspect-stage StartError", err, err)
	}
}

func TestVerifyProvisioningComplete(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	if err := VerifyProvisioningComplete("watermelon-test-12345678"); err != nil {
		t.Fatalf("VerifyProvisioningComplete() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected one quiet marker check, got %d commands", len(captured))
	}
	for _, want := range []string{"limactl shell watermelon-test-12345678 -- sh -c", "/run/watermelon-provisioning-complete", "stat -c %u", "stat -c %a", "= 600"} {
		if !strings.Contains(captured[0], want) {
			t.Errorf("marker check %q does not contain %q", captured[0], want)
		}
	}
}

func TestVerifyProvisioningCompleteRejectsMissingMarker(t *testing.T) {
	withFakeExec(t, "", 1)

	err := VerifyProvisioningComplete("watermelon-test-12345678")
	if err == nil || !strings.Contains(err.Error(), "/run/watermelon-provisioning-complete") {
		t.Fatalf("VerifyProvisioningComplete() error = %v, want marker error", err)
	}
}

func TestVerifyPolicyAppliedUsesStrongerCompletionProbe(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	if err := VerifyPolicyApplied("watermelon-test-12345678"); err != nil {
		t.Fatalf("VerifyPolicyApplied() compatibility wrapper error = %v", err)
	}
	if len(captured) != 1 || !strings.Contains(captured[0], "/run/watermelon-provisioning-complete") {
		t.Fatalf("compatibility probe = %q, want final provisioning marker", captured)
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

func TestCopyCallsLimactlWithOptionSeparator(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		dst       string
		recursive bool
		want      string
	}{
		{
			name: "host to vm",
			src:  "./file.txt",
			dst:  "somospollo-vm:/tmp/",
			want: "limactl copy -- ./file.txt somospollo-vm:/tmp/",
		},
		{
			name: "vm to host",
			src:  "somospollo-vm:/tmp/output.log",
			dst:  "./",
			want: "limactl copy -- somospollo-vm:/tmp/output.log ./",
		},
		{
			name:      "recursive",
			src:       "./dir/",
			dst:       "somospollo-vm:/tmp/",
			recursive: true,
			want:      "limactl copy --recursive -- ./dir/ somospollo-vm:/tmp/",
		},
		{
			name: "leading dash remains an operand",
			src:  "-local-file",
			dst:  "somospollo-vm:/tmp/",
			want: "limactl copy -- -local-file somospollo-vm:/tmp/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []string
			old := execCommand
			execCommand = fakeExecCommandCapture(&captured, "")
			t.Cleanup(func() { execCommand = old })

			if err := Copy(tt.src, tt.dst, tt.recursive); err != nil {
				t.Fatalf("Copy() error = %v", err)
			}
			if len(captured) != 1 {
				t.Fatalf("captured %d commands, want 1", len(captured))
			}
			if captured[0] != tt.want {
				t.Fatalf("Copy() command = %q, want %q", captured[0], tt.want)
			}
		})
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

func TestExecMarksNumericGuestExitStatuses(t *testing.T) {
	for _, want := range []int{1, 2, 126, 127, 128, 130, 143, 254, 255} {
		t.Run(fmt.Sprint(want), func(t *testing.T) {
			withFakeExec(t, "", want)

			err := Exec("watermelon-test-12345678", []string{"guest-command"})
			guestErr, ok := err.(interface{ GuestExitCode() int })
			if !ok {
				t.Fatalf("Exec() error = %T %v, want guest exit marker", err, err)
			}
			if got := guestErr.GuestExitCode(); got != want {
				t.Fatalf("guest exit code = %d, want %d", got, want)
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("Exec() error does not retain *exec.ExitError: %v", err)
			}
		})
	}
}

func TestExecDoesNotMarkSignalKilledLimactlAsGuestExit(t *testing.T) {
	old := execCommand
	execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "kill -TERM $$")
	}
	t.Cleanup(func() { execCommand = old })

	err := Exec("watermelon-test-12345678", []string{"guest-command"})
	if err == nil {
		t.Fatal("Exec() unexpectedly succeeded")
	}
	if _, ok := err.(interface{ GuestExitCode() int }); ok {
		t.Fatalf("signal-killed limactl was marked as a guest exit: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Exec() error = %T %v, want *exec.ExitError", err, err)
	}
	if got := exitErr.ExitCode(); got != -1 {
		t.Fatalf("signal-killed process exit code = %d, want -1", got)
	}
}

func TestExecDoesNotMarkLaunchFailuresAsGuestExit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-limactl")
	old := execCommand
	execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command(missing)
	}
	t.Cleanup(func() { execCommand = old })

	err := Exec("watermelon-test-12345678", []string{"guest-command"})
	if err == nil {
		t.Fatal("Exec() unexpectedly succeeded")
	}
	if _, ok := err.(interface{ GuestExitCode() int }); ok {
		t.Fatalf("launch failure was marked as a guest exit: %v", err)
	}
}

func TestShellAndExecHonorExplicitWorkdir(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "shell custom",
			run:  func() error { return Shell("custom-dev", "/workspace") },
			want: "limactl shell --workdir /workspace custom-dev",
		},
		{
			name: "shell guest home",
			run:  func() error { return Shell("custom-dev", "") },
			want: "limactl shell custom-dev",
		},
		{
			name: "exec custom",
			run:  func() error { return Exec("custom-dev", []string{"docker", "ps"}, "/workspace") },
			want: "limactl shell --workdir /workspace custom-dev -- docker ps",
		},
		{
			name: "exec guest home",
			run:  func() error { return Exec("custom-dev", []string{"pwd"}, "") },
			want: "limactl shell custom-dev -- pwd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []string
			old := execCommand
			execCommand = fakeExecCommandCapture(&captured, "")
			t.Cleanup(func() { execCommand = old })
			if err := tt.run(); err != nil {
				t.Fatalf("lifecycle command error = %v", err)
			}
			if len(captured) != 1 || captured[0] != tt.want {
				t.Fatalf("captured = %v, want [%q]", captured, tt.want)
			}
		})
	}
}

func TestShellAndExecRejectMultipleWorkdirsWithoutCallingLima(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	if err := Shell("custom-dev", "/one", "/two"); err == nil {
		t.Fatal("Shell() accepted multiple workdirs")
	}
	if err := Exec("custom-dev", []string{"pwd"}, "/one", "/two"); err == nil {
		t.Fatal("Exec() accepted multiple workdirs")
	} else if _, ok := err.(interface{ GuestExitCode() int }); ok {
		t.Fatalf("workdir validation error was marked as a guest exit: %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("invalid workdirs invoked Lima: %v", captured)
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
