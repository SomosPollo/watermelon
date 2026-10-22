package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/config"
)

const maxWorkspaceStateFileSize = 16 << 10

// This record is outside every guest mount. It outlives VM identities and is
// the authority for disk ownership and approved setup, never guest markers.
type workspaceState struct {
	Version          int    `json:"version"`
	Owner            string `json:"owner"`
	Disk             string `json:"disk,omitempty"`
	Source           string `json:"source,omitempty"`
	DiskCreated      bool   `json:"disk_created,omitempty"`
	DiskInitialized  bool   `json:"disk_initialized,omitempty"`
	Cloned           bool   `json:"cloned,omitempty"`
	RequestedProfile string `json:"requested_profile,omitempty"`
	RequestedRuntime string `json:"requested_runtime,omitempty"`
	ApprovedConfig   string `json:"approved_config,omitempty"`
	ReadyFingerprint string `json:"ready_fingerprint,omitempty"`
	Stage            string `json:"stage"`
	LastError        string `json:"last_error,omitempty"`
}

func workspaceDiskName(dir string) string { return "w" + fullPathDigest(dir)[:10] }

func workspaceStatePath(dir string, create bool) (string, error) {
	host, err := resolveAppliedPolicyHostContext(dir, nil)
	if err != nil {
		return "", err
	}
	if create {
		if err := secureAppliedPolicyStateDirectories(host, true); err != nil {
			return "", err
		}
	} else {
		for _, component := range []string{host.AppStateRoot, filepath.Join(host.AppStateRoot, "vms"), host.StateDir} {
			if err := validatePrivateIdentityDirectory(component); err != nil {
				return "", err
			}
		}
	}
	return filepath.Join(host.StateDir, "workspace-"+fullPathDigest(host.ProjectRoot)+".json"), nil
}

func loadWorkspaceState(dir string) (workspaceState, error) {
	root, err := canonicalProjectRoot(dir)
	if err != nil {
		return workspaceState{}, err
	}
	path, err := workspaceStatePath(root, false)
	if err != nil {
		return workspaceState{}, err
	}
	data, err := readPrivateStateFile(path, "workspace state", maxWorkspaceStateFileSize)
	if err != nil {
		return workspaceState{}, err
	}
	var state workspaceState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, fmt.Errorf("invalid workspace state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return state, fmt.Errorf("invalid workspace state: %w", err)
	}
	if err := validateWorkspaceState(root, state); err != nil {
		return state, err
	}
	return state, nil
}

func saveWorkspaceState(dir string, state workspaceState) error {
	root, err := canonicalProjectRoot(dir)
	if err != nil {
		return err
	}
	if err := validateWorkspaceState(root, state); err != nil {
		return err
	}
	path, err := workspaceStatePath(root, true)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return replacePrivateStateFile(path, "workspace state", data, maxWorkspaceStateFileSize)
}

func validateWorkspaceState(root string, state workspaceState) error {
	if state.Version != 1 || state.Owner != root || (state.Disk != "" && state.Disk != workspaceDiskName(root)) {
		return errors.New("workspace state does not match its owning directory")
	}
	return nil
}

func requireWorkspaceOwnership(dir, vmName string, cfg *config.Config) error {
	if cfg.Workspace.Disk == "" {
		return nil
	}
	if cfg.Workspace.Disk != workspaceDiskName(dir) || vmName != derivedVMName(dir) {
		return fmt.Errorf("managed workspace disk and VM must belong to this directory; --name overrides are not supported")
	}
	state, err := loadWorkspaceState(dir)
	if err != nil {
		return fmt.Errorf("verifying workspace ownership: %w", err)
	}
	if state.Disk != cfg.Workspace.Disk || state.Source != cfg.Workspace.Source || !state.DiskCreated {
		if state.Stage == "workspace permanently deleted" {
			return fmt.Errorf("workspace was permanently deleted; create a new clone in a new directory")
		}
		return fmt.Errorf("workspace disk is not registered to this project; recover the original host state before starting it")
	}
	return nil
}

func invalidateWorkspaceSetup(dir string) error {
	state, err := loadWorkspaceState(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	state.ReadyFingerprint = ""
	state.Stage = "stopped (VM removed; workspace retained)"
	return saveWorkspaceState(dir, state)
}
