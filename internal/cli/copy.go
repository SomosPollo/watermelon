package cli

import (
	"fmt"
	"strings"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewCopyCmd() *cobra.Command {
	var recursive bool

	cmd := &cobra.Command{
		Use:   "copy <src> <dest>",
		Short: "Copy files between host and VM",
		Long: `Copy files between the host and a VM.
Use vmname:path syntax to specify a VM path.
Exactly one of src or dest must use the vmname:path syntax.

Examples:
  watermelon copy ./file.txt somospollo-vm:/tmp/
  watermelon copy somospollo-vm:/tmp/output.log ./`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]
			if err := validateCopyArgs(src, dst); err != nil {
				return err
			}
			return lima.Copy(src, dst, recursive)
		},
	}

	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Copy directories recursively")
	return cmd
}

// validateCopyArgs ensures exactly one of src/dst uses vmname:path syntax.
func validateCopyArgs(src, dst string) error {
	srcIsVM := strings.Contains(src, ":")
	dstIsVM := strings.Contains(dst, ":")
	if srcIsVM && dstIsVM {
		return fmt.Errorf("copy: both src and dst use vmname:path syntax; exactly one must be a VM path")
	}
	if !srcIsVM && !dstIsVM {
		return fmt.Errorf("copy: neither src nor dst uses vmname:path syntax; one must be vmname:path (e.g. somospollo-vm:/tmp/)")
	}
	return nil
}
