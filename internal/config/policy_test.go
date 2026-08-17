package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testAppliedHostContext(cfg *Config) AppliedHostContext {
	mounts := make(map[string]string, len(cfg.Mounts))
	for source := range cfg.Mounts {
		mounts[source] = source
	}
	return AppliedHostContext{
		ProjectRoot:  "/host/project",
		LimaHome:     "/host/.lima",
		EffectiveUID: 1000,
		MountSources: mounts,
	}
}

func TestEnforcementDescriptors(t *testing.T) {
	for _, mode := range []string{EnforcementLog, EnforcementFail, EnforcementSilent, EnforcementAsk} {
		descriptor, ok := LookupEnforcement(mode)
		if !ok {
			t.Fatalf("LookupEnforcement(%q) not found", mode)
		}
		if descriptor.Mode != mode || descriptor.Summary == "" {
			t.Errorf("LookupEnforcement(%q) = %#v", mode, descriptor)
		}
	}
	if descriptor, _ := LookupEnforcement(EnforcementLog); descriptor.BlocksUnknown {
		t.Error("log enforcement must be described as non-blocking")
	}
	if descriptor, _ := LookupEnforcement(EnforcementFail); !descriptor.BlocksUnknown {
		t.Error("fail enforcement must be described as blocking")
	}
	if description := DescribeEnforcement(EnforcementLog); !strings.Contains(description, "allows") || !strings.Contains(description, "not strict") {
		t.Errorf("log description does not clearly report non-strict behavior: %q", description)
	}
}

func TestAppliedPolicySnapshotRoundTrip(t *testing.T) {
	cfg := NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org", "github.com"}
	cfg.Tools = map[string][]string{"node:20-slim": {"npm", "node"}}
	cfg.Ports.Forward = []int{5173, 3000}

	host := testAppliedHostContext(cfg)
	snapshot, err := NewAppliedPolicySnapshot(cfg, host)
	if err != nil {
		t.Fatalf("NewAppliedPolicySnapshot() error = %v", err)
	}
	data, err := MarshalAppliedPolicySnapshot(snapshot)
	if err != nil {
		t.Fatalf("MarshalAppliedPolicySnapshot() error = %v", err)
	}
	parsed, err := ParseAppliedPolicySnapshot(data)
	if err != nil {
		t.Fatalf("ParseAppliedPolicySnapshot() error = %v", err)
	}
	if parsed.Version != AppliedPolicySnapshotVersion || parsed.Enforcement != EnforcementFail {
		t.Fatalf("parsed snapshot = %#v", parsed)
	}
	if parsed.Host.EffectiveUID != host.EffectiveUID {
		t.Fatalf("parsed effective UID = %d, want %d", parsed.Host.EffectiveUID, host.EffectiveUID)
	}
	matches, err := parsed.MatchesConfig(cfg, host)
	if err != nil || !matches {
		t.Fatalf("MatchesConfig() = %v, %v; want true, nil", matches, err)
	}
}

func TestAppliedPolicyDigestUsesNormalizedVMConfig(t *testing.T) {
	first := NewConfig()
	first.Network.Allow = []string{"b.example", "a.example"}
	first.Tools = map[string][]string{"node:20-slim": {"npm", "node"}}
	first.Ports.Forward = []int{5173, 3000}
	first.IDE.Command = "code"

	second := NewConfig()
	second.Network.Allow = []string{"a.example", "b.example"}
	second.Tools = map[string][]string{"node:20-slim": {"node", "npm"}}
	second.Ports.Forward = []int{3000, 5173}
	second.IDE.Command = "cursor"

	firstDigest, err := AppliedConfigDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := AppliedConfigDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Errorf("semantically equivalent applied configs have different digests: %s != %s", firstDigest, secondDigest)
	}

	second.Security.Enforcement = EnforcementLog
	changedDigest, err := AppliedConfigDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == changedDigest {
		t.Error("changing enforcement did not change applied config digest")
	}
}

func TestAppliedPolicyDigestIncludesNewAppliedFields(t *testing.T) {
	base := NewConfig()
	baseDigest, err := AppliedConfigDigest(base)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "VM name", change: func(cfg *Config) { cfg.VM.Name = "dev" }},
		{name: "VM image", change: func(cfg *Config) { cfg.VM.Image = "ubuntu-24.04" }},
		{name: "mount project", change: func(cfg *Config) { disabled := false; cfg.VM.MountProject = &disabled }},
		{name: "VM workdir", change: func(cfg *Config) { cfg.VM.Workdir = "/workspace" }},
		{name: "provision scripts", change: func(cfg *Config) { cfg.Provision.Scripts = []string{"./setup.sh"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			tt.change(cfg)
			digest, err := AppliedConfigDigest(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseDigest {
				t.Errorf("changing %s did not change applied config digest", tt.name)
			}
		})
	}
}

func TestAppliedPolicyDigestIncludesProvisionScriptBytes(t *testing.T) {
	first := NewConfig()
	first.Provision.Scripts = []string{"./setup.sh"}
	first.Provision.ScriptSHA256 = []string{strings.Repeat("a", 64)}
	second := NewConfig()
	second.Provision.Scripts = []string{"./setup.sh"}
	second.Provision.ScriptSHA256 = []string{strings.Repeat("b", 64)}

	firstDigest, err := AppliedConfigDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := AppliedConfigDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("same-path provision script byte change did not change the applied config digest")
	}
}

func TestAppliedPolicyDigestNormalizesVMDefaultsAndExcludesIDEWorkdir(t *testing.T) {
	implicit := NewConfig()
	implicit.VM.Image = ""
	implicit.VM.MountProject = nil
	implicit.IDE.Workdir = "/ide/one"

	explicit := NewConfig()
	explicit.VM.Image = "ubuntu-22.04"
	explicit.VM.Workdir = "/project"
	explicit.IDE.Workdir = "/ide/two"

	implicitDigest, err := AppliedConfigDigest(implicit)
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := AppliedConfigDigest(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Errorf("semantically equivalent VM defaults or IDE-only workdirs changed digest: %s != %s", implicitDigest, explicitDigest)
	}
}

func TestAppliedPolicyDigestCanonicalizesReadOnlyMountDefault(t *testing.T) {
	implicit := NewConfig()
	implicit.Mounts = map[string]Mount{
		"/host/source": {Target: "/guest/target"},
	}
	explicit := NewConfig()
	explicit.Mounts = map[string]Mount{
		"/host/source": {Target: "/guest/target", Mode: "ro"},
	}

	implicitDigest, err := AppliedConfigDigest(implicit)
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := AppliedConfigDigest(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Errorf("implicit and explicit read-only mounts have different digests: %s != %s", implicitDigest, explicitDigest)
	}
}

func TestAppliedPolicySnapshotIncludesCanonicalHostContext(t *testing.T) {
	cfg := NewConfig()
	cfg.Mounts = map[string]Mount{
		"/host/cache-link": {Target: "/cache", Mode: "ro"},
	}
	firstHost := AppliedHostContext{
		ProjectRoot:  "/host/project",
		LimaHome:     "/host/.lima",
		EffectiveUID: 1000,
		MountSources: map[string]string{
			"/host/cache-link": "/host/cache-a",
		},
	}
	snapshot, err := NewAppliedPolicySnapshot(cfg, firstHost)
	if err != nil {
		t.Fatal(err)
	}
	if matches, err := snapshot.MatchesConfig(cfg, firstHost); err != nil || !matches {
		t.Fatalf("unchanged host context match = %v, %v; want true, nil", matches, err)
	}

	retargeted := firstHost
	retargeted.MountSources = map[string]string{
		"/host/cache-link": "/host/cache-b",
	}
	if matches, err := snapshot.MatchesConfig(cfg, retargeted); err != nil || matches {
		t.Fatalf("retargeted mount match = %v, %v; want false, nil", matches, err)
	}

	differentProject := firstHost
	differentProject.ProjectRoot = "/host/other-project"
	if matches, err := snapshot.MatchesConfig(cfg, differentProject); err != nil || matches {
		t.Fatalf("different project match = %v, %v; want false, nil", matches, err)
	}

	differentUID := firstHost
	differentUID.EffectiveUID = 501
	if matches, err := snapshot.MatchesConfig(cfg, differentUID); err != nil || matches {
		t.Fatalf("different effective UID match = %v, %v; want false, nil", matches, err)
	}
}

func TestParseAppliedPolicySnapshotRejectsInvalidHostContext(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*AppliedHostContext)
		want   string
	}{
		{name: "project root", change: func(host *AppliedHostContext) { host.ProjectRoot = "" }, want: "project root"},
		{name: "zero effective UID", change: func(host *AppliedHostContext) { host.EffectiveUID = 0 }, want: "effective host UID"},
		{name: "reserved effective UID", change: func(host *AppliedHostContext) { host.EffectiveUID = ^uint32(0) }, want: "effective host UID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			snapshot, err := NewAppliedPolicySnapshot(cfg, testAppliedHostContext(cfg))
			if err != nil {
				t.Fatal(err)
			}
			tt.change(&snapshot.Host)
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseAppliedPolicySnapshot(data); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("incomplete host context error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseAppliedPolicySnapshotRejectsLegacyDigest(t *testing.T) {
	_, err := ParseAppliedPolicySnapshot([]byte(strings.Repeat("a", 64) + "\n"))
	if !errors.Is(err, ErrLegacyAppliedPolicySnapshot) {
		t.Fatalf("legacy digest error = %v, want ErrLegacyAppliedPolicySnapshot", err)
	}
}

func TestParseAppliedPolicySnapshotRejectsVersionThree(t *testing.T) {
	cfg := NewConfig()
	snapshot, err := NewAppliedPolicySnapshot(cfg, testAppliedHostContext(cfg))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Version = 3
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAppliedPolicySnapshot(data); err == nil || !strings.Contains(err.Error(), "unsupported applied-policy snapshot version 3") {
		t.Fatalf("version 3 snapshot error = %v", err)
	}
}

func TestParseAppliedPolicySnapshotRejectsTampering(t *testing.T) {
	cfg := NewConfig()
	snapshot, err := NewAppliedPolicySnapshot(cfg, testAppliedHostContext(cfg))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Security.Enforcement = EnforcementLog
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAppliedPolicySnapshot(data); err == nil {
		t.Fatal("tampered snapshot unexpectedly parsed")
	}
}
