package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/logs"
	"github.com/spf13/cobra"
)

var cliGetVMStatusWithError = lima.GetStatusWithError

func NewStatusCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sandbox VM status for current project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			return runStatusForName(os.Stdout, dir, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the path-derived name)")
	return cmd
}

func runStatus(out io.Writer, dir string) error {
	return runStatusForName(out, dir, "")
}

func runStatusForName(out io.Writer, dir, name string) error {
	target, err := resolveManagementTarget(dir, name)
	if err != nil {
		return err
	}
	if target.Config != nil {
		target, err = prepareTargetProvisionScripts(target)
		if err != nil {
			return fmt.Errorf("preparing provision scripts for status: %w", err)
		}
	}
	dir = target.ProjectRoot
	vmName := target.VMName
	lifecycleLock, err := acquireVMLifecycleLock(vmName)
	if err != nil {
		return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
	}
	defer lifecycleLock.Release()
	status, err := cliGetVMStatusWithError(vmName)
	if err != nil {
		return fmt.Errorf("reading Lima VM state: %w; run 'watermelon doctor' for installation guidance", err)
	}
	var staleIdentity *namedVMInstanceIdentity
	if status == lima.StatusNotFound {
		instance, identityErr := loadOwnedNamedVMIdentity(dir, vmName)
		if identityErr == nil {
			staleIdentity = &instance
		} else if !errors.Is(identityErr, os.ErrNotExist) {
			return fmt.Errorf("loading stale VM identity for %q: %w", vmName, identityErr)
		}
	}
	if status != lima.StatusNotFound {
		if err := requireVMProjectBinding(dir, vmName); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "Project:  %s\n", dir)
	fmt.Fprintf(out, "VM Name:  %s\n", vmName)
	fmt.Fprintf(out, "Status:   %s\n", status)
	if status != lima.StatusNotFound {
		fmt.Fprintf(out, "SSH Host: %s\n", lima.GetSSHHost(vmName))
	}

	cfg := target.Config
	if cfg == nil {
		assessment := assessAppliedPolicyForVM(dir, vmName, status, nil)
		fmt.Fprintln(out, "Config:   missing (.watermelon.toml not found; run 'watermelon init')")
		fmt.Fprintln(out, "Configured Policy: unavailable (config missing)")
		if staleIdentity != nil {
			fmt.Fprintf(out, "Next:     %s; then watermelon init\n", destroyPolicyCommandForVM(dir, vmName, target.NameExplicit))
		} else if status == lima.StatusNotFound {
			fmt.Fprintln(out, "Next:     watermelon init")
		}
		fmt.Fprintf(out, "Applied Policy:    %s\n", formatAppliedPolicy(assessment))
		return nil
	}
	assessment := assessAppliedPolicyForVM(dir, vmName, status, cfg)
	fmt.Fprintf(out, "Config:   %s\n", configSnapshotStatus(assessment))
	fmt.Fprintf(out, "Configured Policy: %s\n", config.DescribeEnforcement(cfg.Security.Enforcement))
	fmt.Fprintf(out, "Applied Policy:    %s\n", formatAppliedPolicy(assessment))
	fmt.Fprintf(out, "Network:  %s, %s\n",
		countLabel(len(cfg.Network.Allow), "allow rule", "allow rules"),
		countLabel(len(cfg.Network.Process), "process rule", "process rules"),
	)
	fmt.Fprintf(out, "Tools:    %s\n", formatTools(cfg.Tools))
	fmt.Fprintf(out, "Ports:    %s\n", formatPorts(cfg.Ports.Forward))
	fmt.Fprintf(out, "Resources: %s memory, %s, %s disk\n",
		cfg.Resources.Memory,
		countLabel(cfg.Resources.CPUs, "CPU", "CPUs"),
		cfg.Resources.Disk,
	)
	logPath := logs.LogPath(dir)
	if staleIdentity != nil {
		logPath = staleIdentity.Paths.GuestNetworkLogPath
	} else if status != lima.StatusNotFound {
		resolved, err := resolvedNetworkLogPath(target)
		if err != nil {
			return err
		}
		logPath = resolved
	}
	fmt.Fprintf(out, "Logs:     %s\n", logStatusPath(logPath))
	if staleIdentity != nil {
		fmt.Fprintf(out, "Next:     %s\n", recreatePolicyCommandForVM(dir, vmName, target.NameExplicit))
	} else if status == lima.StatusNotFound {
		fmt.Fprintf(out, "Next:     %s\n", runPolicyCommandForVM(dir, vmName, target.NameExplicit))
	} else if policyRequiresRecreation(assessment.State) {
		fmt.Fprintf(out, "Next:     %s\n", recreatePolicyCommandForVM(dir, vmName, target.NameExplicit))
	}

	return nil
}

func configSnapshotStatus(assessment appliedPolicyAssessment) string {
	switch assessment.State {
	case policyNotApplied:
		return "valid (not yet applied)"
	case policyCurrent:
		return "current"
	case policyStale:
		return "changed since VM creation"
	case policyUnverifiedLegacy:
		return "valid (legacy VM policy is unverified)"
	case policyUnverifiedMissing:
		return "valid (VM policy snapshot missing)"
	case policyComparisonUnavailable:
		return "valid (could not compare configured and applied policy)"
	default:
		return "valid (VM policy snapshot unreadable)"
	}
}

func formatAppliedPolicy(assessment appliedPolicyAssessment) string {
	switch assessment.State {
	case policyNotApplied:
		return "none (VM not created)"
	case policyCurrent:
		return config.DescribeEnforcement(assessment.Snapshot.Enforcement) + " (recorded, current)"
	case policyStale:
		return config.DescribeEnforcement(assessment.Snapshot.Enforcement) + " (recorded, stale; differs from current configuration)"
	case policyUnverifiedLegacy:
		return "unverified (legacy snapshot does not record enforcement)"
	case policyUnverifiedMissing:
		return "unverified (applied-policy snapshot missing)"
	case policyComparisonUnavailable:
		return config.DescribeEnforcement(assessment.Snapshot.Enforcement) + " (recorded; current configuration unavailable for comparison)"
	default:
		return fmt.Sprintf("unverified (applied-policy snapshot unreadable: %v)", assessment.Err)
	}
}

func policyRequiresRecreation(state appliedPolicyState) bool {
	switch state {
	case policyStale, policyUnverifiedLegacy, policyUnverifiedMissing, policyUnverifiedInvalid:
		return true
	default:
		return false
	}
}

func formatTools(tools map[string][]string) string {
	if len(tools) == 0 {
		return "none"
	}

	images := make([]string, 0, len(tools))
	for image := range tools {
		images = append(images, image)
	}
	sort.Strings(images)

	parts := make([]string, 0, len(images))
	for _, image := range images {
		commands := append([]string(nil), tools[image]...)
		sort.Strings(commands)
		parts = append(parts, fmt.Sprintf("%s [%s]", image, strings.Join(commands, ", ")))
	}
	return strings.Join(parts, "; ")
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return "none"
	}

	values := append([]int(nil), ports...)
	sort.Ints(values)

	parts := make([]string, 0, len(values))
	for _, port := range values {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ", ")
}

func logStatus(dir string) string {
	return logStatusPath(logs.LogPath(dir))
}

func logStatusPath(path string) string {
	lines, err := logs.ReadPath(path)
	if err != nil {
		return fmt.Sprintf("unavailable (%v)", err)
	}
	return countLabel(len(lines), "entry", "entries")
}

func countLabel(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}
