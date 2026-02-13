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
	rootCmd.AddCommand(cli.NewViolationsCmd())
	rootCmd.AddCommand(cli.NewCodeCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
