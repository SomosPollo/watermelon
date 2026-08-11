package main

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/cli"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "watermelon",
	Short:   "Sandbox that isolates your project inside a Linux VM",
	Long:    "Watermelon is a sandbox that isolates your project inside a Linux VM.",
	Version: Version,
}

func init() {
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.AddCommand(cli.NewInitCmd())
	rootCmd.AddCommand(cli.NewRunCmd())
	rootCmd.AddCommand(cli.NewStopCmd())
	rootCmd.AddCommand(cli.NewDestroyCmd())
	rootCmd.AddCommand(cli.NewStatusCmd())
	rootCmd.AddCommand(cli.NewExecCmd())
	rootCmd.AddCommand(cli.NewListCmd())
	rootCmd.AddCommand(cli.NewLogsCmd())
	rootCmd.AddCommand(cli.NewCodeCmd())
	rootCmd.AddCommand(cli.NewCopyCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		if code, ok := exitCodeForError(err); ok {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Keep this interface specific to guest execution. In particular,
// *exec.ExitError also has an ExitCode method and is returned by Watermelon's
// own subprocesses, so matching a generic exit-code interface would be unsafe.
type guestExitCoder interface {
	GuestExitCode() int
}

func exitCodeForError(err error) (int, bool) {
	guestErr, ok := err.(guestExitCoder)
	if !ok {
		return 1, false
	}
	code := guestErr.GuestExitCode()
	if code < 1 || code > 255 {
		return 1, false
	}
	return code, true
}
