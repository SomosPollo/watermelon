package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewDestroyCmd() *cobra.Command {
	var force bool
	var name string

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy the project sandbox VM and all its state",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}

			vmName := resolveVMName(name, dir)
			status := lima.GetStatus(vmName)

			if status == lima.StatusNotFound {
				return fmt.Errorf("no sandbox VM found for this project")
			}

			if !force {
				fmt.Printf("This will delete VM '%s' and all installed dependencies.\n", vmName)
				fmt.Print("Are you sure? [y/N] ")
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			fmt.Println("Destroying sandbox VM...")
			return lima.Delete(vmName)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides path-derived name and vm.name config)")
	return cmd
}
