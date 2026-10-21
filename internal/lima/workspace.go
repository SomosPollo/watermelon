package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Workspace disks are never force-deleted or silently adopted. Ownership and
// the separate disk lifecycle are managed by the CLI's host-only record.
func CreateWorkspaceDisk(name, size string) error {
	cmd := execCommand("limactl", "disk", "create", name, "--size", size, "--format", "raw")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func DeleteWorkspaceDisk(name string) error {
	cmd := execCommand("limactl", "disk", "delete", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	// Lima can return success while skipping disks referenced by a stopped
	// instance. Do not report permanent deletion or clear ownership in that case.
	_, err := InspectWorkspaceDisk(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("workspace disk %q was retained; another instance may still reference it", name)
}

type WorkspaceDiskInfo struct {
	Name     string `json:"name"`
	Instance string `json:"instance"`
}

func InspectWorkspaceDisk(name string) (WorkspaceDiskInfo, error) {
	cmd := execCommand("limactl", "disk", "list", "--json")
	var output bytes.Buffer
	writer := &limitedGuestWriter{buffer: &output, remaining: 1 << 20}
	cmd.Stdout = writer
	if err := cmd.Run(); err != nil {
		return WorkspaceDiskInfo{}, fmt.Errorf("listing workspace disks: %w", err)
	}
	if writer.exceeded {
		return WorkspaceDiskInfo{}, fmt.Errorf("workspace disk inventory exceeds limit")
	}
	decoder := json.NewDecoder(&output)
	for {
		var disk WorkspaceDiskInfo
		err := decoder.Decode(&disk)
		if err == io.EOF {
			break
		}
		if err != nil {
			return disk, err
		}
		if disk.Name == name {
			return disk, nil
		}
	}
	return WorkspaceDiskInfo{}, fmt.Errorf("workspace disk %q is missing; refusing to initialize a replacement: %w", name, os.ErrNotExist)
}

// Lima can leave an in-use symlink after stopping/deleting an instance. Only
// release that stale reference after checking its exact owner and VM state.
// The caller holds the owning VM's lifecycle lock through this operation.
func ReleaseWorkspaceDisk(name, vmName string) error {
	disk, err := InspectWorkspaceDisk(name)
	if err != nil {
		return err
	}
	if disk.Instance == "" {
		return nil
	}
	if disk.Instance != vmName {
		return fmt.Errorf("workspace disk %q is attached to another instance %q", name, disk.Instance)
	}
	status, err := GetStatusWithError(vmName)
	if err != nil {
		return err
	}
	if status != StatusNotFound && status != StatusStopped {
		return fmt.Errorf("refusing to unlock workspace disk used by a running or unknown VM")
	}
	cmd := execCommand("limactl", "disk", "unlock", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// GuestOutput is a bounded, noninteractive control-plane read. It must not
// inherit guest stdin, execute repository hooks, or allocate a guest TTY.
func GuestOutput(ctx context.Context, vmName, workdir string, args []string, limit int64) ([]byte, error) {
	cmd := execCommand("limactl", append([]string{"shell", "--workdir", workdir, vmName, "--"}, args...)...)
	var output bytes.Buffer
	writer := &limitedGuestWriter{buffer: &output, remaining: limit}
	cmd.Stdout = writer
	var stderr bytes.Buffer
	cmd.Stderr = &limitedGuestWriter{buffer: &stderr, remaining: 4096}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Preserve the package's command factory used by tests, while cancellation
	// terminates the attached Lima client (the VM is kept for explicit recovery).
	cmd.Cancel = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if writer.exceeded {
		return nil, fmt.Errorf("guest metadata exceeds the %d-byte limit", limit)
	}
	if err != nil {
		return nil, fmt.Errorf("reading guest metadata: %w (%q)", err, stderr.String())
	}
	return output.Bytes(), nil
}

type limitedGuestWriter struct {
	buffer    *bytes.Buffer
	remaining int64
	exceeded  bool
}

func (w *limitedGuestWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(n) > w.remaining {
		p = p[:w.remaining]
		w.exceeded = true
	}
	written, _ := w.buffer.Write(p)
	w.remaining -= int64(written)
	// Continue draining the pipe after reaching the bound, avoiding memory growth
	// and deadlocks on a guest that keeps writing.
	return n, nil
}

var _ io.Writer = (*limitedGuestWriter)(nil)
