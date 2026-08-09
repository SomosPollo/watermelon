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
		ProjectRoot: "/host/project",
		LimaHome:    "/host/.lima",
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
}

func TestParseAppliedPolicySnapshotRejectsIncompleteHostContext(t *testing.T) {
	cfg := NewConfig()
	snapshot, err := NewAppliedPolicySnapshot(cfg, testAppliedHostContext(cfg))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Host.ProjectRoot = ""
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAppliedPolicySnapshot(data); err == nil || !strings.Contains(err.Error(), "project root") {
		t.Fatalf("incomplete host context error = %v", err)
	}
}

func TestParseAppliedPolicySnapshotRejectsLegacyDigest(t *testing.T) {
	_, err := ParseAppliedPolicySnapshot([]byte(strings.Repeat("a", 64) + "\n"))
	if !errors.Is(err, ErrLegacyAppliedPolicySnapshot) {
		t.Fatalf("legacy digest error = %v, want ErrLegacyAppliedPolicySnapshot", err)
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
