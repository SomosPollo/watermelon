package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the project sandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			vmName := lima.VMNameFromPath(dir)
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
}
