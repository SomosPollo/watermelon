package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestAppliedPolicyRejectsOversizedValidPrefix(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	if err := saveAppliedPolicySnapshot(dir, cfg); err != nil {
		t.Fatal(err)
	}
	path, err := appliedPolicySnapshotPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The old reader parsed only the first 4 MiB and silently ignored the tail.
	data = append(data, bytes.Repeat([]byte(" "), (4<<20)-len(data))...)
	data = append(data, []byte("invalid trailing data")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	assessment := assessAppliedPolicy(dir, lima.StatusRunning, cfg)
	if assessment.State != policyUnverifiedInvalid || assessment.Err == nil || !strings.Contains(assessment.Err.Error(), "exceeds") {
		t.Fatalf("oversized snapshot assessment = %+v; want invalid with size error", assessment)
	}
}

func TestWorkspaceStateRejectsInvalidWritesWithoutReplacingRecord(t *testing.T) {
	for _, name := range []string{"wrong owner", "wrong version", "wrong disk", "oversized"} {
		t.Run(name, func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			original := workspaceState{Version: 1, Owner: dir, Disk: workspaceDiskName(dir), Stage: "ready"}
			if err := saveWorkspaceState(dir, original); err != nil {
				t.Fatal(err)
			}
			changed := original
			switch name {
			case "wrong owner":
				changed.Owner = t.TempDir()
			case "wrong version":
				changed.Version = 2
			case "wrong disk":
				changed.Disk = "someone-elses-disk"
			case "oversized":
				changed.LastError = strings.Repeat("x", maxWorkspaceStateFileSize)
			}
			if err := saveWorkspaceState(dir, changed); err == nil {
				t.Fatal("invalid workspace state was saved")
			}
			got, err := loadWorkspaceState(dir)
			if err != nil || got != original {
				t.Fatalf("failed write changed existing record: %+v, %v", got, err)
			}
		})
	}
}

func TestWorkspaceStateRejectsCorruptRecords(t *testing.T) {
	for _, kind := range []string{"unknown field", "wrong owner", "wrong disk", "wrong version", "null", "truncated", "trailing JSON"} {
		t.Run(kind, func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			state := workspaceState{Version: 1, Owner: dir, Stage: "ready"}
			path, err := workspaceStatePath(dir, true)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "wrong owner":
				state.Owner = t.TempDir()
			case "wrong disk":
				state.Disk = "another-disk"
			case "wrong version":
				state.Version = 2
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unknown field":
				data = append(data[:len(data)-1], []byte(`,"disk_cretaed":true}`)...)
			case "null":
				data = []byte("null")
			case "truncated":
				data = data[:len(data)-1]
			case "trailing JSON":
				data = append(data, []byte(` {"disk_created":true}`)...)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadWorkspaceState(dir); err == nil {
				t.Fatalf("corrupt workspace state accepted: %s", kind)
			}
		})
	}
}

func TestPrivateStateReadersRejectHardLinks(t *testing.T) {
	for name, read := range map[string]func(string) ([]byte, error){
		"identity": readPrivateIdentityFile,
		"policy":   readAppliedPolicySnapshotFile,
	} {
		t.Run(name, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, "record")
			if err := os.WriteFile(path, []byte("private state"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
				t.Fatal(err)
			}
			if _, err := read(path); err == nil {
				t.Fatal("hard-linked private state accepted")
			}
		})
	}
}
