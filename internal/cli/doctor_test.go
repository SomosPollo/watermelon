package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

func healthyDoctorDeps() doctorDeps {
	return doctorDeps{
		watermelonVersion: "v1.2.3",
		goos:              "linux",
		goarch:            "amd64",
		geteuid:           func() int { return 1000 },
		executable:        func() (string, error) { return "/opt/watermelon/bin/watermelon", nil },
		lookPath: func(name string) (string, error) {
			switch name {
			case "watermelon":
				return "/opt/watermelon/bin/watermelon", nil
			case "ssh":
				return "/usr/bin/ssh", nil
			default:
				return "", errors.New("unexpected executable lookup: " + name)
			}
		},
		evalSymlinks: func(path string) (string, error) { return path, nil },
		macOSVersion: func() (string, error) { return "14.6.1", nil },
		checkKVM:     func(string) error { return nil },
		inspectLima: func() (lima.InstallationInfo, error) {
			return lima.InstallationInfo{
				ExecutablePath:     "/usr/local/bin/limactl",
				QEMUExecutablePath: "/usr/bin/qemu-system-x86_64",
				QEMUVersion:        "9.2.0",
				Version:            "2.1.3",
				HostOS:             "linux",
				HostArch:           "x86_64",
				LimaHome:           "/home/test/.lima",
				VMTypes:            []string{"qemu"},
			}, nil
		},
		listLimaVMs: func() ([]lima.VMInfo, error) { return nil, nil },
	}
}

func executeDoctor(t *testing.T, deps doctorDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newDoctorCmd(deps)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

func TestDoctorTextReportIsDeterministicAndWarningsSucceed(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.checkKVM = func(path string) error {
		if path != "/dev/kvm" {
			t.Fatalf("KVM check path = %q, want /dev/kvm", path)
		}
		return os.ErrNotExist
	}

	cmd := newDoctorCmd(deps)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor warning result = %v, want success", err)
	}

	want := `Watermelon doctor
PASS platform: linux/amd64 is supported
PASS privileges: running as non-root user 1000
PASS watermelon: Watermelon v1.2.3 is running from /opt/watermelon/bin/watermelon
PASS path: PATH resolves to the running executable at /opt/watermelon/bin/watermelon
PASS lima: Lima 2.1.3 at /usr/local/bin/limactl is compatible (minimum 2.0.0); host linux/x86_64; home /home/test/.lima
PASS lima-state: Lima state store is readable (0 instances)
PASS vm-backend: QEMU 9.2.0 backend is available at /usr/bin/qemu-system-x86_64 (reported VM types: qemu)
PASS ssh: OpenSSH client found at /usr/bin/ssh
WARN acceleration: KVM acceleration is unavailable: file does not exist
  Fix: Enable access to /dev/kvm for faster VMs; Lima can fall back to QEMU TCG.
Summary: 8 passed, 1 warning, 0 failures, 0 skipped
`
	if output.String() != want {
		t.Fatalf("doctor output mismatch\n--- got ---\n%s--- want ---\n%s", output.String(), want)
	}
}

func TestDoctorAggregatesFailuresAndSkipsDependentChecks(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.geteuid = func() int { return 0 }
	deps.executable = func() (string, error) { return "", errors.New("executable unavailable") }
	inspectCalls := 0
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		inspectCalls++
		return lima.InstallationInfo{ExecutablePath: "/opt/lima/bin/limactl", Version: "1.2.3"}, errors.New("Lima 1.2.3 is too old")
	}
	listCalls := 0
	deps.listLimaVMs = func() ([]lima.VMInfo, error) {
		listCalls++
		return nil, nil
	}
	sshCalls := 0
	deps.lookPath = func(name string) (string, error) {
		if name == "ssh" {
			sshCalls++
		}
		return "", errors.New(name + " not found")
	}
	deps.checkKVM = func(string) error { return os.ErrNotExist }

	output, err := executeDoctor(t, deps)
	if err == nil || err.Error() != "doctor found 4 failing checks" {
		t.Fatalf("doctor error = %v, want four failing checks", err)
	}
	if inspectCalls != 1 || listCalls != 1 || sshCalls != 1 {
		t.Fatalf("independent checks were not aggregated: inspect=%d list=%d ssh=%d", inspectCalls, listCalls, sshCalls)
	}
	for _, want := range []string{
		"FAIL privileges:",
		"FAIL watermelon:",
		"SKIP path:",
		"FAIL lima:",
		"PASS lima-state:",
		"SKIP vm-backend:",
		"FAIL ssh:",
		"WARN acceleration:",
		"Summary: 2 passed, 1 warning, 4 failures, 2 skipped",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("doctor output missing %q:\n%s", want, output)
		}
	}
	assertOrderedSubstrings(t, output,
		"PASS platform:",
		"FAIL privileges:",
		"FAIL watermelon:",
		"SKIP path:",
		"FAIL lima:",
		"PASS lima-state:",
		"SKIP vm-backend:",
		"FAIL ssh:",
		"WARN acceleration:",
	)
}

func TestDoctorJSONReportHasStableSchemaAndOrderedChecks(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.checkKVM = func(string) error { return os.ErrNotExist }

	output, err := executeDoctor(t, deps, "--json")
	if err != nil {
		t.Fatalf("doctor --json warning result = %v, want success", err)
	}
	var report doctorReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("doctor --json output is invalid: %v\n%s", err, output)
	}
	if report.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", report.SchemaVersion)
	}
	if !report.OK {
		t.Errorf("ok = false with warning-only report: %+v", report.Summary)
	}
	wantNames := []string{"platform", "privileges", "watermelon", "path", "lima", "lima-state", "vm-backend", "ssh", "acceleration"}
	gotNames := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		gotNames = append(gotNames, check.Name)
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("check order = %v, want %v", gotNames, wantNames)
	}
	if report.Summary != (doctorSummary{Passed: 8, Warnings: 1}) {
		t.Errorf("summary = %+v, want eight passes and one warning", report.Summary)
	}
	if got := report.Checks[2]; got.Path != "/opt/watermelon/bin/watermelon" || got.Version != "v1.2.3" {
		t.Errorf("watermelon structured fields = %+v", got)
	}
	if got := report.Checks[4]; got.Path != "/usr/local/bin/limactl" || got.Version != "2.1.3" {
		t.Errorf("Lima structured fields = %+v", got)
	}
	if got := report.Checks[6]; got.Path != "/usr/bin/qemu-system-x86_64" || got.Version != "9.2.0" {
		t.Errorf("QEMU structured fields = %+v", got)
	}
}

func TestDoctorJSONFailureIsValidAndReturnsError(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{}, exec.ErrNotFound
	}

	output, err := executeDoctor(t, deps, "--json")
	if err == nil {
		t.Fatal("doctor --json succeeded despite failing Lima check")
	}
	var report doctorReport
	if unmarshalErr := json.Unmarshal([]byte(output), &report); unmarshalErr != nil {
		t.Fatalf("failing doctor JSON is invalid: %v\n%s", unmarshalErr, output)
	}
	if report.OK || report.Summary.Failed != 1 {
		t.Errorf("failing report = %+v", report)
	}
}

func TestDoctorUsesVZOnMacOSAndVMTypesEx(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.goos = "darwin"
	deps.goarch = "arm64"
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{
			ExecutablePath: "/opt/homebrew/bin/limactl",
			Version:        "2.2.0",
			HostOS:         "darwin",
			HostArch:       "aarch64",
			VMTypesEx:      map[string]lima.VMTypeInfo{"vz": {}},
		}, nil
	}

	report := collectDoctorReport(deps)
	backend := report.Checks[6]
	if backend.Status != doctorPass || !strings.Contains(backend.Message, "VZ backend") {
		t.Errorf("macOS backend check = %+v", backend)
	}
	acceleration := report.Checks[8]
	if acceleration.Status != doctorSkip || !strings.Contains(acceleration.Message, "macOS uses VZ") {
		t.Errorf("macOS acceleration check = %+v", acceleration)
	}
}

func TestDoctorRequiresMacOS13OrNewer(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.goos = "darwin"
	deps.goarch = "arm64"
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{
			ExecutablePath: "/opt/homebrew/bin/limactl",
			Version:        "2.2.0",
			HostOS:         "darwin",
			HostArch:       "aarch64",
			VMTypes:        []string{"vz"},
		}, nil
	}

	tests := []struct {
		name        string
		version     string
		versionErr  error
		wantStatus  doctorStatus
		wantMessage string
	}{
		{name: "minimum major", version: "13", wantStatus: doctorPass, wantMessage: "macOS 13"},
		{name: "current patch", version: "15.6.1\n", wantStatus: doctorPass, wantMessage: "macOS 15.6.1"},
		{name: "too old", version: "12.7.6", wantStatus: doctorFail, wantMessage: "requires macOS 13 or newer"},
		{name: "malformed", version: "13.beta", wantStatus: doctorFail, wantMessage: "could not parse"},
		{name: "unreadable", versionErr: errors.New("sw_vers failed"), wantStatus: doctorFail, wantMessage: "could not determine"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseDeps := deps
			caseDeps.macOSVersion = func() (string, error) { return test.version, test.versionErr }
			report := collectDoctorReport(caseDeps)
			platform := report.Checks[0]
			if platform.Status != test.wantStatus || !strings.Contains(platform.Message, test.wantMessage) {
				t.Errorf("platform check = %+v, want %s containing %q", platform, test.wantStatus, test.wantMessage)
			}
			if test.wantStatus == doctorFail {
				if !strings.Contains(platform.Remediation, "macOS 13 or newer") {
					t.Errorf("platform remediation = %q, want macOS 13 guidance", platform.Remediation)
				}
				if got := report.Checks[6]; got.Status != doctorSkip {
					t.Errorf("backend check = %+v, want skip after platform failure", got)
				}
			}
		})
	}
}

func TestDoctorDoesNotProbeMacOSVersionOnLinux(t *testing.T) {
	deps := healthyDoctorDeps()
	macOSCalls := 0
	deps.macOSVersion = func() (string, error) {
		macOSCalls++
		return "", errors.New("must not be called")
	}

	report := collectDoctorReport(deps)
	if macOSCalls != 0 {
		t.Fatalf("macOS version probe called %d times on Linux", macOSCalls)
	}
	if got := report.Checks[0]; got.Status != doctorPass || got.Message != "linux/amd64 is supported" {
		t.Errorf("Linux platform check = %+v", got)
	}
}

func TestParseMacOSProductVersionRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", ".13", "13.", "13..1", "013.0", "13.01", "13.0.1.2", "13beta", "9999999999.0"} {
		t.Run(value, func(t *testing.T) {
			if _, _, err := parseMacOSProductVersion(value); err == nil {
				t.Fatalf("parseMacOSProductVersion(%q) succeeded", value)
			}
		})
	}
}

func TestDoctorReportsPathShadowingAsWarning(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.lookPath = func(name string) (string, error) {
		if name == "watermelon" {
			return "/usr/bin/watermelon", nil
		}
		return "/usr/bin/ssh", nil
	}

	report := collectDoctorReport(deps)
	pathCheck := report.Checks[3]
	if pathCheck.Status != doctorWarn || !strings.Contains(pathCheck.Message, "/usr/bin/watermelon") {
		t.Errorf("PATH check = %+v", pathCheck)
	}
	if !report.OK {
		t.Errorf("PATH warning made report fail: %+v", report.Summary)
	}
}

func TestDoctorLimaStateReportsCountAndListErrors(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.listLimaVMs = func() ([]lima.VMInfo, error) {
		return []lima.VMInfo{{Name: "one"}, {Name: "two"}}, nil
	}
	report := collectDoctorReport(deps)
	state := report.Checks[5]
	if state.Status != doctorPass || !strings.Contains(state.Message, "2 instances") {
		t.Errorf("readable Lima state check = %+v", state)
	}

	deps.listLimaVMs = func() ([]lima.VMInfo, error) {
		return nil, errors.New("state metadata is corrupt")
	}
	report = collectDoctorReport(deps)
	state = report.Checks[5]
	if state.Status != doctorFail || !strings.Contains(state.Message, "state metadata is corrupt") {
		t.Errorf("unreadable Lima state check = %+v", state)
	}
	if report.OK || report.Summary.Failed != 1 {
		t.Errorf("state-list failure did not fail doctor: %+v", report.Summary)
	}
}

func TestDoctorLimaStateSkipsOnlyWhenLimactlIsMissing(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{}, errors.Join(errors.New("finding limactl"), exec.ErrNotFound)
	}
	listCalls := 0
	deps.listLimaVMs = func() ([]lima.VMInfo, error) {
		listCalls++
		return nil, nil
	}

	report := collectDoctorReport(deps)
	state := report.Checks[5]
	if state.Status != doctorSkip || !strings.Contains(state.Message, "requires limactl") {
		t.Errorf("missing-Lima state check = %+v", state)
	}
	if listCalls != 0 {
		t.Errorf("ListAllVMs called %d times despite missing limactl", listCalls)
	}
}

func TestDoctorRejectsHostOSMismatchButAcceptsRosettaArchitecture(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{
			ExecutablePath: "/usr/local/bin/limactl",
			Version:        "2.2.0",
			HostOS:         "darwin",
			HostArch:       "arm64",
			VMTypes:        []string{"vz"},
		}, nil
	}
	report := collectDoctorReport(deps)
	if got := report.Checks[4]; got.Status != doctorFail || !strings.Contains(got.Message, "reports host OS darwin") || !strings.Contains(got.Message, "running on linux") {
		t.Errorf("host-OS mismatch check = %+v", got)
	}

	deps.goos = "darwin"
	deps.goarch = "amd64" // A Rosetta CLI may inspect an arm64 Lima host.
	report = collectDoctorReport(deps)
	if got := report.Checks[4]; got.Status != doctorPass || !strings.Contains(got.Message, "darwin/arm64") {
		t.Errorf("Rosetta Lima check = %+v", got)
	}
	if !report.OK {
		t.Errorf("Rosetta architecture difference failed doctor: %+v", report.Summary)
	}
}

func TestDoctorKVMAccessFailureIsWarningOnly(t *testing.T) {
	deps := healthyDoctorDeps()
	checkedPath := ""
	deps.checkKVM = func(path string) error {
		checkedPath = path
		return os.ErrPermission
	}
	report := collectDoctorReport(deps)
	if checkedPath != "/dev/kvm" {
		t.Errorf("KVM access path = %q, want /dev/kvm", checkedPath)
	}
	check := report.Checks[8]
	if check.Status != doctorWarn || !strings.Contains(check.Message, "permission denied") {
		t.Errorf("KVM access check = %+v", check)
	}
	if !report.OK {
		t.Errorf("KVM warning failed doctor: %+v", report.Summary)
	}
}

func TestDoctorDoesNotReportQEMUReadyWhenExecutableResolutionFails(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{
			ExecutablePath:     "/usr/local/bin/limactl",
			QEMUExecutablePath: "/bin/false",
			Version:            "2.2.0",
			HostOS:             "linux",
			HostArch:           "x86_64",
			VMTypes:            []string{"qemu"},
		}, errors.New("running QEMU executable /bin/false --version failed")
	}

	report := collectDoctorReport(deps)
	if limaCheck := report.Checks[4]; limaCheck.Status != doctorFail || !strings.Contains(limaCheck.Remediation, "QEMU system emulator") {
		t.Fatalf("QEMU Lima check = %+v, want QEMU-specific remediation", limaCheck)
	}
	backend := report.Checks[6]
	if backend.Status != doctorSkip || !strings.Contains(backend.Message, "executable readiness") {
		t.Fatalf("QEMU backend check = %+v, want skipped readiness after compatibility failure", backend)
	}
	if report.OK || report.Summary.Failed != 1 {
		t.Fatalf("QEMU compatibility failure report = %+v", report.Summary)
	}
}

func TestDoctorHasExplicitNoArgumentContract(t *testing.T) {
	cmd := newDoctorCmd(healthyDoctorDeps())
	if cmd.Args == nil {
		t.Fatal("doctor command has no explicit argument contract")
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("doctor rejected no arguments: %v", err)
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Fatal("doctor accepted an unexpected argument")
	}
}

func TestDoctorLinuxLimaRemediationIsActionable(t *testing.T) {
	deps := healthyDoctorDeps()
	deps.inspectLima = func() (lima.InstallationInfo, error) {
		return lima.InstallationInfo{}, errors.New("limactl not found")
	}
	report := collectDoctorReport(deps)
	if got := report.Checks[4].Remediation; !strings.Contains(got, lima.MinimumSupportedVersion) || !strings.Contains(got, "lima-vm.io/docs/installation") {
		t.Errorf("Linux Lima remediation = %q", got)
	}

	deps.goos = "darwin"
	report = collectDoctorReport(deps)
	if got := report.Checks[4].Remediation; !strings.Contains(got, "brew install lima") {
		t.Errorf("macOS Lima remediation = %q", got)
	}
}

func assertOrderedSubstrings(t *testing.T, value string, parts ...string) {
	t.Helper()
	remaining := value
	for _, part := range parts {
		index := strings.Index(remaining, part)
		if index < 0 {
			t.Fatalf("%q does not appear in order in:\n%s", part, value)
		}
		remaining = remaining[index+len(part):]
	}
}
