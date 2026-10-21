package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/project"
	"github.com/spf13/cobra"
)

var createWorkspaceDisk = lima.CreateWorkspaceDisk

func NewCloneCmd() *cobra.Command {
	var profile, runtime string
	var yes, noSetup bool
	cmd := &cobra.Command{Use: "clone <github-url> [directory]", Short: "Clone a public project into a managed guest workspace", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		source, err := config.PublicRepositoryURL(args[0])
		if err != nil {
			return NewUsageError(err)
		}
		if profile != "" && profile != "all" && config.SetupCommands(profile) == nil {
			return NewUsageError(fmt.Errorf("--template must be all, node, python, go, rust, java, dotnet, ruby, php or cpp; --no-setup leaves a project for manual preparation"))
		}
		if runtime != "" && profile == "" && !strings.Contains(runtime, "=") {
			return NewUsageError(fmt.Errorf("--runtime requires --template"))
		}
		if profile != "" && profile != "all" && !strings.Contains(runtime, "=") {
			if _, err := config.SetupImage(config.SetupConfig{Profile: profile, Runtime: runtime}); err != nil {
				return NewUsageError(err)
			}
		}
		if _, err := project.RuntimeOverrides(runtime); err != nil {
			return NewUsageError(err)
		}
		directory := strings.TrimSuffix(filepath.Base(source), ".git")
		if len(args) == 2 {
			directory = args[1]
		}
		return withSetupContext(cmd, func() error { return runClone(cmd, source, directory, profile, runtime, yes, noSetup) })
	}}
	cmd.Flags().StringVar(&profile, "template", "", "Use all detected languages (all), or restrict to node, python, go, rust, java, dotnet, ruby, php or cpp")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Set a version for one template, or overrides like node=22,go=1.27")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve the displayed setup plan without prompting")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "Clone source without installing project dependencies")
	return cmd
}

func runClone(cmd *cobra.Command, source, directory, profile, runtime string, yes, noSetup bool) error {
	if _, err := config.PublicRepositoryURL(source); err != nil {
		return err
	}
	if err := requireCompatibleLima(); err != nil {
		return err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err := config.ValidateMountSource(absolute); err != nil {
		return NewUsageError(fmt.Errorf("invalid workspace destination: %w", err))
	}
	if _, err := os.Lstat(absolute); err == nil {
		return fmt.Errorf("destination %q already exists; choose a new directory, or use 'watermelon up' to resume an existing workspace", absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(absolute, 0700); err != nil {
		return err
	}
	dir, err := canonicalProjectRoot(absolute)
	if err != nil {
		return err
	}
	cfg := config.NewConfig()
	mount := false
	cfg.VM.MountProject = &mount
	cfg.VM.Workdir = "/workspace/project"
	cfg.Workspace = config.WorkspaceConfig{Disk: workspaceDiskName(dir), Source: source}
	cfg.Network.Allow = []string{"github.com:443"}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if _, err := loadWorkspaceState(dir); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("this destination has a previously registered workspace; recover its original settings instead of adopting its disk")
	}
	if err := writeProjectConfig(dir, cfg, true); err != nil {
		return err
	}
	state := workspaceState{Version: 1, Owner: dir, Disk: cfg.Workspace.Disk, Source: source, RequestedProfile: profile, RequestedRuntime: runtime, Stage: "create workspace disk"}
	if err := saveWorkspaceState(dir, state); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Host workspace settings: %s\nSource location: guest /workspace/project\nCreating persistent workspace disk %s...\n", dir, cfg.Workspace.Disk)
	if err := createWorkspaceDisk(cfg.Workspace.Disk, cfg.Resources.Disk); err != nil {
		return fmt.Errorf("creating workspace disk: %w; settings were retained at %q; an existing disk is never adopted automatically", err, dir)
	}
	state.DiskCreated = true
	if err := saveWorkspaceState(dir, state); err != nil {
		return fmt.Errorf("disk created but ownership state could not be committed: %w", err)
	}
	if err := runUp(cmd, dir, "", setupOptions{Yes: yes, AcquireOnly: noSetup}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace settings are in %q. Change to that directory to use Watermelon. Source files remain inside the guest.\n", dir)
	return nil
}
