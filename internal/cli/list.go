package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

var cliListAllVMs = lima.ListAllVMs

func NewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all watermelon sandbox VMs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			vms, err := listOwnedWatermelonVMs()
			if err != nil {
				return err
			}

			if len(vms) == 0 {
				fmt.Println("No watermelon VMs found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tPROJECT")
			for _, vm := range vms {
				projectDir := vm.ProjectDir
				if projectDir == "" {
					projectDir = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", vm.Name, vm.Status, projectDir)
			}
			w.Flush()

			return nil
		},
	}
}

func listOwnedWatermelonVMs() ([]lima.VMInfo, error) {
	allVMs, err := cliListAllVMs()
	if err != nil {
		return nil, fmt.Errorf("listing VMs: %w", err)
	}
	identities, err := listNamedVMIdentities()
	if err != nil {
		return nil, fmt.Errorf("reading Watermelon VM identities: %w", err)
	}
	registered := make(map[string]namedVMInstanceIdentity, len(identities))
	for _, identity := range identities {
		registered[identity.Identity.VMName] = identity
	}

	vms := make([]lima.VMInfo, 0, len(allVMs))
	for _, vm := range allVMs {
		if _, ok := registered[vm.Name]; ok {
			include, inspected, err := inspectRegisteredVMForList(vm)
			if err != nil {
				return nil, err
			}
			if include {
				vms = append(vms, inspected)
			}
			continue
		}
		if strings.HasPrefix(vm.Name, "watermelon-") {
			vms = append(vms, vm)
		}
	}
	return vms, nil
}

func inspectRegisteredVMForList(vm lima.VMInfo) (bool, lima.VMInfo, error) {
	lifecycleLock, err := acquireVMLifecycleLock(vm.Name)
	if err != nil {
		return false, lima.VMInfo{}, fmt.Errorf("locking VM %q lifecycle while listing: %w", vm.Name, err)
	}
	defer lifecycleLock.Release()

	// A destroy may have completed after ListAllVMs returned. Treat that as a
	// disappeared row, not as a corrupt registry or a fatal list operation.
	if cliGetVMStatus(vm.Name) == lima.StatusNotFound {
		return false, lima.VMInfo{}, nil
	}
	identity, err := loadNamedVMIdentity(vm.Name)
	if errors.Is(err, os.ErrNotExist) {
		return false, lima.VMInfo{}, nil
	}
	if err != nil {
		return false, lima.VMInfo{}, fmt.Errorf("VM %q has an invalid Watermelon identity: %w", vm.Name, err)
	}
	if err := requireNamedVMIdentityMount(identity); err != nil {
		return false, lima.VMInfo{}, fmt.Errorf("VM %q has an invalid Watermelon identity: %w", vm.Name, err)
	}
	vm.ProjectDir = identity.Identity.OwnerProject
	return true, vm, nil
}
