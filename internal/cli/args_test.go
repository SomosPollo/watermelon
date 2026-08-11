package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func executeCommandWithArgs(cmd *cobra.Command, args ...string) error {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestNoArgumentCommandsHaveExplicitContracts(t *testing.T) {
	tests := []struct {
		name string
		new  func() *cobra.Command
	}{
		{name: "run", new: NewRunCmd},
		{name: "init", new: NewInitCmd},
		{name: "stop", new: NewStopCmd},
		{name: "destroy", new: NewDestroyCmd},
		{name: "status", new: NewStatusCmd},
		{name: "list", new: NewListCmd},
		{name: "logs", new: NewLogsCmd},
		{name: "code", new: NewCodeCmd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.new()
			if cmd.Args == nil {
				t.Fatal("command has no explicit positional-argument contract")
			}
			if err := cmd.Args(cmd, nil); err != nil {
				t.Fatalf("command rejected an empty argument list: %v", err)
			}
			if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
				t.Fatal("command accepted an unexpected positional argument")
			}
		})
	}
}

func TestNoArgumentCommandsRejectUnexpectedArgumentsBeforeHandlers(t *testing.T) {
	tests := []struct {
		name string
		new  func() *cobra.Command
		args []string
	}{
		{name: "run", new: NewRunCmd, args: []string{"unexpected", "--no-shell"}},
		{name: "init", new: NewInitCmd, args: []string{"unexpected"}},
		{name: "stop", new: NewStopCmd, args: []string{"some-name"}},
		{name: "destroy", new: NewDestroyCmd, args: []string{"some-name", "--force"}},
		{name: "status", new: NewStatusCmd, args: []string{"some-name"}},
		{name: "list", new: NewListCmd, args: []string{"some-name"}},
		{name: "logs", new: NewLogsCmd, args: []string{"some-name", "--clear"}},
		{name: "code", new: NewCodeCmd, args: []string{"some-name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.new()
			handlerCalls := 0
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				handlerCalls++
				return nil
			}

			err := executeCommandWithArgs(cmd, tt.args...)
			if err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("argument error = %v, want unknown-command error", err)
			}
			if handlerCalls != 0 {
				t.Fatalf("handler called %d times, want 0", handlerCalls)
			}
		})
	}
}
