package cli

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func NewRunCmd() *cobra.Command {
	var noShell bool
	var name string
	var workdir string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Enter the project sandbox VM",
		Long:  "Start the project VM (creating it if needed) and open an interactive shell.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunWithOptions(runOptions{OpenShell: !noShell, Name: name, Workdir: workdir})
		},
	}
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Start or create the VM without opening an interactive shell")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the path-derived name)")
	cmd.Flags().StringVar(&workdir, "workdir", "", "Working directory inside the VM")
	return cmd
}

type runOptions struct {
	OpenShell bool
	Name      string
	Workdir   string
}

const recreatePolicyCommand = "watermelon destroy --force && watermelon run"

func policyCommandNeedsExplicitName(dir, vmName string, nameExplicit []bool) bool {
	return (len(nameExplicit) > 0 && nameExplicit[0]) || vmName != lima.VMNameFromPath(dir)
}

func runPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon run"
	}
	return fmt.Sprintf("watermelon run --name %s", vmName)
}

func destroyPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon destroy --force"
	}
	return fmt.Sprintf("watermelon destroy --name %s --force", vmName)
}

func recreatePolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return recreatePolicyCommand
	}
	// VM names have already passed config.ValidateVMName, whose accepted
	// character set is safe to render directly in a suggested shell command.
	return fmt.Sprintf("watermelon destroy --name %s --force && watermelon run --name %s", vmName, vmName)
}

func restartPolicyCommandForVM(dir, vmName string, nameExplicit ...bool) string {
	if !policyCommandNeedsExplicitName(dir, vmName, nameExplicit) {
		return "watermelon stop && watermelon run"
	}
	return fmt.Sprintf("watermelon stop --name %s && watermelon run --name %s", vmName, vmName)
}

type appliedPolicyState int

const (
	policyNotApplied appliedPolicyState = iota
	policyCurrent
	policyStale
	policyUnverifiedLegacy
	policyUnverifiedMissing
	policyUnverifiedInvalid
	policyComparisonUnavailable
)

type appliedPolicyAssessment struct {
	State    appliedPolicyState
	Snapshot config.AppliedPolicySnapshot
	Err      error
}

type appliedPolicyHostContext struct {
	VMName             string
	ProjectRoot        string
	ProjectRootLexical string
	UserConfig         string
	UserConfigLexical  string
	AppStateRoot       string
	LimaHome           string
	LimaHomeLexical    string
	MountsLexical      map[string]string
	StateDir           string
	SnapshotPath       string
	Recorded           config.AppliedHostContext
}

var (
	cliProjectMountSource = lima.ProjectMountSource
	cliInstanceMount      = lima.InstanceMount
	cliGetVMStatus        = lima.GetStatus
	cliStartVM            = lima.Start
	cliStopVM             = lima.Stop
	cliShellVM            = lima.Shell
	cliVerifyPolicy       = lima.VerifyProvisioningComplete
	cliGenerateConfig     = lima.GenerateConfigForInstance
	cliSavePolicySnapshot = saveAppliedPolicySnapshotWithHost
	cliSaveVerdictPort    = savePort
	cliSaveVerdictPortAt  = savePortAt
)

func runRun() error {
	return runRunWithOptions(runOptions{OpenShell: true})
}

func listenForAskVerdicts(vmName string, port int) (net.Listener, error) {
	return listenForAskVerdictsWith(vmName, port, net.Listen)
}

func startAskVerdictServerForExistingVM(dir, vmName string) (net.Listener, error) {
	instance, err := loadOwnedNamedVMIdentity(dir, vmName)
	if err != nil {
		return nil, fmt.Errorf("loading ask-mode VM runtime state: %w", err)
	}
	if _, err := hashPreparedNfqdBinaryAtPath(instance.Paths.NfqdPath); err != nil {
		return nil, fmt.Errorf("verifying the registered nfqd bootstrap: %w", err)
	}
	authKey, err := verdictAuthKeyForInstance(instance)
	if err != nil {
		return nil, err
	}

	savedPort, err := savedAskVerdictPortForExistingVM(instance)
	if err != nil {
		return nil, err
	}
	listener, err := listenForAskVerdicts(vmName, savedPort)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(dir, ".watermelon.toml")
	project := filepath.Base(dir)
	srv := ask.NewServer(project, configPath, authKey, ask.ShowDialog)
	go srv.Serve(listener)
	return listener, nil
}

func savedAskVerdictPortForExistingVM(instance namedVMInstanceIdentity) (int, error) {
	port, exists, err := readSavedPortAtStrict(instance.Paths.VerdictPortPath)
	if err != nil {
		return 0, fmt.Errorf("reading the saved ask port for VM %q: %w", instance.Identity.VMName, err)
	}
	if !exists {
		return 0, fmt.Errorf("ask-mode VM %q has no saved verdict port; the selected VM must be destroyed and recreated", instance.Identity.VMName)
	}
	return port, nil
}

func verdictAuthKeyForInstance(instance namedVMInstanceIdentity) (ask.AuthKey, error) {
	key, err := ask.ParseAuthKey(instance.VerdictAuthKey)
	if err != nil {
		return ask.AuthKey{}, fmt.Errorf("ask-mode VM %q has no valid verdict authentication key; the selected VM must be destroyed and recreated: %w", instance.Identity.VMName, err)
	}
	return key, nil
}

func runRunWithOptions(opts runOptions) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target, err := resolveConfiguredTarget(dir, opts.Name)
	if err != nil {
		return err
	}
	dir = target.ProjectRoot
	cfg := target.Config
	vmName := target.VMName
	workdir := target.Workdir
	if opts.Workdir != "" {
		if err := config.ValidateGuestWorkdir(opts.Workdir); err != nil {
			return fmt.Errorf("invalid --workdir %q: %w", opts.Workdir, err)
		}
		workdir = opts.Workdir
	}
	lifecycleLock, err := acquireVMLifecycleLock(vmName)
	if err != nil {
		return fmt.Errorf("locking VM %q lifecycle: %w", vmName, err)
	}
	defer lifecycleLock.Release()

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
	// Rejecting a headless ask-mode session must happen only after the locked
	// applied-policy check above. Otherwise changing a running VM from a more
	// permissive policy to ask and invoking --no-shell could leave the stale VM
	// running instead of failing closed.
	if cfg.Security.Enforcement == config.EnforcementAsk && !opts.OpenShell {
		return errors.New("ask enforcement requires a foreground verdict server; run without --no-shell and keep the interactive session open")
	}

	needsIdentity := !config.MountProjectEnabled(&cfg.VM) || cfg.VM.Name != "" || cfg.Security.Enforcement == config.EnforcementAsk
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
	if cfg.Security.Enforcement == "ask" {
		if instanceIdentity == nil {
			return errors.New("internal error: ask enforcement requires a VM identity bootstrap")
		}
		if status == lima.StatusNotFound {
			if err := ensureNfqdBinaryAtPath(instanceIdentity.Paths.NfqdPath); err != nil {
				return fmt.Errorf("building nfqd: %w", err)
			}
		} else if _, err := hashPreparedNfqdBinaryAtPath(instanceIdentity.Paths.NfqdPath); err != nil {
			return fmt.Errorf("verifying the registered nfqd bootstrap: %w", err)
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
		verdictListener, listenErr = listenForAskVerdicts(vmName, verdictPort)
		if listenErr != nil {
			return listenErr
		}
		defer verdictListener.Close()

		verdictPort = verdictListener.Addr().(*net.TCPAddr).Port

		configPath := filepath.Join(dir, ".watermelon.toml")
		project := filepath.Base(dir)
		srv := ask.NewServer(project, configPath, verdictAuthKey, ask.ShowDialog)
		go srv.Serve(verdictListener)
		fmt.Printf("Verdict server listening on port %d...\n", verdictPort)
	}

	created := false
	var creationHost *appliedPolicyHostContext
	if status == lima.StatusNotFound {
		if err := clearAppliedPolicySnapshotForVM(dir, vmName); err != nil {
			return fmt.Errorf("clearing previous applied-policy snapshot: %w", err)
		}
		fmt.Println("Creating sandbox VM...")

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

		if err := startVMFailClosed(dir, vmName, tmpFile.Name()); err != nil {
			var staged *lima.StartError
			if errors.As(err, &staged) && staged.Stage == lima.StartStageStart {
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
		fmt.Println("Starting sandbox VM...")
		if err := startVMFailClosed(dir, vmName, ""); err != nil {
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

	sshHost := lima.GetSSHHost(vmName)
	fmt.Printf("IDE: connect to %s\n", sshHost)
	fmt.Println()

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
	shellErr := cliShellVM(vmName, workdir)
	leaseErr := usageLease.Release()
	return errors.Join(shellErr, leaseErr)
}

func loadProjectConfig(dir string) (*config.Config, error) {
	configPath := filepath.Join(dir, ".watermelon.toml")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no .watermelon.toml found (run 'watermelon init' first): %w", os.ErrNotExist)
		}
		return nil, fmt.Errorf("checking config: %w", err)
	}
	return config.ParseFile(configPath)
}

func loadValidatedProjectConfigFailClosed(dir string) (*config.Config, error) {
	return loadValidatedProjectConfigForTarget(dir, "")
}

func stopBoundVMForConfigError(dir string, configErr error) error {
	return stopBoundVMForConfigErrorForTarget(dir, "", configErr)
}

func stopBoundVMForConfigErrorForTarget(dir, vmName string, configErr error) error {
	candidates := []string{vmName}
	var lookupErr error
	if vmName == "" {
		candidates[0] = lima.VMNameFromPath(dir)
		identities, err := listNamedVMIdentitiesBestEffort()
		if err != nil {
			lookupErr = fmt.Errorf("owned named VMs could not be completely enumerated after the config failure: %w", err)
		}
		canonicalDir, canonicalErr := canonicalProjectRoot(dir)
		if canonicalErr != nil {
			lookupErr = errors.Join(lookupErr, canonicalErr)
		} else {
			for _, identity := range identities {
				if identity.Identity.OwnerProject == canonicalDir && identity.Identity.VMName != candidates[0] {
					candidates = append(candidates, identity.Identity.VMName)
				}
			}
		}
	}
	sort.Strings(candidates)

	var stopErrors []error
	stopped := 0
	for _, candidate := range candidates {
		lifecycleLock, err := acquireVMLifecycleLock(candidate)
		if err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("VM %q was not inspected because its lifecycle could not be locked: %w", candidate, err))
			continue
		}
		func() {
			defer func() {
				if err := lifecycleLock.Release(); err != nil {
					stopErrors = append(stopErrors, err)
				}
			}()
			status := cliGetVMStatus(candidate)
			if status == lima.StatusNotFound || status == lima.StatusStopped {
				return
			}
			if err := requireVMProjectBinding(dir, candidate); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("VM %q was not stopped because its project identity could not be verified: %w", candidate, err))
				return
			}
			if err := cliStopVM(candidate); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("stopping existing VM %q failed: %w", candidate, err))
				return
			}
			stopped++
		}()
	}
	if lookupErr != nil {
		stopErrors = append(stopErrors, lookupErr)
	}
	resultErr := configErr
	if stopped == 1 {
		resultErr = fmt.Errorf("%w; the existing VM was stopped because its policy cannot be verified without a valid config", configErr)
	} else if stopped > 1 {
		resultErr = fmt.Errorf("%w; %d existing VM(s) were stopped because their policy cannot be verified without a valid config", configErr, stopped)
	}
	if len(stopErrors) > 0 {
		return errors.Join(resultErr, errors.Join(stopErrors...))
	}
	return resultErr
}

func startVMFailClosed(dir, vmName, configPath string) error {
	if err := cliStartVM(vmName, configPath); err != nil {
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

func saveNewVMPolicySnapshotOrStop(dir, vmName string, host appliedPolicyHostContext, cfg *config.Config, nameExplicit ...bool) error {
	if err := cliSavePolicySnapshot(host, cfg); err != nil {
		saveErr := fmt.Errorf("recording applied policy after VM creation: %w; destroy and recreate the VM with '%s'", err, recreatePolicyCommandForVM(dir, vmName, nameExplicit...))
		if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
			return errors.Join(saveErr, fmt.Errorf("the newly created VM was not stopped because its project identity could not be re-verified: %w", bindErr))
		}
		if stopErr := cliStopVM(vmName); stopErr != nil {
			return errors.Join(saveErr, fmt.Errorf("stopping the newly created VM after its policy snapshot could not be recorded: %w", stopErr))
		}
		return fmt.Errorf("%w; the newly created VM was stopped because its applied policy could not be recorded", saveErr)
	}
	return nil
}

func ensureNfqdBinary(projectDir string) error {
	binDir, err := prepareNfqdRuntimeDirectories(projectDir)
	if err != nil {
		return err
	}
	return ensureNfqdBinaryAtPath(filepath.Join(binDir, "watermelon-nfqd"))
}

func ensureNfqdBinaryAtPath(nfqdPath string) error {
	if err := preparePrivateRuntimeDirectory(filepath.Dir(nfqdPath)); err != nil {
		return err
	}

	if source, err := findNfqdBinary(); err == nil {
		fmt.Println("Installing network interceptor for VM...")
		return copyExecutable(source, nfqdPath)
	}

	sourceRoot, err := findWatermelonSourceRoot()
	if err != nil {
		return errors.New("watermelon-nfqd sidecar not found; install the release sidecar or set WATERMELON_NFQD_BINARY")
	}

	fmt.Println("Building network interceptor for VM...")
	return installBuiltNfqd(nfqdPath, func(outputPath string) error {
		cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/watermelon-nfqd")
		cmd.Dir = sourceRoot
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	})
}

func hashPreparedNfqdBinaryAtPath(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory %q without following symlinks: %w", dir, err)
	}
	defer unix.Close(dirFD)
	fileFD, err := unix.Openat(dirFD, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("opening prepared network interceptor without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fileFD), path)
	if file == nil {
		_ = unix.Close(fileFD)
		return "", errors.New("opening prepared network interceptor: invalid file descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspecting prepared network interceptor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("prepared network interceptor must be a regular file, got %s", info.Mode().Type())
	}
	if !ownedByCurrentUser(info) {
		return "", fmt.Errorf("prepared network interceptor %q is not owned by the current user", path)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hashing prepared network interceptor: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func prepareNfqdRuntimeDirectories(projectDir string) (string, error) {
	watermelonDir := filepath.Join(projectDir, ".watermelon")
	if err := preparePrivateRuntimeDirectory(watermelonDir); err != nil {
		return "", err
	}
	binDir := filepath.Join(watermelonDir, "bin")
	if err := preparePrivateRuntimeDirectory(binDir); err != nil {
		return "", err
	}
	return binDir, nil
}

func preparePrivateRuntimeDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating private runtime directory %q: %w", path, err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("runtime path %q must be a real directory, not a symlink or non-directory: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("opening private runtime directory %q: invalid file descriptor", path)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspecting private runtime directory %q: %w", path, err)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("private runtime directory %q is not owned by the current user", path)
	}
	if err := file.Chmod(0700); err != nil {
		return fmt.Errorf("securing private runtime directory %q: %w", path, err)
	}
	return nil
}

func hashPreparedNfqdBinary(projectDir string) (string, error) {
	const directoryFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_DIRECTORY | unix.O_NOFOLLOW
	projectFD, err := unix.Open(projectDir, directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening project directory for network interceptor verification: %w", err)
	}
	defer unix.Close(projectFD)

	watermelonFD, err := unix.Openat(projectFD, ".watermelon", directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory component %q without following symlinks: %w", ".watermelon", err)
	}
	defer unix.Close(watermelonFD)
	binFD, err := unix.Openat(watermelonFD, "bin", directoryFlags, 0)
	if err != nil {
		return "", fmt.Errorf("opening network interceptor directory component %q without following symlinks: %w", "bin", err)
	}
	defer unix.Close(binFD)

	fileFD, err := unix.Openat(binFD, "watermelon-nfqd", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("opening prepared network interceptor without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fileFD), filepath.Join(projectDir, ".watermelon", "bin", "watermelon-nfqd"))
	if file == nil {
		_ = unix.Close(fileFD)
		return "", errors.New("opening prepared network interceptor: invalid file descriptor")
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspecting prepared network interceptor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("prepared network interceptor must be a regular file, got %s", info.Mode().Type())
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hashing prepared network interceptor: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
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
		logHostDir = identity.Paths.GuestStateDir
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
	return cliGenerateConfig(cfg, projectDir, mountSources, lima.GenerateOptions{
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

func installBuiltNfqd(dest string, build func(outputPath string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".watermelon-nfqd-build-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := build(tmpPath); err != nil {
		return err
	}
	info, err := os.Lstat(tmpPath)
	if err != nil {
		return fmt.Errorf("inspecting built network interceptor: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("built network interceptor %q is not a regular file", tmpPath)
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func findNfqdBinary() (string, error) {
	if override := os.Getenv("WATERMELON_NFQD_BINARY"); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("WATERMELON_NFQD_BINARY %q does not exist", override)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{
		"watermelon-nfqd-linux-" + runtime.GOARCH,
		"watermelon-nfqd",
	} {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func findWatermelonSourceRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		nfqdMain := filepath.Join(dir, "cmd", "watermelon-nfqd", "main.go")
		if data, err := os.ReadFile(goMod); err == nil &&
			strings.Contains(string(data), "module github.com/saeta-eth/watermelon") {
			if _, err := os.Stat(nfqdMain); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

func copyExecutable(source, dest string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".watermelon-nfqd-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func readSavedPort(dir string) int {
	return readSavedPortAt(filepath.Join(dir, ".watermelon", "verdict-port"))
}

func readSavedPortAt(portPath string) int {
	dirFD, err := openPrivateRuntimeDir(filepath.Dir(portPath), "verdict-port state")
	if err != nil {
		return 0
	}
	defer unix.Close(dirFD)

	name := filepath.Base(portPath)
	fileFD, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return 0
	}
	file := os.NewFile(uintptr(fileFD), portPath)
	if file == nil {
		_ = unix.Close(fileFD)
		return 0
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > 16 {
		return 0
	}
	data, err := io.ReadAll(io.LimitReader(file, 32))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || validatePortNumber(port) != nil {
		return 0
	}
	return port
}

func savePort(dir string, port int) error {
	return savePortAt(filepath.Join(dir, ".watermelon", "verdict-port"), port)
}

func savePortAt(portPath string, port int) error {
	if err := validatePortNumber(port); err != nil {
		return err
	}
	dirFD, err := openPrivateRuntimeDir(filepath.Dir(portPath), "verdict-port state")
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	name := filepath.Base(portPath)

	var existing unix.Stat_t
	if err := unix.Fstatat(dirFD, name, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if existing.Mode&unix.S_IFMT != unix.S_IFREG {
			return errors.New("saved verdict port must be a regular file, not a symlink or non-regular file")
		}
		if existing.Uid != uint32(os.Geteuid()) {
			return errors.New("saved verdict port is not owned by the current user")
		}
		if os.FileMode(existing.Mode).Perm() != 0600 {
			return fmt.Errorf("saved verdict port has insecure mode %04o; want 0600", os.FileMode(existing.Mode).Perm())
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("inspecting saved verdict port: %w", err)
	}

	tmpName, tmpFD, err := createPrivateTempFileAt(dirFD, ".verdict-port-")
	if err != nil {
		return fmt.Errorf("creating temporary verdict-port file: %w", err)
	}
	defer unix.Unlinkat(dirFD, tmpName, 0)
	tmp := os.NewFile(uintptr(tmpFD), tmpName)
	if tmp == nil {
		_ = unix.Close(tmpFD)
		return errors.New("creating temporary verdict-port file: invalid file descriptor")
	}
	data := []byte(strconv.Itoa(port) + "\n")
	if written, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temporary verdict-port file: %w", err)
	} else if written != len(data) {
		tmp.Close()
		return fmt.Errorf("writing temporary verdict-port file: %w", io.ErrShortWrite)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temporary verdict-port file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temporary verdict-port file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary verdict-port file: %w", err)
	}
	if err := unix.Renameat(dirFD, tmpName, dirFD, name); err != nil {
		return fmt.Errorf("publishing saved verdict port: %w", err)
	}
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("syncing saved verdict-port directory: %w", err)
	}
	return nil
}

func validatePortNumber(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

func openPrivateWatermelonRuntimeDir(projectDir string) (int, error) {
	const directoryFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_DIRECTORY | unix.O_NOFOLLOW
	projectFD, err := unix.Open(projectDir, directoryFlags, 0)
	if err != nil {
		return -1, fmt.Errorf("opening project directory for verdict-port state: %w", err)
	}
	watermelonFD, err := unix.Openat(projectFD, ".watermelon", directoryFlags, 0)
	_ = unix.Close(projectFD)
	if err != nil {
		return -1, fmt.Errorf("opening private .watermelon runtime directory without following symlinks: %w", err)
	}
	if err := validatePrivateRuntimeDirFD(watermelonFD, ".watermelon runtime directory"); err != nil {
		_ = unix.Close(watermelonFD)
		return -1, err
	}
	return watermelonFD, nil
}

func openPrivateRuntimeDir(path, label string) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("opening private %s %q without following symlinks: %w", label, path, err)
	}
	if err := validatePrivateRuntimeDirFD(fd, label); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func validatePrivateRuntimeDirFD(fd int, label string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspecting private %s: %w", label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) || os.FileMode(stat.Mode).Perm() != 0700 {
		return fmt.Errorf("%s must be a real, current-user-owned 0700 directory", label)
	}
	return nil
}

func createPrivateTempFileAt(dirFD int, prefix string) (string, int, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var random [12]byte
		if _, err := cryptorand.Read(random[:]); err != nil {
			return "", -1, err
		}
		name := prefix + hex.EncodeToString(random[:])
		fd, err := unix.Openat(dirFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err == nil {
			return name, fd, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", -1, err
		}
	}
	return "", -1, errors.New("could not allocate a unique temporary file")
}

func appliedPolicySnapshotPath(dir string) (string, error) {
	host, err := resolveAppliedPolicyHostContext(dir, nil)
	if err != nil {
		return "", err
	}
	return host.SnapshotPath, nil
}

func appliedPolicySnapshotPathForVM(dir, vmName string) (string, error) {
	host, err := resolveAppliedPolicyHostContextForVM(dir, nil, vmName)
	if err != nil {
		return "", err
	}
	return host.SnapshotPath, nil
}

func canonicalProjectRoot(dir string) (string, error) {
	canonical, err := canonicalizeHostPath("project root", dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspecting canonical project root %q: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("canonical project root %q is not a directory", canonical)
	}
	return canonical, nil
}

func resolveAppliedPolicyHostContext(dir string, cfg *config.Config) (appliedPolicyHostContext, error) {
	return resolveAppliedPolicyHostContextForVM(dir, cfg, "")
}

func resolveAppliedPolicyHostContextForVM(dir string, cfg *config.Config, vmName string) (appliedPolicyHostContext, error) {
	projectRootLexical, err := cleanAbsoluteHostPath("project root", dir)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	projectRoot, err := canonicalProjectRoot(dir)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}

	userConfigLexical, err := os.UserConfigDir()
	if err != nil {
		return appliedPolicyHostContext{}, fmt.Errorf("locating user config directory: %w", err)
	}
	userConfigLexical, err = cleanAbsoluteHostPath("user config directory", userConfigLexical)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	userConfigDir, err := canonicalizeHostPath("user config directory", userConfigLexical)
	if err != nil {
		return appliedPolicyHostContext{}, err
	}
	if err := validateTrustedDirectoryIfPresent("user config directory", userConfigDir); err != nil {
		return appliedPolicyHostContext{}, err
	}

	limaHomeLexical, limaHome, err := effectiveLimaHome()
	if err != nil {
		return appliedPolicyHostContext{}, err
	}

	appStateRoot := filepath.Join(userConfigDir, "watermelon")
	namespace := "lima-" + fullPathDigest(limaHome)
	stateDir := filepath.Join(appStateRoot, "vms", namespace)
	defaultVMName := lima.VMNameFromPath(projectRoot)
	if vmName == "" {
		vmName = defaultVMName
	}
	if err := config.ValidateVMName(vmName); err != nil {
		return appliedPolicyHostContext{}, fmt.Errorf("invalid VM name %q: %w", vmName, err)
	}
	snapshotKey := fullPathDigest(projectRoot)
	if vmName != defaultVMName {
		snapshotKey = fullPathDigest(projectRoot + "\x00" + vmName)
	}
	snapshotPath := filepath.Join(stateDir, snapshotKey+".json")

	for _, component := range []string{appStateRoot, filepath.Join(appStateRoot, "vms"), stateDir} {
		if err := validateAppStateComponentIfPresent(component); err != nil {
			return appliedPolicyHostContext{}, err
		}
	}

	mountSources := make(map[string]string)
	mountsLexical := make(map[string]string)
	if cfg != nil {
		mountsLexical, mountSources, err = resolveMountSources(cfg.Mounts)
		if err != nil {
			return appliedPolicyHostContext{}, err
		}
	}

	host := appliedPolicyHostContext{
		VMName:             vmName,
		ProjectRoot:        projectRoot,
		ProjectRootLexical: projectRootLexical,
		UserConfig:         userConfigDir,
		UserConfigLexical:  userConfigLexical,
		AppStateRoot:       appStateRoot,
		LimaHome:           limaHome,
		LimaHomeLexical:    limaHomeLexical,
		MountsLexical:      mountsLexical,
		StateDir:           stateDir,
		SnapshotPath:       snapshotPath,
		Recorded: config.AppliedHostContext{
			ProjectRoot:  projectRoot,
			LimaHome:     limaHome,
			EffectiveUID: uint32(os.Geteuid()),
			MountSources: mountSources,
		},
	}
	if err := validateAppliedPolicyHostIsolation(host, cfg); err != nil {
		return appliedPolicyHostContext{}, err
	}
	return host, nil
}

func effectiveLimaHome() (string, string, error) {
	limaHome := os.Getenv("LIMA_HOME")
	if limaHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("locating home directory for default LIMA_HOME: %w", err)
		}
		limaHome = filepath.Join(home, ".lima")
	}
	lexical, err := cleanAbsoluteHostPath("LIMA_HOME", limaHome)
	if err != nil {
		return "", "", err
	}
	canonical, err := canonicalizeHostPath("LIMA_HOME", lexical)
	if err != nil {
		return "", "", err
	}
	return lexical, canonical, nil
}

func canonicalMountSources(mounts map[string]config.Mount) (map[string]string, error) {
	_, canonical, err := resolveMountSources(mounts)
	return canonical, err
}

func resolveMountSources(mounts map[string]config.Mount) (map[string]string, map[string]string, error) {
	lexical := make(map[string]string, len(mounts))
	canonical := make(map[string]string, len(mounts))
	for source := range mounts {
		expanded, err := expandPolicyMountSource(source)
		if err != nil {
			return nil, nil, err
		}
		cleaned, err := cleanAbsoluteHostPath(fmt.Sprintf("mount source %q", source), expanded)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := canonicalizeHostPath(fmt.Sprintf("mount source %q", source), cleaned)
		if err != nil {
			return nil, nil, err
		}
		lexical[source] = cleaned
		canonical[source] = resolved
	}
	return lexical, canonical, nil
}

func expandPolicyMountSource(source string) (string, error) {
	if source != "~" && !strings.HasPrefix(source, "~/") {
		return source, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding mount source %q: %w", source, err)
	}
	if source == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(source, "~/")), nil
}

// canonicalizeHostPath resolves every existing symlink while retaining any
// not-yet-created suffix beneath the longest existing ancestor.
func canonicalizeHostPath(label, path string) (string, error) {
	cleaned, err := cleanAbsoluteHostPath(label, path)
	if err != nil {
		return "", err
	}
	current := cleaned
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolving %s %q: %w", label, path, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspecting %s %q: %w", label, path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolving %s %q: no existing ancestor", label, path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func cleanAbsoluteHostPath(label, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute, got %q", label, path)
	}
	return filepath.Clean(path), nil
}

func validateAppliedPolicyHostIsolation(host appliedPolicyHostContext, cfg *config.Config) error {
	if pathsOverlap(host.AppStateRoot, host.ProjectRoot) {
		return fmt.Errorf("applied-policy host state %q overlaps the guest-writable project %q", host.AppStateRoot, host.ProjectRoot)
	}
	if pathsOverlap(host.AppStateRoot, host.LimaHome) {
		return fmt.Errorf("applied-policy host state %q overlaps LIMA_HOME %q", host.AppStateRoot, host.LimaHome)
	}
	if pathsOverlap(host.ProjectRoot, host.LimaHome) {
		return fmt.Errorf("guest-writable project %q overlaps LIMA_HOME %q", host.ProjectRoot, host.LimaHome)
	}
	if pathVariantsOverlap(
		[]string{host.ProjectRoot, host.ProjectRootLexical},
		[]string{host.LimaHome, host.LimaHomeLexical},
	) {
		return fmt.Errorf("guest-writable project path %q (canonical %q) overlaps lexical LIMA_HOME %q (canonical %q)", host.ProjectRootLexical, host.ProjectRoot, host.LimaHomeLexical, host.LimaHome)
	}
	if host.UserConfigLexical != host.UserConfig && pathVariantsOverlap(
		[]string{host.ProjectRoot, host.ProjectRootLexical},
		[]string{host.UserConfigLexical},
	) {
		return fmt.Errorf("guest-writable project path %q (canonical %q) overlaps lexical user config directory %q", host.ProjectRootLexical, host.ProjectRoot, host.UserConfigLexical)
	}
	if cfg == nil {
		return nil
	}
	for source, mount := range cfg.Mounts {
		canonical := host.Recorded.MountSources[source]
		lexical := host.MountsLexical[source]
		if pathVariantsOverlap(
			[]string{canonical, lexical},
			[]string{host.LimaHome, host.LimaHomeLexical},
		) {
			return fmt.Errorf("mount source %q (lexical %s, canonical %s) overlaps LIMA_HOME (lexical %q, canonical %q)", source, lexical, canonical, host.LimaHomeLexical, host.LimaHome)
		}
		if host.UserConfigLexical != host.UserConfig && pathVariantsOverlap(
			[]string{canonical, lexical},
			[]string{host.UserConfigLexical},
		) {
			return fmt.Errorf("mount source %q (lexical %s, canonical %s) overlaps lexical user config directory %q", source, lexical, canonical, host.UserConfigLexical)
		}
		if mount.Mode == "rw" && pathsOverlap(canonical, host.AppStateRoot) {
			return fmt.Errorf("read-write mount source %q (%s) overlaps applied-policy host state %q", source, canonical, host.AppStateRoot)
		}
	}
	return nil
}

func pathVariantsOverlap(first, second []string) bool {
	for _, firstPath := range first {
		if firstPath == "" {
			continue
		}
		for _, secondPath := range second {
			if secondPath != "" && pathsOverlap(firstPath, secondPath) {
				return true
			}
		}
	}
	return false
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func fullPathDigest(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func validateTrustedDirectoryIfPresent(label, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting %s %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", label, path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("%s %q is not owned by the current user", label, path)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s %q is writable by group or other users (mode %04o)", label, path, info.Mode().Perm())
	}
	return nil
}

func validateAppStateComponentIfPresent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting applied-policy state directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("applied-policy state path %q must be a real directory, not a symlink or non-directory", path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("applied-policy state directory %q is not owned by the current user", path)
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func legacyConfigDigestPath(dir string) string {
	return filepath.Join(dir, ".watermelon", "config.sha256")
}

func assessAppliedPolicy(dir string, status lima.VMStatus, cfg *config.Config) appliedPolicyAssessment {
	return assessAppliedPolicyForVM(dir, lima.VMNameFromPath(dir), status, cfg)
}

func assessAppliedPolicyForVM(dir, vmName string, status lima.VMStatus, cfg *config.Config) appliedPolicyAssessment {
	host, err := resolveAppliedPolicyHostContextForVM(dir, cfg, vmName)
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	if status == lima.StatusNotFound {
		return appliedPolicyAssessment{State: policyNotApplied}
	}

	if err := secureAppliedPolicyStateDirectories(host, false); err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	data, err := readAppliedPolicySnapshotFile(host.SnapshotPath)
	if os.IsNotExist(err) {
		if _, legacyErr := os.Stat(legacyConfigDigestPath(dir)); legacyErr == nil {
			return appliedPolicyAssessment{State: policyUnverifiedLegacy, Err: config.ErrLegacyAppliedPolicySnapshot}
		} else if !os.IsNotExist(legacyErr) {
			return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: legacyErr}
		}
		return appliedPolicyAssessment{State: policyUnverifiedMissing, Err: err}
	}
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}

	snapshot, err := config.ParseAppliedPolicySnapshot(data)
	if errors.Is(err, config.ErrLegacyAppliedPolicySnapshot) {
		return appliedPolicyAssessment{State: policyUnverifiedLegacy, Err: err}
	}
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Err: err}
	}
	if cfg == nil {
		return appliedPolicyAssessment{State: policyComparisonUnavailable, Snapshot: snapshot}
	}

	matches, err := snapshot.MatchesConfig(cfg, host.Recorded)
	if err != nil {
		return appliedPolicyAssessment{State: policyUnverifiedInvalid, Snapshot: snapshot, Err: err}
	}
	if !matches {
		return appliedPolicyAssessment{State: policyStale, Snapshot: snapshot}
	}
	return appliedPolicyAssessment{State: policyCurrent, Snapshot: snapshot}
}

func requireCurrentAppliedPolicy(dir string, status lima.VMStatus, cfg *config.Config) error {
	return requireCurrentAppliedPolicyForVM(dir, lima.VMNameFromPath(dir), status, cfg)
}

func requireCurrentAppliedPolicyForVM(dir, vmName string, status lima.VMStatus, cfg *config.Config, nameExplicit ...bool) error {
	assessment := assessAppliedPolicyForVM(dir, vmName, status, cfg)
	configured := config.DescribeEnforcement(cfg.Security.Enforcement)
	remedy := fmt.Sprintf("run '%s' to recreate the VM with the configured policy", recreatePolicyCommandForVM(dir, vmName, nameExplicit...))

	switch assessment.State {
	case policyNotApplied, policyCurrent:
		return nil
	case policyStale:
		applied := config.DescribeEnforcement(assessment.Snapshot.Enforcement)
		if assessment.Snapshot.Enforcement == cfg.Security.Enforcement {
			return fmt.Errorf("sandbox configuration is stale: VM-affecting settings changed since creation (recorded enforcement remains %s); %s", applied, remedy)
		}
		return fmt.Errorf("sandbox policy is stale: configured policy is %s, but recorded policy is %s; %s", configured, applied, remedy)
	case policyUnverifiedLegacy:
		return fmt.Errorf("sandbox policy is unverified: the legacy VM snapshot does not record its applied enforcement mode; %s", remedy)
	case policyUnverifiedMissing:
		return fmt.Errorf("sandbox policy is unverified: the VM has no applied-policy snapshot; %s", remedy)
	case policyComparisonUnavailable:
		return fmt.Errorf("sandbox policy cannot be compared with the configured policy; %s", remedy)
	default:
		return fmt.Errorf("sandbox policy is unverified: cannot read its applied-policy snapshot (%v); %s", assessment.Err, remedy)
	}
}

func requireCurrentAppliedPolicyAndStopUnsafe(dir, vmName string, status lima.VMStatus, cfg *config.Config, nameExplicit ...bool) error {
	policyErr := requireCurrentAppliedPolicyForVM(dir, vmName, status, cfg, nameExplicit...)
	if policyErr == nil || status == lima.StatusNotFound || status == lima.StatusStopped {
		return policyErr
	}
	if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
		return errors.Join(policyErr, fmt.Errorf("the existing VM was not stopped because its project identity could not be re-verified: %w", bindErr))
	}
	if stopErr := cliStopVM(vmName); stopErr != nil {
		return errors.Join(policyErr, fmt.Errorf("stopping the existing VM failed: %w", stopErr))
	}
	return fmt.Errorf("%w; the existing VM was stopped so its unverified or stale policy cannot keep sending traffic", policyErr)
}

func requireVMProjectBinding(dir, vmName string) error {
	instance, identityErr := loadNamedVMIdentity(vmName)
	if identityErr == nil {
		if err := verifyNamedVMIdentityOwner(instance.Identity, dir); err != nil {
			return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
		}
		if err := requireNamedVMIdentityMount(instance); err != nil {
			return err
		}
		if !instance.Identity.MountProject {
			return nil
		}
		// A registered mounted VM must satisfy both the durable identity and
		// the original project-mount binding.
		if err := requireLegacyProjectMountBinding(dir, vmName); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(identityErr, os.ErrNotExist) {
		return fmt.Errorf("cannot verify the registered identity for VM %q: %w", vmName, identityErr)
	}
	if vmName != lima.VMNameFromPath(dir) {
		return fmt.Errorf("refusing to use custom-named VM %q without a valid Watermelon identity record", vmName)
	}
	return requireLegacyProjectMountBinding(dir, vmName)
}

func requireNamedVMIdentityMount(instance namedVMInstanceIdentity) error {
	vmName := instance.Identity.VMName
	mount, err := cliInstanceMount(vmName, "/mnt/watermelon/bootstrap")
	if err != nil {
		return fmt.Errorf("cannot verify the identity mount for VM %q: %w", vmName, err)
	}
	if mount.Writable {
		return fmt.Errorf("refusing to use VM %q: its identity bootstrap mount is writable", vmName)
	}
	canonicalBootstrap, err := canonicalizeHostPath("VM bootstrap mount source", mount.Location)
	if err != nil {
		return fmt.Errorf("cannot verify the identity mount for VM %q: %w", vmName, err)
	}
	expectedBootstrap, err := canonicalizeHostPath("registered VM bootstrap directory", instance.Paths.BootstrapDir)
	if err != nil {
		return fmt.Errorf("cannot verify the registered identity for VM %q: %w", vmName, err)
	}
	if canonicalBootstrap != expectedBootstrap {
		return fmt.Errorf("refusing to use VM %q: its identity mount is %q, not the registered bootstrap %q", vmName, canonicalBootstrap, expectedBootstrap)
	}
	return nil
}

func requireLegacyProjectMountBinding(dir, vmName string) error {
	recordedSource, err := cliProjectMountSource(vmName)
	if err != nil {
		return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
	}
	canonicalSource, err := canonicalizeHostPath("VM /project mount source", recordedSource)
	if err != nil {
		return fmt.Errorf("cannot verify that VM %q belongs to this project: %w", vmName, err)
	}
	canonicalDir, err := canonicalProjectRoot(dir)
	if err != nil {
		return err
	}
	if canonicalSource != canonicalDir {
		return fmt.Errorf("refusing to use VM %q: its /project mount is %q, not the current project %q", vmName, canonicalSource, canonicalDir)
	}
	return nil
}

func requireRuntimePolicyApplied(dir, vmName string, newlyCreated bool, nameExplicit ...bool) error {
	recreate := recreatePolicyCommandForVM(dir, vmName, nameExplicit...)
	if err := cliVerifyPolicy(vmName); err != nil {
		if !newlyCreated {
			return fmt.Errorf("sandbox provisioning is not complete: %w; run '%s' to retry provisioning, or '%s' if the problem persists", err, restartPolicyCommandForVM(dir, vmName, nameExplicit...), recreate)
		}
		return fmt.Errorf("sandbox provisioning is not complete: %w; run '%s' to recreate the VM and retry provisioning", err, recreate)
	}
	return nil
}

func requireRuntimePolicyAppliedAndStopUnsafe(dir, vmName string, newlyCreated bool, nameExplicit ...bool) error {
	policyErr := requireRuntimePolicyApplied(dir, vmName, newlyCreated, nameExplicit...)
	if policyErr == nil {
		return nil
	}
	status := cliGetVMStatus(vmName)
	if status == lima.StatusNotFound || status == lima.StatusStopped {
		return policyErr
	}
	if bindErr := requireVMProjectBinding(dir, vmName); bindErr != nil {
		return errors.Join(policyErr, fmt.Errorf("the existing VM was not stopped because its project identity could not be re-verified: %w", bindErr))
	}
	if stopErr := cliStopVM(vmName); stopErr != nil {
		return errors.Join(policyErr, fmt.Errorf("stopping the VM after its policy readiness check failed: %w", stopErr))
	}
	return fmt.Errorf("%w; the VM was stopped because its provisioning completion could not be verified", policyErr)
}

func warnIfNonStrictPolicy(out io.Writer, cfg *config.Config) {
	descriptor, ok := config.LookupEnforcement(cfg.Security.Enforcement)
	if ok && !descriptor.BlocksUnknown {
		fmt.Fprintf(out, "Warning: configured network policy is %s; connections outside the allowlist are allowed.\n", config.DescribeEnforcement(descriptor.Mode))
	}
}

func saveAppliedPolicySnapshot(dir string, cfg *config.Config) error {
	return saveAppliedPolicySnapshotForVM(dir, lima.VMNameFromPath(dir), cfg)
}

func saveAppliedPolicySnapshotForVM(dir, vmName string, cfg *config.Config) error {
	host, err := resolveAppliedPolicyHostContextForVM(dir, cfg, vmName)
	if err != nil {
		return err
	}
	return saveAppliedPolicySnapshotWithHost(host, cfg)
}

func saveAppliedPolicySnapshotWithHost(host appliedPolicyHostContext, cfg *config.Config) error {
	snapshot, err := config.NewAppliedPolicySnapshot(cfg, host.Recorded)
	if err != nil {
		return err
	}
	data, err := config.MarshalAppliedPolicySnapshot(snapshot)
	if err != nil {
		return err
	}

	if err := secureAppliedPolicyStateDirectories(host, true); err != nil {
		return err
	}
	if info, err := os.Lstat(host.SnapshotPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("applied-policy snapshot %q must be a regular file, not a symlink or non-regular file", host.SnapshotPath)
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("applied-policy snapshot %q is not owned by the current user", host.SnapshotPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspecting applied-policy snapshot %q: %w", host.SnapshotPath, err)
	}

	tmp, err := os.CreateTemp(host.StateDir, ".applied-policy-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, host.SnapshotPath)
}

func clearAppliedPolicySnapshot(dir string) error {
	return clearAppliedPolicySnapshotForVM(dir, lima.VMNameFromPath(dir))
}

func clearAppliedPolicySnapshotForVM(dir, vmName string) error {
	host, err := resolveAppliedPolicyHostContextForVM(dir, nil, vmName)
	if err != nil {
		return err
	}
	if err := secureAppliedPolicyStateDirectories(host, false); err != nil {
		return err
	}
	if info, err := os.Lstat(host.SnapshotPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to remove non-regular applied-policy snapshot %q", host.SnapshotPath)
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("refusing to remove applied-policy snapshot %q not owned by the current user", host.SnapshotPath)
		}
	} else if os.IsNotExist(err) {
		return nil
	} else {
		return fmt.Errorf("inspecting applied-policy snapshot %q: %w", host.SnapshotPath, err)
	}
	if err := os.Remove(host.SnapshotPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func secureAppliedPolicyStateDirectories(host appliedPolicyHostContext, create bool) error {
	if create {
		if err := os.MkdirAll(host.StateDir, 0700); err != nil {
			return fmt.Errorf("creating applied-policy state directory %q: %w", host.StateDir, err)
		}
		if err := validateTrustedDirectoryIfPresent("user config directory", host.UserConfig); err != nil {
			return err
		}
	}
	for _, path := range []string{host.AppStateRoot, filepath.Join(host.AppStateRoot, "vms"), host.StateDir} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && !create {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspecting applied-policy state directory %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("applied-policy state path %q must be a real directory, not a symlink or non-directory", path)
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("applied-policy state directory %q is not owned by the current user", path)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("securing applied-policy state directory %q: %w", path, err)
		}
	}
	return nil
}

func readAppliedPolicySnapshotFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("applied-policy snapshot %q must be a regular file, not a symlink or non-regular file", path)
	}
	if !ownedByCurrentUser(before) {
		return nil, fmt.Errorf("applied-policy snapshot %q is not owned by the current user", path)
	}
	if before.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("applied-policy snapshot %q has insecure mode %04o; want 0600", path, before.Mode().Perm())
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() || !ownedByCurrentUser(after) || after.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("applied-policy snapshot %q changed while it was being opened", path)
	}
	return io.ReadAll(io.LimitReader(file, 4<<20))
}
