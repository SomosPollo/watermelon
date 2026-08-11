package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultConfig = `# Watermelon sandbox configuration
# See: https://github.com/saeta-eth/watermelon

[vm]
image = "ubuntu-22.04"
# name = "my-project-vm"       # Optional stable Lima instance name
# mount_project = true         # Set false to keep the host project out of the VM
# workdir = "/project"         # Shell/exec directory; guest home when unset without a mount

[network]
# Domains allowed by policy. Connections to other domains follow [security].
allow = [
    # "registry.npmjs.org",
    # "github.com",
]

[tools]
# Tools run as containers - format: "image:tag" = ["cmd1", "cmd2", ...]
# "node:20-slim" = ["node", "npm", "npx"]
# "python:3.12-slim" = ["python", "python3", "pip"]

[provision]
# Project-relative host scripts embedded into the Lima configuration and run
# as root. They must be safe to repeat because Lima may reprovision the VM.
scripts = []

[mounts]
# Additional host paths to mount (read-only by default). Guest targets must
# stay under /mnt/watermelon so mounts cannot shadow system, home, or project paths.
# "~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }

[ports]
# Ports to forward from VM to host
forward = []

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"

[security]
# How to enforce network policy: "log", "fail", "silent", or "ask"
# "fail" is the strict default: block and log connections outside the allowlist.
# "log" is non-strict: it records unknown connections but allows them.
enforcement = "fail"

[ide]
# IDE command for 'watermelon code' (e.g., "code", "cursor", "codium")
command = "code"
# workdir = "/project"         # Optional IDE-only remote directory override
`

func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .watermelon.toml in the current directory",
		Args:  cobra.NoArgs,
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
	configPath := filepath.Join(dir, projectConfigName)

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
