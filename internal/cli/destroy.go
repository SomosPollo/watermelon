package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	errNoSandboxVM       = errors.New("no sandbox VM found for this project")
	destroyGetStatus     = lima.GetStatus
	destroyStop          = lima.Stop
	destroyDelete        = lima.Delete
	destroyInstanceDir   = lima.InstanceDir
	destroyWorkspaceDisk = lima.DeleteWorkspaceDisk
	releaseWorkspaceDisk = lima.ReleaseWorkspaceDisk
)

type legacyDestroyIncarnation struct {
	file *os.File
	info os.FileInfo
	path string
}

func captureLegacyDestroyIncarnation(vmName string) (*legacyDestroyIncarnation, error) {
	reportedDir, err := destroyInstanceDir(vmName)
	if err != nil {
		return nil, err
	}
	canonicalDir, err := canonicalizeHostPath("Lima instance directory", reportedDir)
	if err != nil {
		return nil, err
	}
	_, limaHome, err := effectiveLimaHome()
	if err != nil {
		return nil, err
	}
	expected := filepath.Join(limaHome, vmName)
	if canonicalDir != expected {
		return nil, fmt.Errorf("Lima instance directory for VM %q is %q, not %q", vmName, canonicalDir, expected)
	}
	fd, err := unix.Open(canonicalDir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("opening Lima instance directory for VM %q without following symlinks: %w", vmName, err)
	}
	file := os.NewFile(uintptr(fd), canonicalDir)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("opening Lima instance directory for VM %q: invalid file descriptor", vmName)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspecting Lima instance directory for VM %q: %w", vmName, err)
	}
	current, err := os.Lstat(canonicalDir)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(info, current) {
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("rechecking Lima instance directory for VM %q: %w", vmName, err)
		}
		return nil, fmt.Errorf("Lima instance directory for VM %q changed while it was opened", vmName)
	}
	if !ownedByCurrentUser(info) {
		_ = file.Close()
		return nil, fmt.Errorf("Lima instance directory for VM %q is not owned by the current user", vmName)
	}
	return &legacyDestroyIncarnation{file: file, info: info, path: canonicalDir}, nil
}

func sameLegacyDestroyIncarnation(first *legacyDestroyIncarnation, vmName string) (bool, error) {
	if first == nil {
		return false, errors.New("missing legacy VM incarnation")
	}
	current, err := captureLegacyDestroyIncarnation(vmName)
	if err != nil {
		return false, err
	}
	defer current.file.Close()
	return os.SameFile(first.info, current.info), nil
}

// resolveDestroyTarget keeps malformed-config recovery narrow: only an
// explicit, validated name with a durable identity owned by this project may
// bypass normal management-target resolution.
func resolveDestroyTarget(dir, name string) (targetContext, *namedVMIdentity, error) {
	target, configErr := resolveManagementTarget(dir, name)
	if configErr == nil {
		return target, nil, nil
	}
	if target.ProjectRoot != "" {
		dir = target.ProjectRoot
	}
	if name == "" {
		return target, nil, configErr
	}
	instance, identityErr := loadOwnedNamedVMIdentity(dir, name)
	if identityErr != nil {
		return targetContext{}, nil, errors.Join(configErr, fmt.Errorf("cannot recover VM %q for destruction without a valid project-owned identity: %w", name, identityErr))
	}
	target = targetContext{ProjectRoot: dir, VMName: name, NameExplicit: true}
	return target, &instance.Identity, nil
}

func inspectDestroyTarget(target targetContext, identity *namedVMIdentity) (lima.VMStatus, *namedVMIdentity, error) {
	dir := target.ProjectRoot
	vmName := target.VMName
	status := destroyGetStatus(vmName)
	if identity == nil {
		if instance, err := loadOwnedNamedVMIdentity(dir, vmName); err == nil {
			identity = &instance.Identity
		} else if !errors.Is(err, os.ErrNotExist) {
			return status, nil, fmt.Errorf("loading VM identity before destruction: %w", err)
		}
	}
	if status == lima.StatusNotFound && identity == nil {
		if target.Config == nil || target.Config.Workspace.Disk == "" {
			return status, nil, errNoSandboxVM
		}
		if err := requireWorkspaceOwnership(dir, vmName, target.Config); err != nil {
			return status, nil, err
		}
	}
	if status != lima.StatusNotFound {
		if err := requireVMProjectBinding(dir, vmName); err != nil {
			return status, nil, err
		}
	}
	return status, identity, nil
}

func sameDestroyIdentity(first, second *namedVMIdentity) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func NewDestroyCmd() *cobra.Command {
	return newDestroyCmdAt("")
}

func newDestroyCmdAt(directory string) *cobra.Command {
	var force bool
	var deleteWorkspace bool
	var name string

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Delete the sandbox VM, retaining managed workspace files by default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := directory
			var err error
			if dir == "" {
				dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			dir, err = canonicalProjectRoot(dir)
			if err != nil {
				return err
			}
			if name != "" {
				if err := config.ValidateVMName(name); err != nil {
					return NewUsageError(fmt.Errorf("invalid --name %q: %w", name, err))
				}
			}
			initialTarget, initialIdentity, err := resolveDestroyTarget(dir, name)
			if err != nil {
				return err
			}
			vmName := initialTarget.VMName
			var storageLock *persistentVMLock
			if deleteWorkspace {
				if initialTarget.Config == nil || initialTarget.Config.Workspace.Disk == "" {
					return fmt.Errorf("--delete-workspace requires a valid managed workspace configuration")
				}
				storageLock, err = acquirePersistentVMLock("wm-setup-"+fullPathDigest(initialTarget.ProjectRoot)[:24], lifecycleLockSuffix, "workspace removal lock", unix.LOCK_EX|unix.LOCK_NB)
				if err != nil {
					return fmt.Errorf("cannot remove workspace while preparation is active; stop setup first: %w", err)
				}
				defer storageLock.release()
			}
			var promptedStatus lima.VMStatus
			var legacyIncarnation *legacyDestroyIncarnation
			if !force {
				// This preflight is read-only and intentionally unlocked. Never hold
				// the lifecycle mutex while waiting for terminal input: stop and all
				// fail-closed recovery paths must remain immediately available.
				promptedStatus, initialIdentity, err = inspectDestroyTarget(initialTarget, initialIdentity)
				if err != nil {
					return err
				}
				if initialIdentity == nil && promptedStatus != lima.StatusNotFound {
					legacyIncarnation, err = captureLegacyDestroyIncarnation(vmName)
					if err != nil {
						return fmt.Errorf("recording VM %q incarnation before confirmation: %w", vmName, err)
					}
					defer legacyIncarnation.file.Close()
				}

				if promptedStatus == lima.StatusNotFound {
					fmt.Fprintf(cmd.OutOrStdout(), "This will remove stale Watermelon host state for VM '%s'.\n", vmName)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "This will delete VM '%s' and all installed dependencies.\n", vmName)
				}
				if initialTarget.Config != nil && initialTarget.Config.Workspace.Disk != "" {
					if deleteWorkspace {
						fmt.Fprintf(cmd.OutOrStdout(), "This also permanently deletes workspace disk %s, including source files and uncommitted edits.\n", initialTarget.Config.Workspace.Disk)
					} else {
						fmt.Fprintln(cmd.OutOrStdout(), "The managed workspace disk, source files and project dependencies will be retained.")
					}
				}
				fmt.Fprint(cmd.OutOrStdout(), "Are you sure? [y/N] ")
				reader := bufio.NewReader(cmd.InOrStdin())
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelled")
					return nil
				}
			}

			lifecycleLock, err := acquireVMLifecycleLock(vmName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
			}
			defer lifecycleLock.Release()

			// Resolve and authenticate again after the prompt and under the lock.
			// Configuration, ownership, and Lima state may all have changed while
			// the command waited for the user.
			target, identity, err := resolveDestroyTarget(dir, name)
			if err != nil {
				return err
			}
			if target.ProjectRoot != initialTarget.ProjectRoot {
				return fmt.Errorf("sandbox project root changed from %q to %q while destruction was awaiting confirmation; retry the command", initialTarget.ProjectRoot, target.ProjectRoot)
			}
			if target.VMName != vmName {
				return fmt.Errorf("sandbox target changed from %q to %q while destruction was awaiting confirmation; retry the command", vmName, target.VMName)
			}
			if deleteWorkspace && (target.Config == nil || target.Config.Workspace != initialTarget.Config.Workspace) {
				return fmt.Errorf("workspace settings changed while awaiting confirmation; retry")
			}
			status, identity, err := inspectDestroyTarget(target, identity)
			if err != nil {
				return err
			}
			if !force {
				if promptedStatus == lima.StatusNotFound && status != lima.StatusNotFound {
					return fmt.Errorf("VM %q appeared while destruction was awaiting confirmation; retry the command", vmName)
				}
				if !sameDestroyIdentity(initialIdentity, identity) {
					return fmt.Errorf("VM %q identity changed while destruction was awaiting confirmation; retry the command", vmName)
				}
				if initialIdentity == nil && promptedStatus != lima.StatusNotFound {
					same, err := sameLegacyDestroyIncarnation(legacyIncarnation, vmName)
					if err != nil {
						return fmt.Errorf("rechecking VM %q incarnation after confirmation: %w", vmName, err)
					}
					if !same {
						return fmt.Errorf("VM %q incarnation changed while destruction was awaiting confirmation; retry the command", vmName)
					}
				}
			}
			dir = target.ProjectRoot

			if status == lima.StatusNotFound {
				fmt.Fprintln(cmd.OutOrStdout(), "Removing stale sandbox VM host state...")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Destroying sandbox VM...")
				if err := requireVMProjectBinding(dir, vmName); err != nil {
					return err
				}
				if status != lima.StatusStopped {
					if err := destroyStop(vmName); err != nil {
						if status != lima.StatusUnknown {
							return fmt.Errorf("stopping VM before destruction: %w", err)
						}
						// A broken/unknown Lima instance may not support a clean stop.
						// Continue only after the exclusive usage lease below proves no
						// Watermelon client is still attached.
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: VM could not be stopped cleanly before forced deletion: %v\n", err)
					}
				}
			}
			// Stop active clients first, then wait for limactl/IDE processes to
			// detach. The lifecycle mutex stays held, so no command can attach a
			// new shared user between this exclusive lease and deletion. Waiting
			// before the stop would prevent an independent stop from terminating
			// the very session whose lease destroy was waiting for.
			usageLease, leaseErr := acquireExclusiveVMUsageLease(vmName)
			if leaseErr != nil {
				return fmt.Errorf("waiting for terminated VM %q sessions before deletion: %w", vmName, leaseErr)
			}
			defer usageLease.Release()
			if status != lima.StatusNotFound {
				if err := destroyDelete(vmName); err != nil {
					return err
				}
			}
			var cleanupErrors []error
			if target.Config != nil && target.Config.Workspace.Disk != "" {
				if err := releaseWorkspaceDisk(target.Config.Workspace.Disk, vmName); err != nil && !errors.Is(err, os.ErrNotExist) {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("workspace disk was retained but its attachment could not be released: %w", err))
				}
			}
			if err := clearAppliedPolicySnapshotForVM(dir, vmName); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("applied-policy snapshot could not be removed: %w", err))
			}
			if identity != nil {
				if err := removeNamedVMIdentity(*identity); err != nil {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("VM identity state could not be removed: %w", err))
				}
			}
			if len(cleanupErrors) > 0 {
				return fmt.Errorf("sandbox state cleanup failed: %w", errors.Join(cleanupErrors...))
			}
			if target.Config != nil && (target.Config.Setup.Profile != "" || target.Config.Workspace.Disk != "") {
				if err := invalidateWorkspaceSetup(dir); err != nil {
					return fmt.Errorf("VM removed but setup state could not be updated: %w", err)
				}
			}
			if deleteWorkspace {
				state, err := loadWorkspaceState(dir)
				if err != nil {
					return err
				}
				if state.Disk != target.Config.Workspace.Disk || !state.DiskCreated {
					return fmt.Errorf("workspace disk ownership changed; refusing deletion")
				}
				if err := destroyWorkspaceDisk(state.Disk); err != nil {
					return fmt.Errorf("VM removed, but workspace disk was retained: %w", err)
				}
				state.DiskCreated = false
				state.DiskInitialized = false
				state.Cloned = false
				state.ReadyFingerprint = ""
				state.ApprovedConfig = ""
				state.Stage = "workspace permanently deleted"
				state.LastError = ""
				if err := saveWorkspaceState(dir, state); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Workspace disk permanently deleted. Create a new clone in a new directory to start again.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&deleteWorkspace, "delete-workspace", false, "Permanently delete the owned managed disk, source files and edits")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the name derived from the resolved project root)")
	return cmd
}
