package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

const (
	doctorSchemaVersion        = 1
	minimumSupportedMacOSMajor = 13
)

type doctorStatus string

const (
	doctorPass doctorStatus = "PASS"
	doctorWarn doctorStatus = "WARN"
	doctorFail doctorStatus = "FAIL"
	doctorSkip doctorStatus = "SKIP"
)

type doctorCheck struct {
	Name        string       `json:"name"`
	Status      doctorStatus `json:"status"`
	Message     string       `json:"message"`
	Path        string       `json:"path,omitempty"`
	Version     string       `json:"version,omitempty"`
	Remediation string       `json:"remediation,omitempty"`
}

type doctorSummary struct {
	Passed   int `json:"passed"`
	Warnings int `json:"warnings"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
}

type doctorReport struct {
	SchemaVersion int           `json:"schemaVersion"`
	OK            bool          `json:"ok"`
	Checks        []doctorCheck `json:"checks"`
	Summary       doctorSummary `json:"summary"`
}

// doctorDeps keeps doctor read-only and makes every host observation
// replaceable in unit tests. None of these operations load project state or
// create, start, stop, or modify a Lima instance.
type doctorDeps struct {
	watermelonVersion string
	goos              string
	goarch            string
	geteuid           func() int
	executable        func() (string, error)
	lookPath          func(string) (string, error)
	evalSymlinks      func(string) (string, error)
	macOSVersion      func() (string, error)
	checkKVM          func(string) error
	inspectLima       func() (lima.InstallationInfo, error)
	listLimaVMs       func() ([]lima.VMInfo, error)
}

// NewDoctorCmd creates the global, project-independent environment diagnostic.
func NewDoctorCmd(version string) *cobra.Command {
	return newDoctorCmd(doctorDeps{
		watermelonVersion: version,
		goos:              runtime.GOOS,
		goarch:            runtime.GOARCH,
		geteuid:           os.Geteuid,
		executable:        os.Executable,
		lookPath:          exec.LookPath,
		evalSymlinks:      filepath.EvalSymlinks,
		macOSVersion:      readMacOSProductVersion,
		checkKVM:          checkKVMReadWrite,
		inspectLima:       lima.InspectCompatibleInstallation,
		listLimaVMs:       lima.ListAllVMs,
	})
}

func newDoctorCmd(deps doctorDeps) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check whether Watermelon and Lima are ready to use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := collectDoctorReport(deps)
			if err := writeDoctorReport(cmd.OutOrStdout(), report, jsonOutput); err != nil {
				return err
			}
			if !report.OK {
				return fmt.Errorf("doctor found %s", countLabel(report.Summary.Failed, "failing check", "failing checks"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print a machine-readable diagnostic report")
	return cmd
}

func collectDoctorReport(deps doctorDeps) doctorReport {
	checks := make([]doctorCheck, 0, 9)

	platformCheck, platformSupported := doctorPlatformCheck(deps)
	checks = append(checks, platformCheck)

	euid := deps.geteuid()
	switch {
	case euid == 0:
		checks = append(checks, doctorCheck{
			Name:        "privileges",
			Status:      doctorFail,
			Message:     "Watermelon and Lima must not run as root",
			Remediation: "Run Watermelon as your normal user without sudo.",
		})
	case euid > 0:
		checks = append(checks, doctorCheck{
			Name:    "privileges",
			Status:  doctorPass,
			Message: fmt.Sprintf("running as non-root user %d", euid),
		})
	default:
		checks = append(checks, doctorCheck{
			Name:        "privileges",
			Status:      doctorWarn,
			Message:     "could not determine the effective user ID",
			Remediation: "Confirm that Watermelon is running as your normal user without sudo.",
		})
	}

	executablePath, executableErr := deps.executable()
	version := strings.TrimSpace(deps.watermelonVersion)
	if version == "" {
		version = "unknown"
	}
	if executableErr != nil {
		checks = append(checks, doctorCheck{
			Name:        "watermelon",
			Status:      doctorFail,
			Message:     fmt.Sprintf("could not resolve the running Watermelon executable: %v", executableErr),
			Version:     version,
			Remediation: "Reinstall Watermelon and run the installed binary by its exact path.",
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:    "watermelon",
			Status:  doctorPass,
			Message: fmt.Sprintf("Watermelon %s is running from %s", version, executablePath),
			Path:    executablePath,
			Version: version,
		})
	}

	if executableErr != nil {
		checks = append(checks, doctorCheck{
			Name:    "path",
			Status:  doctorSkip,
			Message: "PATH comparison requires the running executable path",
		})
	} else if pathExecutable, err := deps.lookPath("watermelon"); err != nil {
		checks = append(checks, doctorCheck{
			Name:        "path",
			Status:      doctorWarn,
			Message:     fmt.Sprintf("watermelon is not discoverable through PATH: %v", err),
			Path:        executablePath,
			Remediation: fmt.Sprintf("Add %s to PATH.", filepath.Dir(executablePath)),
		})
	} else if sameDoctorPath(deps, executablePath, pathExecutable) {
		checks = append(checks, doctorCheck{
			Name:    "path",
			Status:  doctorPass,
			Message: fmt.Sprintf("PATH resolves to the running executable at %s", pathExecutable),
			Path:    pathExecutable,
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:        "path",
			Status:      doctorWarn,
			Message:     fmt.Sprintf("PATH resolves watermelon to %s instead of the running executable %s", pathExecutable, executablePath),
			Path:        pathExecutable,
			Remediation: "Remove the shadowing executable or put the intended install directory earlier in PATH.",
		})
	}

	installation, limaErr := deps.inspectLima()
	hostOSMismatch := installation.HostOS != "" && installation.HostOS != deps.goos
	if hostOSMismatch {
		message := fmt.Sprintf("Lima reports host OS %s, but Watermelon is running on %s", installation.HostOS, deps.goos)
		if limaErr != nil {
			message += fmt.Sprintf("; Lima compatibility also failed: %v", limaErr)
		}
		checks = append(checks, doctorCheck{
			Name:        "lima",
			Status:      doctorFail,
			Message:     message,
			Path:        installation.ExecutablePath,
			Version:     installation.Version,
			Remediation: "Ensure watermelon and limactl run locally on the same host and are not shadowed by wrappers in PATH.",
		})
	} else if limaErr != nil {
		message := fmt.Sprintf("Lima is not ready: %v", limaErr)
		if installation.ExecutablePath != "" || installation.Version != "" {
			message = fmt.Sprintf("Lima %s at %s is not ready: %v",
				valueOrUnknown(installation.Version), valueOrUnknown(installation.ExecutablePath), limaErr)
		}
		checks = append(checks, doctorCheck{
			Name:        "lima",
			Status:      doctorFail,
			Message:     message,
			Path:        installation.ExecutablePath,
			Version:     installation.Version,
			Remediation: doctorLimaFailureRemediation(deps.goos, limaErr),
		})
	} else {
		message := fmt.Sprintf("Lima %s at %s is compatible (minimum %s)",
			installation.Version, installation.ExecutablePath, lima.MinimumSupportedVersion)
		host := strings.Trim(strings.Join([]string{installation.HostOS, installation.HostArch}, "/"), "/")
		if host != "" {
			message += "; host " + host
		}
		if installation.LimaHome != "" {
			message += "; home " + installation.LimaHome
		}
		checks = append(checks, doctorCheck{
			Name:    "lima",
			Status:  doctorPass,
			Message: message,
			Path:    installation.ExecutablePath,
			Version: installation.Version,
		})
	}

	if errors.Is(limaErr, exec.ErrNotFound) {
		checks = append(checks, doctorCheck{
			Name:    "lima-state",
			Status:  doctorSkip,
			Message: "Lima state-store check requires limactl",
		})
	} else if vms, err := deps.listLimaVMs(); errors.Is(err, exec.ErrNotFound) {
		checks = append(checks, doctorCheck{
			Name:    "lima-state",
			Status:  doctorSkip,
			Message: "Lima state-store check requires limactl",
		})
	} else if err != nil {
		checks = append(checks, doctorCheck{
			Name:        "lima-state",
			Status:      doctorFail,
			Message:     fmt.Sprintf("Lima state store is not readable: %v", err),
			Remediation: "Check the permissions and integrity of the Lima home reported above, then rerun watermelon doctor.",
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:    "lima-state",
			Status:  doctorPass,
			Message: fmt.Sprintf("Lima state store is readable (%s)", countLabel(len(vms), "instance", "instances")),
		})
	}

	checks = append(checks, doctorBackendCheck(deps.goos, platformSupported, installation, limaErr))

	if sshPath, err := deps.lookPath("ssh"); err != nil {
		checks = append(checks, doctorCheck{
			Name:        "ssh",
			Status:      doctorFail,
			Message:     fmt.Sprintf("OpenSSH client was not found in PATH: %v", err),
			Remediation: "Install an OpenSSH client and ensure ssh is available in PATH.",
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:    "ssh",
			Status:  doctorPass,
			Message: fmt.Sprintf("OpenSSH client found at %s", sshPath),
			Path:    sshPath,
		})
	}

	switch deps.goos {
	case "linux":
		if err := deps.checkKVM("/dev/kvm"); err != nil {
			checks = append(checks, doctorCheck{
				Name:        "acceleration",
				Status:      doctorWarn,
				Message:     fmt.Sprintf("KVM acceleration is unavailable: %v", err),
				Path:        "/dev/kvm",
				Remediation: "Enable access to /dev/kvm for faster VMs; Lima can fall back to QEMU TCG.",
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:    "acceleration",
				Status:  doctorPass,
				Message: "KVM acceleration device is available",
				Path:    "/dev/kvm",
			})
		}
	case "darwin":
		checks = append(checks, doctorCheck{
			Name:    "acceleration",
			Status:  doctorSkip,
			Message: "KVM acceleration applies only to Linux hosts; macOS uses VZ",
		})
	default:
		checks = append(checks, doctorCheck{
			Name:    "acceleration",
			Status:  doctorSkip,
			Message: "acceleration check is unavailable on this host platform",
		})
	}

	report := doctorReport{
		SchemaVersion: doctorSchemaVersion,
		Checks:        checks,
	}
	for _, check := range checks {
		switch check.Status {
		case doctorPass:
			report.Summary.Passed++
		case doctorWarn:
			report.Summary.Warnings++
		case doctorFail:
			report.Summary.Failed++
		case doctorSkip:
			report.Summary.Skipped++
		}
	}
	report.OK = report.Summary.Failed == 0
	return report
}

func supportedDoctorPlatform(goos, goarch string) bool {
	if goos != "darwin" && goos != "linux" {
		return false
	}
	return goarch == "amd64" || goarch == "arm64"
}

func doctorPlatformCheck(deps doctorDeps) (doctorCheck, bool) {
	if !supportedDoctorPlatform(deps.goos, deps.goarch) {
		return doctorCheck{
			Name:        "platform",
			Status:      doctorFail,
			Message:     fmt.Sprintf("%s/%s is not supported", deps.goos, deps.goarch),
			Remediation: fmt.Sprintf("Use macOS %d or newer, or Linux, on an amd64 or arm64 host.", minimumSupportedMacOSMajor),
		}, false
	}

	if deps.goos != "darwin" {
		return doctorCheck{
			Name:    "platform",
			Status:  doctorPass,
			Message: fmt.Sprintf("%s/%s is supported", deps.goos, deps.goarch),
		}, true
	}

	if deps.macOSVersion == nil {
		return doctorCheck{
			Name:        "platform",
			Status:      doctorFail,
			Message:     "could not determine the macOS version: version probe is unavailable",
			Remediation: fmt.Sprintf("Run 'sw_vers -productVersion' and upgrade this Mac to macOS %d or newer.", minimumSupportedMacOSMajor),
		}, false
	}
	versionOutput, err := deps.macOSVersion()
	if err != nil {
		return doctorCheck{
			Name:        "platform",
			Status:      doctorFail,
			Message:     fmt.Sprintf("could not determine the macOS version: %v", err),
			Remediation: fmt.Sprintf("Run 'sw_vers -productVersion' and upgrade this Mac to macOS %d or newer.", minimumSupportedMacOSMajor),
		}, false
	}
	version, major, err := parseMacOSProductVersion(versionOutput)
	if err != nil {
		return doctorCheck{
			Name:        "platform",
			Status:      doctorFail,
			Message:     fmt.Sprintf("could not parse the macOS product version %q: %v", strings.TrimSpace(versionOutput), err),
			Remediation: fmt.Sprintf("Run 'sw_vers -productVersion' and ensure it reports macOS %d or newer.", minimumSupportedMacOSMajor),
		}, false
	}
	if major < minimumSupportedMacOSMajor {
		return doctorCheck{
			Name:        "platform",
			Status:      doctorFail,
			Message:     fmt.Sprintf("macOS %s on darwin/%s is unsupported; Watermelon requires macOS %d or newer", version, deps.goarch, minimumSupportedMacOSMajor),
			Remediation: fmt.Sprintf("Upgrade this Mac to macOS %d or newer.", minimumSupportedMacOSMajor),
		}, false
	}
	return doctorCheck{
		Name:    "platform",
		Status:  doctorPass,
		Message: fmt.Sprintf("darwin/%s is supported (macOS %s)", deps.goarch, version),
	}, true
}

func readMacOSProductVersion() (string, error) {
	output, err := exec.Command("sw_vers", "-productVersion").CombinedOutput()
	version := strings.TrimSpace(string(output))
	if err != nil {
		if version != "" {
			return "", fmt.Errorf("running sw_vers -productVersion: %w: %s", err, version)
		}
		return "", fmt.Errorf("running sw_vers -productVersion: %w", err)
	}
	return version, nil
}

func parseMacOSProductVersion(output string) (string, int, error) {
	version := strings.TrimSpace(output)
	parts := strings.Split(version, ".")
	if version == "" || len(parts) > 3 {
		return "", 0, errors.New("expected one to three numeric components")
	}
	for _, part := range parts {
		if part == "" {
			return "", 0, errors.New("version components must not be empty")
		}
		if len(part) > 1 && part[0] == '0' {
			return "", 0, errors.New("version components must not contain leading zeroes")
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", 0, errors.New("version components must be numeric")
			}
		}
	}
	major, err := strconv.ParseUint(parts[0], 10, 31)
	if err != nil {
		return "", 0, fmt.Errorf("invalid major version: %w", err)
	}
	return version, int(major), nil
}

// checkKVMReadWrite verifies the access mode QEMU needs without issuing any
// ioctl or writing data to the device.
func checkKVMReadWrite(path string) error {
	device, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := device.Close(); err != nil {
		return fmt.Errorf("closing KVM device: %w", err)
	}
	return nil
}

func doctorBackendCheck(goos string, platformSupported bool, info lima.InstallationInfo, limaErr error) doctorCheck {
	if !platformSupported {
		return doctorCheck{
			Name:    "vm-backend",
			Status:  doctorSkip,
			Message: "VM backend selection requires a supported host platform",
		}
	}

	required, display := "qemu", "QEMU"
	if goos == "darwin" {
		required, display = "vz", "VZ"
	}
	vmTypes := doctorVMTypes(info)
	if len(vmTypes) == 0 && limaErr != nil {
		return doctorCheck{
			Name:    "vm-backend",
			Status:  doctorSkip,
			Message: fmt.Sprintf("%s availability requires readable Lima host information", display),
		}
	}
	for _, vmType := range vmTypes {
		if vmType == required {
			if goos == "linux" {
				if info.QEMUExecutablePath == "" || info.QEMUVersion == "" {
					if limaErr != nil {
						return doctorCheck{
							Name:    "vm-backend",
							Status:  doctorSkip,
							Message: fmt.Sprintf("QEMU driver is registered, but executable readiness depends on the failed Lima compatibility check: %v", limaErr),
						}
					}
					return doctorCheck{
						Name:        "vm-backend",
						Status:      doctorFail,
						Message:     "QEMU driver is registered, but no runnable architecture-appropriate QEMU executable was verified",
						Remediation: doctorLimaRemediation(goos),
					}
				}
				return doctorCheck{
					Name:    "vm-backend",
					Status:  doctorPass,
					Message: fmt.Sprintf("QEMU %s backend is available at %s (reported VM types: %s)", info.QEMUVersion, info.QEMUExecutablePath, strings.Join(vmTypes, ", ")),
					Path:    info.QEMUExecutablePath,
					Version: info.QEMUVersion,
				}
			}
			return doctorCheck{
				Name:    "vm-backend",
				Status:  doctorPass,
				Message: fmt.Sprintf("%s backend is available (reported VM types: %s)", display, strings.Join(vmTypes, ", ")),
			}
		}
	}
	return doctorCheck{
		Name:        "vm-backend",
		Status:      doctorFail,
		Message:     fmt.Sprintf("required %s backend is unavailable (reported VM types: %s)", display, valueOrUnknown(strings.Join(vmTypes, ", "))),
		Remediation: doctorLimaRemediation(goos),
	}
}

func doctorVMTypes(info lima.InstallationInfo) []string {
	seen := make(map[string]struct{}, len(info.VMTypes)+len(info.VMTypesEx))
	for _, vmType := range info.VMTypes {
		vmType = strings.ToLower(strings.TrimSpace(vmType))
		if vmType != "" {
			seen[vmType] = struct{}{}
		}
	}
	for vmType := range info.VMTypesEx {
		vmType = strings.ToLower(strings.TrimSpace(vmType))
		if vmType != "" {
			seen[vmType] = struct{}{}
		}
	}
	vmTypes := make([]string, 0, len(seen))
	for vmType := range seen {
		vmTypes = append(vmTypes, vmType)
	}
	sort.Strings(vmTypes)
	return vmTypes
}

func sameDoctorPath(deps doctorDeps, first, second string) bool {
	resolvedFirst, firstErr := deps.evalSymlinks(first)
	resolvedSecond, secondErr := deps.evalSymlinks(second)
	if firstErr == nil && secondErr == nil {
		return filepath.Clean(resolvedFirst) == filepath.Clean(resolvedSecond)
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

func doctorLimaRemediation(goos string) string {
	switch goos {
	case "darwin":
		return fmt.Sprintf("Install or upgrade Lima %s or newer with brew install lima (or brew upgrade lima).", lima.MinimumSupportedVersion)
	case "linux":
		return fmt.Sprintf("Install or upgrade Lima %s or newer: https://lima-vm.io/docs/installation/.", lima.MinimumSupportedVersion)
	default:
		return fmt.Sprintf("Install or upgrade Lima %s or newer on a supported host.", lima.MinimumSupportedVersion)
	}
}

func doctorLimaFailureRemediation(goos string, err error) string {
	if goos == "linux" && err != nil && strings.Contains(err.Error(), "QEMU") {
		return "Install the architecture-appropriate QEMU system emulator, or correct QEMU_SYSTEM_AARCH64/QEMU_SYSTEM_X86_64 using Lima shell-word syntax."
	}
	return doctorLimaRemediation(goos)
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func writeDoctorReport(out io.Writer, report doctorReport, jsonOutput bool) error {
	if jsonOutput {
		if err := json.NewEncoder(out).Encode(report); err != nil {
			return fmt.Errorf("writing doctor JSON report: %w", err)
		}
		return nil
	}

	fmt.Fprintln(out, "Watermelon doctor")
	for _, check := range report.Checks {
		fmt.Fprintf(out, "%s %s: %s\n", check.Status, check.Name, check.Message)
		if check.Remediation != "" {
			fmt.Fprintf(out, "  Fix: %s\n", check.Remediation)
		}
	}
	fmt.Fprintf(out, "Summary: %d passed, %s, %s, %d skipped\n",
		report.Summary.Passed,
		countLabel(report.Summary.Warnings, "warning", "warnings"),
		countLabel(report.Summary.Failed, "failure", "failures"),
		report.Summary.Skipped,
	)
	return nil
}
