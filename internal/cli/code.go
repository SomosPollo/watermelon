package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewCodeCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "code",
		Short: "Open project in IDE (VS Code by default)",
		Long:  "Launch your IDE connected to the sandbox VM via SSH. Configure with [ide] command in .watermelon.toml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodeWithName(name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the path-derived name)")
	return cmd
}

func runCode() error {
	return runCodeWithName("")
}

func runCodeWithName(name string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target, err := resolveConfiguredTarget(dir, name)
	if err != nil {
		return err
	}
	dir = target.ProjectRoot
	cfg := target.Config
	vmName := target.VMName
	lifecycleLock, err := acquireVMLifecycleLock(vmName)
	if err != nil {
		return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
	}
	defer lifecycleLock.Release()
	status := cliGetVMStatus(vmName)

	// Check VM exists
	if status == lima.StatusNotFound {
		return fmt.Errorf("sandbox not found. Run 'watermelon run' first to create it")
	}
	if err := requireVMProjectBinding(dir, vmName); err != nil {
		return err
	}
	warnIfNonStrictPolicy(os.Stderr, cfg)
	if err := requireCurrentAppliedPolicyAndStopUnsafe(dir, vmName, status, cfg, target.NameExplicit); err != nil {
		return err
	}
	if status == lima.StatusUnknown {
		return fmt.Errorf("cannot safely use VM %q because its Lima state is unknown", vmName)
	}
	if cfg.Security.Enforcement == config.EnforcementAsk {
		verdictListener, err := startAskVerdictServerForExistingVM(dir, vmName)
		if err != nil {
			return err
		}
		defer verdictListener.Close()
		fmt.Println("Verdict server listening for network policy prompts while the IDE is open...")
	}

	// Start VM if stopped
	if status == lima.StatusStopped {
		if err := requireVMProjectBinding(dir, vmName); err != nil {
			return err
		}
		fmt.Println("Starting sandbox VM...")
		if err := startVMFailClosed(dir, vmName, ""); err != nil {
			return err
		}
	}
	if err := requireVMProjectBinding(dir, vmName); err != nil {
		return err
	}
	if err := requireRuntimePolicyAppliedAndStopUnsafe(dir, vmName, false, target.NameExplicit); err != nil {
		return err
	}

	// Ensure SSH config is set up
	if err := lima.EnsureSSHConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
	}

	// Get IDE command from config (default: "code")
	ideCmd := cfg.IDE.Command
	if ideCmd == "" {
		ideCmd = "code"
	}

	sshHost := lima.GetSSHHost(vmName)
	cmd, args := buildIDECommand(ideCmd, sshHost, target.IDEWorkdir)

	fmt.Printf("Opening %s...\n", ideCmd)

	// Check if IDE command exists
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("%s not found. Install it or set ide.command in .watermelon.toml", cmd)
	}

	// Keep the launcher in the foreground. VS Code-family CLIs honor --wait,
	// which lets Watermelon retain the instance lease (and ask-mode verdict
	// server) until the remote window closes.
	execCmd := exec.Command(cmd, args...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	if err := requireVMProjectBinding(dir, vmName); err != nil {
		return err
	}
	usageLease, err := acquireSharedVMUsageLease(vmName)
	if err != nil {
		return fmt.Errorf("leasing VM %q for the IDE session: %w", vmName, err)
	}
	if err := lifecycleLock.Release(); err != nil {
		return errors.Join(err, usageLease.Release())
	}
	runErr := execCmd.Run()
	leaseErr := usageLease.Release()
	if runErr != nil {
		return errors.Join(fmt.Errorf("launching %s: %w", cmd, runErr), leaseErr)
	}
	return leaseErr
}

// buildIDECommand returns the command and arguments to launch the IDE
func buildIDECommand(ideCmd, sshHost, workdir string) (string, []string) {
	args := []string{
		"--wait",
		"--remote",
		"ssh-remote+" + sshHost,
	}
	if workdir != "" {
		args = append(args, workdir)
	}
	return ideCmd, args
}
