package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sandbox VM status for current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			vmName := lima.VMNameFromPath(dir)
			status := lima.GetStatus(vmName)

			fmt.Printf("Project: %s\n", dir)
			fmt.Printf("VM Name: %s\n", vmName)
			fmt.Printf("Status:  %s\n", status)

			return nil
		},
	}
}
