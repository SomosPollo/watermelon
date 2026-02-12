package cli

import (
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/violations"
	"github.com/spf13/cobra"
)

func NewViolationsCmd() *cobra.Command {
	var clear bool

	cmd := &cobra.Command{
		Use:   "violations",
		Short: "Show network policy violations",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			if clear {
				return violations.Clear(dir)
			}

			lines, err := violations.Read(dir)
			if err != nil {
				return err
			}

			if len(lines) == 0 {
				fmt.Println("No violations recorded")
				return nil
			}

			for _, line := range lines {
				fmt.Println(line)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&clear, "clear", false, "Clear the violations log")
	return cmd
}
