package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/project"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type setupOptions struct {
	Yes, Refresh, DryRun, AcquireOnly bool
	Runtime                           string
}

func withSetupContext(cmd *cobra.Command, action func() error) error {
	parent := cmd.Context()
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	cmd.SetContext(ctx)
	defer func() { cancel(); cmd.SetContext(parent) }()
	return action()
}

var setupStartVM = runRunWithOptions
var setupGuestOutput = lima.GuestOutput
var setupExecute = func(cmd *cobra.Command, target targetContext, args []string) error {
	return lima.ExecWithRunner(target.VMName, args, &setupRunner{ctx: cmd.Context(), out: cmd.OutOrStdout(), err: cmd.ErrOrStderr()}, target.Workdir)
}

type setupRunner struct {
	ctx      context.Context
	in       io.Reader
	out, err io.Writer
}

func (r *setupRunner) Run(command *exec.Cmd) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	command.Stdin, command.Stdout, command.Stderr = r.in, r.out, r.err
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-r.ctx.Done():
			_ = command.Process.Kill()
		case <-done:
		}
	}()
	err := command.Wait()
	close(done)
	if r.ctx.Err() != nil {
		return r.ctx.Err()
	}
	return err
}

func NewUpCmd() *cobra.Command {
	var options setupOptions
	var name string
	cmd := &cobra.Command{Use: "up", Short: "Prepare the approved project environment and dependencies", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			return withSetupContext(cmd, func() error { return runUp(cmd, dir, name, options) })
		}}
	cmd.Flags().BoolVarP(&options.Yes, "yes", "y", false, "Approve the displayed setup plan without prompting")
	cmd.Flags().BoolVar(&options.Refresh, "refresh", false, "Reinstall dependencies even when manifests are unchanged")
	cmd.Flags().BoolVar(&options.DryRun, "dry-run", false, "Show local setup settings without creating a VM or running project code")
	cmd.Flags().StringVar(&options.Runtime, "runtime", "", "Override runtime versions, e.g. node=22,go=1.27 (requires setup approval)")
	cmd.Flags().StringVar(&name, "name", "", "VM name override (local mounted projects only)")
	return cmd
}

func ensureSetupForCommand(cmd *cobra.Command, dir, name string) error {
	target, err := resolveConfiguredTarget(dir, name)
	if err != nil {
		return err
	}
	if target.Config.Setup.Profile == "" {
		return nil
	}
	if target.Config.Security.Enforcement == config.EnforcementAsk {
		return nil
	}
	prepare := &cobra.Command{}
	prepare.SetContext(cmd.Context())
	prepare.SetIn(cmd.InOrStdin())
	prepare.SetOut(cmd.ErrOrStderr())
	prepare.SetErr(cmd.ErrOrStderr())
	return withSetupContext(prepare, func() error { return runUp(prepare, target.ProjectRoot, name, setupOptions{}) })
}

func runUp(cmd *cobra.Command, dir, name string, options setupOptions) (result error) {
	var target targetContext
	var err error
	if options.DryRun {
		target, err = resolveManagementTarget(dir, name)
		if err == nil && target.Config == nil {
			err = fmt.Errorf("no project configuration; run 'watermelon init' first")
		}
	} else {
		target, err = resolveConfiguredTarget(dir, name)
	}
	if err != nil {
		return err
	}
	dir = target.ProjectRoot
	cfg := target.Config
	if _, err := project.RuntimeOverrides(options.Runtime); err != nil {
		return NewUsageError(err)
	}
	if options.DryRun {
		if cfg.Setup.Profile == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "No automatic setup profile configured. Managed source inspection requires a running sandbox.")
			return nil
		}
		image, _ := config.SetupImage(cfg.Setup)
		fmt.Fprintf(cmd.OutOrStdout(), "Configured profile: %s\nConfigured runtime image: %s\nGuest project: %s\nPolicy: %s\n", cfg.Setup.Profile, image, target.Workdir, config.DescribeEnforcement(cfg.Security.Enforcement))
		if options.Runtime != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Requested runtime override: %q\n", options.Runtime)
		}
		if cfg.Workspace.Disk != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "The proposed managed setup requires live guest inspection; run 'watermelon up' to review it.")
		}
		if cfg.Workspace.Disk == "" {
			files, err := project.ReadLocal(dir)
			if err != nil {
				return err
			}
			plan, err := project.DetectConfiguredWithRuntime(files, cfg.Setup, options.Runtime)
			if err != nil {
				return err
			}
			proposed := proposeProfile(cfg, plan)
			printSetupPlan(cmd, &proposed, plan)
		}
		return nil
	}
	if err := requireCompatibleLima(); err != nil {
		return err
	}
	if err := checkSetupPolicy(target); err != nil {
		return err
	}
	if cfg.Security.Enforcement == config.EnforcementAsk {
		return fmt.Errorf("up requires a noninteractive network policy; use 'watermelon run' to keep an ask-mode controller in the foreground, or configure fail/silent before automatic setup")
	}
	lock, err := acquireVMLifecycleLock("wm-setup-" + fullPathDigest(dir)[:24])
	if err != nil {
		return err
	}
	defer lock.Release()
	// Resolve again after taking the setup lock: another preparation may have
	// completed or changed the effective profile while this command waited.
	target, err = resolveConfiguredTarget(dir, name)
	if err != nil {
		return err
	}
	cfg = target.Config
	state, err := loadWorkspaceState(dir)
	if errors.Is(err, os.ErrNotExist) && cfg.Workspace.Disk == "" {
		state = workspaceState{Version: 1, Owner: dir, Stage: "not prepared"}
	} else if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			state.LastError = result.Error()
			if len(state.LastError) > 2048 {
				state.LastError = state.LastError[:2048]
			}
			state.ReadyFingerprint = ""
			if saveErr := saveWorkspaceState(dir, state); saveErr != nil {
				result = errors.Join(result, fmt.Errorf("saving recovery state: %w", saveErr))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Project files were retained. Retry from %q with 'watermelon up'.\n", dir)
			if cmd.Context().Err() != nil {
				_ = stopSetupVM(target)
			}
		}
	}()
	startVM := func() error {
		digest, err := config.AppliedConfigDigest(cfg)
		if err != nil {
			return err
		}
		err = setupStartVM(runOptions{Dir: dir, Name: name, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), Context: cmd.Context(), ExpectedConfigDigest: digest, ExpectedVMName: target.VMName})
		if err == nil && cfg.Workspace.Disk != "" {
			state.DiskInitialized = true
		}
		return err
	}
	stage := func(label string, action func() error) error {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		state.Stage, state.LastError = label, ""
		if err := saveWorkspaceState(dir, state); err != nil {
			return err
		}
		start := time.Now()
		fmt.Fprintf(cmd.OutOrStdout(), "[%s]\n", label)
		if err := action(); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[%s complete in %s]\n", label, time.Since(start).Round(time.Second))
		return nil
	}
	var files map[string][]byte
	// Local manifest inspection precedes VM creation and all project execution.
	if cfg.Workspace.Disk == "" && cfg.Setup.Profile != "" {
		files, err = project.ReadLocal(dir)
		if err != nil {
			return err
		}
	}
	if cfg.Workspace.Disk != "" {
		if err := stage("Start isolated workspace", startVM); err != nil {
			return err
		}
		if !state.Cloned {
			if err := stage("Clone public repository", func() error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
				defer cancel()
				cloneCmd := &cobra.Command{}
				cloneCmd.SetContext(ctx)
				cloneCmd.SetOut(cmd.OutOrStdout())
				cloneCmd.SetErr(cmd.ErrOrStderr())
				var err error
				for attempt := 1; attempt <= 3; attempt++ {
					err = withSetupGuest(target, func(current targetContext) error {
						return setupExecute(cloneCmd, current, cloneGuestArgs(cfg.Workspace.Source))
					})
					if err == nil || ctx.Err() != nil {
						break
					}
					if attempt < 3 {
						fmt.Fprintf(cmd.ErrOrStderr(), "Checkout failed; retrying (%d/3).\n", attempt+1)
						timer := time.NewTimer(2 * time.Second)
						select {
						case <-ctx.Done():
							timer.Stop()
						case <-timer.C:
						}
					}
				}
				if ctx.Err() != nil {
					_ = stopSetupVM(target)
					return fmt.Errorf("checkout timed out or was canceled; source was retained: %w", ctx.Err())
				}
				return err
			}); err != nil {
				return err
			}
			state.Cloned = true
			if err := saveWorkspaceState(dir, state); err != nil {
				return err
			}
		}
		if options.AcquireOnly {
			state.Stage = "source cloned (setup pending)"
			if err := saveWorkspaceState(dir, state); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Source cloned. Run 'watermelon up' to inspect and prepare dependencies.")
			return nil
		}
		if err := stage("Inspect project manifests", func() error { var readErr error; files, readErr = readGuestManifests(cmd, target); return readErr }); err != nil {
			return err
		}
	}
	if cfg.Workspace.Disk == "" && cfg.Setup.Profile == "" {
		return startVM()
	}
	profile, runtime := cfg.Setup.Profile, cfg.Setup.Runtime
	if profile == "" {
		profile, runtime = state.RequestedProfile, state.RequestedRuntime
	}
	var plan project.Plan
	if cfg.Setup.Profile != "" {
		plan, err = project.DetectConfiguredWithRuntime(files, cfg.Setup, options.Runtime)
	} else {
		if options.Runtime != "" {
			runtime = options.Runtime
		}
		plan, err = project.Detect(files, profile, runtime)
	}
	if err != nil {
		return err
	}
	proposed := proposeProfile(cfg, plan)
	if err := config.Validate(&proposed); err != nil {
		return fmt.Errorf("invalid proposed setup: %w", err)
	}
	digest, err := config.AppliedConfigDigest(&proposed)
	if err != nil {
		return err
	}
	if state.ApprovedConfig != digest {
		printSetupPlan(cmd, &proposed, plan)
		if cfg.Setup.Profile != "" && !reflect.DeepEqual(cfg.Setup, proposed.Setup) {
			fmt.Fprintln(cmd.OutOrStdout(), "This plan replaces the VM while retaining managed workspace data and mounted project files.")
		}
		if err := approveSetup(cmd, options.Yes); err != nil {
			return err
		}
		if err := requireUnchangedSetupTarget(target); err != nil {
			return err
		}
		// Approved runtime changes replace compute, retaining the separate source disk.
		if !reflect.DeepEqual(cfg.Setup, proposed.Setup) {
			if err := destroyAt(cmd, dir, target.VMName); err != nil {
				return err
			}
			persisted := proposed
			if target.NameExplicit {
				// --name selects this invocation's VM; it must not silently change
				// which VM ordinary commands select in the saved host policy.
				raw, err := loadProjectConfig(dir)
				if err != nil {
					return err
				}
				persisted.VM.Name = raw.VM.Name
			}
			if err := writeProjectConfig(dir, &persisted, false); err != nil {
				return err
			}
			target, err = resolveConfiguredTarget(dir, name)
			if err != nil {
				return err
			}
			cfg = target.Config
		}
		state.ApprovedConfig = digest
		state.ReadyFingerprint = ""
		if err := saveWorkspaceState(dir, state); err != nil {
			return err
		}
	}
	if err := stage("Prepare project runtime", startVM); err != nil {
		return err
	}
	if cfg.Workspace.Disk != "" {
		retained, err := readGuestManifests(cmd, target)
		if err != nil {
			return err
		}
		retainedPlan, err := project.DetectConfigured(retained, cfg.Setup)
		if err != nil {
			return fmt.Errorf("checking retained workspace source before installation: %w", err)
		}
		if retainedPlan.Fingerprint != plan.Fingerprint {
			return fmt.Errorf("project manifests changed during preparation; retry 'watermelon up'")
		}
	}
	// Include immutable VM identity: a fresh compute VM must validate its actual
	// runtime and reprepare dependencies even when the disk was retained.
	instance, err := loadOwnedNamedVMIdentity(dir, target.VMName)
	if err != nil {
		return fmt.Errorf("reading prepared VM identity: %w", err)
	}
	instanceID := instance.Identity.InstanceID
	fingerprint := fullPathDigest(digest + "\x00" + plan.Fingerprint + "\x00" + instanceID)
	if state.ReadyFingerprint == fingerprint && !options.Refresh {
		state.Stage = "ready"
		state.LastError = ""
		if err := saveWorkspaceState(dir, state); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Project ready; dependency manifests are unchanged.")
		return nil
	}
	if err := stage("Verify runtime versions", func() error {
		for _, part := range plan.Parts() {
			plan := part
			args := []string{"/usr/local/bin/node", "-p", "process.versions.node"}
			if plan.Profile == "python" {
				args = []string{"/usr/local/bin/python", "-I", "-c", "import sys; print('.'.join(map(str, sys.version_info[:3])))"}
			} else if plan.Profile == "go" {
				args = []string{"/usr/local/go/bin/go", "version"}
			} else if plan.Profile == "rust" {
				args = []string{"/usr/local/cargo/bin/rustc", "--version"}
			}
			if config.ExtendedProfile(plan.Profile) {
				args = project.RuntimeVersionCommand(plan.Profile)
			}

			var output []byte
			err := withSetupGuest(target, func(current targetContext) error {
				var readErr error
				output, readErr = setupGuestOutput(cmd.Context(), current.VMName, current.Workdir, projectCommandArgsInDir(current.Config, plan.Directory, args), 256)
				return readErr
			})
			if err != nil {
				return err
			}
			actual := strings.TrimSpace(string(output))
			if plan.Profile == "go" || plan.Profile == "rust" {
				fields := strings.Fields(actual)
				if plan.Profile == "go" && len(fields) == 4 && fields[0] == "go" && fields[1] == "version" {
					actual = strings.TrimPrefix(fields[2], "go")
				} else if plan.Profile == "rust" && len(fields) >= 2 && fields[0] == "rustc" {
					actual = fields[1]
				} else {
					return fmt.Errorf("unexpected %s runtime version %q", plan.Profile, actual)
				}
			}
			if config.ExtendedProfile(plan.Profile) {
				actual, err = project.ParseRuntimeVersion(plan.Profile, actual)
				if err != nil {
					return err
				}
			}
			if plan.Requirement != "" {
				ok, err := project.MatchesRuntime(actual, plan.Requirement)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("runtime %q does not satisfy project requirement %q", actual, plan.Requirement)
				}
			}
			if plan.NPMVersion != "" {
				var npm []byte
				if err := withSetupGuest(target, func(current targetContext) error {
					var err error
					npm, err = setupGuestOutput(cmd.Context(), current.VMName, current.Workdir, projectCommandArgsInDir(current.Config, plan.Directory, []string{"/usr/local/bin/npm", "--version"}), 256)
					return err
				}); err != nil {
					return err
				}
				if strings.TrimSpace(string(npm)) != plan.NPMVersion {
					return fmt.Errorf("project pins npm %s but this runtime bundles npm %q; use a compatible runtime or manual tools", plan.NPMVersion, strings.TrimSpace(string(npm)))
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := stage("Install project dependencies", func() error {
		for _, part := range plan.Parts() {
			commands := part.Commands
			if options.Refresh && strings.HasPrefix(part.Manager, "poetry@") && len(commands) > 0 {
				commands = append([][]string{{"/usr/local/bin/python", "-m", "venv", "--clear", ".venv"}}, commands...)
			}
			for _, args := range commands {
				if options.Refresh && strings.HasPrefix(part.Manager, "uv@") && len(args) > 1 && args[1] == "sync" {
					args = append(append([]string{}, args...), "--reinstall")
				}

				if err := withSetupGuest(target, func(current targetContext) error {
					return setupExecute(cmd, current, projectCommandArgsInDir(current.Config, part.Directory, args))
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Dependency resolution can create sum/lock files. Record the resulting metadata
	// so the first subsequent command does not immediately repeat installation.
	if cfg.Workspace.Disk != "" {
		files, err = readGuestManifests(cmd, target)
	} else {
		files, err = project.ReadLocal(dir)
	}
	if err != nil {
		return err
	}
	finalPlan, err := project.DetectConfiguredWithRuntime(files, cfg.Setup, options.Runtime)
	if err != nil {
		return err
	}
	state.ReadyFingerprint = fullPathDigest(digest + "\x00" + finalPlan.Fingerprint + "\x00" + instanceID)
	state.Stage, state.LastError = "ready", ""
	if err := saveWorkspaceState(dir, state); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Project ready. Use 'watermelon run', 'watermelon exec <command>', or 'watermelon code'.")
	return nil
}

// Fail closed before metadata errors or unsupported preparation modes can
// bypass the same stale-policy shutdown used by run/exec/code.
func checkSetupPolicy(target targetContext) error {
	lock, err := acquireVMLifecycleLock(target.VMName)
	if err != nil {
		return err
	}
	defer lock.Release()
	status := cliGetVMStatus(target.VMName)
	if status != lima.StatusNotFound {
		if err := requireVMProjectBinding(target.ProjectRoot, target.VMName); err != nil {
			return err
		}
	}
	if err := requireCurrentAppliedPolicyAndStopUnsafe(target.ProjectRoot, target.VMName, status, target.Config, target.NameExplicit); err != nil {
		return err
	}
	if status == lima.StatusUnknown {
		return fmt.Errorf("cannot safely prepare VM %q because its Lima state is unknown", target.VMName)
	}
	return nil
}

func printSetupPlan(cmd *cobra.Command, cfg *config.Config, plan project.Plan) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Setup plan: %s (%s)\nGuest project: %s\nPolicy: %s\nNetwork allowlist: %q\nHost project mounted: %t\nPorts on localhost: %v\nResources: %s memory, %d CPUs, %s VM disk\n", plan.Profile, plan.Image, config.DefaultWorkdir(cfg), config.DescribeEnforcement(cfg.Security.Enforcement), cfg.Network.Allow, config.MountProjectEnabled(&cfg.VM), cfg.Ports.Forward, cfg.Resources.Memory, cfg.Resources.CPUs, cfg.Resources.Disk)
	if len(cfg.Mounts) != 0 {
		fmt.Fprintf(out, "Additional host mounts: %q\n", cfg.Mounts)
	}
	if len(cfg.Provision.Scripts) != 0 {
		fmt.Fprintf(out, "Privileged provision scripts (review exact host files): %q\n", cfg.Provision.Scripts)
	}
	for _, part := range plan.Parts() {
		fmt.Fprintf(out, "  %s %s at %q\n", part.Profile, part.Runtime, part.Directory)
		if part.Manager != "" {
			fmt.Fprintf(out, "    Package manager: %s\n", part.Manager)
		}
		for _, args := range part.Commands {
			quoted := make([]string, len(args))
			for i, arg := range args {
				quoted[i] = strconv.Quote(arg)
			}
			fmt.Fprintf(out, "    %s\n", strings.Join(quoted, " "))
		}
		for _, note := range part.Notes {
			fmt.Fprintf(out, "    %s\n", note)
		}
	}
	fmt.Fprintln(out, "Installation can execute repository and dependency code inside the sandbox. Configured network destinations remain reachable.")
}

func approveSetup(cmd *cobra.Command, yes bool) error {
	if yes {
		return nil
	}
	if file, ok := cmd.InOrStdin().(*os.File); ok && !term.IsTerminal(int(file.Fd())) {
		return fmt.Errorf("setup approval requires a terminal; review the plan and pass --yes for noninteractive setup")
	}
	fmt.Fprint(cmd.OutOrStdout(), "Prepare this project? [y/N] ")
	type inputResult struct {
		answer string
		err    error
	}
	input := make(chan inputResult, 1)
	go func() {
		answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		input <- inputResult{answer, err}
	}()
	var answer string
	var err error
	select {
	case <-cmd.Context().Done():
		return cmd.Context().Err()
	case result := <-input:
		answer, err = result.answer, result.err
	}
	if err != nil && err != io.EOF {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
		return fmt.Errorf("setup was not approved; source files were retained")
	}
	return nil
}

// Keep reviewed custom grants and tools when discovery changes the project set.
func proposeProfile(cfg *config.Config, plan project.Plan) config.Config {
	proposed := *cfg
	if !reflect.DeepEqual(cfg.Setup, plan.Setup()) {
		configureProfile(&proposed, plan)
		if cfg.Setup.Profile != "" {
			// Remove grants supplied by old profiles as their languages disappear.
			// Explicit grants outside those defaults remain host policy choices.
			oldDefaults := map[string]bool{}
			oldParts := cfg.Setup.Projects
			if cfg.Setup.Profile != "multi" {
				oldParts = []config.SetupProject{{Profile: cfg.Setup.Profile, Manager: cfg.Setup.Manager}}
			}
			for _, part := range oldParts {
				for _, rule := range profileNetworkRules(part.Profile, part.Manager) {
					oldDefaults[rule] = true
				}
			}
			for _, rule := range cfg.Network.Allow {
				if oldDefaults[rule] {
					continue
				}
				found := false
				for _, existing := range proposed.Network.Allow {
					found = found || existing == rule
				}
				if !found {
					proposed.Network.Allow = append(proposed.Network.Allow, rule)
				}
			}
			proposed.Resources = cfg.Resources
			if proposed.Resources.Disk == "10GB" && len(proposed.Setup.Runtimes) > 1 {
				proposed.Resources.Disk = "20GB"
			}
			if proposed.Resources.Disk == "10GB" || proposed.Resources.Disk == "20GB" {
				for _, part := range plan.Parts() {
					if config.ExtendedProfile(part.Profile) {
						proposed.Resources.Disk = "40GB"
						break
					}
				}
			}
			oldCommands := map[string]bool{}
			for _, command := range config.AllSetupCommands(cfg.Setup) {
				oldCommands[command] = true
			}
			for image, commands := range cfg.Tools {
				for _, command := range commands {
					if !oldCommands[command] {
						proposed.Tools[image] = append(proposed.Tools[image], command)
					}
				}
			}
		}
	}
	return proposed
}

func configureProfile(cfg *config.Config, plan project.Plan) {
	cfg.Setup = plan.Setup()
	cfg.Network.Allow = nil
	for _, part := range plan.Parts() {
		for _, rule := range profileNetworkRules(part.Profile, part.Manager) {
			found := false
			for _, old := range cfg.Network.Allow {
				found = found || old == rule
			}
			if !found {
				cfg.Network.Allow = append(cfg.Network.Allow, rule)
			}
		}
	}
	cfg.Tools = map[string][]string{plan.Image: config.AllSetupCommands(cfg.Setup)}
	cfg.Resources.Memory = "4GB"
	cfg.Resources.CPUs = 2
	if len(cfg.Setup.Runtimes) > 1 {
		cfg.Resources.Disk = "20GB"
	}
	for _, part := range plan.Parts() {
		if config.ExtendedProfile(part.Profile) {
			cfg.Resources.Disk = "40GB"
			break
		}
	}
}

func profileNetworkRules(profile, manager string) []string {
	var rules []string
	switch profile {
	case "node":
		rules = []string{"registry.npmjs.org:443"}
	case "python":
		rules = []string{"pypi.org:443", "files.pythonhosted.org:443"}
	case "go":
		rules = []string{"proxy.golang.org:443", "sum.golang.org:443", "storage.googleapis.com:443"}
	case "rust":
		rules = []string{"index.crates.io:443", "static.crates.io:443"}
	case "java":
		rules = []string{"repo.maven.apache.org:443", "repo1.maven.org:443", "plugins.gradle.org:443", "plugins-artifacts.gradle.org:443", "services.gradle.org:443", "downloads.gradle.org:443", "github.com:443", "release-assets.githubusercontent.com:443"}
	case "dotnet":
		rules = []string{"api.nuget.org:443", "globalcdn.nuget.org:443", "www.nuget.org:443"}
	case "ruby":
		rules = []string{"rubygems.org:443", "index.rubygems.org:443"}
	case "php":
		rules = []string{"repo.packagist.org:443", "packagist.org:443", "api.github.com:443", "codeload.github.com:443", "github.com:443"}

	}
	if strings.HasPrefix(manager, "yarn@") {
		rules = append(rules, "registry.yarnpkg.com:443", "repo.yarnpkg.com:443")
	}
	return rules
}

func writeProjectConfig(dir string, cfg *config.Config, exclusive bool) error {
	var content strings.Builder
	content.WriteString("# Host-approved Watermelon policy. Repository setup runs inside the sandbox.\n")
	if cfg.Workspace.Disk != "" {
		content.WriteString("# Managed workspace: source files are on a guest disk, not in this host directory.\n")
	}
	if err := toml.NewEncoder(&content).Encode(cfg); err != nil {
		return err
	}
	path := filepath.Join(dir, projectConfigName)
	if exclusive {
		return writeNewPrivateIdentityFile(path, []byte(content.String()))
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) {
		return fmt.Errorf("refusing to replace untrusted project configuration")
	}
	f, err := os.CreateTemp(dir, ".watermelon-policy-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content.String()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func destroyAt(cmd *cobra.Command, dir, name string) error {
	destroy := newDestroyCmdAt(dir)
	destroy.SilenceErrors, destroy.SilenceUsage = true, true
	destroy.SetOut(cmd.OutOrStdout())
	destroy.SetErr(cmd.ErrOrStderr())
	destroy.SetContext(cmd.Context())
	args := []string{"--force"}
	if name != "" {
		args = append(args, "--name", name)
	}
	destroy.SetArgs(args)
	err := destroy.Execute()
	// A local project can change its detected profile before its first VM is
	// created. There is no compute state to replace in that case.
	if errors.Is(err, errNoSandboxVM) {
		return nil
	}
	return err
}

func requireUnchangedSetupTarget(expected targetContext) error {
	name := ""
	if expected.NameExplicit {
		name = expected.VMName
	}
	current, err := resolveConfiguredTarget(expected.ProjectRoot, name)
	if err != nil {
		return err
	}
	want, err := config.AppliedConfigDigest(expected.Config)
	if err != nil {
		return err
	}
	got, err := config.AppliedConfigDigest(current.Config)
	if err != nil {
		return err
	}
	if current.VMName != expected.VMName || current.ProjectRoot != expected.ProjectRoot || want != got {
		return fmt.Errorf("project settings changed during setup; retry 'watermelon up' to review the current plan")
	}
	return nil
}

func projectCommandArgs(cfg *config.Config, args []string) []string {
	if cfg.Setup.Profile == "" {
		return args
	}
	if len(args) == 1 && strings.ContainsAny(args[0], " \t\n;&|<>*?$`") {
		args = []string{"sh", "-c", args[0]}
	}
	return append([]string{"/usr/local/bin/watermelon-project"}, args...)
}

// Directory is passed as a positional argument, never interpolated as shell code.
func projectCommandArgsInDir(cfg *config.Config, directory string, args []string) []string {
	if directory == "" || directory == "." {
		return projectCommandArgs(cfg, args)
	}
	result := []string{"/bin/sh", "-c", `cd -- "$1" || exit; shift; exec "$@"`, "watermelon-project-cwd", path.Join(config.DefaultWorkdir(cfg), directory)}
	return append(result, projectCommandArgs(cfg, args)...)
}

// Each guest operation revalidates both identity and policy under the same
// lifecycle/usage-lease handoff as exec. Never attach by a bare public name.
func withSetupGuest(expected targetContext, action func(targetContext) error) error {
	if err := requireUnchangedSetupTarget(expected); err != nil {
		return err
	}
	current := expected
	lock, err := acquireVMLifecycleLock(current.VMName)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := requireVMProjectBinding(current.ProjectRoot, current.VMName); err != nil {
		return err
	}
	if err := requireCurrentAppliedPolicyAndStopUnsafe(current.ProjectRoot, current.VMName, cliGetVMStatus(current.VMName), current.Config, current.NameExplicit); err != nil {
		return err
	}
	if cliGetVMStatus(current.VMName) != lima.StatusRunning {
		return fmt.Errorf("sandbox stopped during setup; retry 'watermelon up'")
	}
	if err := requireRuntimePolicyAppliedAndStopUnsafe(current.ProjectRoot, current.VMName, false, current.NameExplicit); err != nil {
		return err
	}
	lease, err := acquireSharedVMUsageLease(current.VMName)
	if err != nil {
		return err
	}
	defer lease.Release()
	if err := lock.Release(); err != nil {
		return err
	}
	return action(current)
}

const guestManifestReader = `import os,stat,json,base64,sys,fnmatch
result={}
total=0
entries=0
directories=0
names=set(sys.argv[1:])
excluded={'node_modules','vendor','target','venv','dist','build','__pycache__','bin','obj','out','CMakeFiles'}
def walk(directory, prefix, depth):
 global total,entries,directories
 directories+=1
 if directories>2048: raise ValueError('project discovery exceeds 2048 directories')
 with os.scandir(directory) as listing:
  for entry in listing:
   entries+=1
   if entries>20000: raise ValueError('project discovery exceeds 20000 entries')
   name=entry.name
   relative=prefix+name
   if entry.is_dir(follow_symlinks=False) and not name.startswith('.') and name not in excluded:
    if depth>=8: raise ValueError('project discovery exceeds depth 8: '+relative)
    child=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_DIRECTORY|os.O_NONBLOCK,dir_fd=directory)
    try: walk(child,relative+'/',depth+1)
    finally: os.close(child)
   elif any(fnmatch.fnmatchcase(name, pattern) for pattern in names):
    fd=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=directory)
    with os.fdopen(fd,'rb') as f:
     info=os.fstat(f.fileno())
     if not stat.S_ISREG(info.st_mode) or info.st_size>8388608: raise ValueError('unsafe or oversized manifest: '+relative)
     data=f.read(8388609)
     total+=len(data)
     if len(data)>8388608 or total>25165824: raise ValueError('project metadata is too large')
     result[relative]=base64.b64encode(data).decode('ascii')
root=os.open('.',os.O_RDONLY|os.O_DIRECTORY)
try: walk(root,'',0)
finally: os.close(root)
print(json.dumps(result))`

func readGuestManifests(cmd *cobra.Command, target targetContext) (map[string][]byte, error) {
	var output []byte
	err := withSetupGuest(target, func(current targetContext) error {
		var err error
		args := append([]string{"/usr/bin/python3", "-I", "-c", guestManifestReader}, project.ManifestNames...)
		output, err = setupGuestOutput(cmd.Context(), current.VMName, current.Workdir, args, 36<<20)
		return err
	})
	if err != nil {
		return nil, err
	}
	var encoded map[string]string
	if err := json.Unmarshal(output, &encoded); err != nil {
		return nil, fmt.Errorf("invalid guest metadata: %w", err)
	}
	files := map[string][]byte{}
	total := 0
	for name, value := range encoded {
		if !project.ValidManifestPath(name) {
			return nil, fmt.Errorf("unexpected guest manifest")
		}
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if len(data) > project.MaxFileSize || total > project.MaxMetadataSize {
			return nil, fmt.Errorf("guest metadata exceeds limits")
		}
		files[name] = data
	}
	return files, nil
}

func stopSetupVM(target targetContext) error {
	lock, err := acquireVMLifecycleLock(target.VMName)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := requireVMProjectBinding(target.ProjectRoot, target.VMName); err != nil {
		return err
	}
	return cliStopVM(target.VMName)
}

func cloneGuestArgs(source string) []string {
	if !strings.HasSuffix(source, ".git") {
		source += ".git"
	}
	return []string{"/bin/sh", "-c", `set -eu
git_clean() {
 /usr/bin/env -i HOME=/var/empty PATH=/usr/bin:/bin GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 GIT_LFS_SKIP_SMUDGE=1 /usr/bin/git -c core.hooksPath=/dev/null -c init.templateDir= -c protocol.allow=never -c protocol.https.allow=always -c http.followRedirects=false -c http.version=HTTP/1.1 -c http.lowSpeedLimit=1 -c http.lowSpeedTime=30 "$@"
}
if [ -d /workspace/project/.git ] && git_clean -C /workspace/project rev-parse --verify HEAD >/dev/null 2>&1; then
 origin=$(git_clean -C /workspace/project remote get-url origin)
 [ "${origin%.git}" = "${1%.git}" ] || { echo 'existing guest checkout has a different origin' >&2; exit 1; }
else
 git_clean clone --no-recurse-submodules -- "$1" /workspace/project
fi`, "watermelon-clone", source}
}
