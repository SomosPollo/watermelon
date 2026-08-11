package lima

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-shellwords"
)

// MinimumSupportedVersion is the oldest Lima release whose information and
// configuration interfaces provide everything Watermelon relies on.
const MinimumSupportedVersion = "2.0.0"

const maxInspectionStderrBytes = 4 * 1024

// VMTypeInfo describes one VM backend reported by limactl info. Lima may add
// fields to this object; unknown fields are intentionally ignored.
type VMTypeInfo struct {
	Location string `json:"location,omitempty"`
}

// GuestAgentInfo describes one guest agent reported by limactl info. Lima may
// add fields to this object; unknown fields are intentionally ignored.
type GuestAgentInfo struct {
	Location string `json:"location,omitempty"`
}

// InstallationInfo contains the subset of limactl info used by Watermelon plus
// exact executable paths and QEMU identity resolved locally during inspection.
type InstallationInfo struct {
	ExecutablePath     string                    `json:"executablePath"`
	QEMUExecutablePath string                    `json:"qemuExecutablePath,omitempty"`
	QEMUVersion        string                    `json:"qemuVersion,omitempty"`
	Version            string                    `json:"version"`
	HostOS             string                    `json:"hostOS"`
	HostArch           string                    `json:"hostArch"`
	LimaHome           string                    `json:"limaHome"`
	VMTypes            []string                  `json:"vmTypes,omitempty"`
	VMTypesEx          map[string]VMTypeInfo     `json:"vmTypesEx,omitempty"`
	GuestAgents        map[string]GuestAgentInfo `json:"guestAgents,omitempty"`
}

// Version is a parsed semantic version. Build metadata does not participate
// in precedence comparisons.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease string
	Build      string
}

// ParseVersion parses a semantic version, accepting the conventional leading
// "v" emitted by Lima.
func ParseVersion(input string) (Version, error) {
	original := input
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "v") {
		input = strings.TrimPrefix(input, "v")
	}
	if input == "" {
		return Version{}, fmt.Errorf("invalid semantic version %q: version is empty", original)
	}

	var build string
	if before, after, ok := strings.Cut(input, "+"); ok {
		input, build = before, after
		if err := validateIdentifiers(build, false); err != nil {
			return Version{}, fmt.Errorf("invalid semantic version %q: build metadata %w", original, err)
		}
		if strings.Contains(build, "+") {
			return Version{}, fmt.Errorf("invalid semantic version %q: build metadata contains '+'", original)
		}
	}

	var prerelease string
	if before, after, ok := strings.Cut(input, "-"); ok {
		input, prerelease = before, after
		if err := validateIdentifiers(prerelease, true); err != nil {
			return Version{}, fmt.Errorf("invalid semantic version %q: prerelease %w", original, err)
		}
	}

	parts := strings.Split(input, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid semantic version %q: want major.minor.patch", original)
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		if !isDigits(part) || len(part) > 1 && part[0] == '0' {
			return Version{}, fmt.Errorf("invalid semantic version %q: invalid numeric component %q", original, part)
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("invalid semantic version %q: %w", original, err)
		}
		values[index] = value
	}

	return Version{
		Major:      values[0],
		Minor:      values[1],
		Patch:      values[2],
		Prerelease: prerelease,
		Build:      build,
	}, nil
}

func validateIdentifiers(value string, rejectNumericLeadingZero bool) error {
	if value == "" {
		return fmt.Errorf("is empty")
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return fmt.Errorf("contains an empty identifier")
		}
		for _, char := range identifier {
			if !(char >= '0' && char <= '9') && !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') && char != '-' {
				return fmt.Errorf("identifier %q contains an invalid character", identifier)
			}
		}
		if rejectNumericLeadingZero && len(identifier) > 1 && identifier[0] == '0' && isDigits(identifier) {
			return fmt.Errorf("numeric identifier %q has a leading zero", identifier)
		}
	}
	return nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// String returns the canonical semantic version without a leading "v".
func (v Version) String() string {
	result := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		result += "-" + v.Prerelease
	}
	if v.Build != "" {
		result += "+" + v.Build
	}
	return result
}

// Compare reports -1, 0, or 1 when v has lower, equal, or higher semantic
// precedence than other.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]uint64{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return comparePrerelease(v.Prerelease, other.Prerelease)
}

// AtLeast reports whether v has equal or higher semantic precedence than the
// minimum version.
func (v Version) AtLeast(minimum Version) bool {
	return v.Compare(minimum) >= 0
}

func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftPart, rightPart := leftParts[index], rightParts[index]
		if leftPart == rightPart {
			continue
		}
		leftNumeric, rightNumeric := isDigits(leftPart), isDigits(rightPart)
		switch {
		case leftNumeric && rightNumeric:
			// Semantic version numeric prerelease identifiers are not size
			// limited. Leading zeroes are rejected during parsing, so length
			// followed by lexical order compares them without overflow.
			if len(leftPart) < len(rightPart) {
				return -1
			}
			if len(leftPart) > len(rightPart) {
				return 1
			}
			if leftPart < rightPart {
				return -1
			}
			return 1
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case leftPart < rightPart:
			return -1
		default:
			return 1
		}
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}
	return 1
}

type decodeInstallationInfoError struct {
	err error
}

func (e *decodeInstallationInfoError) Error() string {
	return fmt.Sprintf("decoding Lima installation information: %v", e.err)
}

func (e *decodeInstallationInfoError) Unwrap() error { return e.err }

type boundedErrorBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *boundedErrorBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := maxInspectionStderrBytes - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLength > 0
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *boundedErrorBuffer) String() string {
	message := strings.ToValidUTF8(strings.TrimSpace(b.buffer.String()), "?")
	if b.truncated {
		if message != "" {
			message += " "
		}
		message += "[truncated]"
	}
	return message
}

// InspectInstallation reads the installation metadata reported by Lima. It
// does not reject old versions or unavailable VM backends; callers that need
// to run a Watermelon workload should additionally call CheckCompatibility.
func InspectInstallation() (InstallationInfo, error) {
	cmd := execCommand("limactl", "info")
	info := InstallationInfo{ExecutablePath: cmd.Path}
	if cmd.Err != nil {
		return info, fmt.Errorf("finding Lima executable %q: %w", cmd.Path, cmd.Err)
	}

	var stdout bytes.Buffer
	var stderr boundedErrorBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := stderr.String()
		if message == "" {
			return info, fmt.Errorf("running %q info: %w", cmd.Path, err)
		}
		return info, fmt.Errorf("running %q info: %w: %s", cmd.Path, err, message)
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return info, &decodeInstallationInfoError{err: err}
	}
	// Executable paths are local inspection data, not Lima-provided JSON. Clear
	// any identically named fields before adding paths resolved by Watermelon.
	info.ExecutablePath = cmd.Path
	info.QEMUExecutablePath = ""
	info.QEMUVersion = ""
	return info, nil
}

// CheckCompatibility verifies the static Lima, host, and backend contract. Use
// InspectCompatibleInstallation when the QEMU executable must also be probed.
func CheckCompatibility(info InstallationInfo) error {
	return checkCompatibility(&info)
}

func checkCompatibility(info *InstallationInfo) error {
	version, err := ParseVersion(info.Version)
	if err != nil {
		return unsupportedLimaReleaseError(info.Version)
	}
	if version.Prerelease != "" || version.Build != "" || !hasStableReleaseSyntax(info.Version) {
		return unsupportedLimaReleaseError(info.Version)
	}
	minimum, err := ParseVersion(MinimumSupportedVersion)
	if err != nil {
		panic("invalid internal minimum Lima version: " + err.Error())
	}
	if !version.AtLeast(minimum) {
		return fmt.Errorf("Lima %s is too old; Watermelon requires Lima %s or newer", version, MinimumSupportedVersion)
	}

	if _, err := goHostArchitecture(info.HostArch); err != nil {
		return err
	}

	var requiredVMType string
	switch info.HostOS {
	case "darwin":
		requiredVMType = "vz"
	case "linux":
		requiredVMType = "qemu"
	case "":
		return fmt.Errorf("Lima did not report its host operating system")
	default:
		return fmt.Errorf("unsupported Lima host operating system %q", info.HostOS)
	}
	if !hasVMType(*info, requiredVMType) {
		return fmt.Errorf("Lima does not provide the %q VM backend required on %s", requiredVMType, info.HostOS)
	}
	info.QEMUExecutablePath = ""
	if info.HostOS == "linux" {
		qemuPath, err := resolveQEMUExecutable(info.HostArch)
		if err != nil {
			return err
		}
		info.QEMUExecutablePath = qemuPath
	}
	return nil
}

func unsupportedLimaReleaseError(version string) error {
	return fmt.Errorf("Lima reported non-release version %q; install an official stable Lima release in vMAJOR.MINOR.PATCH form (%s or newer)", version, MinimumSupportedVersion)
}

func hasStableReleaseSyntax(input string) bool {
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "v")
	parts := strings.Split(input, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !isDigits(part) || len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func resolveQEMUExecutable(hostArch string) (string, error) {
	var qemuArch string
	switch hostArch {
	case "aarch64", "arm64":
		qemuArch = "aarch64"
	case "x86_64", "amd64":
		qemuArch = "x86_64"
	default:
		// checkCompatibility reports the more specific architecture error
		// before reaching this function.
		return "", fmt.Errorf("cannot select QEMU for Lima host architecture %q", hostArch)
	}

	executable := "qemu-system-" + qemuArch
	overrideName := "QEMU_SYSTEM_" + strings.ToUpper(qemuArch)
	if override := os.Getenv(overrideName); override != "" {
		words, err := shellwords.Parse(override)
		if err != nil {
			return "", fmt.Errorf("Lima %s override %q cannot be parsed: %w", overrideName, override, err)
		}
		if len(words) == 0 || words[0] == "" {
			return "", fmt.Errorf("Lima %s override does not name a QEMU executable", overrideName)
		}
		// Lima permits trailing arguments here for debugging. Watermelon only
		// needs to resolve the same first shell word Lima will execute.
		executable = words[0]
	}

	path, err := execLookPath(executable)
	if err != nil {
		return "", fmt.Errorf("QEMU executable %q required by Lima for Linux/%s was not found: %w; install QEMU or set %s to the architecture-appropriate executable", executable, qemuArch, err, overrideName)
	}
	return path, nil
}

func hasVMType(info InstallationInfo, required string) bool {
	for _, vmType := range info.VMTypes {
		if vmType == required {
			return true
		}
	}
	_, ok := info.VMTypesEx[required]
	return ok
}

// InspectCompatibleInstallation inspects Lima and verifies that it can run
// Watermelon. It returns successfully inspected metadata even when the
// compatibility check fails so diagnostic callers can report useful details.
func InspectCompatibleInstallation() (InstallationInfo, error) {
	info, err := InspectInstallation()
	if err != nil {
		return info, err
	}
	if err := checkCompatibility(&info); err != nil {
		return info, err
	}
	if info.HostOS == "linux" {
		qemuVersion, err := probeQEMUVersion(info.QEMUExecutablePath)
		if err != nil {
			return info, err
		}
		info.QEMUVersion = qemuVersion
	}
	return info, nil
}

func probeQEMUVersion(path string) (string, error) {
	cmd := qemuExecCommand(path, "--version")
	var stdout, stderr boundedErrorBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		details := stderr.String()
		if details == "" {
			details = stdout.String()
		}
		if details == "" {
			return "", fmt.Errorf("running QEMU executable %q --version: %w", path, err)
		}
		return "", fmt.Errorf("running QEMU executable %q --version: %w: %s", path, err, details)
	}

	firstLine, _, _ := strings.Cut(stdout.String(), "\n")
	firstLine = strings.TrimSpace(firstLine)
	const prefix = "QEMU emulator version "
	if !strings.HasPrefix(firstLine, prefix) || strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)) == "" {
		return "", fmt.Errorf("%q --version did not identify a QEMU system emulator (got %q)", path, firstLine)
	}
	return strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)), nil
}

func goHostArchitecture(hostArch string) (string, error) {
	switch hostArch {
	case "aarch64", "arm64":
		return "arm64", nil
	case "x86_64", "amd64":
		return "amd64", nil
	case "":
		return "", fmt.Errorf("Lima did not report its host architecture")
	default:
		return "", fmt.Errorf("unsupported Lima host architecture %q", hostArch)
	}
}
