package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/logs"
	"github.com/spf13/cobra"
)

func NewLogsCmd() *cobra.Command {
	var clear bool
	var name string

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show network policy logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			target, err := resolveManagementTarget(dir, name)
			if err != nil {
				return err
			}
			dir = target.ProjectRoot
			lifecycleLock, err := acquireVMLifecycleLock(target.VMName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle: %w", target.VMName, err)
			}
			defer lifecycleLock.Release()
			logPath, err := resolvedNetworkLogPath(target)
			if err != nil {
				return err
			}

			if clear {
				return logs.ClearPath(logPath)
			}

			lines, err := logs.ReadPath(logPath)
			if err != nil {
				return err
			}

			if len(lines) == 0 {
				fmt.Println("No logs recorded")
				return nil
			}

			for _, line := range lines {
				fmt.Println(line)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&clear, "clear", false, "Clear the log")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the name derived from the resolved project root)")
	return cmd
}

func resolvedNetworkLogPath(target targetContext) (string, error) {
	logPath := logs.LogPath(target.ProjectRoot)
	instance, err := loadOwnedNamedVMIdentity(target.ProjectRoot, target.VMName)
	if err == nil {
		return instance.Paths.GuestNetworkLogPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolving logs for VM %q: %w", target.VMName, err)
	}
	if target.VMName != lima.VMNameFromPath(target.ProjectRoot) {
		return "", fmt.Errorf("refusing to read logs for custom-named VM %q without a valid Watermelon identity record", target.VMName)
	}
	return logPath, nil
}
