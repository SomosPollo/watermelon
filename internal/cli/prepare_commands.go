package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect project configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var jsonOutput bool
	validate := &cobra.Command{Use: "validate", Short: "Validate host policy without changing a VM", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		root, _, err := discoverProjectRoot(dir)
		if err != nil {
			return err
		}
		cfg, err := loadProjectConfig(root)
		if err != nil {
			return err
		}
		if err := config.Validate(cfg); err != nil {
			return err
		}
		if _, err := prepareTargetProvisionScripts(targetContext{ProjectRoot: root, Config: cfg}); err != nil {
			return err
		}
		if cfg.Workspace.Disk != "" {
			if err := requireWorkspaceOwnership(root, derivedVMName(root), cfg); err != nil {
				return err
			}
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				Version int    `json:"schema_version"`
				Valid   bool   `json:"valid"`
				Project string `json:"project"`
			}{1, true, root})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Valid configuration: %s/%s\n", root, projectConfigName)
		return nil
	}}
	validate.Flags().BoolVar(&jsonOutput, "json", false, "Emit a versioned JSON validation result")
	cmd.AddCommand(validate)
	return cmd
}

func NewRebuildCmd() *cobra.Command {
	var force bool
	var yes bool
	var name string
	cmd := &cobra.Command{Use: "rebuild", Short: "Replace the VM while retaining project files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		target, err := resolveConfiguredTarget(dir, name)
		if err != nil {
			return err
		}
		dir = target.ProjectRoot
		fmt.Fprintln(cmd.OutOrStdout(), "Rebuilding replaces VM-local system state. Managed workspace disks and mounted host project files are retained.")
		if !force {
			if f, ok := cmd.InOrStdin().(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
				return fmt.Errorf("rebuild requires a terminal or --force")
			}
			fmt.Fprint(cmd.OutOrStdout(), "Rebuild this project's VM? [y/N] ")
			answer, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				fmt.Fprintln(cmd.OutOrStdout(), "Cancelled")
				return nil
			}
		}
		if err := requireUnchangedSetupTarget(target); err != nil {
			return err
		}
		destroy := newDestroyCmdAt(dir)
		destroy.SilenceErrors, destroy.SilenceUsage = true, true
		destroy.SetOut(cmd.OutOrStdout())
		destroy.SetErr(cmd.ErrOrStderr())
		destroy.SetContext(cmd.Context())
		// Keep the destructive operation bound to the VM the user reviewed,
		// including when a configured name changes while confirmation waits.
		args := []string{"--force", "--name", target.VMName}
		destroy.SetArgs(args)
		if err := destroy.Execute(); err != nil {
			return err
		}
		if err := requireUnchangedSetupTarget(target); err != nil {
			return err
		}
		if target.Config.Setup.Profile == "" {
			digest, err := config.AppliedConfigDigest(target.Config)
			if err != nil {
				return err
			}
			return withSetupContext(cmd, func() error {
				return setupStartVM(runOptions{Dir: dir, Name: name, Context: cmd.Context(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), ExpectedConfigDigest: digest, ExpectedVMName: target.VMName})
			})
		}
		return withSetupContext(cmd, func() error { return runUp(cmd, dir, name, setupOptions{Yes: yes}) })
	}}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Confirm replacement of VM-local state")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve any changed setup plan without prompting")
	cmd.Flags().StringVar(&name, "name", "", "VM name override (local mounted projects only)")
	return cmd
}
