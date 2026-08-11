package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

// targetContext is the single, validated interpretation of a command's
// project, configuration, and Lima instance. Commands must not independently
// fall back to a name or a default config after this resolution fails.
type targetContext struct {
	ProjectRoot              string
	VMName                   string
	NameExplicit             bool
	Config                   *config.Config
	Workdir                  string
	IDEWorkdir               string
	PreparedProvisionScripts *lima.PreparedProvisionScripts
}

// resolveManagementTarget permits the legacy path-derived target when a
// project has no config and no explicit name. A present-but-invalid config is
// always an error, and an explicit name never turns a missing config into a
// default configuration.
func resolveManagementTarget(dir, flagName string) (targetContext, error) {
	canonicalDir, err := canonicalProjectRoot(dir)
	if err != nil {
		return targetContext{}, err
	}
	if flagName != "" {
		if err := config.ValidateVMName(flagName); err != nil {
			return targetContext{}, NewUsageError(fmt.Errorf("invalid --name %q: %w", flagName, err))
		}
	}

	cfg, err := loadProjectConfig(canonicalDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && flagName == "" {
			return targetContext{
				ProjectRoot: canonicalDir,
				VMName:      lima.VMNameFromPath(canonicalDir),
			}, nil
		}
		return targetContext{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return targetContext{}, fmt.Errorf("invalid config: %w", err)
	}

	vmName := flagName
	if vmName == "" {
		vmName = cfg.VM.Name
	}
	if vmName == "" {
		vmName = lima.VMNameFromPath(canonicalDir)
	}
	if err := config.ValidateVMName(vmName); err != nil {
		return targetContext{}, fmt.Errorf("invalid VM name %q: %w", vmName, err)
	}
	effective := *cfg
	effective.VM = cfg.VM
	if flagName != "" {
		effective.VM.Name = flagName
	}

	return targetContext{
		ProjectRoot:  canonicalDir,
		VMName:       vmName,
		NameExplicit: flagName != "",
		Config:       &effective,
		Workdir:      config.DefaultWorkdir(&effective),
	}, nil
}

// resolveConfiguredTarget resolves a VM that is configured by the current
// project. A --name value overrides vm.name, but it never makes a missing or
// invalid project configuration acceptable.
func resolveConfiguredTarget(dir, flagName string) (targetContext, error) {
	canonicalDir, err := canonicalProjectRoot(dir)
	if err != nil {
		return targetContext{}, err
	}
	if flagName != "" {
		if err := config.ValidateVMName(flagName); err != nil {
			return targetContext{}, NewUsageError(fmt.Errorf("invalid --name %q: %w", flagName, err))
		}
	}

	cfg, err := loadValidatedProjectConfigForTarget(canonicalDir, flagName)
	if err != nil {
		return targetContext{}, err
	}

	vmName := flagName
	if vmName == "" {
		vmName = cfg.VM.Name
	}
	if vmName == "" {
		vmName = lima.VMNameFromPath(canonicalDir)
	}
	if err := config.ValidateVMName(vmName); err != nil {
		return targetContext{}, fmt.Errorf("invalid VM name %q: %w", vmName, err)
	}

	// Record an explicit CLI override in the effective VM configuration. This
	// binds policy snapshots to the instance that was actually selected while
	// leaving the parsed configuration object untouched.
	effective := *cfg
	effective.VM = cfg.VM
	if flagName != "" {
		effective.VM.Name = flagName
	}

	workdir := config.DefaultWorkdir(&effective)
	ideWorkdir := effective.IDE.Workdir
	if ideWorkdir == "" {
		ideWorkdir = workdir
	}

	target := targetContext{
		ProjectRoot:  canonicalDir,
		VMName:       vmName,
		NameExplicit: flagName != "",
		Config:       &effective,
		Workdir:      workdir,
		IDEWorkdir:   ideWorkdir,
	}
	target, err = prepareTargetProvisionScripts(target)
	if err != nil {
		return targetContext{}, stopBoundVMForConfigErrorForTarget(canonicalDir, vmName, fmt.Errorf("preparing provision scripts: %w", err))
	}
	return target, nil
}

func prepareTargetProvisionScripts(target targetContext) (targetContext, error) {
	if target.Config == nil {
		return target, nil
	}
	prepared, err := lima.PrepareProvisionScripts(target.ProjectRoot, target.Config.Provision.Scripts)
	if err != nil {
		return targetContext{}, err
	}
	effective := *target.Config
	effective.Provision = target.Config.Provision
	effective.Provision.ScriptSHA256 = append([]string(nil), prepared.SHA256...)
	if err := config.Validate(&effective); err != nil {
		return targetContext{}, fmt.Errorf("validating prepared provision scripts: %w", err)
	}
	target.Config = &effective
	target.PreparedProvisionScripts = &prepared
	return target, nil
}

func loadValidatedProjectConfigForTarget(dir, vmNameHint string) (*config.Config, error) {
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		return nil, stopBoundVMForConfigErrorForTarget(dir, vmNameHint, err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, stopBoundVMForConfigErrorForTarget(dir, vmNameHint, fmt.Errorf("invalid config: %w", err))
	}
	return cfg, nil
}
