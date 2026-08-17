package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/sys/unix"
)

const (
	namedVMIdentityVersion     = 1
	maxNamedVMIdentityFileSize = 16 << 10
	namedVMInstanceIDBytes     = 32
)

var (
	errNamedVMIdentityExists   = errors.New("named VM identity already exists")
	errNamedVMIdentityMismatch = errors.New("named VM identity does not match")
	errNamedVMOwnerMismatch    = errors.New("named VM belongs to another project")
)

// namedVMIdentity is the durable ownership record for a public Lima instance
// name. Paths are deliberately not serialized: every host path is derived from
// the effective LIMA_HOME and the exact validated VM name when the record is
// loaded.
type namedVMIdentity struct {
	Version             int    `json:"version"`
	VMName              string `json:"vm_name"`
	OwnerProject        string `json:"owner_project"`
	LimaHome            string `json:"lima_home"`
	InstanceID          string `json:"instance_id"`
	AppliedConfigDigest string `json:"applied_config_digest"`
	MountProject        bool   `json:"mount_project"`
	Workdir             string `json:"workdir"`
}

// namedVMIdentityMarker is the guest-visible subset of the host identity. Host
// project and LIMA_HOME paths remain private even when BootstrapDir is mounted
// into a VM created without a project mount.
type namedVMIdentityMarker struct {
	Version    int    `json:"version"`
	VMName     string `json:"vm_name"`
	InstanceID string `json:"instance_id"`
}

// namedVMIdentityPaths contains the host paths associated with a named VM.
// BootstrapDir is intended to be mounted read-only in the guest. GuestStateDir
// is the only per-instance directory intended for a read-write guest mount.
// The registry record, verdict port, and verdict authentication key are never
// placed in that guest-writable directory.
type namedVMIdentityPaths struct {
	NamespaceDir        string
	InstancesDir        string
	InstanceDir         string
	RecordPath          string
	BootstrapDir        string
	MarkerPath          string
	NfqdPath            string
	GuestStateDir       string
	GuestNetworkLogPath string
	VerdictPortPath     string
	VerdictAuthKeyPath  string
}

type namedVMInstanceIdentity struct {
	Identity       namedVMIdentity
	Paths          namedVMIdentityPaths
	VerdictAuthKey string
}

// reserveNamedVMIdentity atomically reserves vmName for ownerProject. The
// per-name directory is the reservation primitive, so two creators cannot both
// acquire the same name. A partially initialized reservation is treated as a
// collision/corrupt record and is never silently adopted.
func reserveNamedVMIdentity(ownerProject, vmName string, configs ...*config.Config) (namedVMInstanceIdentity, error) {
	if err := config.ValidateVMName(vmName); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("invalid VM name: %w", err)
	}
	cfg := config.NewConfig()
	if len(configs) > 1 {
		return namedVMInstanceIdentity{}, errors.New("named VM identity accepts at most one configuration")
	}
	if len(configs) == 1 {
		if configs[0] == nil {
			return namedVMInstanceIdentity{}, errors.New("named VM identity configuration cannot be nil")
		}
		cfg = configs[0]
	}
	if err := config.Validate(cfg); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("invalid named VM configuration: %w", err)
	}
	appliedDigest, err := config.AppliedConfigDigest(cfg)
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("digesting named VM configuration: %w", err)
	}

	host, err := resolveAppliedPolicyHostContext(ownerProject, nil)
	if err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if err := secureAppliedPolicyStateDirectories(host, true); err != nil {
		return namedVMInstanceIdentity{}, err
	}

	paths := deriveNamedVMIdentityPaths(host.StateDir, vmName)
	if err := validateNamedVMIdentityPathLayout(paths, vmName); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if err := ensurePrivateIdentityDirectory(paths.InstancesDir); err != nil {
		return namedVMInstanceIdentity{}, err
	}

	if err := os.Mkdir(paths.InstanceDir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, loadErr := loadNamedVMIdentity(vmName)
			if loadErr != nil {
				return namedVMInstanceIdentity{}, fmt.Errorf("%w for %q, but its reservation is invalid: %v", errNamedVMIdentityExists, vmName, loadErr)
			}
			return namedVMInstanceIdentity{}, fmt.Errorf("%w for %q (owned by %q)", errNamedVMIdentityExists, vmName, existing.Identity.OwnerProject)
		}
		return namedVMInstanceIdentity{}, fmt.Errorf("reserving identity for VM %q: %w", vmName, err)
	}
	createdInstanceDir := true
	committed := false
	defer func() {
		if createdInstanceDir && !committed {
			cleanupUncommittedNamedVMIdentity(paths)
		}
	}()

	if err := os.Chmod(paths.InstanceDir, 0700); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("securing identity directory for VM %q: %w", vmName, err)
	}
	if err := validatePrivateIdentityDirectory(paths.InstanceDir); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	for _, path := range []string{paths.BootstrapDir, paths.GuestStateDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			return namedVMInstanceIdentity{}, fmt.Errorf("creating named VM runtime directory %q: %w", path, err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return namedVMInstanceIdentity{}, fmt.Errorf("securing named VM runtime directory %q: %w", path, err)
		}
		if err := validatePrivateIdentityDirectory(path); err != nil {
			return namedVMInstanceIdentity{}, err
		}
	}

	instanceID, err := newNamedVMInstanceID()
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("generating named VM instance identity: %w", err)
	}
	identity := namedVMIdentity{
		Version:             namedVMIdentityVersion,
		VMName:              vmName,
		OwnerProject:        host.ProjectRoot,
		LimaHome:            host.LimaHome,
		InstanceID:          instanceID,
		AppliedConfigDigest: appliedDigest,
		MountProject:        config.MountProjectEnabled(&cfg.VM),
		Workdir:             config.DefaultWorkdir(cfg),
	}
	verdictAuthKey := ""
	if cfg.Security.Enforcement == config.EnforcementAsk {
		key, err := ask.NewAuthKey()
		if err != nil {
			return namedVMInstanceIdentity{}, fmt.Errorf("generating verdict authentication key: %w", err)
		}
		verdictAuthKey = key.Hex()
	}
	recordData, err := marshalNamedVMIdentity(identity)
	if err != nil {
		return namedVMInstanceIdentity{}, err
	}
	markerData, err := marshalNamedVMIdentityMarker(markerForNamedVMIdentity(identity))
	if err != nil {
		return namedVMInstanceIdentity{}, err
	}

	// Publish the guest-visible marker first and the host-only registry record
	// last. The record is therefore the commit marker for a complete reservation.
	if err := writeNewPrivateIdentityFile(paths.MarkerPath, markerData); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("writing named VM identity marker: %w", err)
	}
	if err := syncIdentityDirectory(paths.BootstrapDir); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if verdictAuthKey != "" {
		if err := writeNewPrivateIdentityFile(paths.VerdictAuthKeyPath, []byte(verdictAuthKey)); err != nil {
			return namedVMInstanceIdentity{}, fmt.Errorf("writing verdict authentication key: %w", err)
		}
	}
	if err := writeNewPrivateIdentityFile(paths.RecordPath, recordData); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("writing named VM identity record: %w", err)
	}
	if err := syncIdentityDirectory(paths.InstanceDir); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if err := syncIdentityDirectory(paths.InstancesDir); err != nil {
		return namedVMInstanceIdentity{}, err
	}

	committed = true
	return namedVMInstanceIdentity{Identity: identity, Paths: paths, VerdictAuthKey: verdictAuthKey}, nil
}

// loadNamedVMIdentity loads and validates the registry record and its
// guest-visible marker for vmName in the effective LIMA_HOME namespace.
func loadNamedVMIdentity(vmName string) (namedVMInstanceIdentity, error) {
	if err := config.ValidateVMName(vmName); err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("invalid VM name: %w", err)
	}

	namespaceDir, limaHome, err := resolveNamedVMIdentityNamespace()
	if err != nil {
		return namedVMInstanceIdentity{}, err
	}
	paths := deriveNamedVMIdentityPaths(namespaceDir, vmName)
	if err := validateNamedVMIdentityPathLayout(paths, vmName); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if err := validateNamedVMIdentityDirectories(paths); err != nil {
		return namedVMInstanceIdentity{}, err
	}

	recordData, err := readPrivateIdentityFile(paths.RecordPath)
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("reading named VM identity record for %q: %w", vmName, err)
	}
	identity, err := parseNamedVMIdentity(recordData)
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("parsing named VM identity record for %q: %w", vmName, err)
	}
	if err := validateNamedVMIdentity(identity, vmName, limaHome); err != nil {
		return namedVMInstanceIdentity{}, err
	}

	markerData, err := readPrivateIdentityFile(paths.MarkerPath)
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("reading named VM identity marker for %q: %w", vmName, err)
	}
	marker, err := parseNamedVMIdentityMarker(markerData)
	if err != nil {
		return namedVMInstanceIdentity{}, fmt.Errorf("parsing named VM identity marker for %q: %w", vmName, err)
	}
	if err := validateNamedVMIdentityMarker(marker, vmName); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if marker != markerForNamedVMIdentity(identity) {
		return namedVMInstanceIdentity{}, fmt.Errorf("%w: host record and guest marker differ for VM %q", errNamedVMIdentityMismatch, vmName)
	}

	verdictAuthKey := ""
	keyData, keyErr := readPrivateIdentityFile(paths.VerdictAuthKeyPath)
	if keyErr == nil {
		verdictAuthKey = string(keyData)
		if _, err := ask.ParseAuthKey(verdictAuthKey); err != nil {
			return namedVMInstanceIdentity{}, fmt.Errorf("parsing verdict authentication key for %q: %w", vmName, err)
		}
	} else if !errors.Is(keyErr, os.ErrNotExist) {
		return namedVMInstanceIdentity{}, fmt.Errorf("reading verdict authentication key for %q: %w", vmName, keyErr)
	}

	return namedVMInstanceIdentity{Identity: identity, Paths: paths, VerdictAuthKey: verdictAuthKey}, nil
}

// loadOwnedNamedVMIdentity is the fail-closed project-owned lookup used by
// lifecycle commands. Named VMs are not shared between projects.
func loadOwnedNamedVMIdentity(ownerProject, vmName string) (namedVMInstanceIdentity, error) {
	instance, err := loadNamedVMIdentity(vmName)
	if err != nil {
		return namedVMInstanceIdentity{}, err
	}
	if err := verifyNamedVMIdentityOwner(instance.Identity, ownerProject); err != nil {
		return namedVMInstanceIdentity{}, err
	}
	return instance, nil
}

func listNamedVMIdentities() ([]namedVMInstanceIdentity, error) {
	identities, err := enumerateNamedVMIdentities(false)
	if err != nil {
		// Strict callers must never act on a partial registry view.
		return nil, err
	}
	return identities, nil
}

// listNamedVMIdentitiesBestEffort returns every identity that can be fully
// authenticated, together with joined diagnostics for entries that remain in
// the registry but are corrupt or only partially reserved. It is reserved for
// recovery paths that must still act on independently verified identities.
func listNamedVMIdentitiesBestEffort() ([]namedVMInstanceIdentity, error) {
	return enumerateNamedVMIdentities(true)
}

func enumerateNamedVMIdentities(bestEffort bool) ([]namedVMInstanceIdentity, error) {
	namespaceDir, _, err := resolveNamedVMIdentityNamespace()
	if err != nil {
		return nil, err
	}
	instancesDir := filepath.Join(namespaceDir, "instances")
	entries, err := os.ReadDir(instancesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading named VM identity registry: %w", err)
	}
	if err := validatePrivateIdentityDirectory(instancesDir); err != nil {
		return nil, err
	}

	identities := make([]namedVMInstanceIdentity, 0, len(entries))
	var entryErrors []error
	reportEntryError := func(entryPath string, entryErr error) error {
		// ReadDir is a snapshot. A lifecycle-locked destroy may remove an entry
		// before it is opened; once the entry itself is gone there is no corrupt
		// reservation left to report or recover.
		if _, statErr := os.Lstat(entryPath); errors.Is(statErr, os.ErrNotExist) {
			return nil
		} else if statErr != nil {
			entryErr = errors.Join(entryErr, fmt.Errorf("rechecking named VM identity registry entry %q: %w", filepath.Base(entryPath), statErr))
		}
		if !bestEffort {
			return entryErr
		}
		entryErrors = append(entryErrors, entryErr)
		return nil
	}

	for _, entry := range entries {
		entryPath := filepath.Join(instancesDir, entry.Name())
		if !entry.IsDir() || len(entry.Name()) != sha256HexLength {
			entryErr := fmt.Errorf("named VM identity registry contains invalid entry %q", entry.Name())
			if !bestEffort {
				return nil, entryErr
			}
			if err := reportEntryError(entryPath, entryErr); err != nil {
				return nil, err
			}
			continue
		}
		recordPath := filepath.Join(entryPath, "identity.json")
		data, err := readPrivateIdentityFile(recordPath)
		if err != nil {
			if !bestEffort {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("reading named VM identity registry entry %q: %w", entry.Name(), err)
			}
			entryErr := fmt.Errorf("reading named VM identity registry entry %q: %w", entry.Name(), err)
			if err := reportEntryError(entryPath, entryErr); err != nil {
				return nil, err
			}
			continue
		}
		record, err := parseNamedVMIdentity(data)
		if err != nil {
			entryErr := fmt.Errorf("parsing named VM identity registry entry %q: %w", entry.Name(), err)
			if !bestEffort {
				return nil, entryErr
			}
			if err := reportEntryError(entryPath, entryErr); err != nil {
				return nil, err
			}
			continue
		}
		if fullPathDigest(record.VMName) != entry.Name() {
			entryErr := fmt.Errorf("named VM identity registry key %q does not match recorded name %q", entry.Name(), record.VMName)
			if !bestEffort {
				return nil, entryErr
			}
			if err := reportEntryError(entryPath, entryErr); err != nil {
				return nil, err
			}
			continue
		}
		instance, err := loadNamedVMIdentity(record.VMName)
		if err != nil {
			if !bestEffort {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			entryErr := fmt.Errorf("loading named VM identity registry entry %q: %w", entry.Name(), err)
			if err := reportEntryError(entryPath, entryErr); err != nil {
				return nil, err
			}
			continue
		}
		identities = append(identities, instance)
	}
	sort.Slice(identities, func(i, j int) bool {
		return identities[i].Identity.VMName < identities[j].Identity.VMName
	})
	return identities, errors.Join(entryErrors...)
}

func verifyNamedVMIdentityOwner(identity namedVMIdentity, ownerProject string) error {
	canonicalOwner, err := canonicalProjectRoot(ownerProject)
	if err != nil {
		return err
	}
	if identity.OwnerProject != canonicalOwner {
		return fmt.Errorf("%w: VM %q is owned by %q, not %q", errNamedVMOwnerMismatch, identity.VMName, identity.OwnerProject, canonicalOwner)
	}
	return nil
}

// removeNamedVMIdentity removes only the host state belonging to expected. It
// re-loads the current record and refuses cleanup if any identity field has
// changed. GuestStateDir is recursively removed because its contents are
// intentionally guest-writable and therefore cannot be treated as trusted
// host state. The directory itself is a validated, derived child of the
// private instance directory; os.RemoveAll does not follow symlinks found
// inside it.
func removeNamedVMIdentity(expected namedVMIdentity) error {
	current, err := loadNamedVMIdentity(expected.VMName)
	if err != nil {
		return err
	}
	if current.Identity != expected {
		return fmt.Errorf("%w: refusing to remove VM %q identity because the stored instance changed", errNamedVMIdentityMismatch, expected.VMName)
	}
	interruptedTemps, err := preflightNamedVMIdentityCleanup(current.Paths)
	if err != nil {
		return err
	}

	for _, path := range append(interruptedTemps, []string{
		current.Paths.NfqdPath,
		current.Paths.VerdictPortPath,
		current.Paths.VerdictAuthKeyPath,
	}...) {
		if err := removeOptionalOwnedRegularFile(path); err != nil {
			return err
		}
	}
	if err := makeGuestWritableStateRemovable(current.Paths.GuestStateDir); err != nil {
		return err
	}
	if err := os.RemoveAll(current.Paths.GuestStateDir); err != nil {
		return fmt.Errorf("removing guest-writable named VM state %q: %w", current.Paths.GuestStateDir, err)
	}
	for _, path := range []string{
		current.Paths.MarkerPath,
		current.Paths.BootstrapDir,
		current.Paths.RecordPath,
		current.Paths.InstanceDir,
	} {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing named VM identity path %q: %w", path, err)
		}
	}
	return syncIdentityDirectory(current.Paths.InstancesDir)
}

func resolveNamedVMIdentityNamespace() (string, string, error) {
	userConfigLexical, err := effectiveUserConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("locating user config directory: %w", err)
	}
	userConfigLexical, err = cleanAbsoluteHostPath("user config directory", userConfigLexical)
	if err != nil {
		return "", "", err
	}
	userConfig, err := canonicalizeHostPath("user config directory", userConfigLexical)
	if err != nil {
		return "", "", err
	}
	if err := validateTrustedDirectoryIfPresent("user config directory", userConfig); err != nil {
		return "", "", err
	}
	_, limaHome, err := effectiveLimaHome()
	if err != nil {
		return "", "", err
	}
	appStateRoot := filepath.Join(userConfig, "watermelon")
	if pathsOverlap(appStateRoot, limaHome) {
		return "", "", fmt.Errorf("named VM host state %q overlaps LIMA_HOME %q", appStateRoot, limaHome)
	}
	return filepath.Join(appStateRoot, "vms", "lima-"+fullPathDigest(limaHome)), limaHome, nil
}

func deriveNamedVMIdentityPaths(namespaceDir, vmName string) namedVMIdentityPaths {
	instancesDir := filepath.Join(namespaceDir, "instances")
	instanceDir := filepath.Join(instancesDir, fullPathDigest(vmName))
	bootstrapDir := filepath.Join(instanceDir, "bootstrap")
	guestStateDir := filepath.Join(instanceDir, "guest-state")
	return namedVMIdentityPaths{
		NamespaceDir:        namespaceDir,
		InstancesDir:        instancesDir,
		InstanceDir:         instanceDir,
		RecordPath:          filepath.Join(instanceDir, "identity.json"),
		BootstrapDir:        bootstrapDir,
		MarkerPath:          filepath.Join(bootstrapDir, "identity.json"),
		NfqdPath:            filepath.Join(bootstrapDir, "watermelon-nfqd"),
		GuestStateDir:       guestStateDir,
		GuestNetworkLogPath: filepath.Join(guestStateDir, "logs.log"),
		VerdictPortPath:     filepath.Join(instanceDir, "verdict-port"),
		VerdictAuthKeyPath:  filepath.Join(instanceDir, "verdict-auth-key"),
	}
}

func validateNamedVMIdentityPathLayout(paths namedVMIdentityPaths, vmName string) error {
	key := fullPathDigest(vmName)
	if len(key) != sha256HexLength || filepath.Base(paths.InstanceDir) != key || filepath.Dir(paths.InstanceDir) != paths.InstancesDir {
		return errors.New("invalid named VM identity path key")
	}
	if paths.InstancesDir != filepath.Join(paths.NamespaceDir, "instances") ||
		paths.RecordPath != filepath.Join(paths.InstanceDir, "identity.json") ||
		paths.BootstrapDir != filepath.Join(paths.InstanceDir, "bootstrap") ||
		paths.MarkerPath != filepath.Join(paths.BootstrapDir, "identity.json") ||
		paths.NfqdPath != filepath.Join(paths.BootstrapDir, "watermelon-nfqd") ||
		paths.GuestStateDir != filepath.Join(paths.InstanceDir, "guest-state") ||
		paths.GuestNetworkLogPath != filepath.Join(paths.GuestStateDir, "logs.log") ||
		paths.VerdictPortPath != filepath.Join(paths.InstanceDir, "verdict-port") ||
		paths.VerdictAuthKeyPath != filepath.Join(paths.InstanceDir, "verdict-auth-key") {
		return errors.New("invalid named VM identity path layout")
	}
	if pathContains(paths.GuestStateDir, paths.RecordPath) ||
		pathContains(paths.GuestStateDir, paths.MarkerPath) ||
		pathContains(paths.GuestStateDir, paths.VerdictPortPath) ||
		pathContains(paths.GuestStateDir, paths.VerdictAuthKeyPath) {
		return errors.New("named VM identity or host-only state is guest-writable")
	}
	return nil
}

const sha256HexLength = 64

func validateNamedVMIdentityDirectories(paths namedVMIdentityPaths) error {
	for _, path := range []string{
		filepath.Dir(filepath.Dir(paths.NamespaceDir)),
		filepath.Dir(paths.NamespaceDir),
		paths.NamespaceDir,
		paths.InstancesDir,
		paths.InstanceDir,
		paths.BootstrapDir,
	} {
		if err := validatePrivateIdentityDirectory(path); err != nil {
			return err
		}
	}
	return validateGuestWritableStateDirectory(paths.GuestStateDir)
}

// validateGuestWritableStateDirectory authenticates only the stable directory
// object. Its mode and contents are intentionally not trusted: the sandbox
// user can mutate both through the writable state mount.
func validateGuestWritableStateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspecting guest-writable named VM state %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("guest-writable named VM state %q must be a real directory", path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("guest-writable named VM state %q must be owned by the current user", path)
	}
	return nil
}

func ensurePrivateIdentityDirectory(path string) error {
	created := false
	if err := os.Mkdir(path, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("creating named VM identity directory %q: %w", path, err)
		}
	} else {
		created = true
	}
	if created {
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("securing named VM identity directory %q: %w", path, err)
		}
	}
	return validatePrivateIdentityDirectory(path)
}

func validatePrivateIdentityDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("named VM identity path %q must be a real directory: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("opening named VM identity directory %q: invalid file descriptor", path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspecting named VM identity directory %q: %w", path, err)
	}
	if !info.IsDir() || !ownedByCurrentUser(info) {
		return fmt.Errorf("named VM identity directory %q must be owned by the current user", path)
	}
	if info.Mode().Perm() != 0700 {
		return fmt.Errorf("named VM identity directory %q has insecure mode %04o; want 0700", path, info.Mode().Perm())
	}
	return nil
}

func newNamedVMInstanceID() (string, error) {
	value := make([]byte, namedVMInstanceIDBytes)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func marshalNamedVMIdentity(identity namedVMIdentity) ([]byte, error) {
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("marshalling named VM identity: %w", err)
	}
	return append(data, '\n'), nil
}

func marshalNamedVMIdentityMarker(marker namedVMIdentityMarker) ([]byte, error) {
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, fmt.Errorf("marshalling named VM identity marker: %w", err)
	}
	return append(data, '\n'), nil
}

func parseNamedVMIdentity(data []byte) (namedVMIdentity, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var identity namedVMIdentity
	if err := decoder.Decode(&identity); err != nil {
		return namedVMIdentity{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return namedVMIdentity{}, err
	}
	return identity, nil
}

func parseNamedVMIdentityMarker(data []byte) (namedVMIdentityMarker, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker namedVMIdentityMarker
	if err := decoder.Decode(&marker); err != nil {
		return namedVMIdentityMarker{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return namedVMIdentityMarker{}, err
	}
	return marker, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("identity file contains trailing JSON data")
	}
	return fmt.Errorf("invalid trailing identity data: %w", err)
}

func validateNamedVMIdentity(identity namedVMIdentity, requestedName, limaHome string) error {
	if identity.Version != namedVMIdentityVersion {
		return fmt.Errorf("unsupported named VM identity version %d", identity.Version)
	}
	if err := config.ValidateVMName(identity.VMName); err != nil {
		return fmt.Errorf("invalid recorded VM name: %w", err)
	}
	if identity.VMName != requestedName {
		return fmt.Errorf("%w: requested VM %q, record names %q", errNamedVMIdentityMismatch, requestedName, identity.VMName)
	}
	if !filepath.IsAbs(identity.OwnerProject) || filepath.Clean(identity.OwnerProject) != identity.OwnerProject {
		return errors.New("named VM identity owner project is not a clean absolute path")
	}
	if !filepath.IsAbs(identity.LimaHome) || filepath.Clean(identity.LimaHome) != identity.LimaHome {
		return errors.New("named VM identity LIMA_HOME is not a clean absolute path")
	}
	if identity.LimaHome != limaHome {
		return fmt.Errorf("%w: VM %q was registered under LIMA_HOME %q, not %q", errNamedVMIdentityMismatch, requestedName, identity.LimaHome, limaHome)
	}
	instanceID, err := hex.DecodeString(identity.InstanceID)
	if err != nil || len(instanceID) != namedVMInstanceIDBytes || identity.InstanceID != strings.ToLower(identity.InstanceID) {
		return errors.New("named VM identity has an invalid instance ID")
	}
	if len(identity.AppliedConfigDigest) != sha256HexLength {
		return errors.New("named VM identity has an invalid applied-config digest")
	}
	if _, err := hex.DecodeString(identity.AppliedConfigDigest); err != nil || identity.AppliedConfigDigest != strings.ToLower(identity.AppliedConfigDigest) {
		return errors.New("named VM identity has an invalid applied-config digest")
	}
	if identity.Workdir != "" {
		if err := config.ValidateGuestWorkdir(identity.Workdir); err != nil {
			return fmt.Errorf("named VM identity has an invalid workdir: %w", err)
		}
	}
	return nil
}

func markerForNamedVMIdentity(identity namedVMIdentity) namedVMIdentityMarker {
	return namedVMIdentityMarker{
		Version:    identity.Version,
		VMName:     identity.VMName,
		InstanceID: identity.InstanceID,
	}
}

func validateNamedVMIdentityMarker(marker namedVMIdentityMarker, requestedName string) error {
	if marker.Version != namedVMIdentityVersion {
		return fmt.Errorf("unsupported named VM identity marker version %d", marker.Version)
	}
	if err := config.ValidateVMName(marker.VMName); err != nil {
		return fmt.Errorf("invalid VM name in identity marker: %w", err)
	}
	if marker.VMName != requestedName {
		return fmt.Errorf("%w: requested VM %q, marker names %q", errNamedVMIdentityMismatch, requestedName, marker.VMName)
	}
	instanceID, err := hex.DecodeString(marker.InstanceID)
	if err != nil || len(instanceID) != namedVMInstanceIDBytes || marker.InstanceID != strings.ToLower(marker.InstanceID) {
		return errors.New("named VM identity marker has an invalid instance ID")
	}
	return nil
}

func writeNewPrivateIdentityFile(path string, data []byte) error {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("invalid identity file descriptor")
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func readPrivateIdentityFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("invalid identity file descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("identity path %q must be a regular file", path)
	}
	if !ownedByCurrentUser(info) {
		return nil, fmt.Errorf("identity file %q is not owned by the current user", path)
	}
	if info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("identity file %q has insecure mode %04o; want 0600", path, info.Mode().Perm())
	}
	if info.Size() > maxNamedVMIdentityFileSize {
		return nil, fmt.Errorf("identity file %q exceeds %d bytes", path, maxNamedVMIdentityFileSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNamedVMIdentityFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxNamedVMIdentityFileSize {
		return nil, fmt.Errorf("identity file %q exceeds %d bytes", path, maxNamedVMIdentityFileSize)
	}
	return data, nil
}

func syncIdentityDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("opening named VM identity directory %q for sync: %w", path, err)
	}
	defer unix.Close(fd)
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("syncing named VM identity directory %q: %w", path, err)
	}
	return nil
}

func cleanupUncommittedNamedVMIdentity(paths namedVMIdentityPaths) {
	// Every remove targets a single known path created by the failed reservation.
	// Errors are intentionally ignored because the primary creation error is more
	// useful, while any leftover directory remains a fail-closed reservation.
	_ = os.Remove(paths.RecordPath)
	_ = os.Remove(paths.MarkerPath)
	_ = os.Remove(paths.VerdictAuthKeyPath)
	_ = os.Remove(paths.GuestStateDir)
	_ = os.Remove(paths.BootstrapDir)
	_ = os.Remove(paths.InstanceDir)
}

func preflightNamedVMIdentityCleanup(paths namedVMIdentityPaths) ([]string, error) {
	if err := requireOnlyDirectoryEntries(paths.InstanceDir, map[string]struct{}{
		"identity.json":    {},
		"bootstrap":        {},
		"guest-state":      {},
		"verdict-port":     {},
		"verdict-auth-key": {},
	}); err != nil {
		return nil, err
	}
	interruptedTemps, err := preflightBootstrapDirectoryCleanup(paths.BootstrapDir)
	if err != nil {
		return nil, err
	}
	// GuestStateDir is a writable mount in the sandbox, so arbitrary entries
	// (including symlinks) are expected and must not be able to poison host
	// cleanup. loadNamedVMIdentity has already verified that the directory
	// itself is a private real directory at the exact derived path.
	for _, path := range []string{paths.NfqdPath, paths.VerdictPortPath, paths.VerdictAuthKeyPath} {
		if err := validateOptionalOwnedRegularFile(path); err != nil {
			return nil, err
		}
	}
	return interruptedTemps, nil
}

func preflightBootstrapDirectoryCleanup(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("reading named VM identity directory %q: %w", path, err)
	}
	var interruptedTemps []string
	for _, entry := range entries {
		switch entry.Name() {
		case "identity.json", "watermelon-nfqd":
			continue
		}
		if !isInterruptedNfqdTempName(entry.Name()) {
			return nil, fmt.Errorf("refusing to remove named VM identity directory %q containing unexpected entry %q", path, entry.Name())
		}
		tempPath := filepath.Join(path, entry.Name())
		if err := validateOptionalOwnedRegularFile(tempPath); err != nil {
			return nil, err
		}
		interruptedTemps = append(interruptedTemps, tempPath)
	}
	return interruptedTemps, nil
}

func isInterruptedNfqdTempName(name string) bool {
	for _, prefix := range []string{".watermelon-nfqd-build-", ".watermelon-nfqd-"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		if suffix == "" {
			continue
		}
		allDigits := true
		for _, char := range suffix {
			if char < '0' || char > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
}

func makeGuestWritableStateRemovable(root string) error {
	if err := validateGuestWritableStateDirectory(root); err != nil {
		return err
	}
	// The VM has already been deleted before identity cleanup, so the guest is
	// no longer mutating this mount. Restore owner access on directories before
	// RemoveAll; WalkDir does not follow symlinks and invokes the callback for a
	// directory before attempting to read its children.
	if err := os.Chmod(root, 0700); err != nil {
		return fmt.Errorf("restoring access to guest-writable named VM state %q: %w", root, err)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if err := os.Chmod(path, 0700); err != nil {
				return fmt.Errorf("restoring access to guest-created directory %q: %w", path, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("preparing guest-writable named VM state %q for removal: %w", root, err)
	}
	return nil
}

func requireOnlyDirectoryEntries(path string, allowed map[string]struct{}) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading named VM identity directory %q: %w", path, err)
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return fmt.Errorf("refusing to remove named VM identity directory %q containing unexpected entry %q", path, entry.Name())
		}
	}
	return nil
}

func validateOptionalOwnedRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting named VM runtime file %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular named VM runtime path %q", path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("refusing to remove named VM runtime file %q not owned by the current user", path)
	}
	return nil
}

func removeOptionalOwnedRegularFile(path string) error {
	if err := validateOptionalOwnedRegularFile(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing named VM runtime file %q: %w", path, err)
	}
	return nil
}
