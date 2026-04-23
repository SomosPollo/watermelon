package cli

import (
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(name)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides path-derived name and vm.name config)")
	return cmd
}

func runCode(name string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := loadProjectConfig(dir)
	if err != nil {
		if name != "" {
			cfg = config.NewConfig()
		} else {
			return err
		}
	}

	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	vmName := resolveVMNameFromConfig(name, cfg.VM.Name, dir)
	status := lima.GetStatus(vmName)

	if status == lima.StatusNotFound {
		return fmt.Errorf("sandbox not found. Run 'watermelon run' first to create it")
	}

	if status == lima.StatusStopped {
		fmt.Println("Starting sandbox VM...")
		if err := lima.Start(vmName, ""); err != nil {
			return fmt.Errorf("starting VM: %w", err)
		}
	}

	if err := lima.EnsureSSHConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
	}

	ideCmd := cfg.IDE.Command
	if ideCmd == "" {
		ideCmd = "code"
	}

	sshHost := lima.GetSSHHost(vmName)
	cmd, args := buildIDECommand(ideCmd, sshHost)

	fmt.Printf("Opening %s...\n", ideCmd)

	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("%s not found. Install it or set ide.command in .watermelon.toml", cmd)
	}

	execCmd := exec.Command(cmd, args...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	if err := execCmd.Start(); err != nil {
		return fmt.Errorf("launching %s: %w", cmd, err)
	}

	return nil
}

// buildIDECommand returns the command and arguments to launch the IDE
func buildIDECommand(ideCmd, sshHost string) (string, []string) {
	return ideCmd, []string{
		"--remote",
		"ssh-remote+" + sshHost,
		"/project",
	}
}
