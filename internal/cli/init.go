package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultConfig = `# Watermelon sandbox configuration
# See: https://github.com/saeta/watermelon

[vm]
image = "ubuntu-22.04"

[network]
# Allowlisted domains (all other network access blocked)
allow = [
    # "registry.npmjs.org",
    # "github.com",
]

[tools]
# Tools run as containers - format: "image:tag" = ["cmd1", "cmd2", ...]
# "node:20-slim" = ["node", "npm", "npx"]
# "python:3.12-slim" = ["python", "python3", "pip"]

[mounts]
# Additional host paths to mount (read-only by default)
# "~/.gitconfig" = { target = "/home/dev/.gitconfig" }

[ports]
# Ports to forward from VM to host
forward = []

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"

[security]
# What to do on policy violations: "log", "fail", or "silent"
on_violation = "log"

[ide]
# IDE command for 'watermelon code' (e.g., "code", "cursor", "codium")
command = "code"
`

func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a .watermelon.toml config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			return runInit(dir)
		},
	}
}

func runInit(dir string) error {
	configPath := filepath.Join(dir, ".watermelon.toml")

	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf(".watermelon.toml already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Created %s\n", configPath)
	return nil
}
