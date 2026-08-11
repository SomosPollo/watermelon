package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
)

func TestNamedVMIdentityReserveLoadAndOwnerVerification(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	const vmName = "watermelon-fixed-dev"

	reserved, err := reserveNamedVMIdentity(project, vmName)
	if err != nil {
		t.Fatalf("reserveNamedVMIdentity() error = %v", err)
	}
	if reserved.Identity.Version != namedVMIdentityVersion {
		t.Fatalf("identity version = %d, want %d", reserved.Identity.Version, namedVMIdentityVersion)
	}
	if reserved.Identity.VMName != vmName {
		t.Fatalf("identity VM name = %q, want %q", reserved.Identity.VMName, vmName)
	}
	canonicalProject, err := canonicalProjectRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Identity.OwnerProject != canonicalProject {
		t.Fatalf("identity owner = %q, want %q", reserved.Identity.OwnerProject, canonicalProject)
	}
	if len(reserved.Identity.InstanceID) != namedVMInstanceIDBytes*2 {
		t.Fatalf("instance ID length = %d, want %d", len(reserved.Identity.InstanceID), namedVMInstanceIDBytes*2)
	}

	instanceKey := filepath.Base(reserved.Paths.InstanceDir)
	if len(instanceKey) != sha256HexLength || instanceKey != fullPathDigest(vmName) {
		t.Fatalf("instance directory key = %q, want full SHA-256 digest", instanceKey)
	}
	if instanceKey == vmName {
		t.Fatal("instance directory exposes the raw VM name")
	}
	if pathContains(reserved.Paths.GuestStateDir, reserved.Paths.RecordPath) ||
		pathContains(reserved.Paths.GuestStateDir, reserved.Paths.MarkerPath) ||
		pathContains(reserved.Paths.GuestStateDir, reserved.Paths.VerdictPortPath) {
		t.Fatal("identity or host verdict state was placed in the guest-writable directory")
	}
	for _, dir := range []string{reserved.Paths.InstanceDir, reserved.Paths.BootstrapDir, reserved.Paths.GuestStateDir} {
		assertFileMode(t, dir, os.ModeDir|0700)
	}
	for _, path := range []string{reserved.Paths.RecordPath, reserved.Paths.MarkerPath} {
		assertFileMode(t, path, 0600)
	}
	markerData, err := os.ReadFile(reserved.Paths.MarkerPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(markerData), reserved.Identity.OwnerProject) || strings.Contains(string(markerData), reserved.Identity.LimaHome) {
		t.Fatalf("guest-visible marker leaks host paths: %s", markerData)
	}

	loaded, err := loadNamedVMIdentity(vmName)
	if err != nil {
		t.Fatalf("loadNamedVMIdentity() error = %v", err)
	}
	if loaded != reserved {
		t.Fatalf("loaded identity = %#v, want %#v", loaded, reserved)
	}
	if _, err := loadOwnedNamedVMIdentity(project, vmName); err != nil {
		t.Fatalf("loadOwnedNamedVMIdentity(owner) error = %v", err)
	}

	alias := filepath.Join(filepath.Dir(project), "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	if err := verifyNamedVMIdentityOwner(loaded.Identity, alias); err != nil {
		t.Fatalf("verifyNamedVMIdentityOwner(canonical alias) error = %v", err)
	}

	otherProject := filepath.Join(filepath.Dir(project), "other-project")
	if err := os.Mkdir(otherProject, 0700); err != nil {
		t.Fatal(err)
	}
	if err := verifyNamedVMIdentityOwner(loaded.Identity, otherProject); !errors.Is(err, errNamedVMOwnerMismatch) {
		t.Fatalf("verifyNamedVMIdentityOwner(other) error = %v, want owner mismatch", err)
	}
}

func TestNamedVMIdentityReservationCollisionIsFailClosed(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	const vmName = "fixed-name"
	first, err := reserveNamedVMIdentity(project, vmName)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reserveNamedVMIdentity(project, vmName); !errors.Is(err, errNamedVMIdentityExists) {
		t.Fatalf("second reservation error = %v, want identity-exists error", err)
	}
	otherProject := filepath.Join(filepath.Dir(project), "collision-project")
	if err := os.Mkdir(otherProject, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveNamedVMIdentity(otherProject, vmName); !errors.Is(err, errNamedVMIdentityExists) {
		t.Fatalf("cross-project reservation error = %v, want identity-exists error", err)
	}

	loaded, err := loadNamedVMIdentity(vmName)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity != first.Identity {
		t.Fatalf("collision changed stored identity: got %#v, want %#v", loaded.Identity, first.Identity)
	}
}

func TestAskIdentityStoresUniqueAuthKeyOnlyInPrivateHostState(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk
	first, err := reserveNamedVMIdentity(project, "ask-auth-one", cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reserveNamedVMIdentity(project, "ask-auth-two", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask.ParseAuthKey(first.VerdictAuthKey); err != nil {
		t.Fatalf("first identity key is invalid: %v", err)
	}
	if first.VerdictAuthKey == second.VerdictAuthKey {
		t.Fatal("separate ask instances reused a verdict authentication key")
	}
	if filepath.Dir(first.Paths.VerdictAuthKeyPath) != first.Paths.InstanceDir ||
		pathContains(first.Paths.BootstrapDir, first.Paths.VerdictAuthKeyPath) ||
		pathContains(first.Paths.GuestStateDir, first.Paths.VerdictAuthKeyPath) {
		t.Fatalf("verdict authentication key is not host-only: %q", first.Paths.VerdictAuthKeyPath)
	}
	assertFileMode(t, first.Paths.VerdictAuthKeyPath, 0600)
	data, err := os.ReadFile(first.Paths.VerdictAuthKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != first.VerdictAuthKey {
		t.Fatal("in-memory authentication key differs from private host state")
	}
	loaded, err := loadNamedVMIdentity(first.Identity.VMName)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != first {
		t.Fatalf("loaded ask identity = %#v, want %#v", loaded, first)
	}
}

func TestNamedVMIdentityBestEffortEnumerationRetainsValidRecords(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	valid, err := reserveNamedVMIdentity(project, "valid-enumeration")
	if err != nil {
		t.Fatal(err)
	}

	corruptKey := strings.Repeat("a", sha256HexLength)
	if corruptKey == filepath.Base(valid.Paths.InstanceDir) {
		corruptKey = strings.Repeat("b", sha256HexLength)
	}
	corruptDir := filepath.Join(valid.Paths.InstancesDir, corruptKey)
	if err := os.Mkdir(corruptDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "identity.json"), []byte("{corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	partialKey := strings.Repeat("b", sha256HexLength)
	if partialKey == filepath.Base(valid.Paths.InstanceDir) {
		partialKey = strings.Repeat("c", sha256HexLength)
	}
	if err := os.Mkdir(filepath.Join(valid.Paths.InstancesDir, partialKey), 0700); err != nil {
		t.Fatal(err)
	}

	identities, enumerationErr := listNamedVMIdentitiesBestEffort()
	if enumerationErr == nil ||
		!strings.Contains(enumerationErr.Error(), "parsing named VM identity registry entry") ||
		!strings.Contains(enumerationErr.Error(), corruptKey) ||
		!strings.Contains(enumerationErr.Error(), "reading named VM identity registry entry") ||
		!strings.Contains(enumerationErr.Error(), partialKey) {
		t.Fatalf("best-effort enumeration error = %v, want accumulated corrupt and partial-entry diagnostics", enumerationErr)
	}
	if len(identities) != 1 || identities[0].Identity != valid.Identity {
		t.Fatalf("best-effort identities = %#v, want valid identity %#v", identities, valid.Identity)
	}

	strictIdentities, strictErr := listNamedVMIdentities()
	if strictErr == nil || !strings.Contains(strictErr.Error(), "parsing named VM identity registry entry") {
		t.Fatalf("strict enumeration error = %v, want corrupt-entry failure", strictErr)
	}
	if strictIdentities != nil {
		t.Fatalf("strict enumeration returned partial identities: %#v", strictIdentities)
	}
}

func TestNamedVMIdentityReservationIsAtomic(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	const contenders = 16
	errorsByContender := make([]error, contenders)
	identities := make([]namedVMInstanceIdentity, contenders)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(contenders)
	for i := range contenders {
		go func() {
			defer wait.Done()
			<-start
			identities[i], errorsByContender[i] = reserveNamedVMIdentity(project, "atomic-name")
		}()
	}
	close(start)
	wait.Wait()

	successes := 0
	var winner namedVMInstanceIdentity
	for i, err := range errorsByContender {
		if err == nil {
			successes++
			winner = identities[i]
			continue
		}
		if !errors.Is(err, errNamedVMIdentityExists) {
			t.Errorf("contender %d error = %v, want identity-exists", i, err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful reservations = %d, want exactly 1", successes)
	}
	loaded, err := loadNamedVMIdentity("atomic-name")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity != winner.Identity {
		t.Fatalf("loaded identity = %#v, want winner %#v", loaded.Identity, winner.Identity)
	}
}

func TestNamedVMIdentityLoadRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, instance namedVMInstanceIdentity)
	}{
		{
			name: "record contents",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.WriteFile(instance.Paths.RecordPath, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "marker differs",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				changed := markerForNamedVMIdentity(instance.Identity)
				changed.InstanceID = strings.Repeat("a", namedVMInstanceIDBytes*2)
				data, err := marshalNamedVMIdentityMarker(changed)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(instance.Paths.MarkerPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversize record",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.WriteFile(instance.Paths.RecordPath, make([]byte, maxNamedVMIdentityFileSize+1), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "record symlink",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.Remove(instance.Paths.RecordPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(instance.Paths.MarkerPath, instance.Paths.RecordPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "record nonregular",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.Remove(instance.Paths.RecordPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(instance.Paths.RecordPath, 0700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "record insecure mode",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.Chmod(instance.Paths.RecordPath, 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "runtime directory insecure mode",
			mutate: func(t *testing.T, instance namedVMInstanceIdentity) {
				t.Helper()
				if err := os.Chmod(instance.Paths.BootstrapDir, 0755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, _, _ := setupNamedVMIdentityTest(t)
			instance, err := reserveNamedVMIdentity(project, "tamper-test")
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, instance)
			if _, err := loadNamedVMIdentity(instance.Identity.VMName); err == nil {
				t.Fatal("loadNamedVMIdentity() succeeded after tampering")
			}
		})
	}
}

func TestNamedVMIdentityNamespacesEffectiveLimaHome(t *testing.T) {
	project, firstLimaHome, _ := setupNamedVMIdentityTest(t)
	const vmName = "same-public-name"
	first, err := reserveNamedVMIdentity(project, vmName)
	if err != nil {
		t.Fatal(err)
	}

	secondLimaHome := filepath.Join(filepath.Dir(firstLimaHome), "second-lima")
	if err := os.Mkdir(secondLimaHome, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", secondLimaHome)
	if _, err := loadNamedVMIdentity(vmName); err == nil {
		t.Fatal("identity from first LIMA_HOME was visible in second namespace")
	}
	second, err := reserveNamedVMIdentity(project, vmName)
	if err != nil {
		t.Fatalf("reservation in second LIMA_HOME error = %v", err)
	}
	if first.Paths.NamespaceDir == second.Paths.NamespaceDir || first.Paths.InstanceDir == second.Paths.InstanceDir {
		t.Fatalf("LIMA_HOME namespaces overlap: first %q, second %q", first.Paths.NamespaceDir, second.Paths.NamespaceDir)
	}
	if first.Identity.InstanceID == second.Identity.InstanceID {
		t.Fatal("separate LIMA_HOME reservations reused an instance ID")
	}

	t.Setenv("LIMA_HOME", firstLimaHome)
	loaded, err := loadNamedVMIdentity(vmName)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity != first.Identity {
		t.Fatalf("restored namespace identity = %#v, want %#v", loaded.Identity, first.Identity)
	}
}

func TestRemoveNamedVMIdentityRequiresExpectedIdentityAndCleansKnownPaths(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	instance, err := reserveNamedVMIdentity(project, "cleanup-test")
	if err != nil {
		t.Fatal(err)
	}
	writeTestRuntimeFile(t, instance.Paths.NfqdPath, 0700)
	writeTestRuntimeFile(t, instance.Paths.GuestNetworkLogPath, 0600)
	writeTestRuntimeFile(t, instance.Paths.VerdictPortPath, 0600)

	wrong := instance.Identity
	wrong.InstanceID = strings.Repeat("0", namedVMInstanceIDBytes*2)
	if err := removeNamedVMIdentity(wrong); !errors.Is(err, errNamedVMIdentityMismatch) {
		t.Fatalf("remove with stale identity error = %v, want mismatch", err)
	}
	if _, err := loadNamedVMIdentity(instance.Identity.VMName); err != nil {
		t.Fatalf("stale removal damaged identity: %v", err)
	}

	if err := removeNamedVMIdentity(instance.Identity); err != nil {
		t.Fatalf("removeNamedVMIdentity() error = %v", err)
	}
	if _, err := os.Lstat(instance.Paths.InstanceDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instance directory still exists or stat failed: %v", err)
	}
	if info, err := os.Stat(instance.Paths.InstancesDir); err != nil || !info.IsDir() {
		t.Fatalf("namespace instances directory should remain: info=%v err=%v", info, err)
	}
}

func TestRemoveNamedVMIdentityHandlesUntrustedGuestStateAndRefusesUnexpectedHostState(t *testing.T) {
	t.Run("guest-writable content cannot poison cleanup", func(t *testing.T) {
		project, _, _ := setupNamedVMIdentityTest(t)
		instance, err := reserveNamedVMIdentity(project, "cleanup-unexpected")
		if err != nil {
			t.Fatal(err)
		}
		unexpectedDir := filepath.Join(instance.Paths.GuestStateDir, "guest-created", "nested")
		if err := os.MkdirAll(unexpectedDir, 0700); err != nil {
			t.Fatal(err)
		}
		writeTestRuntimeFile(t, filepath.Join(unexpectedDir, "data"), 0600)
		target := filepath.Join(filepath.Dir(project), "keep-target")
		writeTestRuntimeFile(t, target, 0600)
		if err := os.Symlink(target, instance.Paths.GuestNetworkLogPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Dir(unexpectedDir), 0); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(instance.Paths.GuestStateDir, 0); err != nil {
			t.Fatal(err)
		}

		if err := removeNamedVMIdentity(instance.Identity); err != nil {
			t.Fatalf("guest-created state blocked cleanup: %v", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "runtime\n" {
			t.Fatalf("guest symlink target changed: data=%q err=%v", data, err)
		}
		if _, err := os.Lstat(instance.Paths.InstanceDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("identity state remains after cleanup: %v", err)
		}
	})

	t.Run("unexpected host-only file", func(t *testing.T) {
		project, _, _ := setupNamedVMIdentityTest(t)
		instance, err := reserveNamedVMIdentity(project, "cleanup-host-state")
		if err != nil {
			t.Fatal(err)
		}
		unexpected := filepath.Join(instance.Paths.BootstrapDir, "do-not-delete")
		writeTestRuntimeFile(t, unexpected, 0600)
		if err := removeNamedVMIdentity(instance.Identity); err == nil {
			t.Fatal("cleanup accepted unexpected host-only state")
		}
		if _, err := os.Stat(unexpected); err != nil {
			t.Fatalf("unexpected host state was damaged: %v", err)
		}
		if _, err := loadNamedVMIdentity(instance.Identity.VMName); err != nil {
			t.Fatalf("refused cleanup damaged identity: %v", err)
		}
	})
}

func setupNamedVMIdentityTest(t *testing.T) (project, limaHome, userConfig string) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "project")
	limaHome = filepath.Join(root, "lima")
	userConfig = filepath.Join(root, "config")
	for _, path := range []string{project, limaHome, userConfig} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", userConfig)
	t.Setenv("LIMA_HOME", limaHome)
	return project, limaHome, userConfig
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Mode() & (os.ModeType | os.ModePerm)
	if got != want {
		t.Fatalf("mode for %q = %v, want %v", path, got, want)
	}
}

func writeTestRuntimeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("runtime\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
