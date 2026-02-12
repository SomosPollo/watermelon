package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all watermelon sandbox VMs",
		RunE: func(cmd *cobra.Command, args []string) error {
			vms, err := lima.ListWatermelonVMs()
			if err != nil {
				return fmt.Errorf("listing VMs: %w", err)
			}

			if len(vms) == 0 {
				fmt.Println("No watermelon VMs found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS")
			for _, vm := range vms {
				fmt.Fprintf(w, "%s\t%s\n", vm.Name, vm.Status)
			}
			w.Flush()

			return nil
		},
	}
}
