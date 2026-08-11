package lima

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type VMStatus int

const (
	StatusNotFound VMStatus = iota
	StatusStopped
	StatusRunning
	StatusUnknown
)

const startTimeout = "30m"

const (
	statusListFormat = "{{json .Name}}\t{{json .Status}}"
	mountListFormat  = "{{json .Name}}\t{{json .Config.Mounts}}"
	dirListFormat    = "{{json .Name}}\t{{json .Dir}}"
)

type StartStage string

const (
	StartStageInspect StartStage = "inspect"
	StartStageCreate  StartStage = "create"
	StartStageStart   StartStage = "start"
)

// StartError identifies how far Start progressed. Callers may only clean up an
// instance after StartStageStart: a create or inspect failure may belong to a
// concurrent process that won the instance-name race.
type StartError struct {
	Stage StartStage
	Err   error
}

func (e *StartError) Error() string { return e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

func (s VMStatus) String() string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusStopped:
		return "Stopped"
	case StatusUnknown:
		return "Unknown"
	default:
		return "Not found"
	}
}

// VMNameFromPath generates a consistent VM name from project path
func VMNameFromPath(projectPath string) string {
	base := strings.ToLower(filepath.Base(projectPath))
	var sanitized strings.Builder
	lastWasSeparator := false
	for _, r := range base {
		isAlphaNumeric := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNumeric {
			sanitized.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		separator := r
		if separator != '.' && separator != '_' && separator != '-' {
			separator = '-'
		}
		if sanitized.Len() > 0 && !lastWasSeparator {
			sanitized.WriteRune(separator)
			lastWasSeparator = true
		}
	}
	base = strings.Trim(sanitized.String(), "._-")
	if base == "" {
		base = "project"
	}

	hash := sha256.Sum256([]byte(projectPath))
	shortHash := hex.EncodeToString(hash[:])[:8]
	const maxNameBytes = 76
	const fixedBytes = len("watermelon-") + 1 + 8
	if len(base) > maxNameBytes-fixedBytes {
		base = strings.TrimRight(base[:maxNameBytes-fixedBytes], "._-")
	}
	if base == "" {
		base = "project"
	}
	return fmt.Sprintf("watermelon-%s-%s", base, shortHash)
}

// GetStatus returns the status of a VM
func GetStatus(vmName string) VMStatus {
	// Listing all instances makes a missing exact name an ordinary empty lookup.
	// `limactl list NAME` exits non-zero both when NAME is absent and when Lima
	// itself fails, which would make an operational error indistinguishable from
	// a VM that is safe to create.
	cmd := execCommand("limactl", "list", "--format", statusListFormat)
	out, err := cmd.Output()
	if err != nil {
		return StatusUnknown
	}
	status, err := statusFromInstanceList(out, vmName)
	if err != nil {
		return StatusUnknown
	}
	return status
}

func statusFromInstanceList(data []byte, vmName string) (VMStatus, error) {
	records, err := parseLimaTemplateRecords(data, 2)
	if err != nil {
		return StatusUnknown, fmt.Errorf("decoding Lima instance list: %w", err)
	}

	found := false
	status := StatusNotFound
	for _, record := range records {
		var name, limaStatus string
		if err := json.Unmarshal(record[0], &name); err != nil {
			return StatusUnknown, fmt.Errorf("decoding Lima instance list name: %w", err)
		}
		if err := json.Unmarshal(record[1], &limaStatus); err != nil {
			return StatusUnknown, fmt.Errorf("decoding Lima instance list status: %w", err)
		}
		if name != vmName {
			continue
		}
		if found {
			return StatusUnknown, fmt.Errorf("Lima returned VM %q more than once", vmName)
		}
		found = true
		status = parseStatus(limaStatus)
	}
	if !found {
		return StatusNotFound, nil
	}
	return status, nil
}

// parseLimaTemplateRecords parses newline-delimited records emitted by a
// limactl Go template. Each field is JSON encoded, so literal tabs and newlines
// in values are escaped and cannot be confused with record delimiters. Using
// bytes.Split rather than bufio.Scanner also avoids a second 64 KiB line limit.
func parseLimaTemplateRecords(data []byte, fieldCount int) ([][][]byte, error) {
	var records [][][]byte
	for lineNumber, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		fields := bytes.Split(line, []byte{'\t'})
		if len(fields) != fieldCount {
			return nil, fmt.Errorf("record %d has %d fields, want %d", lineNumber+1, len(fields), fieldCount)
		}
		records = append(records, fields)
	}
	return records, nil
}

// ProjectMountSource returns the host source currently recorded for the
// instance's /project mount. Callers use this to bind a short public VM name to
// the canonical project that actually created the Lima instance.
func ProjectMountSource(vmName string) (string, error) {
	return MountSource(vmName, "/project")
}

// MountSource returns the unique host source recorded for mountPoint in an
// existing Lima instance's immutable config.
func MountSource(vmName, mountPoint string) (string, error) {
	mount, err := InstanceMount(vmName, mountPoint)
	if err != nil {
		return "", err
	}
	return mount.Location, nil
}

// InstanceDir returns Lima's host instance directory for one exact VM. The
// narrow template avoids reading full config JSON, which may include large
// embedded provision scripts.
func InstanceDir(vmName string) (string, error) {
	cmd := execCommand("limactl", "list", "--format", dirListFormat, vmName)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading Lima instance directory for VM %q: %w", vmName, err)
	}
	records, err := parseLimaTemplateRecords(out, 2)
	if err != nil {
		return "", fmt.Errorf("decoding Lima instance directory for VM %q: %w", vmName, err)
	}
	if len(records) != 1 {
		return "", fmt.Errorf("decoding Lima instance directory for VM %q: got %d instances, want 1", vmName, len(records))
	}
	var instanceName, dir string
	if err := json.Unmarshal(records[0][0], &instanceName); err != nil {
		return "", fmt.Errorf("decoding Lima instance name for VM %q: %w", vmName, err)
	}
	if err := json.Unmarshal(records[0][1], &dir); err != nil {
		return "", fmt.Errorf("decoding Lima instance directory for VM %q: %w", vmName, err)
	}
	if instanceName != vmName {
		return "", fmt.Errorf("Lima returned instance %q while checking VM %q", instanceName, vmName)
	}
	if dir == "" {
		return "", fmt.Errorf("Lima returned an empty instance directory for VM %q", vmName)
	}
	return dir, nil
}

// LimaMount describes a host mount recorded in an instance's immutable config.
type LimaMount struct {
	Location   string `json:"location"`
	MountPoint string `json:"mountPoint"`
	Writable   bool   `json:"writable"`
}

// InstanceMount returns the unique mount at mountPoint, including its
// read/write property so callers can verify security-sensitive bootstrap
// mounts rather than trusting only their host source.
func InstanceMount(vmName, mountPoint string) (LimaMount, error) {
	cmd := execCommand("limactl", "list", "--format", mountListFormat, vmName)
	out, err := cmd.Output()
	if err != nil {
		return LimaMount{}, fmt.Errorf("reading Lima config for VM %q: %w", vmName, err)
	}

	records, err := parseLimaTemplateRecords(out, 2)
	if err != nil {
		return LimaMount{}, fmt.Errorf("decoding Lima config for VM %q: %w", vmName, err)
	}
	if len(records) != 1 {
		return LimaMount{}, fmt.Errorf("decoding Lima config for VM %q: got %d instances, want 1", vmName, len(records))
	}

	var instanceName string
	if err := json.Unmarshal(records[0][0], &instanceName); err != nil {
		return LimaMount{}, fmt.Errorf("decoding Lima config name for VM %q: %w", vmName, err)
	}
	var mounts []LimaMount
	if err := json.Unmarshal(records[0][1], &mounts); err != nil {
		return LimaMount{}, fmt.Errorf("decoding Lima mounts for VM %q: %w", vmName, err)
	}
	if instanceName != vmName {
		return LimaMount{}, fmt.Errorf("Lima returned instance %q while checking VM %q", instanceName, vmName)
	}

	var matched *LimaMount
	for _, mount := range mounts {
		if mount.MountPoint != mountPoint {
			continue
		}
		if matched != nil {
			return LimaMount{}, fmt.Errorf("VM %q has multiple %s mounts", vmName, mountPoint)
		}
		if mount.Location == "" {
			return LimaMount{}, fmt.Errorf("VM %q has an empty source for %s mount", vmName, mountPoint)
		}
		copy := mount
		matched = &copy
	}
	if matched == nil {
		return LimaMount{}, fmt.Errorf("VM %q has no %s mount", vmName, mountPoint)
	}
	return *matched, nil
}

func parseStatus(s string) VMStatus {
	switch s {
	case "Running":
		return StatusRunning
	case "Stopped":
		return StatusStopped
	default:
		return StatusUnknown
	}
}

// Start creates a VM from configPath or starts an existing VM when configPath
// is empty. Creation is deliberately create-only: an existing instance must
// make the create command fail instead of being accepted with an unapplied
// configuration.
func Start(vmName, configPath string) error {
	if configPath != "" {
		createCmd := execCommand("limactl", "create", "--name", vmName, configPath)
		createCmd.Stdout = os.Stdout
		createCmd.Stderr = os.Stderr
		if err := createCmd.Run(); err != nil {
			return &StartError{Stage: StartStageCreate, Err: fmt.Errorf("creating VM %q: %w", vmName, err)}
		}

		startCmd := execCommand("limactl", "start", "--timeout="+startTimeout, vmName)
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return &StartError{Stage: StartStageStart, Err: fmt.Errorf("starting newly created VM %q: %w", vmName, err)}
		}
		return nil
	}

	status := GetStatus(vmName)

	switch status {
	case StatusRunning:
		return nil // already running
	case StatusStopped:
		cmd := execCommand("limactl", "start", "--timeout="+startTimeout, vmName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return &StartError{Stage: StartStageStart, Err: fmt.Errorf("starting VM %q: %w", vmName, err)}
		}
		return nil
	case StatusNotFound:
		return &StartError{Stage: StartStageInspect, Err: fmt.Errorf("cannot start VM %q: instance not found", vmName)}
	default:
		return &StartError{Stage: StartStageInspect, Err: fmt.Errorf("cannot start VM %q: instance state is unknown", vmName)}
	}
}

// VerifyProvisioningComplete quietly checks the root-owned, per-boot marker
// written only after every Watermelon-generated provision stage succeeds.
// Lima's own ready signal is insufficient because Lima continues after a
// failed provision script and can still report the instance as ready.
func VerifyProvisioningComplete(vmName string) error {
	const check = `test -f /run/watermelon-provisioning-complete && test ! -L /run/watermelon-provisioning-complete && test "$(stat -c %u /run/watermelon-provisioning-complete 2>/dev/null)" = 0 && test "$(stat -c %a /run/watermelon-provisioning-complete 2>/dev/null)" = 600`
	cmd := execCommand("limactl", "shell", vmName, "--", "sh", "-c", check)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("root-owned /run/watermelon-provisioning-complete marker is unavailable: %w", err)
	}
	return nil
}

// VerifyPolicyApplied is retained for package compatibility. Provisioning
// completion is now the stronger condition required by callers.
func VerifyPolicyApplied(vmName string) error {
	return VerifyProvisioningComplete(vmName)
}

// Stop stops a VM
func Stop(vmName string) error {
	cmd := execCommand("limactl", "stop", vmName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Delete deletes a VM
func Delete(vmName string) error {
	cmd := execCommand("limactl", "delete", "--force", vmName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Copy copies files between the host and a VM using limactl copy. The source
// and destination have already been validated by the CLI as one local path and
// one vmname:path operand.
func Copy(src, dst string, recursive bool) error {
	args := []string{"copy"}
	if recursive {
		args = append(args, "--recursive")
	}
	// End limactl option parsing before accepting user-controlled paths. This is
	// required even after our own Cobra parser handles its separator because a
	// local filename can legitimately begin with a dash.
	args = append(args, "--", src, dst)
	cmd := execCommand("limactl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Shell opens an interactive shell in the VM. With no workdir argument it
// retains the historical /project default; an explicitly empty workdir lets
// Lima select the guest user's login directory.
func Shell(vmName string, workdir ...string) error {
	resolved, err := resolveLifecycleWorkdir(workdir)
	if err != nil {
		return err
	}
	cmdArgs := []string{"shell"}
	if resolved != "" {
		cmdArgs = append(cmdArgs, "--workdir", resolved)
	}
	cmdArgs = append(cmdArgs, vmName)
	cmd := execCommand("limactl", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	// Ignore normal shell exit codes (0, 130=SIGINT, 143=SIGTERM)
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code == 0 || code == 130 || code == 143 {
			return nil
		}
	}
	return err
}

// Exec runs a command in the VM. Workdir follows Shell's compatibility and
// explicit-empty semantics.
func Exec(vmName string, args []string, workdir ...string) error {
	resolved, err := resolveLifecycleWorkdir(workdir)
	if err != nil {
		return err
	}
	cmdArgs := []string{"shell"}
	if resolved != "" {
		cmdArgs = append(cmdArgs, "--workdir", resolved)
	}
	cmdArgs = append(cmdArgs, vmName, "--")
	if len(args) == 1 && shouldRunViaShell(args[0]) {
		args = []string{"sh", "-lc", args[0]}
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := execCommand("limactl", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func resolveLifecycleWorkdir(values []string) (string, error) {
	switch len(values) {
	case 0:
		return "/project", nil
	case 1:
		return values[0], nil
	default:
		return "", fmt.Errorf("expected at most one guest workdir, got %d", len(values))
	}
}

func shouldRunViaShell(arg string) bool {
	return strings.ContainsAny(arg, " \t\n;&|<>*?$`")
}
