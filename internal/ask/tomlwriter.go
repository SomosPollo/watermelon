package ask

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
)

// AddDomainToConfig adds a domain to the network.allow list in .watermelon.toml.
// It is a no-op if the domain is already in the list.
// Uses advisory file locking to prevent concurrent write corruption.
func AddDomainToConfig(configPath string, domain string) error {
	// Open the file for locking (create if needed for the lock)
	lockFile, err := os.OpenFile(configPath, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	// Acquire exclusive lock
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	cfg, err := config.ParseFile(configPath)
	if err != nil {
		return err
	}

	// Check for duplicates
	for _, d := range cfg.Network.Allow {
		if d == domain {
			return nil
		}
	}

	cfg.Network.Allow = append(cfg.Network.Allow, domain)

	// Write to temp file then rename for atomic update
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, ".watermelon.toml.tmp*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	encoder := toml.NewEncoder(tmp)
	if err := encoder.Encode(cfg); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, configPath)
}
