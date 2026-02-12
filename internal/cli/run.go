package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Enter the project sandbox VM",
		Long:  "Start the project VM (creating it if needed) and open an interactive shell.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun()
		},
	}
}

func runRun() error {
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
		fmt.Println("Creating sandbox VM...")

		// Setup SSH config for IDE access
		if err := lima.EnsureSSHConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
		}

		yamlContent, err := lima.GenerateConfig(cfg, dir)
		if err != nil {
			return fmt.Errorf("generating Lima config: %w", err)
		}

		// Write temp Lima config
		tmpFile, err := os.CreateTemp("", "watermelon-*.yaml")
		if err != nil {
			return fmt.Errorf("creating temp config file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString(yamlContent); err != nil {
			tmpFile.Close()
			return err
		}
		tmpFile.Close()

		if err := lima.Start(vmName, tmpFile.Name()); err != nil {
			return fmt.Errorf("starting VM: %w", err)
		}
	} else if status == lima.StatusStopped {
		fmt.Println("Starting sandbox VM...")
		if err := lima.Start(vmName, ""); err != nil {
			return fmt.Errorf("starting VM: %w", err)
		}
	}

	sshHost := lima.GetSSHHost(vmName)
	fmt.Printf("IDE: connect to %s\n", sshHost)
	fmt.Println()

	return lima.Shell(vmName)
}

func loadProjectConfig(dir string) (*config.Config, error) {
	configPath := filepath.Join(dir, ".watermelon.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no .watermelon.toml found (run 'watermelon init' first)")
	}
	return config.ParseFile(configPath)
}
