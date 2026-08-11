package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/saeta-eth/watermelon/internal/cli"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags
var Version = "dev"

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "watermelon",
		Short:         "Sandbox that isolates your project inside a Linux VM",
		Long:          "Watermelon is a sandbox that isolates your project inside a Linux VM.",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}

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
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return cli.NewUsageError(err)
	})
	return rootCmd
}

func configureGeneratedCommands(rootCmd *cobra.Command) {
	rootCmd.InitDefaultHelpCmd()
	var helpCmd *cobra.Command
	for _, candidate := range rootCmd.Commands() {
		if candidate.Name() == "help" {
			helpCmd = candidate
			break
		}
	}
	if helpCmd == nil {
		panic("Cobra did not initialize the help command")
	}

	// Cobra's generated help command prints an unknown-topic message and exits
	// successfully. Return a normal invocation error instead so help failures
	// use the same top-level formatting and exit behavior as every other command.
	helpCmd.Run = nil
	helpCmd.RunE = func(cmd *cobra.Command, args []string) error {
		target, remaining, err := cmd.Root().Find(args)
		if err != nil || target == nil || len(remaining) != 0 {
			return cli.NewUsageError(fmt.Errorf("unknown help topic %q", strings.Join(args, " ")))
		}
		target.SetContext(cmd.Context())
		target.InitDefaultHelpFlag()
		target.InitDefaultVersionFlag()
		return target.Help()
	}

	rootCmd.InitDefaultCompletionCmd()
	for _, candidate := range rootCmd.Commands() {
		if candidate.Name() == "completion" && candidate.Run == nil && candidate.RunE == nil {
			candidate.RunE = func(cmd *cobra.Command, _ []string) error {
				return cmd.Help()
			}
			break
		}
	}
	markArgumentErrors(rootCmd)
}

func markArgumentErrors(cmd *cobra.Command) {
	if cmd.Args != nil {
		validate := cmd.Args
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			return cli.NewUsageError(validate(cmd, args))
		}
	}
	for _, child := range cmd.Commands() {
		markArgumentErrors(child)
	}
}

var rootCmd = newRootCmd()

func main() {
	if code := executeCommand(rootCmd); code != 0 {
		os.Exit(code)
	}
}

func executeCommand(rootCmd *cobra.Command) int {
	configureGeneratedCommands(rootCmd)
	executedCmd, err := rootCmd.ExecuteC()
	if err == nil {
		return 0
	}
	if code, ok := exitCodeForError(err); ok {
		return code
	}
	if executedCmd == nil {
		executedCmd = rootCmd
	}

	errOut := executedCmd.ErrOrStderr()
	fmt.Fprintln(errOut, executedCmd.ErrPrefix(), err)
	if cli.IsUsageError(err) || executedCmd == rootCmd {
		fmt.Fprint(errOut, executedCmd.UsageString())
	}
	return 1
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
