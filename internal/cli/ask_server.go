package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/lima"
	"golang.org/x/sys/unix"
)

type askTerminalCoordinator interface {
	lima.CommandRunner
	Dialog(process, domain string, port int, project string) string
}

func listenForAskVerdicts(vmName string, port int, forwardedPorts ...int) (net.Listener, error) {
	return listenForAskVerdictsWith(vmName, port, net.Listen, forwardedPorts...)
}

func startAskVerdictServerForExistingVM(dir, vmName string) (net.Listener, error) {
	return startAskVerdictServerForExistingVMWithDialog(dir, vmName, ask.ShowDialog)
}

func startAskVerdictServerForExistingVMWithDialog(dir, vmName string, dialog ask.DialogFunc) (net.Listener, error) {
	instance, err := loadOwnedNamedVMIdentity(dir, vmName)
	if err != nil {
		return nil, fmt.Errorf("loading ask-mode VM runtime state: %w", err)
	}
	if err := verifyRegisteredNfqdBinary(instance.Paths.NfqdPath); err != nil {
		return nil, fmt.Errorf("verifying the registered nfqd bootstrap: %w; recreate it with '%s'", err, deferredRecreatePolicyCommandForVM(dir, vmName))
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
	if dialog == nil {
		dialog = ask.ShowDialog
	}
	srv := ask.NewServer(
		project,
		configPath,
		authKey,
		dialog,
		ask.WithRecreateCommand(deferredRecreatePolicyCommandForVM(dir, vmName)),
	)
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
