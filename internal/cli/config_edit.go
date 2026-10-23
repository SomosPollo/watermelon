package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/mattn/go-shellwords"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var runConfigEditor = func(cmd *cobra.Command, path string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	// Parse arguments without invoking a shell or enabling substitutions.
	parser := shellwords.NewParser()
	parser.ParseEnv, parser.ParseBacktick = false, false
	args, err := parser.Parse(editor)
	if err != nil || len(args) == 0 || args[0] == "" {
		return fmt.Errorf("invalid editor command %q", editor)
	}
	process := exec.CommandContext(cmd.Context(), args[0], append(args[1:], path)...)
	process.Stdin, process.Stdout, process.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := process.Run(); err != nil {
		return fmt.Errorf("running editor %q (set VISUAL or EDITOR): %w", args[0], err)
	}
	return nil
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use: "edit", Short: "Edit and validate host policy using VISUAL, EDITOR, or vi", Args: cobra.NoArgs,
		Long: "Edit a private draft of .watermelon.toml using VISUAL, EDITOR, or vi. Only valid changes replace the host config. VM-affecting changes require a separate rebuild.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSetupContext(cmd, func() error {
				dir, err := os.Getwd()
				if err != nil {
					return err
				}
				root, _, err := discoverProjectRoot(dir)
				if err != nil {
					return err
				}
				original, err := readMutableProjectConfig(root)
				if err != nil {
					return err
				}
				updated, err := editConfigDocument(cmd, root, original)
				if err != nil {
					return err
				}
				if bytes.Equal(original, updated) {
					fmt.Fprintln(cmd.OutOrStdout(), "Configuration unchanged.")
					return nil
				}
				if err := replaceProjectConfigDocument(root, original, updated); err != nil {
					return retainConfigDraft(root, updated, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Saved valid configuration: %s\n", filepath.Join(root, projectConfigName))
				before, beforeErr := config.Parse(original)
				after, _ := config.Parse(updated)
				if beforeErr == nil {
					oldTarget, oldErr := prepareTargetProvisionScripts(targetContext{ProjectRoot: root, Config: before})
					newTarget, newErr := prepareTargetProvisionScripts(targetContext{ProjectRoot: root, Config: after})
					if oldErr == nil && newErr == nil {
						oldDigest, _ := config.AppliedConfigDigest(oldTarget.Config)
						newDigest, _ := config.AppliedConfigDigest(newTarget.Config)
						if oldDigest == newDigest {
							fmt.Fprintln(cmd.OutOrStdout(), "No VM rebuild required.")
							return nil
						}
					}
				}
				if after.Security.Enforcement == config.EnforcementAsk {
					fmt.Fprintln(cmd.OutOrStdout(), "VM settings changed. Ask mode needs foreground recreation: run 'watermelon status' and follow its VM-specific recreation command, then keep 'watermelon run' in the foreground.")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "VM settings changed. Run 'watermelon rebuild' for an existing VM, or 'watermelon up' for a new environment.")
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Until rebuilt, workload commands refuse stale policy and may stop the VM. Rebuild replaces VM system files; managed source and mounted project files are retained.")
				return nil
			})
		},
	}
}

func retainConfigDraft(root string, document []byte, cause error) error {
	dir, err := os.MkdirTemp(root, ".watermelon-edit-*")
	if err != nil {
		return fmt.Errorf("%w; could not retain draft: %v", cause, err)
	}
	path := filepath.Join(dir, projectConfigName)
	if err := os.WriteFile(path, document, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("%w; could not retain draft: %v", cause, err)
	}
	return fmt.Errorf("%w; draft retained at %q", cause, path)
}

// editConfigDocument never exposes a partial edit as active policy. Retain an
// unsuccessful draft so the user's editor work can be recovered.
func editConfigDocument(cmd *cobra.Command, root string, original []byte) ([]byte, error) {
	draftDir, err := os.MkdirTemp(root, ".watermelon-edit-*")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(draftDir, projectConfigName)
	if err := os.WriteFile(path, original, 0600); err != nil {
		_ = os.RemoveAll(draftDir)
		return nil, err
	}
	retained := func(err error) ([]byte, error) {
		return nil, fmt.Errorf("%w; active configuration unchanged; draft retained at %q", err, path)
	}
	if err := runConfigEditor(cmd, path); err != nil {
		return retained(err)
	}
	updated, err := readMutableProjectConfig(draftDir)
	if err != nil {
		return retained(err)
	}
	if _, err := validateEditedConfig(root, original, updated); err != nil {
		return retained(err)
	}
	if err := os.RemoveAll(draftDir); err != nil {
		return nil, err
	}
	return updated, nil
}

func validateEditedConfig(root string, original, updated []byte) (*config.Config, error) {
	cfg, err := config.Parse(updated)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	before, _ := config.Parse(original)
	if before != nil && before.Workspace.Disk != "" && !reflect.DeepEqual(before.Workspace, cfg.Workspace) {
		return nil, fmt.Errorf("managed workspace source and disk are generated identity and cannot be edited")
	}
	if cfg.Workspace.Disk != "" {
		if err := requireWorkspaceOwnership(root, derivedVMName(root), cfg); err != nil {
			return nil, err
		}
	}
	if _, err := prepareTargetProvisionScripts(targetContext{ProjectRoot: root, Config: cfg}); err != nil {
		return nil, err
	}
	return cfg, nil
}

func readMutableProjectConfig(root string) ([]byte, error) {
	path := filepath.Join(root, projectConfigName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("reading project config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) {
		return nil, fmt.Errorf("refusing to edit untrusted project configuration %q: require a current-user-owned regular file, not a symlink", path)
	}
	return readProjectConfigBytes(root)
}

// Coordinate with other config writers, including saved ask-mode decisions.
// Checking the reviewed bytes after locking prevents an editor or a setup
// plan from silently overwriting concurrent changes.
func replaceProjectConfigDocument(root string, expected, updated []byte) error {
	if len(updated) > maxProjectConfigSize {
		return fmt.Errorf("edited config exceeds the %d-byte limit", maxProjectConfigSize)
	}
	path := filepath.Join(root, projectConfigName)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || !os.SameFile(info, current) {
		return fmt.Errorf("project configuration changed while preparing the update; retry")
	}
	trusted, err := trustedProjectConfigFile(info)
	if err != nil || !trusted {
		return fmt.Errorf("refusing to replace project configuration with untrusted ownership or permissions")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProjectConfigSize+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return fmt.Errorf("project configuration changed while preparing the update; retry")
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	tmp, err := os.CreateTemp(root, ".watermelon-policy-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if _, err := tmp.Write(updated); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
