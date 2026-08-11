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
	"syscall"

	"golang.org/x/sys/unix"
)

type askTCPListenFunc func(network, address string) (net.Listener, error)

const askVerdictListenAddress = "127.0.0.1"

// listenForAskVerdictsWith preserves an existing VM's explicit saved-port
// behavior. For a new VM, it keeps every rejected ephemeral listener open
// until a non-reserved port is bound. That prevents the kernel from returning
// the same rejected port again and leaves the selected listener reserved while
// the caller durably saves it and creates the VM.
func listenForAskVerdictsWith(vmName string, port int, listen askTCPListenFunc) (net.Listener, error) {
	if port != 0 {
		return listenForAskVerdictsAtPort(vmName, port, listen)
	}

	reserved, err := reservedAskVerdictPorts(vmName)
	if err != nil {
		return nil, fmt.Errorf("checking reserved ask ports for VM %q: %w", vmName, err)
	}

	rejected := make([]net.Listener, 0)
	defer func() {
		for _, listener := range rejected {
			_ = listener.Close()
		}
	}()

	// Holding collided listeners means each successful bind has a distinct
	// port. There can therefore be at most len(reserved) collisions before a
	// free result, or the final attempt reports allocation exhaustion.
	for attempt := 0; attempt <= len(reserved); attempt++ {
		listener, err := listen("tcp4", net.JoinHostPort(askVerdictListenAddress, "0"))
		if err != nil {
			return nil, fmt.Errorf("starting verdict server on an ephemeral TCP4 port: %w", err)
		}
		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok || validatePortNumber(addr.Port) != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("starting verdict server: listener returned invalid TCP address %q", listener.Addr())
		}
		if _, collision := reserved[addr.Port]; !collision {
			return listener, nil
		}
		rejected = append(rejected, listener)
	}

	return nil, fmt.Errorf("starting verdict server: could not allocate an ephemeral TCP4 port distinct from %d reserved ports", len(reserved))
}

func listenForAskVerdictsAtPort(vmName string, port int, listen askTCPListenFunc) (net.Listener, error) {
	listener, err := listen("tcp4", net.JoinHostPort(askVerdictListenAddress, strconv.Itoa(port)))
	if err == nil {
		return listener, nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, fmt.Errorf("saved ask port %d for VM %q is already in use; close the other Watermelon controller or unrelated process before starting run, exec, or code", port, vmName)
	}
	return nil, fmt.Errorf("starting verdict server on port %d: %w", port, err)
}

// reservedAskVerdictPorts returns ports durably owned by other identities in
// the effective LIMA_HOME namespace. Strict identity enumeration and strict
// port-file reads ensure corrupt or unsafe registry state cannot be mistaken
// for an available port.
func reservedAskVerdictPorts(vmName string) (map[int]string, error) {
	identities, err := listNamedVMIdentities()
	if err != nil {
		return nil, fmt.Errorf("enumerating named VM identities: %w", err)
	}

	reserved := make(map[int]string)
	for _, instance := range identities {
		port, exists, err := readSavedPortAtStrict(instance.Paths.VerdictPortPath)
		if err != nil {
			return nil, fmt.Errorf("reading saved ask port for VM %q: %w", instance.Identity.VMName, err)
		}
		if !exists || instance.Identity.VMName == vmName {
			continue
		}
		reserved[port] = instance.Identity.VMName
	}
	return reserved, nil
}

// readSavedPortAtStrict distinguishes an absent port (valid for non-ask and
// not-yet-created identities) from malformed or unsafe state. Allocation must
// fail closed for the latter instead of silently making a conflicting choice.
func readSavedPortAtStrict(portPath string) (port int, exists bool, err error) {
	dirFD, err := openPrivateRuntimeDir(filepath.Dir(portPath), "verdict-port state")
	if err != nil {
		return 0, false, err
	}
	defer unix.Close(dirFD)

	name := filepath.Base(portPath)
	fileFD, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("opening saved verdict port without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fileFD), portPath)
	if file == nil {
		_ = unix.Close(fileFD)
		return 0, false, errors.New("opening saved verdict port: invalid file descriptor")
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, false, fmt.Errorf("inspecting saved verdict port: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, false, errors.New("saved verdict port must be a regular file")
	}
	if !ownedByCurrentUser(info) {
		return 0, false, errors.New("saved verdict port is not owned by the current user")
	}
	if info.Mode().Perm() != 0600 {
		return 0, false, fmt.Errorf("saved verdict port has insecure mode %04o; want 0600", info.Mode().Perm())
	}
	if info.Size() < 1 || info.Size() > 16 {
		return 0, false, errors.New("saved verdict port has an invalid size")
	}

	data, err := io.ReadAll(io.LimitReader(file, 32))
	if err != nil {
		return 0, false, fmt.Errorf("reading saved verdict port: %w", err)
	}
	port, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, fmt.Errorf("parsing saved verdict port: %w", err)
	}
	if err := validatePortNumber(port); err != nil {
		return 0, false, fmt.Errorf("validating saved verdict port: %w", err)
	}
	return port, true, nil
}
