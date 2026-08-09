package lima

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	base := filepath.Base(projectPath)
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")

	// Add short hash for uniqueness
	hash := sha256.Sum256([]byte(projectPath))
	shortHash := hex.EncodeToString(hash[:])[:8]

	return fmt.Sprintf("watermelon-%s-%s", base, shortHash)
}

// GetStatus returns the status of a VM
func GetStatus(vmName string) VMStatus {
	// Listing all instances makes a missing exact name an ordinary empty lookup.
	// `limactl list NAME` exits non-zero both when NAME is absent and when Lima
	// itself fails, which would make an operational error indistinguishable from
	// a VM that is safe to create.
	cmd := execCommand("limactl", "list", "--format", "json")
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	found := false
	status := StatusNotFound
	for {
		var instance struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		if err := decoder.Decode(&instance); err != nil {
			if err == io.EOF {
				break
			}
			return StatusUnknown, fmt.Errorf("decoding Lima instance list: %w", err)
		}
		if instance.Name != vmName {
			continue
		}
		if found {
			return StatusUnknown, fmt.Errorf("Lima returned VM %q more than once", vmName)
		}
		found = true
		status = parseStatus(instance.Status)
	}
	if !found {
		return StatusNotFound, nil
	}
	return status, nil
}

// ProjectMountSource returns the host source currently recorded for the
// instance's /project mount. Callers use this to bind a short public VM name to
// the canonical project that actually created the Lima instance.
func ProjectMountSource(vmName string) (string, error) {
	cmd := execCommand("limactl", "list", "--format", "json", vmName)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading Lima config for VM %q: %w", vmName, err)
	}

	var instance struct {
		Name   string `json:"name"`
		Config struct {
			Mounts []struct {
				Location   string `json:"location"`
				MountPoint string `json:"mountPoint"`
			} `json:"mounts"`
		} `json:"config"`
	}
	if err := json.Unmarshal(out, &instance); err != nil {
		return "", fmt.Errorf("decoding Lima config for VM %q: %w", vmName, err)
	}
	if instance.Name != vmName {
		return "", fmt.Errorf("Lima returned instance %q while checking VM %q", instance.Name, vmName)
	}

	projectSource := ""
	for _, mount := range instance.Config.Mounts {
		if mount.MountPoint != "/project" {
			continue
		}
		if projectSource != "" {
			return "", fmt.Errorf("VM %q has multiple /project mounts", vmName)
		}
		projectSource = mount.Location
	}
	if projectSource == "" {
		return "", fmt.Errorf("VM %q has no /project mount", vmName)
	}
	return projectSource, nil
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

// VerifyPolicyApplied quietly checks the root-owned, per-boot marker written
// only after Watermelon's network policy provisioning completes.
func VerifyPolicyApplied(vmName string) error {
	const check = `test -f /run/watermelon-policy-applied && test ! -L /run/watermelon-policy-applied && test "$(stat -c %u /run/watermelon-policy-applied 2>/dev/null)" = 0`
	cmd := execCommand("limactl", "shell", vmName, "--", "sh", "-c", check)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("root-owned /run/watermelon-policy-applied marker is unavailable: %w", err)
	}
	return nil
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

// Shell opens an interactive shell in the VM
func Shell(vmName string) error {
	cmd := execCommand("limactl", "shell", "--workdir", "/project", vmName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	// Ignore normal shell exit codes (0, 130=SIGINT, 143=SIGTERM)
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code == 0 || code == 130 || code == 143 {
			return nil
		}
	}
	return err
}

// Exec runs a command in the VM
func Exec(vmName string, args []string) error {
	cmdArgs := []string{"shell", "--workdir", "/project", vmName, "--"}
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

func shouldRunViaShell(arg string) bool {
	return strings.ContainsAny(arg, " \t\n;&|<>*?$`")
}
