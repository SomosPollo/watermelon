package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	EnforcementLog    = "log"
	EnforcementFail   = "fail"
	EnforcementSilent = "silent"
	EnforcementAsk    = "ask"

	AppliedPolicySnapshotVersion = 2
)

// ErrLegacyAppliedPolicySnapshot identifies the old raw SHA-256 snapshot
// format. That format did not record the effective enforcement mode, so it
// cannot prove which policy an existing VM is running.
var ErrLegacyAppliedPolicySnapshot = errors.New("legacy applied-policy snapshot does not record enforcement")

// EnforcementDescriptor is the user-facing meaning of an enforcement mode.
// BlocksUnknown is false only for log mode, which records but permits traffic
// outside the allowlist.
type EnforcementDescriptor struct {
	Mode          string
	Summary       string
	BlocksUnknown bool
}

var enforcementDescriptors = map[string]EnforcementDescriptor{
	EnforcementLog: {
		Mode:          EnforcementLog,
		Summary:       "logs but allows connections outside the allowlist; not strict",
		BlocksUnknown: false,
	},
	EnforcementFail: {
		Mode:          EnforcementFail,
		Summary:       "blocks and logs connections outside the allowlist",
		BlocksUnknown: true,
	},
	EnforcementSilent: {
		Mode:          EnforcementSilent,
		Summary:       "blocks connections outside the allowlist without logging",
		BlocksUnknown: true,
	},
	EnforcementAsk: {
		Mode:          EnforcementAsk,
		Summary:       "prompts for connections outside the allowlist and blocks without a verdict",
		BlocksUnknown: true,
	},
}

// LookupEnforcement returns the centrally defined behavior for a mode.
func LookupEnforcement(mode string) (EnforcementDescriptor, bool) {
	descriptor, ok := enforcementDescriptors[mode]
	return descriptor, ok
}

// DescribeEnforcement formats a mode together with its security behavior.
func DescribeEnforcement(mode string) string {
	if descriptor, ok := LookupEnforcement(mode); ok {
		return fmt.Sprintf("%s (%s)", descriptor.Mode, descriptor.Summary)
	}
	return fmt.Sprintf("%s (unknown enforcement mode)", mode)
}

// AppliedConfig is the normalized subset of Config that is provisioned into a
// VM. Host-only IDE configuration is intentionally excluded.
type AppliedConfig struct {
	VM        VMConfig            `json:"vm"`
	Network   NetworkConfig       `json:"network"`
	Provision ProvisionConfig     `json:"provision"`
	Tools     map[string][]string `json:"tools"`
	Mounts    map[string]Mount    `json:"mounts"`
	Ports     PortsConfig         `json:"ports"`
	Resources ResourcesConfig     `json:"resources"`
	Security  SecurityConfig      `json:"security"`
}

// AppliedHostContext records canonical host paths that affect which VM and
// host resources an applied configuration refers to. MountSources is keyed by
// the source spelling from Config.Mounts and contains its canonical host path.
type AppliedHostContext struct {
	ProjectRoot  string            `json:"project_root"`
	LimaHome     string            `json:"lima_home"`
	MountSources map[string]string `json:"mount_sources"`
}

// AppliedPolicySnapshot records the exact normalized configuration used to
// create a VM. Digest makes malformed or partially written snapshots fail
// closed instead of being treated as a current record.
type AppliedPolicySnapshot struct {
	Version     int                `json:"version"`
	Digest      string             `json:"digest"`
	Enforcement string             `json:"enforcement"`
	Config      AppliedConfig      `json:"config"`
	Host        AppliedHostContext `json:"host"`
}

// NormalizeAppliedConfig returns a deterministic, deep-copied representation
// of the configuration that affects the VM.
func NormalizeAppliedConfig(cfg *Config) AppliedConfig {
	networkAllow := cloneStrings(cfg.Network.Allow)
	sort.Strings(networkAllow)

	networkProcess := cloneStringMap(cfg.Network.Process, true)
	tools := cloneStringMap(cfg.Tools, true)
	mounts := make(map[string]Mount, len(cfg.Mounts))
	for source, mount := range cfg.Mounts {
		if mount.Mode == "" {
			mount.Mode = "ro"
		}
		mounts[source] = mount
	}

	ports := append([]int(nil), cfg.Ports.Forward...)
	sort.Ints(ports)

	return AppliedConfig{
		VM: cfg.VM,
		Network: NetworkConfig{
			Allow:   networkAllow,
			Process: networkProcess,
		},
		Provision: ProvisionConfig{
			Npm:   cloneStrings(cfg.Provision.Npm),
			Pip:   cloneStrings(cfg.Provision.Pip),
			Cargo: cloneStrings(cfg.Provision.Cargo),
			Go:    cloneStrings(cfg.Provision.Go),
			Gem:   cloneStrings(cfg.Provision.Gem),
		},
		Tools:  tools,
		Mounts: mounts,
		Ports: PortsConfig{
			Forward: ports,
		},
		Resources: cfg.Resources,
		Security:  cfg.Security,
	}
}

// NewAppliedPolicySnapshot captures the policy/config that will be applied to
// a newly created VM.
func NewAppliedPolicySnapshot(cfg *Config, host AppliedHostContext) (AppliedPolicySnapshot, error) {
	applied := NormalizeAppliedConfig(cfg)
	host = normalizeAppliedHostContext(host)
	digest, err := appliedPolicyDigest(applied, host)
	if err != nil {
		return AppliedPolicySnapshot{}, err
	}
	return AppliedPolicySnapshot{
		Version:     AppliedPolicySnapshotVersion,
		Digest:      digest,
		Enforcement: applied.Security.Enforcement,
		Config:      applied,
		Host:        host,
	}, nil
}

// MatchesConfig reports whether a snapshot exactly matches the normalized VM
// configuration that would be generated now.
func (snapshot AppliedPolicySnapshot) MatchesConfig(cfg *Config, host AppliedHostContext) (bool, error) {
	digest, err := appliedPolicyDigest(NormalizeAppliedConfig(cfg), normalizeAppliedHostContext(host))
	if err != nil {
		return false, err
	}
	return snapshot.Digest == digest, nil
}

// AppliedConfigDigest returns the semantic digest of VM-applied configuration.
func AppliedConfigDigest(cfg *Config) (string, error) {
	return appliedConfigDigest(NormalizeAppliedConfig(cfg))
}

// MarshalAppliedPolicySnapshot serializes a versioned snapshot.
func MarshalAppliedPolicySnapshot(snapshot AppliedPolicySnapshot) ([]byte, error) {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding applied-policy snapshot: %w", err)
	}
	return append(data, '\n'), nil
}

// ParseAppliedPolicySnapshot validates a versioned snapshot. A legacy raw
// digest returns ErrLegacyAppliedPolicySnapshot and must never be considered a
// current strict-policy record.
func ParseAppliedPolicySnapshot(data []byte) (AppliedPolicySnapshot, error) {
	trimmed := strings.TrimSpace(string(data))
	if isSHA256(trimmed) {
		return AppliedPolicySnapshot{}, ErrLegacyAppliedPolicySnapshot
	}

	var snapshot AppliedPolicySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return AppliedPolicySnapshot{}, fmt.Errorf("decoding applied-policy snapshot: %w", err)
	}
	if snapshot.Version != AppliedPolicySnapshotVersion {
		return AppliedPolicySnapshot{}, fmt.Errorf("unsupported applied-policy snapshot version %d", snapshot.Version)
	}
	if _, ok := LookupEnforcement(snapshot.Enforcement); !ok {
		return AppliedPolicySnapshot{}, fmt.Errorf("snapshot has invalid enforcement %q", snapshot.Enforcement)
	}
	if snapshot.Config.Security.Enforcement != snapshot.Enforcement {
		return AppliedPolicySnapshot{}, errors.New("snapshot enforcement does not match normalized config")
	}
	if err := validateAppliedHostContext(snapshot.Config, snapshot.Host); err != nil {
		return AppliedPolicySnapshot{}, err
	}
	wantDigest, err := appliedPolicyDigest(snapshot.Config, normalizeAppliedHostContext(snapshot.Host))
	if err != nil {
		return AppliedPolicySnapshot{}, err
	}
	if snapshot.Digest != wantDigest {
		return AppliedPolicySnapshot{}, errors.New("snapshot digest does not match normalized config")
	}
	return snapshot, nil
}

type appliedPolicyDigestInput struct {
	Config AppliedConfig      `json:"config"`
	Host   AppliedHostContext `json:"host"`
}

func appliedPolicyDigest(applied AppliedConfig, host AppliedHostContext) (string, error) {
	data, err := json.Marshal(appliedPolicyDigestInput{Config: applied, Host: host})
	if err != nil {
		return "", fmt.Errorf("encoding applied policy: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeAppliedHostContext(host AppliedHostContext) AppliedHostContext {
	mounts := make(map[string]string, len(host.MountSources))
	for source, canonical := range host.MountSources {
		mounts[source] = canonical
	}
	host.MountSources = mounts
	return host
}

func validateAppliedHostContext(applied AppliedConfig, host AppliedHostContext) error {
	if err := validateCanonicalSnapshotPath("project root", host.ProjectRoot); err != nil {
		return err
	}
	if err := validateCanonicalSnapshotPath("Lima home", host.LimaHome); err != nil {
		return err
	}
	if len(host.MountSources) != len(applied.Mounts) {
		return errors.New("snapshot canonical mount sources do not match normalized config")
	}
	for source := range applied.Mounts {
		canonical := host.MountSources[source]
		if canonical == "" {
			return fmt.Errorf("snapshot canonical mount source for %q is missing", source)
		}
		if err := validateCanonicalSnapshotPath(fmt.Sprintf("mount source for %q", source), canonical); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalSnapshotPath(label, path string) error {
	if path == "" {
		return fmt.Errorf("snapshot canonical %s is missing", label)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("snapshot canonical %s %q is not a clean absolute path", label, path)
	}
	return nil
}

func appliedConfigDigest(applied AppliedConfig) (string, error) {
	data, err := json.Marshal(applied)
	if err != nil {
		return "", fmt.Errorf("encoding applied config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func cloneStringMap(values map[string][]string, sortValues bool) map[string][]string {
	cloned := make(map[string][]string, len(values))
	for key, entries := range values {
		copied := cloneStrings(entries)
		if sortValues {
			sort.Strings(copied)
		}
		cloned[key] = copied
	}
	return cloned
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
