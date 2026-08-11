package lima

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  Version
	}{
		{input: "v2.0.0", want: Version{Major: 2}},
		{input: " 2.1.3 ", want: Version{Major: 2, Minor: 1, Patch: 3}},
		{
			input: "2.0.0-rc.1+build.7",
			want:  Version{Major: 2, Prerelease: "rc.1", Build: "build.7"},
		},
		{
			input: "18446744073709551615.0.0",
			want:  Version{Major: ^uint64(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseVersion(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ParseVersion(%q) = %+v, want %+v", test.input, got, test.want)
			}
			if reparsed, err := ParseVersion(got.String()); err != nil || !reflect.DeepEqual(reparsed, got) {
				t.Fatalf("ParseVersion(%q.String()) = (%+v, %v), want %+v", test.input, reparsed, err, got)
			}
		})
	}
}

func TestParseVersionRejectsMalformedValues(t *testing.T) {
	for _, input := range []string{
		"", "v", "2", "2.0", "2.0.0.1", "02.0.0", "2.00.0", "2.0.00",
		"2.x.0", "2.0.0-", "2.0.0+", "2.0.0-01", "2.0.0-a..b",
		"2.0.0+a+b", "2.0.0+build_1", "18446744073709551616.0.0",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseVersion(input); err == nil {
				t.Fatalf("ParseVersion(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestVersionComparisonUsesSemanticPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
	}
	for index := 0; index < len(ordered)-1; index++ {
		left, err := ParseVersion(ordered[index])
		if err != nil {
			t.Fatal(err)
		}
		right, err := ParseVersion(ordered[index+1])
		if err != nil {
			t.Fatal(err)
		}
		if got := left.Compare(right); got >= 0 {
			t.Fatalf("%s.Compare(%s) = %d, want negative", left, right, got)
		}
		if !right.AtLeast(left) {
			t.Fatalf("%s.AtLeast(%s) = false, want true", right, left)
		}
	}

	left, _ := ParseVersion("1.0.0-184467440737095516160")
	right, _ := ParseVersion("1.0.0-184467440737095516161")
	if left.Compare(right) >= 0 {
		t.Fatal("arbitrarily large numeric prerelease identifiers compared incorrectly")
	}
	buildOne, _ := ParseVersion("2.0.0+one")
	buildTwo, _ := ParseVersion("2.0.0+two")
	if got := buildOne.Compare(buildTwo); got != 0 {
		t.Fatalf("build metadata affected precedence: Compare() = %d", got)
	}
}

func TestInspectInstallationParsesLimaInfoAndUsesExactCommand(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, `{
		"version":"v2.1.3",
		"hostOS":"linux",
		"hostArch":"aarch64",
		"limaHome":"/host/.lima",
		"executablePath":"/untrusted/limactl",
		"qemuExecutablePath":"/untrusted/qemu",
		"qemuVersion":"untrusted",
		"vmTypes":["qemu"],
		"vmTypesEx":{"qemu":{"location":"internal"}},
		"guestAgents":{"aarch64":{"location":"/share/lima-guestagent.gz"}}
	}`)
	t.Cleanup(func() { execCommand = old })

	info, err := InspectInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if info.ExecutablePath == "" || info.Version != "v2.1.3" || info.HostOS != "linux" || info.HostArch != "aarch64" || info.LimaHome != "/host/.lima" {
		t.Fatalf("InspectInstallation() = %+v", info)
	}
	if info.ExecutablePath == "/untrusted/limactl" || info.QEMUExecutablePath != "" || info.QEMUVersion != "" {
		t.Fatalf("InspectInstallation() trusted executable paths from Lima JSON: %+v", info)
	}
	if !reflect.DeepEqual(info.VMTypes, []string{"qemu"}) || info.VMTypesEx["qemu"].Location != "internal" {
		t.Fatalf("InspectInstallation() VM types = %+v / %+v", info.VMTypes, info.VMTypesEx)
	}
	if info.GuestAgents["aarch64"].Location != "/share/lima-guestagent.gz" {
		t.Fatalf("InspectInstallation() guest agents = %+v", info.GuestAgents)
	}
	if len(captured) != 1 || captured[0] != "limactl info" {
		t.Fatalf("InspectInstallation() commands = %q, want [limactl info]", captured)
	}
}

func TestInspectInstallationKeepsStderrSeparateFromJSON(t *testing.T) {
	old := execCommand
	execCommand = fakeInfoCommand(`{"version":"v2.0.0"}`, "a harmless Lima warning", 0)
	t.Cleanup(func() { execCommand = old })

	info, err := InspectInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "v2.0.0" {
		t.Fatalf("InspectInstallation() version = %q, want v2.0.0", info.Version)
	}
}

func TestInspectInstallationReportsLookupExecutionAndDecodeErrors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		old := execCommand
		execCommand = exec.Command
		t.Cleanup(func() { execCommand = old })
		t.Setenv("PATH", t.TempDir())

		info, err := InspectInstallation()
		if err == nil || !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("InspectInstallation() = (%+v, %v), want exec.ErrNotFound", info, err)
		}
	})

	t.Run("nonzero with stderr", func(t *testing.T) {
		old := execCommand
		execCommand = fakeInfoCommand("", "backend initialization failed", 4)
		t.Cleanup(func() { execCommand = old })

		info, err := InspectInstallation()
		if err == nil || !strings.Contains(err.Error(), "backend initialization failed") {
			t.Fatalf("InspectInstallation() = (%+v, %v), want stderr context", info, err)
		}
		if info.ExecutablePath == "" {
			t.Fatal("InspectInstallation() discarded the selected executable path")
		}
	})

	t.Run("stderr is bounded", func(t *testing.T) {
		old := execCommand
		execCommand = fakeInfoCommand("", strings.Repeat("x", maxInspectionStderrBytes+1024), 1)
		t.Cleanup(func() { execCommand = old })

		_, err := InspectInstallation()
		if err == nil || !strings.Contains(err.Error(), "[truncated]") {
			t.Fatalf("InspectInstallation() error = %v, want truncation marker", err)
		}
		if len(err.Error()) > maxInspectionStderrBytes+256 {
			t.Fatalf("InspectInstallation() error length = %d, stderr was not bounded", len(err.Error()))
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		withFakeExec(t, "{", 0)
		info, err := InspectInstallation()
		if err == nil || !strings.Contains(err.Error(), "decoding Lima installation information") {
			t.Fatalf("InspectInstallation() = (%+v, %v), want decode error", info, err)
		}
		if info.ExecutablePath == "" {
			t.Fatal("InspectInstallation() discarded the selected executable path")
		}
	})
}

func TestCheckCompatibility(t *testing.T) {
	oldLookPath := execLookPath
	execLookPath = func(executable string) (string, error) { return "/resolved/" + executable, nil }
	t.Cleanup(func() { execLookPath = oldLookPath })
	t.Setenv("QEMU_SYSTEM_AARCH64", "")
	t.Setenv("QEMU_SYSTEM_X86_64", "")

	base := InstallationInfo{
		Version:  "v2.0.0",
		HostOS:   "linux",
		HostArch: "aarch64",
		VMTypes:  []string{"qemu"},
	}
	tests := []struct {
		name    string
		mutate  func(*InstallationInfo)
		wantErr string
	}{
		{name: "minimum"},
		{name: "newer", mutate: func(info *InstallationInfo) { info.Version = "v2.2.0" }},
		{
			name: "extended backend map",
			mutate: func(info *InstallationInfo) {
				info.HostOS = "darwin"
				info.HostArch = "x86_64"
				info.VMTypes = nil
				info.VMTypesEx = map[string]VMTypeInfo{"vz": {Location: "internal"}}
			},
		},
		{name: "too old", mutate: func(info *InstallationInfo) { info.Version = "v1.2.3" }, wantErr: "requires Lima 2.0.0 or newer"},
		{name: "minimum prerelease", mutate: func(info *InstallationInfo) { info.Version = "v2.0.0-rc.1" }, wantErr: "official stable Lima release"},
		{name: "newer prerelease", mutate: func(info *InstallationInfo) { info.Version = "v2.2.0-beta.1" }, wantErr: "official stable Lima release"},
		{name: "build metadata", mutate: func(info *InstallationInfo) { info.Version = "v2.0.0+local" }, wantErr: "official stable Lima release"},
		{name: "git describe", mutate: func(info *InstallationInfo) { info.Version = "v2.0.0-16-gabcdef.m" }, wantErr: "official stable Lima release"},
		{name: "dirty suffix", mutate: func(info *InstallationInfo) { info.Version = "v2.0.0.m" }, wantErr: "official stable Lima release"},
		{name: "missing version", mutate: func(info *InstallationInfo) { info.Version = "" }, wantErr: "official stable Lima release"},
		{name: "malformed version", mutate: func(info *InstallationInfo) { info.Version = "development" }, wantErr: "official stable Lima release"},
		{name: "missing architecture", mutate: func(info *InstallationInfo) { info.HostArch = "" }, wantErr: "did not report its host architecture"},
		{name: "unsupported architecture", mutate: func(info *InstallationInfo) { info.HostArch = "riscv64" }, wantErr: `unsupported Lima host architecture "riscv64"`},
		{name: "missing operating system", mutate: func(info *InstallationInfo) { info.HostOS = "" }, wantErr: "did not report its host operating system"},
		{name: "unsupported operating system", mutate: func(info *InstallationInfo) { info.HostOS = "freebsd" }, wantErr: `unsupported Lima host operating system "freebsd"`},
		{name: "missing backend", mutate: func(info *InstallationInfo) { info.VMTypes = nil }, wantErr: `does not provide the "qemu" VM backend`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := base
			if test.mutate != nil {
				test.mutate(&info)
			}
			err := CheckCompatibility(info)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("CheckCompatibility() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestCheckCompatibilityResolvesArchitectureAppropriateQEMU(t *testing.T) {
	for _, test := range []struct {
		hostArch       string
		wantExecutable string
	}{
		{hostArch: "aarch64", wantExecutable: "qemu-system-aarch64"},
		{hostArch: "arm64", wantExecutable: "qemu-system-aarch64"},
		{hostArch: "x86_64", wantExecutable: "qemu-system-x86_64"},
		{hostArch: "amd64", wantExecutable: "qemu-system-x86_64"},
	} {
		t.Run(test.hostArch, func(t *testing.T) {
			t.Setenv("QEMU_SYSTEM_AARCH64", "")
			t.Setenv("QEMU_SYSTEM_X86_64", "")
			var lookedUp string
			oldLookPath := execLookPath
			execLookPath = func(executable string) (string, error) {
				lookedUp = executable
				return "/usr/bin/" + executable, nil
			}
			t.Cleanup(func() { execLookPath = oldLookPath })

			info := InstallationInfo{
				Version:  "v2.0.0",
				HostOS:   "linux",
				HostArch: test.hostArch,
				VMTypes:  []string{"qemu"},
			}
			if err := CheckCompatibility(info); err != nil {
				t.Fatal(err)
			}
			if lookedUp != test.wantExecutable {
				t.Fatalf("QEMU lookup = %q, want %q", lookedUp, test.wantExecutable)
			}
		})
	}
}

func TestCheckCompatibilityReportsMissingQEMUActionably(t *testing.T) {
	t.Setenv("QEMU_SYSTEM_X86_64", "")
	oldLookPath := execLookPath
	execLookPath = func(executable string) (string, error) {
		return "", &exec.Error{Name: executable, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() { execLookPath = oldLookPath })

	err := CheckCompatibility(InstallationInfo{
		Version:  "v2.0.0",
		HostOS:   "linux",
		HostArch: "x86_64",
		VMTypes:  []string{"qemu"},
	})
	for _, want := range []string{"qemu-system-x86_64", "Linux/x86_64", "install QEMU", "QEMU_SYSTEM_X86_64"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("CheckCompatibility() error = %v, want containing %q", err, want)
		}
	}
}

func TestCheckCompatibilityUsesLimaQEMUOverride(t *testing.T) {
	t.Setenv("QEMU_SYSTEM_AARCH64", `"/opt/QEMU tools/qemu-system-aarch64" -display none`)
	var lookedUp string
	oldLookPath := execLookPath
	execLookPath = func(executable string) (string, error) {
		lookedUp = executable
		return "/resolved/qemu-system-aarch64", nil
	}
	t.Cleanup(func() { execLookPath = oldLookPath })

	err := CheckCompatibility(InstallationInfo{
		Version:  "v2.0.0",
		HostOS:   "linux",
		HostArch: "aarch64",
		VMTypes:  []string{"qemu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookedUp != "/opt/QEMU tools/qemu-system-aarch64" {
		t.Fatalf("QEMU override lookup = %q", lookedUp)
	}
}

func TestCheckCompatibilityRejectsMalformedLimaQEMUOverride(t *testing.T) {
	t.Setenv("QEMU_SYSTEM_AARCH64", `'unterminated`)
	oldLookPath := execLookPath
	execLookPath = func(string) (string, error) {
		t.Fatal("malformed override must fail before executable lookup")
		return "", nil
	}
	t.Cleanup(func() { execLookPath = oldLookPath })

	err := CheckCompatibility(InstallationInfo{
		Version:  "v2.0.0",
		HostOS:   "linux",
		HostArch: "aarch64",
		VMTypes:  []string{"qemu"},
	})
	if err == nil || !strings.Contains(err.Error(), "QEMU_SYSTEM_AARCH64") || !strings.Contains(err.Error(), "cannot be parsed") {
		t.Fatalf("CheckCompatibility() error = %v, want override parse error", err)
	}
}

func TestCheckCompatibilityDoesNotLookUpQEMUForVZ(t *testing.T) {
	oldLookPath := execLookPath
	execLookPath = func(string) (string, error) {
		t.Fatal("Darwin VZ compatibility must not require QEMU")
		return "", nil
	}
	t.Cleanup(func() { execLookPath = oldLookPath })

	err := CheckCompatibility(InstallationInfo{
		Version:  "v2.0.0",
		HostOS:   "darwin",
		HostArch: "aarch64",
		VMTypes:  []string{"vz"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInspectCompatibleInstallationReturnsInfoOnCompatibilityFailure(t *testing.T) {
	withFakeExec(t, `{"version":"v1.2.3","hostOS":"linux","hostArch":"aarch64","vmTypes":["qemu"]}`, 0)
	info, err := InspectCompatibleInstallation()
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("InspectCompatibleInstallation() error = %v, want too old", err)
	}
	if info.Version != "v1.2.3" || info.ExecutablePath == "" {
		t.Fatalf("InspectCompatibleInstallation() discarded info: %+v", info)
	}
}

func TestInspectCompatibleInstallationReturnsResolvedQEMUPath(t *testing.T) {
	t.Setenv("QEMU_SYSTEM_AARCH64", "")
	withFakeExec(t, `{"version":"v2.0.0","hostOS":"linux","hostArch":"aarch64","vmTypes":["qemu"]}`, 0)
	oldLookPath := execLookPath
	execLookPath = func(executable string) (string, error) {
		if executable != "qemu-system-aarch64" {
			t.Fatalf("QEMU lookup = %q", executable)
		}
		return "/opt/qemu/bin/qemu-system-aarch64", nil
	}
	t.Cleanup(func() { execLookPath = oldLookPath })
	oldQEMUCommand := qemuExecCommand
	qemuExecCommand = fakeInfoCommand("QEMU emulator version 9.2.0 (test build)\n", "", 0)
	t.Cleanup(func() { qemuExecCommand = oldQEMUCommand })

	info, err := InspectCompatibleInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if info.QEMUExecutablePath != "/opt/qemu/bin/qemu-system-aarch64" {
		t.Fatalf("InspectCompatibleInstallation() QEMU path = %q", info.QEMUExecutablePath)
	}
	if info.QEMUVersion != "9.2.0 (test build)" {
		t.Fatalf("InspectCompatibleInstallation() QEMU version = %q", info.QEMUVersion)
	}
}

func TestProbeQEMUVersionRequiresRunnableQEMUIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     string
		wantErr  string
	}{
		{name: "valid", stdout: "QEMU emulator version 9.2.0 (test build)\n", want: "9.2.0 (test build)"},
		{name: "wrong executable", stdout: "not qemu\n", wantErr: "did not identify a QEMU system emulator"},
		{name: "execution failure", stderr: "broken executable", exitCode: 1, wantErr: "broken executable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			old := qemuExecCommand
			qemuExecCommand = fakeInfoCommand(test.stdout, test.stderr, test.exitCode)
			t.Cleanup(func() { qemuExecCommand = old })

			got, err := probeQEMUVersion("/opt/qemu")
			if test.wantErr == "" {
				if err != nil || got != test.want {
					t.Fatalf("probeQEMUVersion() = (%q, %v), want (%q, nil)", got, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("probeQEMUVersion() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func fakeInfoCommand(stdout, stderr string, exitCode int) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestExecHelper", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_EXEC_HELPER=1",
			"GO_TEST_EXEC_OUTPUT="+stdout,
			"GO_TEST_EXEC_STDERR="+stderr,
			"GO_TEST_EXEC_EXIT="+strconv.Itoa(exitCode),
		)
		return cmd
	}
}
