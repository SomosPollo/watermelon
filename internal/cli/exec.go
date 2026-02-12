package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [command] [args...]",
		Short: "Run a command in the sandbox without interactive shell",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			cfg, err := loadProjectConfig(dir)
			if err != nil {
				return err
			}

			if err := config.Validate(cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			vmName := lima.VMNameFromPath(dir)
			status := lima.GetStatus(vmName)

			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found (run 'watermelon run' first)")
			}

			if status == lima.StatusStopped {
				fmt.Println("Starting sandbox VM...")
				if err := lima.Start(vmName, ""); err != nil {
					return fmt.Errorf("starting VM: %w", err)
				}
			}

			return lima.Exec(vmName, args)
		},
	}
}
