package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

var (
	cliExecVM           = lima.Exec
	cliExecVMWithRunner = lima.ExecWithRunner
)

func NewExecCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "exec [command] [args...]",
		Short: "Run a command in the sandbox without interactive shell",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if err := requireCompatibleLima(); err != nil {
				return err
			}
			lifecycleLock, err := acquireVMLifecycleLock(vmName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
			}
			defer lifecycleLock.Release()
			status := cliGetVMStatus(vmName)
			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found (run 'watermelon run' first)")
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

			var terminalCoordinator askTerminalCoordinator
			if cfg.Security.Enforcement == config.EnforcementAsk {
				terminalCoordinator = cliNewAskCoordinator()
				verdictListener, err := cliStartAskServer(dir, vmName, terminalCoordinator.Dialog)
				if err != nil {
					return err
				}
				defer verdictListener.Close()
				fmt.Println("Verdict server listening for network policy prompts...")
			}

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
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}
			usageLease, err := acquireSharedVMUsageLease(vmName)
			if err != nil {
				return fmt.Errorf("leasing VM %q for the guest command: %w", vmName, err)
			}
			// Hand off from the exclusive lifecycle mutex to a shared usage
			// lease without a name-reuse gap. Stop remains able to terminate a
			// long-running command, while destroy waits for limactl to detach
			// before cleaning identity state or reusing the public VM name.
			if err := lifecycleLock.Release(); err != nil {
				return errors.Join(err, usageLease.Release())
			}
			var execErr error
			if terminalCoordinator != nil {
				execErr = cliExecVMWithRunner(vmName, args, terminalCoordinator, target.Workdir)
			} else {
				execErr = cliExecVM(vmName, args, target.Workdir)
			}
			return finishExecCommand(cmd, execErr, usageLease.Release())
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the name derived from the resolved project root)")
	// Once the guest command starts, every remaining argument belongs to that
	// command. In particular, flags such as `docker run --name web` must not be
	// consumed as Watermelon flags. Watermelon flags therefore have to precede
	// the command; an optional `--` separator is also accepted.
	cmd.Flags().SetInterspersed(false)

	return cmd
}

type guestExitCoder interface {
	GuestExitCode() int
}

// finishExecCommand keeps a clean guest result as the top-level error so main
// can propagate its status. Usage-lease cleanup failures are Watermelon-owned
// failures and deliberately take the ordinary CLI error path instead.
func finishExecCommand(cmd *cobra.Command, execErr, leaseErr error) error {
	if leaseErr != nil {
		return errors.Join(execErr, leaseErr)
	}
	if guestErr, ok := execErr.(guestExitCoder); ok {
		code := guestErr.GuestExitCode()
		if code >= 1 && code <= 255 {
			// A non-zero guest status is the requested command's result, not a
			// Watermelon invocation error. Keep Cobra from adding error/usage
			// noise to the guest's own output.
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
		}
	}
	return execErr
}
