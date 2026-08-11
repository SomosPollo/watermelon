package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

var cliCopyVM = lima.Copy

func NewCopyCmd() *cobra.Command {
	var recursive bool

	cmd := &cobra.Command{
		Use:   "copy <src> <dest>",
		Short: "Copy files between the host and a VM",
		Long: `Copy files between the host and a VM.
Use vmname:path syntax to specify a VM path.
Exactly one of src or dest must use the vmname:path syntax.
Prefix a colon-containing host filename with ./ to make it explicitly local.

Examples:
  watermelon copy ./file.txt somospollo-vm:/tmp/
  watermelon copy somospollo-vm:/tmp/output.log ./`,
		Args: validateCopyCommandArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]
			vmName, err := copyArgsVMName(src, dst)
			if err != nil {
				return NewUsageError(err)
			}
			if err := requireCompatibleLima(); err != nil {
				return err
			}
			lifecycleLock, err := acquireVMLifecycleLock(vmName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle for copy: %w", vmName, err)
			}
			defer lifecycleLock.Release()
			usageLease, err := acquireSharedVMUsageLease(vmName)
			if err != nil {
				return fmt.Errorf("leasing VM %q for copy: %w", vmName, err)
			}
			if err := lifecycleLock.Release(); err != nil {
				return errors.Join(err, usageLease.Release())
			}
			copyErr := cliCopyVM(src, dst, recursive)
			return errors.Join(copyErr, usageLease.Release())
		},
	}

	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Copy directories recursively")
	return cmd
}

func validateCopyCommandArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(2)(cmd, args); err != nil {
		return err
	}
	return validateCopyArgs(args[0], args[1])
}

// copyOperandIsRemote recognizes Lima's vmname:path syntax. A colon in an
// explicitly local path (for example, ./report:2026) remains part of the host
// path rather than being mistaken for a VM separator.
func copyOperandIsRemote(operand string) (bool, error) {
	_, remote, err := copyOperandVMName(operand)
	return remote, err
}

func copyOperandVMName(operand string) (string, bool, error) {
	separator := strings.IndexByte(operand, ':')
	if separator < 0 {
		return "", false, nil
	}

	prefix, remotePath := operand[:separator], operand[separator+1:]
	if strings.Contains(prefix, "/") {
		return "", false, nil
	}
	if prefix == "" {
		return "", false, fmt.Errorf("copy: invalid VM path %q: VM name cannot be empty", operand)
	}
	if err := config.ValidateVMName(prefix); err != nil {
		return "", false, fmt.Errorf("copy: invalid VM name in %q: %w", operand, err)
	}
	if remotePath == "" {
		return "", false, fmt.Errorf("copy: invalid VM path %q: remote path cannot be empty", operand)
	}
	return prefix, true, nil
}

// validateCopyArgs ensures exactly one of src/dst uses vmname:path syntax.
func validateCopyArgs(src, dst string) error {
	_, err := copyArgsVMName(src, dst)
	return err
}

func copyArgsVMName(src, dst string) (string, error) {
	srcVM, srcIsVM, err := copyOperandVMName(src)
	if err != nil {
		return "", err
	}
	dstVM, dstIsVM, err := copyOperandVMName(dst)
	if err != nil {
		return "", err
	}
	if srcIsVM && dstIsVM {
		return "", fmt.Errorf("copy: both src and dst use vmname:path syntax; exactly one must be a VM path")
	}
	if !srcIsVM && !dstIsVM {
		return "", fmt.Errorf("copy: neither src nor dst uses vmname:path syntax; one must be vmname:path (e.g. somospollo-vm:/tmp/)")
	}
	if srcIsVM {
		return srcVM, nil
	}
	return dstVM, nil
}
