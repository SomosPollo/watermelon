package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewStopCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the project sandbox VM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			dir, err = canonicalProjectRoot(dir)
			if err != nil {
				return err
			}
			if name != "" {
				if err := config.ValidateVMName(name); err != nil {
					return fmt.Errorf("invalid --name %q: %w", name, err)
				}
			}
			target, err := resolveManagementTarget(dir, name)
			if err != nil {
				// Stopping is the fail-closed recovery path. A malformed or
				// unreadable config must not leave a verified project-owned VM
				// running merely because its normal target cannot be parsed.
				return stopBoundVMForConfigErrorForTarget(dir, name, err)
			}
			dir = target.ProjectRoot
			vmName := target.VMName
			lifecycleLock, err := acquireVMLifecycleLock(vmName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
			}
			defer lifecycleLock.Release()
			status := cliGetVMStatus(vmName)

			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found for this project")
			}
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}

			if status == lima.StatusStopped {
				fmt.Println("Sandbox VM is already stopped")
				return nil
			}

			fmt.Println("Stopping sandbox VM...")
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}
			// Stopping must remain the immediate fail-closed escape hatch even
			// while shells or IDEs hold shared usage leases. Those leases protect
			// deletion and name reuse; stopping the VM terminates the sessions and
			// lets them release their leases.
			return cliStopVM(vmName)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the path-derived name)")
	return cmd
}
