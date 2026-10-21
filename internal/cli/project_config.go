package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"golang.org/x/sys/unix"
)

func loadProjectConfig(dir string) (*config.Config, error) {
	configPath := filepath.Join(dir, projectConfigName)
	fd, err := unix.Open(configPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s found at %q (run 'watermelon init' first): %w", projectConfigName, configPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("opening project config %q: %w", configPath, err)
	}
	file := os.NewFile(uintptr(fd), configPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("opening project config %q: invalid file descriptor", configPath)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspecting project config %q: %w", configPath, statErr)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("project config %q must be a regular file, got %s", configPath, info.Mode().Type())
	}
	trusted, trustErr := trustedProjectConfigFile(info)
	if trustErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("checking project config %q ownership: %w", configPath, trustErr)
	}
	if !trusted {
		_ = file.Close()
		return nil, fmt.Errorf("project config %q has untrusted ownership or permissions; require a current-user-owned file that is not world-writable, or a root-owned file that is not group/world-writable", configPath)
	}
	if info.Size() > maxProjectConfigSize {
		_ = file.Close()
		return nil, fmt.Errorf("project config %q exceeds the %d-byte limit", configPath, maxProjectConfigSize)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxProjectConfigSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("reading project config %q: %w", configPath, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing project config %q: %w", configPath, closeErr)
	}
	if len(data) > maxProjectConfigSize {
		return nil, fmt.Errorf("project config %q exceeds the %d-byte limit", configPath, maxProjectConfigSize)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("loading project config %q: %w", configPath, err)
	}
	return cfg, nil
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
		candidates[0] = derivedVMName(dir)
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
