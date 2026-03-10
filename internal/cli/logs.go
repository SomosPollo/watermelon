package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/logs"
	"github.com/spf13/cobra"
)

func NewLogsCmd() *cobra.Command {
	var clear bool

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show network policy logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			if clear {
				return logs.Clear(dir)
			}

			lines, err := logs.Read(dir)
			if err != nil {
				return err
			}

			if len(lines) == 0 {
				fmt.Println("No logs recorded")
				return nil
			}

			for _, line := range lines {
				fmt.Println(line)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&clear, "clear", false, "Clear the log")
	return cmd
}
