package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

const recreatePolicyCommand = "watermelon destroy --force && watermelon run"

func policyCommandNeedsExplicitName(dir, vmName string, nameExplicit []bool) bool {
	return (len(nameExplicit) > 0 && nameExplicit[0]) || vmName != derivedVMName(dir)
}

func runPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon run"
	}
	return fmt.Sprintf("watermelon run --name %s", vmName)
}

func destroyPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon destroy --force"
	}
	return fmt.Sprintf("watermelon destroy --name %s --force", vmName)
}

func recreatePolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return recreatePolicyCommand
	}
	// VM names have already passed config.ValidateVMName, whose accepted
	// character set is safe to render directly in a suggested shell command.
	return fmt.Sprintf("watermelon destroy --name %s --force && watermelon run --name %s", vmName, vmName)
}

// deferredRecreatePolicyCommandForVM pins the VM selected when an ask prompt
// server starts. The user runs this destructive command later, after leaving
// the session; an intervening vm.name edit must not retarget it.
func deferredRecreatePolicyCommandForVM(dir, vmName string) string {
	return recreatePolicyCommandForVM(dir, vmName, true)
}

func restartPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon stop && watermelon run"
	}
	return fmt.Sprintf("watermelon stop --name %s && watermelon run --name %s", vmName, vmName)
}

type appliedPolicyState int

const (
	policyNotApplied appliedPolicyState = iota
	policyCurrent
	policyStale
	policyUnverifiedLegacy
	policyUnverifiedMissing
	policyUnverifiedInvalid
	policyComparisonUnavailable
)

type appliedPolicyAssessment struct {
	State    appliedPolicyState
	Snapshot config.AppliedPolicySnapshot
	Err      error
}

func saveNewVMPolicySnapshotOrStop(dir, vmName string, host appliedPolicyHostContext, cfg *config.Config, nameExplicit ...bool) error {
	if err := cliSavePolicySnapshot(host, cfg); err != nil {
		saveErr := fmt.Errorf("recording applied policy after VM creation: %w; destroy and recreate the VM with '%s'", err, recreatePolicyCommandForVM(dir, vmName, nameExplicit...))
		if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
			return errors.Join(saveErr, fmt.Errorf("the newly created VM was not stopped because its project identity could not be re-verified: %w", bindErr))
		}
		if stopErr := cliStopVM(vmName); stopErr != nil {
			return errors.Join(saveErr, fmt.Errorf("stopping the newly created VM after its policy snapshot could not be recorded: %w", stopErr))
		}
		return fmt.Errorf("%w; the newly created VM was stopped because its applied policy could not be recorded", saveErr)
	}
	return nil
}

func appliedPolicySnapshotPath(dir string) (string, error) {
	host, err := resolveAppliedPolicyHostContext(dir, nil)
	if err != nil {
		return "", err
	}
	return host.SnapshotPath, nil
}

func legacyConfigDigestPath(dir string) string {
	return filepath.Join(dir, ".watermelon", "config.sha256")
}

func assessAppliedPolicy(dir string, status lima.VMStatus, cfg *config.Config) appliedPolicyAssessment {
	return assessAppliedPolicyForVM(dir, derivedVMName(dir), status, cfg)
}

func assessAppliedPolicyForVM(dir, vmName string, status lima.VMStatus, cfg *config.Config) appliedPolicyAssessment {
	host, err := resolveAppliedPolicyHostContextForVM(dir, cfg, vmName)
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	if status == lima.StatusNotFound {
		return appliedPolicyAssessment{State: policyNotApplied}
	}

	if err := secureAppliedPolicyStateDirectories(host, false); err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	data, err := readAppliedPolicySnapshotFile(host.SnapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, legacyErr := os.Stat(legacyConfigDigestPath(dir)); legacyErr == nil {
			return appliedPolicyAssessment{State: policyUnverifiedLegacy, Err: config.ErrLegacyAppliedPolicySnapshot}
		} else if !os.IsNotExist(legacyErr) {
			return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: legacyErr}
		}
		return appliedPolicyAssessment{State: policyUnverifiedMissing, Err: err}
	}
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}

	snapshot, err := config.ParseAppliedPolicySnapshot(data)
	if errors.Is(err, config.ErrLegacyAppliedPolicySnapshot) {
		return appliedPolicyAssessment{State: policyUnverifiedLegacy, Err: err}
	}
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	if cfg == nil {
		return appliedPolicyAssessment{State: policyComparisonUnavailable, Snapshot: snapshot}
	}

	matches, err := snapshot.MatchesConfig(cfg, host.Recorded)
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Snapshot: snapshot, Err: err}
	}
	if !matches {
		return appliedPolicyAssessment{State: policyStale, Snapshot: snapshot}
	}
	return appliedPolicyAssessment{State: policyCurrent, Snapshot: snapshot}
}

func requireCurrentAppliedPolicy(dir string, status lima.VMStatus, cfg *config.Config) error {
	return requireCurrentAppliedPolicyForVM(dir, derivedVMName(dir), status, cfg)
}

func requireCurrentAppliedPolicyForVM(dir, vmName string, status lima.VMStatus, cfg *config.Config, nameExplicit ...bool) error {
	assessment := assessAppliedPolicyForVM(dir, vmName, status, cfg)
	configured := config.DescribeEnforcement(cfg.Security.Enforcement)
	remedy := fmt.Sprintf("run '%s' to recreate the VM with the configured policy", recreatePolicyCommandForVM(dir, vmName, nameExplicit...))

	switch assessment.State {
	case policyNotApplied, policyCurrent:
		return nil
	case policyStale:
		applied := config.DescribeEnforcement(assessment.Snapshot.Enforcement)
		if assessment.Snapshot.Enforcement == cfg.Security.Enforcement {
			return fmt.Errorf("sandbox configuration is stale: VM-affecting settings changed since creation (recorded enforcement remains %s); %s", applied, remedy)
		}
		return fmt.Errorf("sandbox policy is stale: configured policy is %s, but recorded policy is %s; %s", configured, applied, remedy)
	case policyUnverifiedLegacy:
		return fmt.Errorf("sandbox policy is unverified: the legacy VM snapshot does not record its applied enforcement mode; %s", remedy)
	case policyUnverifiedMissing:
		return fmt.Errorf("sandbox policy is unverified: the VM has no applied-policy snapshot; %s", remedy)
	case policyComparisonUnavailable:
		return fmt.Errorf("sandbox policy cannot be compared with the configured policy; %s", remedy)
	default:
		return fmt.Errorf("sandbox policy is unverified: cannot read its applied-policy snapshot (%v); %s", assessment.Err, remedy)
	}
}

func requireCurrentAppliedPolicyAndStopUnsafe(dir, vmName string, status lima.VMStatus, cfg *config.Config, nameExplicit ...bool) error {
	policyErr := requireCurrentAppliedPolicyForVM(dir, vmName, status, cfg, nameExplicit...)
	if policyErr == nil || status == lima.StatusNotFound || status == lima.StatusStopped {
		return policyErr
	}
	if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
		return errors.Join(policyErr, fmt.Errorf("the existing VM was not stopped because its project identity could not be re-verified: %w", bindErr))
	}
	if stopErr := cliStopVM(vmName); stopErr != nil {
		return errors.Join(policyErr, fmt.Errorf("stopping the existing VM failed: %w", stopErr))
	}
	return fmt.Errorf("%w; the existing VM was stopped so its unverified or stale policy cannot keep sending traffic", policyErr)
}

func requireVMProjectBinding(dir, vmName string) error {
	instance, identityErr := loadNamedVMIdentity(vmName)
	if identityErr == nil {
		if err := verifyNamedVMIdentityOwner(instance.Identity, dir); err != nil {
			return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
		}
		if err := requireNamedVMIdentityMount(instance); err != nil {
			return err
		}
		if !instance.Identity.MountProject {
			return nil
		}
		// A registered mounted VM must satisfy both the durable identity and
		// the original project-mount binding.
		if err := requireLegacyProjectMountBinding(dir, vmName); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(identityErr, os.ErrNotExist) {
		return fmt.Errorf("cannot verify the registered identity for VM %q: %w", vmName, identityErr)
	}
	if vmName != derivedVMName(dir) {
		return fmt.Errorf("refusing to use custom-named VM %q without a valid Watermelon identity record", vmName)
	}
	return requireLegacyProjectMountBinding(dir, vmName)
}

func requireNamedVMIdentityMount(instance namedVMInstanceIdentity) error {
	vmName := instance.Identity.VMName
	mount, err := cliInstanceMount(vmName, "/mnt/watermelon/bootstrap")
	if err != nil {
		return fmt.Errorf("cannot verify the identity mount for VM %q: %w", vmName, err)
	}
	if mount.Writable {
		return fmt.Errorf("refusing to use VM %q: its identity bootstrap mount is writable", vmName)
	}
	canonicalBootstrap, err := canonicalizeHostPath("VM bootstrap mount source", mount.Location)
	if err != nil {
		return fmt.Errorf("cannot verify the identity mount for VM %q: %w", vmName, err)
	}
	expectedBootstrap, err := canonicalizeHostPath("registered VM bootstrap directory", instance.Paths.BootstrapDir)
	if err != nil {
		return fmt.Errorf("cannot verify the registered identity for VM %q: %w", vmName, err)
	}
	if canonicalBootstrap != expectedBootstrap {
		return fmt.Errorf("refusing to use VM %q: its identity mount is %q, not the registered bootstrap %q", vmName, canonicalBootstrap, expectedBootstrap)
	}
	return nil
}

func requireLegacyProjectMountBinding(dir, vmName string) error {
	recordedSource, err := cliProjectMountSource(vmName)
	if err != nil {
		return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
	}
	canonicalSource, err := canonicalizeHostPath("VM /project mount source", recordedSource)
	if err != nil {
		return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
	}
	canonicalDir, err := canonicalProjectRoot(dir)
	if err != nil {
		return err
	}
	if canonicalSource != canonicalDir {
		return fmt.Errorf("refusing to use VM %q: its /project mount is %q, not the resolved project %q", vmName, canonicalSource, canonicalDir)
	}
	return nil
}

func requireRuntimePolicyApplied(dir, vmName string, newlyCreated bool, nameExplicit ...bool) error {
	recreate := recreatePolicyCommandForVM(dir, vmName, nameExplicit...)
	if err := cliVerifyPolicy(vmName); err != nil {
		if !newlyCreated {
			return fmt.Errorf("sandbox provisioning is not complete: %w; run '%s' to retry provisioning, or '%s' if the problem persists", err, restartPolicyCommandForVM(dir, vmName, nameExplicit...), recreate)
		}
		return fmt.Errorf("sandbox provisioning is not complete: %w; run '%s' to recreate the VM and retry provisioning", err, recreate)
	}
	return nil
}

func requireRuntimePolicyAppliedAndStopUnsafe(dir, vmName string, newlyCreated bool, nameExplicit ...bool) error {
	policyErr := requireRuntimePolicyApplied(dir, vmName, newlyCreated, nameExplicit...)
	if policyErr == nil {
		return nil
	}
	status := cliGetVMStatus(vmName)
	if status == lima.StatusNotFound || status == lima.StatusStopped {
		return policyErr
	}
	if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
		return errors.Join(policyErr, fmt.Errorf("the existing VM was not stopped because its project identity could not be re-verified: %w", bindErr))
	}
	if stopErr := cliStopVM(vmName); stopErr != nil {
		return errors.Join(policyErr, fmt.Errorf("stopping the VM after its policy readiness check failed: %w", stopErr))
	}
	return fmt.Errorf("%w; the VM was stopped because its provisioning completion could not be verified", policyErr)
}

func warnIfNonStrictPolicy(out io.Writer, cfg *config.Config) {
	descriptor, ok := config.LookupEnforcement(cfg.Security.Enforcement)
	if ok && !descriptor.BlocksUnknown {
		fmt.Fprintf(out, "Warning: configured network policy is %s; connections outside the allowlist are allowed.\n", config.DescribeEnforcement(descriptor.Mode))
	}
}

func saveAppliedPolicySnapshot(dir string, cfg *config.Config) error {
	return saveAppliedPolicySnapshotForVM(dir, derivedVMName(dir), cfg)
}

func saveAppliedPolicySnapshotForVM(dir, vmName string, cfg *config.Config) error {
	host, err := resolveAppliedPolicyHostContextForVM(dir, cfg, vmName)
	if err != nil {
		return err
	}
	return saveAppliedPolicySnapshotWithHost(host, cfg)
}

func saveAppliedPolicySnapshotWithHost(host appliedPolicyHostContext, cfg *config.Config) error {
	snapshot, err := config.NewAppliedPolicySnapshot(cfg, host.Recorded)
	if err != nil {
		return err
	}
	data, err := config.MarshalAppliedPolicySnapshot(snapshot)
	if err != nil {
		return err
	}

	if err := secureAppliedPolicyStateDirectories(host, true); err != nil {
		return err
	}
	return replacePrivateStateFile(host.SnapshotPath, "applied-policy snapshot", data, maxAppliedPolicySnapshotFileSize)
}

func clearAppliedPolicySnapshot(dir string) error {
	return clearAppliedPolicySnapshotForVM(dir, derivedVMName(dir))
}

func clearAppliedPolicySnapshotForVM(dir, vmName string) error {
	host, err := resolveAppliedPolicyHostContextForVM(dir, nil, vmName)
	if err != nil {
		return err
	}
	if err := secureAppliedPolicyStateDirectories(host, false); err != nil {
		return err
	}
	if info, err := os.Lstat(host.SnapshotPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to remove non-regular applied-policy snapshot %q", host.SnapshotPath)
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("refusing to remove applied-policy snapshot %q not owned by the current user", host.SnapshotPath)
		}
	} else if os.IsNotExist(err) {
		return nil
	} else {
		return fmt.Errorf("inspecting applied-policy snapshot %q: %w", host.SnapshotPath, err)
	}
	if err := os.Remove(host.SnapshotPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func secureAppliedPolicyStateDirectories(host appliedPolicyHostContext, create bool) error {
	if create {
		if err := os.MkdirAll(host.StateDir, 0700); err != nil {
			return fmt.Errorf("creating applied-policy state directory %q: %w", host.StateDir, err)
		}
		if err := validateTrustedDirectoryIfPresent("user config directory", host.UserConfig); err != nil {
			return err
		}
	}
	for _, path := range []string{host.AppStateRoot, filepath.Join(host.AppStateRoot, "vms"), host.StateDir} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && !create {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspecting applied-policy state directory %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("applied-policy state path %q must be a real directory, not a symlink or non-directory", path)
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("applied-policy state directory %q is not owned by the current user", path)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("securing applied-policy state directory %q: %w", path, err)
		}
	}
	return nil
}

const maxAppliedPolicySnapshotFileSize = 4 << 20

func readAppliedPolicySnapshotFile(path string) ([]byte, error) {
	return readPrivateStateFile(path, "applied-policy snapshot", maxAppliedPolicySnapshotFileSize)
}
