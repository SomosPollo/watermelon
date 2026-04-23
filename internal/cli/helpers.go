package cli

import (
	"path/filepath"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

// resolveVMName returns the VM name for a command.
// Priority: explicit flag > vm.name in config file > path-derived name.
func resolveVMName(flagName, dir string) string {
	if flagName != "" {
		return flagName
	}
	configPath := filepath.Join(dir, ".watermelon.toml")
	if cfg, err := config.ParseFile(configPath); err == nil && cfg.VM.Name != "" {
		return cfg.VM.Name
	}
	return lima.VMNameFromPath(dir)
}

// resolveVMNameFromConfig resolves the VM name from an already-parsed config,
// avoiding a redundant file read when config is already loaded.
// Priority: explicit flag > config vm.name > path-derived name.
func resolveVMNameFromConfig(flagName, configName, dir string) string {
	if flagName != "" {
		return flagName
	}
	if configName != "" {
		return configName
	}
	return lima.VMNameFromPath(dir)
}
