package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	var noShell bool
	var name string
	var workdir string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Enter the project sandbox VM",
		Long:  "Discover the nearest project config, start its VM (creating it if needed), and open an interactive shell.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if workdir != "" {
				if err := config.ValidateGuestWorkdir(workdir); err != nil {
					return NewUsageError(fmt.Errorf("invalid --workdir %q: %w", workdir, err))
				}
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := ensureSetupForCommand(cmd, dir, name); err != nil {
				return err
			}
			return runRunWithOptions(runOptions{OpenShell: !noShell, Name: name, Workdir: workdir})
		},
	}
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Start or create the VM without opening an interactive shell")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the name derived from the resolved project root)")
	cmd.Flags().StringVar(&workdir, "workdir", "", "Working directory inside the VM")
	return cmd
}

type runOptions struct {
	// Setup binds provisioning to the exact configuration that was inspected
	// and approved, including the selected VM and captured provision scripts.
	ExpectedConfigDigest string
	ExpectedVMName       string
	Context              context.Context
	Err                  io.Writer
	Dir                  string
	Out                  io.Writer
	OpenShell            bool
	Name                 string
	Workdir              string
}

var (
	cliProjectMountSource   = lima.ProjectMountSource
	cliInstanceMount        = lima.InstanceMount
	cliGetVMStatus          = lima.GetStatus
	cliStartVM              = lima.Start
	cliStartVMWithRunner    = lima.StartWithRunner
	cliStopVM               = lima.Stop
	cliShellVM              = lima.Shell
	cliShellVMWithRunner    = lima.ShellWithRunner
	cliVerifyPolicy         = lima.VerifyProvisioningComplete
	cliGenerateConfig       = lima.GenerateConfigForInstance
	cliSavePolicySnapshot   = saveAppliedPolicySnapshotWithHost
	cliSaveVerdictPort      = savePort
	cliSaveVerdictPortAt    = savePortAt
	cliNfqdHostArch         = lima.HostArchitecture
	cliReadBuildInfo        = debug.ReadBuildInfo
	cliExecutable           = os.Executable
	cliNewNfqdRelease       = newNfqdReleaseSource
	cliNewAskCoordinator    = func() askTerminalCoordinator { return ask.NewTerminalCoordinator() }
	cliStartAskServer       = startAskVerdictServerForExistingVMWithDialog
	cliInspectWorkspaceDisk = lima.InspectWorkspaceDisk
	cliCheckForwardedPorts  = checkForwardedPorts
)

func runRun() error {
	return runRunWithOptions(runOptions{OpenShell: true})
}

func runRunWithOptions(opts runOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	var starter lima.CommandRunner
	if opts.Context != nil {
		if err := opts.Context.Err(); err != nil {
			return err
		}
		errOut := opts.Err
		if errOut == nil {
			errOut = os.Stderr
		}
		starter = &setupRunner{ctx: opts.Context, out: out, err: errOut}
	}
	if opts.Workdir != "" {
		if err := config.ValidateGuestWorkdir(opts.Workdir); err != nil {
			return NewUsageError(fmt.Errorf("invalid --workdir %q: %w", opts.Workdir, err))
		}
	}

	dir := opts.Dir
	var err error
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	target, err := resolveConfiguredTarget(dir, opts.Name)
	if err != nil {
		return err
	}
	if opts.ExpectedConfigDigest != "" {
		digest, err := config.AppliedConfigDigest(target.Config)
		if err != nil {
			return err
		}
		if digest != opts.ExpectedConfigDigest || target.VMName != opts.ExpectedVMName {
			return fmt.Errorf("project settings changed during setup; retry 'watermelon up' to review the current plan")
		}
	}
	dir = target.ProjectRoot
	cfg := target.Config
	vmName := target.VMName
	workdir := target.Workdir
	if opts.Workdir != "" {
		workdir = opts.Workdir
	}
	if err := requireCompatibleLima(); err != nil {
		return err
	}
	lifecycleLock, err := acquireVMLifecycleLock(vmName)
	if err != nil {
		return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
	}
	defer lifecycleLock.Release()
	if cfg.Workspace.Disk != "" {
		disk, err := cliInspectWorkspaceDisk(cfg.Workspace.Disk)
		if err != nil {
			return err
		}
		if disk.Instance != "" && disk.Instance != vmName {
			return fmt.Errorf("workspace disk is in use by another VM %q", disk.Instance)
		}
	}

	status := cliGetVMStatus(vmName)
	if status != lima.StatusNotFound {
		if err := requireVMProjectBinding(dir, vmName); err != nil {
			return err
		}
	}
	warnIfNonStrictPolicy(os.Stderr, cfg)
	if err := requireCurrentAppliedPolicyAndStopUnsafe(dir, vmName, status, cfg, target.NameExplicit); err != nil {
		return err
	}
	if status == lima.StatusUnknown {
		return fmt.Errorf("cannot safely use VM %q because its Lima state is unknown", vmName)
	}
	if status == lima.StatusNotFound || status == lima.StatusStopped {
		if err := cliCheckForwardedPorts(cfg.Ports.Forward); err != nil {
			return err
		}
	}
	// Rejecting a headless ask-mode session must happen only after the locked
	// applied-policy check above. Otherwise changing a running VM from a more
	// permissive policy to ask and invoking --no-shell could leave the stale VM
	// running instead of failing closed.
	if cfg.Security.Enforcement == config.EnforcementAsk && !opts.OpenShell {
		return errors.New("ask enforcement requires a foreground verdict server; run without --no-shell and keep the interactive session open")
	}

	needsIdentity := !config.MountProjectEnabled(&cfg.VM) || cfg.VM.Name != "" || cfg.Security.Enforcement == config.EnforcementAsk || cfg.Setup.Profile != ""
	var instanceIdentity *namedVMInstanceIdentity
	removeReservedIdentity := false
	if needsIdentity {
		var instance namedVMInstanceIdentity
		if status == lima.StatusNotFound {
			instance, err = reserveNamedVMIdentity(dir, vmName, cfg)
			if err != nil {
				return fmt.Errorf("reserving VM identity for %q: %w", vmName, err)
			}
			removeReservedIdentity = true
		} else {
			instance, err = loadOwnedNamedVMIdentity(dir, vmName)
			if err != nil {
				return fmt.Errorf("loading VM identity for %q: %w", vmName, err)
			}
		}
		instanceIdentity = &instance
	}
	defer func() {
		if removeReservedIdentity && instanceIdentity != nil {
			if cleanupErr := removeNamedVMIdentity(instanceIdentity.Identity); cleanupErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove failed VM identity reservation for %q: %v\n", vmName, cleanupErr)
			}
		}
	}()

	// Start verdict server for ask enforcement mode
	var verdictListener net.Listener
	var verdictPort int
	var verdictAuthKey ask.AuthKey
	var terminalCoordinator askTerminalCoordinator
	if cfg.Security.Enforcement == "ask" {
		terminalCoordinator = cliNewAskCoordinator()
		if instanceIdentity == nil {
			return errors.New("internal error: ask enforcement requires a VM identity bootstrap")
		}
		if status == lima.StatusNotFound {
			if err := ensureNfqdBinaryAtPath(instanceIdentity.Paths.NfqdPath); err != nil {
				return fmt.Errorf("preparing the ask-mode network interceptor: %w", err)
			}
		} else if err := verifyRegisteredNfqdBinary(instanceIdentity.Paths.NfqdPath); err != nil {
			return fmt.Errorf("verifying the registered nfqd bootstrap: %w; recreate it with '%s'", err, recreatePolicyCommandForVM(dir, vmName, target.NameExplicit))
		}
		verdictAuthKey, err = verdictAuthKeyForInstance(*instanceIdentity)
		if err != nil {
			return err
		}

		// A running or stopped guest can connect only to the port baked into its
		// Lima configuration. Never replace missing or malformed state with a
		// fresh ephemeral port: the host would listen successfully while nfqd
		// kept connecting elsewhere and every prompted connection blocked.
		if status != lima.StatusNotFound {
			verdictPort, err = savedAskVerdictPortForExistingVM(*instanceIdentity)
			if err != nil {
				return err
			}
		}

		var listenErr error
		verdictListener, listenErr = listenForAskVerdicts(vmName, verdictPort, cfg.Ports.Forward...)
		if listenErr != nil {
			return listenErr
		}
		defer verdictListener.Close()

		verdictPort = verdictListener.Addr().(*net.TCPAddr).Port

		configPath := filepath.Join(dir, ".watermelon.toml")
		project := filepath.Base(dir)
		srv := ask.NewServer(
			project,
			configPath,
			verdictAuthKey,
			terminalCoordinator.Dialog,
			ask.WithRecreateCommand(deferredRecreatePolicyCommandForVM(dir, vmName)),
		)
		go srv.Serve(verdictListener)
		fmt.Fprintf(out, "Verdict server listening on port %d...\n", verdictPort)
	}

	created := false
	var creationHost *appliedPolicyHostContext
	if status == lima.StatusNotFound {
		if err := clearAppliedPolicySnapshotForVM(dir, vmName); err != nil {
			return fmt.Errorf("clearing previous applied-policy snapshot: %w", err)
		}
		fmt.Fprintln(out, "Creating sandbox VM...")

		// Save verdict port for future sessions
		if verdictPort > 0 {
			var saveErr error
			if instanceIdentity != nil {
				saveErr = cliSaveVerdictPortAt(instanceIdentity.Paths.VerdictPortPath, verdictPort)
			} else {
				saveErr = cliSaveVerdictPort(dir, verdictPort)
			}
			if saveErr != nil {
				return fmt.Errorf("saving verdict server port: %w", saveErr)
			}
		}

		// Setup SSH config for IDE access
		if err := lima.EnsureSSHConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
		}

		host, err := resolveAppliedPolicyHostContextForVM(dir, cfg, vmName)
		if err != nil {
			return fmt.Errorf("resolving host paths for VM creation: %w", err)
		}
		creationHost = &host

		yamlContent, err := generateConfigForInstanceWithIdentity(cfg, dir, host.Recorded.MountSources, verdictPort, instanceIdentity, target.PreparedProvisionScripts)
		if err != nil {
			return fmt.Errorf("generating Lima config: %w", err)
		}

		// Write temp Lima config
		tmpFile, err := os.CreateTemp("", "watermelon-*.yaml")
		if err != nil {
			return fmt.Errorf("creating temp config file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString(yamlContent); err != nil {
			tmpFile.Close()
			return err
		}
		tmpFile.Close()

		if err := startVMFailClosed(dir, vmName, tmpFile.Name(), starter); err != nil {
			var staged *lima.StartError
			if errors.As(err, &staged) && staged.Stage == lima.StartStageStart {
				removeReservedIdentity = false
			}
			if opts.Context != nil && opts.Context.Err() != nil && instanceIdentity != nil && requireNamedVMIdentityMount(*instanceIdentity) == nil {
				removeReservedIdentity = false
			}
			return err
		}
		removeReservedIdentity = false
		created = true
	} else if status == lima.StatusStopped {
		if err := requireVMProjectBinding(dir, vmName); err != nil {
			return err
		}
		fmt.Fprintln(out, "Starting sandbox VM...")
		if err := startVMFailClosed(dir, vmName, "", starter); err != nil {
			return err
		}
	}

	if err := requireVMProjectBinding(dir, vmName); err != nil {
		return err
	}
	if err := requireRuntimePolicyAppliedAndStopUnsafe(dir, vmName, created, target.NameExplicit); err != nil {
		return err
	}
	if created {
		if creationHost == nil {
			return errors.New("internal error: missing host context for newly created VM")
		}
		if err := saveNewVMPolicySnapshotOrStop(dir, vmName, *creationHost, cfg, target.NameExplicit); err != nil {
			return err
		}
	}

	if cfg.Workspace.Disk != "" {
		state, err := loadWorkspaceState(dir)
		if err != nil {
			return err
		}
		if !state.DiskInitialized {
			state.DiskInitialized = true
			if err := saveWorkspaceState(dir, state); err != nil {
				_ = cliStopVM(vmName)
				return fmt.Errorf("recording workspace initialization: %w", err)
			}
		}
	}
	sshHost := lima.GetSSHHost(vmName)
	fmt.Fprintf(out, "IDE: connect to %s\n\n", sshHost)

	if !opts.OpenShell {
		return lifecycleLock.Release()
	}
	if err := requireVMProjectBinding(dir, vmName); err != nil {
		return err
	}
	usageLease, err := acquireSharedVMUsageLease(vmName)
	if err != nil {
		return fmt.Errorf("leasing VM %q for the interactive shell: %w", vmName, err)
	}
	// Acquire the shared usage lease before releasing the lifecycle mutex. A
	// stop/destroy process therefore cannot delete and recreate the public name
	// while limactl is still attaching this shell to the verified instance.
	if err := lifecycleLock.Release(); err != nil {
		return errors.Join(err, usageLease.Release())
	}
	var shellErr error
	if cfg.Setup.Profile != "" {
		if terminalCoordinator != nil {
			shellErr = cliExecVMWithRunner(vmName, projectCommandArgs(cfg, []string{"bash"}), terminalCoordinator, workdir)
		} else {
			shellErr = cliExecVM(vmName, projectCommandArgs(cfg, []string{"bash"}), workdir)
		}
	} else if terminalCoordinator != nil {
		shellErr = cliShellVMWithRunner(vmName, terminalCoordinator, workdir)
	} else {
		shellErr = cliShellVM(vmName, workdir)
	}
	leaseErr := usageLease.Release()
	return errors.Join(shellErr, leaseErr)
}

func startVMFailClosed(dir, vmName, configPath string, runners ...lima.CommandRunner) error {
	if len(runners) > 1 {
		return errors.New("expected at most one startup runner")
	}
	var err error
	if len(runners) == 1 && runners[0] != nil {
		err = cliStartVMWithRunner(vmName, configPath, runners[0])
	} else {
		err = cliStartVM(vmName, configPath)
	}
	if err != nil {
		startErr := fmt.Errorf("starting VM: %w", err)
		var staged *lima.StartError
		if !errors.As(err, &staged) || staged.Stage != lima.StartStageStart {
			return startErr
		}
		return stopBoundVMForStartError(dir, vmName, startErr)
	}
	return nil
}

func stopBoundVMForStartError(dir, vmName string, startErr error) error {
	status := cliGetVMStatus(vmName)
	if status == lima.StatusNotFound || status == lima.StatusStopped {
		return startErr
	}
	if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
		return errors.Join(startErr, fmt.Errorf("the VM may still be running but was not stopped because its project identity could not be re-verified: %w", bindErr))
	}
	if stopErr := cliStopVM(vmName); stopErr != nil {
		return errors.Join(startErr, fmt.Errorf("stopping the VM after start failed: %w", stopErr))
	}
	return fmt.Errorf("%w; the VM was stopped because startup did not complete safely", startErr)
}

func generateConfigForInstance(cfg *config.Config, projectDir string, mountSources map[string]string, verdictPort int) (string, error) {
	return generateConfigForInstanceWithIdentity(cfg, projectDir, mountSources, verdictPort, nil)
}

func generateConfigForInstanceWithIdentity(cfg *config.Config, projectDir string, mountSources map[string]string, verdictPort int, identity *namedVMInstanceIdentity, preparedScripts ...*lima.PreparedProvisionScripts) (string, error) {
	if len(preparedScripts) > 1 {
		return "", errors.New("expected at most one prepared provision-script set")
	}
	nfqdDigest := ""
	bootstrapHostDir := ""
	logHostDir := ""
	if identity != nil {
		bootstrapHostDir = identity.Paths.BootstrapDir
		if cfg.Workspace.Disk == "" {
			logHostDir = identity.Paths.GuestStateDir
		}
	}
	if cfg.Security.Enforcement == config.EnforcementAsk {
		if identity == nil {
			return "", errors.New("ask enforcement requires a dedicated per-instance identity bootstrap")
		}
		var err error
		nfqdDigest, err = hashPreparedNfqdBinaryAtPath(identity.Paths.NfqdPath)
		if err != nil {
			return "", err
		}
		if _, err := verdictAuthKeyForInstance(*identity); err != nil {
			return "", err
		}
	}
	var prepared *lima.PreparedProvisionScripts
	if len(preparedScripts) == 1 {
		prepared = preparedScripts[0]
	}
	formatWorkspace := false
	if cfg.Workspace.Disk != "" {
		state, err := loadWorkspaceState(projectDir)
		if err != nil {
			return "", err
		}
		formatWorkspace = !state.DiskInitialized
	}
	return cliGenerateConfig(cfg, projectDir, mountSources, lima.GenerateOptions{
		FormatWorkspaceDisk:      formatWorkspace,
		VerdictServerPort:        verdictPort,
		NfqdSHA256:               nfqdDigest,
		VerdictAuthKey:           identityVerdictAuthKey(identity),
		BootstrapHostDir:         bootstrapHostDir,
		LogHostDir:               logHostDir,
		PreparedProvisionScripts: prepared,
	})
}

func identityVerdictAuthKey(identity *namedVMInstanceIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.VerdictAuthKey
}
