package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

var (
	destroyGetStatus = lima.GetStatus
	destroyDelete    = lima.Delete
)

func NewDestroyCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy the project sandbox VM and all its state",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			dir, err = canonicalProjectRoot(dir)
			if err != nil {
				return err
			}

			vmName := lima.VMNameFromPath(dir)
			status := destroyGetStatus(vmName)

			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found for this project")
			}
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}

			if !force {
				fmt.Printf("This will delete VM '%s' and all installed dependencies.\n", vmName)
				fmt.Print("Are you sure? [y/N] ")
				reader := bufio.NewReader(cmd.InOrStdin())
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			fmt.Println("Destroying sandbox VM...")
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}
			if err := destroyDelete(vmName); err != nil {
				return err
			}
			if err := clearAppliedPolicySnapshot(dir); err != nil {
				return fmt.Errorf("VM was destroyed, but its applied-policy snapshot could not be removed: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	return cmd
}
