package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewStopCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the project sandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			vmName := resolveVMName(name, dir)
			status := lima.GetStatus(vmName)

			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found for this project")
			}

			if status == lima.StatusStopped {
				fmt.Println("Sandbox VM is already stopped")
				return nil
			}

			fmt.Println("Stopping sandbox VM...")
			return lima.Stop(vmName)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides path-derived name and vm.name config)")
	return cmd
}
