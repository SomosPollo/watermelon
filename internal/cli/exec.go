package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/ask"
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
			dir, err = canonicalProjectRoot(dir)
			if err != nil {
				return err
			}

			cfg, err := loadValidatedProjectConfigFailClosed(dir)
			if err != nil {
				return err
			}

			vmName := lima.VMNameFromPath(dir)
			status := cliGetVMStatus(vmName)
			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found (run 'watermelon run' first)")
			}
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}
			warnIfNonStrictPolicy(os.Stderr, cfg)
			if err := requireCurrentAppliedPolicyAndStopUnsafe(dir, vmName, status, cfg); err != nil {
				return err
			}
			if status == lima.StatusUnknown {
				return fmt.Errorf("cannot safely use VM %q because its Lima state is unknown", vmName)
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

			if status == lima.StatusStopped {
				if err := requireVMProjectBinding(dir, vmName); err != nil {
					return err
				}
				fmt.Println("Starting sandbox VM...")
				if err := startVMFailClosed(dir, vmName, ""); err != nil {
					return err
				}
			}
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}
			if err := requireRuntimePolicyAppliedAndStopUnsafe(dir, vmName, false); err != nil {
				return err
			}
			if err := requireVMProjectBinding(dir, vmName); err != nil {
				return err
			}

			return lima.Exec(vmName, args)
		},
	}
}
