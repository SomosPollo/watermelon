package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/logs"
	"github.com/spf13/cobra"
)

func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sandbox VM status for current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			return runStatus(os.Stdout, dir)
		},
	}
}

func runStatus(out io.Writer, dir string) error {
	vmName := lima.VMNameFromPath(dir)
	status := lima.GetStatus(vmName)

	fmt.Fprintf(out, "Project:  %s\n", dir)
	fmt.Fprintf(out, "VM Name:  %s\n", vmName)
	fmt.Fprintf(out, "Status:   %s\n", status)
	if status != lima.StatusNotFound {
		fmt.Fprintf(out, "SSH Host: %s\n", lima.GetSSHHost(vmName))
	}

	cfg, err := loadProjectConfig(dir)
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(dir, ".watermelon.toml")); os.IsNotExist(statErr) {
			fmt.Fprintln(out, "Config:   missing (.watermelon.toml not found; run 'watermelon init')")
			if status == lima.StatusNotFound {
				fmt.Fprintln(out, "Next:     watermelon init")
			}
		} else {
			fmt.Fprintf(out, "Config:   unreadable (%v)\n", err)
		}
		return nil
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(out, "Config:   invalid (%v)\n", err)
		return nil
	}

	fmt.Fprintf(out, "Config:   %s\n", configSnapshotStatus(dir, status))
	fmt.Fprintf(out, "Network:  %s enforcement, %s, %s\n",
		cfg.Security.Enforcement,
		countLabel(len(cfg.Network.Allow), "allow rule", "allow rules"),
		countLabel(len(cfg.Network.Process), "process rule", "process rules"),
	)
	fmt.Fprintf(out, "Tools:    %s\n", formatTools(cfg.Tools))
	fmt.Fprintf(out, "Ports:    %s\n", formatPorts(cfg.Ports.Forward))
	fmt.Fprintf(out, "Resources: %s memory, %s, %s disk\n",
		cfg.Resources.Memory,
		countLabel(cfg.Resources.CPUs, "CPU", "CPUs"),
		cfg.Resources.Disk,
	)
	fmt.Fprintf(out, "Logs:     %s\n", logStatus(dir))
	if status == lima.StatusNotFound {
		fmt.Fprintln(out, "Next:     watermelon run")
	}

	return nil
}

func configSnapshotStatus(dir string, status lima.VMStatus) string {
	if status == lima.StatusNotFound {
		return "valid"
	}

	current, err := currentConfigDigest(dir)
	if err != nil {
		return "valid (could not read current snapshot)"
	}
	saved, err := readConfigDigest(dir)
	if os.IsNotExist(err) {
		return "valid (VM snapshot unknown)"
	}
	if err != nil {
		return "valid (could not read VM snapshot)"
	}
	if current != saved {
		return "changed since VM creation (recreate to apply network, tools, ports, mounts, or resources)"
	}
	return "current"
}

func formatTools(tools map[string][]string) string {
	if len(tools) == 0 {
		return "none"
	}

	images := make([]string, 0, len(tools))
	for image := range tools {
		images = append(images, image)
	}
	sort.Strings(images)

	parts := make([]string, 0, len(images))
	for _, image := range images {
		commands := append([]string(nil), tools[image]...)
		sort.Strings(commands)
		parts = append(parts, fmt.Sprintf("%s [%s]", image, strings.Join(commands, ", ")))
	}
	return strings.Join(parts, "; ")
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return "none"
	}

	values := append([]int(nil), ports...)
	sort.Ints(values)

	parts := make([]string, 0, len(values))
	for _, port := range values {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ", ")
}

func logStatus(dir string) string {
	lines, err := logs.Read(dir)
	if err != nil {
		return fmt.Sprintf("unavailable (%v)", err)
	}
	return countLabel(len(lines), "entry", "entries")
}

func countLabel(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}
