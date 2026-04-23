package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewExecCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "exec [command] [args...]",
		Short: "Run a command in the sandbox without interactive shell",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			configIsDefault := false
			cfg, err := loadProjectConfig(dir)
			if err != nil {
				if name != "" {
					cfg = config.NewConfig()
					configIsDefault = true
				} else {
					return err
				}
			}

			if err := config.Validate(cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			// Start verdict server for ask enforcement mode
			var verdictListener net.Listener
			if cfg.Security.Enforcement == "ask" {
				if err := ensureNfqdBinary(dir); err != nil {
					return fmt.Errorf("building nfqd: %w", err)
				}

				savedPort := readSavedPort(dir)
				if savedPort == 0 {
					return fmt.Errorf("no verdict server port found; run 'watermelon run' first to create the VM with ask mode")
				}

				listenAddr := fmt.Sprintf("0.0.0.0:%d", savedPort)
				var listenErr error
				verdictListener, listenErr = net.Listen("tcp", listenAddr)
				if listenErr != nil {
					return fmt.Errorf("starting verdict server on port %d: %w", savedPort, listenErr)
				}
				defer verdictListener.Close()

				configPath := filepath.Join(dir, ".watermelon.toml")
				project := filepath.Base(dir)
				srv := ask.NewServer(project, configPath, ask.ShowDialog)
				go srv.Serve(verdictListener)
				fmt.Println("Verdict server listening for network policy prompts...")
			}

			vmName := resolveVMNameFromConfig(name, cfg.VM.Name, dir)
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

			var effectiveWorkdir string
			if !configIsDefault {
				effectiveWorkdir = cfg.IDE.Workdir
				if effectiveWorkdir == "" {
					effectiveWorkdir = config.DefaultWorkdir(cfg)
				}
			}

			return lima.Exec(vmName, args, effectiveWorkdir)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides path-derived name and vm.name config)")
	return cmd
}
