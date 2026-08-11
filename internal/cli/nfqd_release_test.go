package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func TestGoInstallReleaseVersionAcceptsTaggedOfficialBuild(t *testing.T) {
	info := testCLIReleaseBuildInfo("v1.2.3")

	got, ok := goInstallReleaseVersion(func() (*runtimedebug.BuildInfo, bool) {
		return info, true
	})
	if !ok || got != "v1.2.3" {
		t.Fatalf("goInstallReleaseVersion() = (%q, %v), want (%q, true)", got, ok, "v1.2.3")
	}
}

func TestGoInstallReleaseVersionRejectsNonReleaseBuilds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*runtimedebug.BuildInfo)
		ok     bool
	}{
		{
			name: "pseudo-version",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Version = "v1.2.4-0.20260811192051-d3e5b3a7ffe0"
			},
		},
		{
			name: "dirty version suffix",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Version = "v1.2.3+dirty"
			},
		},
		{
			name: "dirty build setting",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Settings = append(info.Settings, runtimedebug.BuildSetting{Key: "vcs.modified", Value: "true"})
			},
		},
		{
			name: "replaced module",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Replace = &runtimedebug.Module{Path: "/tmp/local-watermelon"}
			},
		},
		{
			name: "wrong command path",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Path = watermelonModulePath + "/cmd/not-watermelon"
			},
		},
		{
			name: "wrong module path",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Path = "example.test/fork/watermelon"
			},
		},
		{
			name: "development build",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Version = "(devel)"
			},
		},
		{
			name: "valid prerelease tag",
			mutate: func(info *runtimedebug.BuildInfo) {
				info.Main.Version = "v1.2.3-rc.1+build.5"
			},
			ok: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := testCLIReleaseBuildInfo("v1.2.3")
			test.mutate(info)
			got, ok := goInstallReleaseVersion(func() (*runtimedebug.BuildInfo, bool) {
				return info, true
			})
			if ok != test.ok {
				t.Fatalf("goInstallReleaseVersion() = (%q, %v), want ok=%v", got, ok, test.ok)
			}
			if test.ok && got != info.Main.Version {
				t.Fatalf("accepted version = %q, want %q", got, info.Main.Version)
			}
			if !test.ok && got != "" {
				t.Fatalf("rejected version = %q, want empty", got)
			}
		})
	}

	for _, test := range []struct {
		name string
		read func() (*runtimedebug.BuildInfo, bool)
	}{
		{name: "nil reader", read: nil},
		{name: "missing build info", read: func() (*runtimedebug.BuildInfo, bool) { return nil, false }},
		{name: "nil build info", read: func() (*runtimedebug.BuildInfo, bool) { return nil, true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := goInstallReleaseVersion(test.read); ok || got != "" {
				t.Fatalf("goInstallReleaseVersion() = (%q, %v), want empty rejection", got, ok)
			}
		})
	}
}

func TestValidateNfqdReleaseBuild(t *testing.T) {
	const (
		version = "v1.2.3"
		arch    = "arm64"
	)
	tests := []struct {
		name    string
		info    func() *runtimedebug.BuildInfo
		wantErr string
	}{
		{
			name: "valid",
			info: func() *runtimedebug.BuildInfo {
				return testNfqdReleaseBuildInfo(version, arch)
			},
		},
		{
			name:    "missing build information",
			info:    func() *runtimedebug.BuildInfo { return nil },
			wantErr: "no Go build information",
		},
		{
			name: "wrong command",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				info.Path = watermelonCLIPath
				return info
			},
			wantErr: "not the official Watermelon nfqd command",
		},
		{
			name: "wrong module",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				info.Main.Path = "example.test/fork/watermelon"
				return info
			},
			wantErr: "not the official Watermelon nfqd command",
		},
		{
			name: "replaced module",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				info.Main.Replace = &runtimedebug.Module{Path: "/tmp/local-watermelon"}
				return info
			},
			wantErr: "not the official Watermelon nfqd command",
		},
		{
			name: "wrong version",
			info: func() *runtimedebug.BuildInfo {
				return testNfqdReleaseBuildInfo("v1.2.2", arch)
			},
			wantErr: `module version is "v1.2.2"`,
		},
		{
			name: "dirty version",
			info: func() *runtimedebug.BuildInfo {
				return testNfqdReleaseBuildInfo(version+"+dirty", arch)
			},
			wantErr: `module version is "v1.2.3+dirty"`,
		},
		{
			name: "wrong operating system",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				setBuildSetting(info, "GOOS", "darwin")
				return info
			},
			wantErr: "target is darwin/arm64",
		},
		{
			name: "wrong architecture",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, "amd64")
				return info
			},
			wantErr: "want linux/arm64",
		},
		{
			name: "cgo enabled",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				setBuildSetting(info, "CGO_ENABLED", "1")
				return info
			},
			wantErr: "not the expected static release build",
		},
		{
			name: "missing cgo metadata",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				info.Settings = info.Settings[:2]
				return info
			},
			wantErr: "not the expected static release build",
		},
		{
			name: "modified source setting",
			info: func() *runtimedebug.BuildInfo {
				info := testNfqdReleaseBuildInfo(version, arch)
				setBuildSetting(info, "vcs.modified", "true")
				return info
			},
			wantErr: "built from modified source",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNfqdReleaseBuild(test.info(), version, arch)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateNfqdReleaseBuild() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateNfqdReleaseBuild() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestNfqdReleaseInstallDownloadsVerifiedAssetAtomically(t *testing.T) {
	const (
		version = "v1.2.3"
		arch    = "arm64"
	)
	assetBody := []byte("test Linux nfqd release binary")
	digest := sha256.Sum256(assetBody)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "watermelon-nfqd")
	if err := os.WriteFile(dest, []byte("old sidecar"), 0600); err != nil {
		t.Fatal(err)
	}

	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/releases/tags/" + version:
			if got := req.Header.Get("X-GitHub-Api-Version"); got != githubAPIVersion {
				t.Errorf("GitHub API version header = %q, want %q", got, githubAPIVersion)
			}
			writeReleaseMetadata(t, w, version, githubReleaseAsset{
				Name:               "watermelon-nfqd-linux-" + arch,
				BrowserDownloadURL: server.URL + "/asset",
				Digest:             "sha256:" + hex.EncodeToString(digest[:]),
				Size:               int64(len(assetBody)),
			})
		case "/asset":
			_, _ = w.Write(assetBody)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	readBuildInfoCalls := 0
	source := nfqdReleaseSource{
		client:        server.Client(),
		releaseAPIURL: server.URL + "/releases/tags",
		readBuildInfo: func(path string) (*runtimedebug.BuildInfo, error) {
			readBuildInfoCalls++
			if path == dest {
				t.Error("download was validated at the published destination instead of a temporary path")
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "old sidecar" {
				t.Errorf("destination changed before validation: contents=%q err=%v", got, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading downloaded temporary asset: %v", err)
			}
			if string(got) != string(assetBody) {
				t.Fatalf("downloaded temporary asset = %q, want %q", got, assetBody)
			}
			return testNfqdReleaseBuildInfo(version, arch), nil
		},
	}

	if err := source.install(dest, version, arch); err != nil {
		t.Fatal(err)
	}
	if readBuildInfoCalls != 1 {
		t.Fatalf("readBuildInfo calls = %d, want 1", readBuildInfoCalls)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(assetBody) {
		t.Fatalf("installed sidecar = %q, want %q", got, assetBody)
	}
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0700 {
		t.Fatalf("installed sidecar mode = %v, want regular 0700 file", info.Mode())
	}
}

func TestNfqdReleaseInstallDoesNotPublishFailedDownloads(t *testing.T) {
	const (
		version = "v1.2.3"
		arch    = "amd64"
	)
	assetBody := []byte("network interceptor")
	assetDigest := sha256.Sum256(assetBody)

	tests := []struct {
		name    string
		failure string
		wantErr string
	}{
		{name: "metadata status", failure: "metadata-status", wantErr: "503 Service Unavailable"},
		{name: "malformed metadata", failure: "malformed-metadata", wantErr: "decoding release metadata"},
		{name: "asset status", failure: "asset-status", wantErr: "502 Bad Gateway"},
		{name: "digest mismatch", failure: "digest", wantErr: "SHA-256 digest does not match"},
		{name: "oversized response", failure: "oversize", wantErr: "response is too large"},
		{name: "invalid build metadata", failure: "build-info", wantErr: "want linux/amd64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/releases/tags/" + version:
					if test.failure == "metadata-status" {
						http.Error(w, "metadata unavailable", http.StatusServiceUnavailable)
						return
					}
					if test.failure == "malformed-metadata" {
						_, _ = w.Write([]byte("{"))
						return
					}
					digest := assetDigest
					if test.failure == "digest" {
						digest = sha256.Sum256([]byte("different asset"))
					}
					writeReleaseMetadata(t, w, version, githubReleaseAsset{
						Name:               "watermelon-nfqd-linux-" + arch,
						BrowserDownloadURL: server.URL + "/asset",
						Digest:             "sha256:" + hex.EncodeToString(digest[:]),
						Size:               int64(len(assetBody)),
					})
				case "/asset":
					if test.failure == "asset-status" {
						http.Error(w, "asset unavailable", http.StatusBadGateway)
						return
					}
					if test.failure == "oversize" {
						w.Header().Set("Content-Length", strconv.FormatInt(maxNfqdReleaseAssetBytes+1, 10))
						w.WriteHeader(http.StatusOK)
						return
					}
					_, _ = w.Write(assetBody)
				default:
					http.NotFound(w, req)
				}
			}))
			defer server.Close()

			source := nfqdReleaseSource{
				client:        server.Client(),
				releaseAPIURL: server.URL + "/releases/tags",
				readBuildInfo: func(string) (*runtimedebug.BuildInfo, error) {
					if test.failure == "build-info" {
						return testNfqdReleaseBuildInfo(version, "arm64"), nil
					}
					return testNfqdReleaseBuildInfo(version, arch), nil
				},
			}
			destDir := t.TempDir()
			dest := filepath.Join(destDir, "watermelon-nfqd")
			err := source.install(dest, version, arch)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("install() error = %v, want containing %q", err, test.wantErr)
			}
			if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("failed install published destination: stat error = %v", statErr)
			}
			entries, readErr := os.ReadDir(destDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed install left temporary files: %v", entries)
			}
		})
	}
}

func TestEnsureNfqdBinaryPreservesExplicitOverrideErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T) string
		wantErr string
	}{
		{
			name: "missing",
			prepare: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing-watermelon-nfqd")
			},
			wantErr: "cannot be used",
		},
		{
			name: "symlink",
			prepare: func(t *testing.T) string {
				dir := t.TempDir()
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("sidecar"), 0700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(dir, "watermelon-nfqd")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			wantErr: "regular, non-symlink file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			override := test.prepare(t)
			t.Setenv("WATERMELON_NFQD_BINARY", override)
			dest := filepath.Join(t.TempDir(), "watermelon-nfqd")
			err := ensureNfqdBinaryAtPath(dest)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), override) {
				t.Fatalf("ensureNfqdBinaryAtPath() error = %v, want override-specific error containing %q and %q", err, test.wantErr, override)
			}
			if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("invalid override published destination: stat error = %v", statErr)
			}
		})
	}
}

func TestEnsureNfqdBinaryDoesNotBuildSourceDiscoveredFromWorkingDirectory(t *testing.T) {
	oldHostArch := cliNfqdHostArch
	cliNfqdHostArch = func() (string, error) { return "arm64", nil }
	t.Cleanup(func() { cliNfqdHostArch = oldHostArch })

	project := t.TempDir()
	nfqdPackage := filepath.Join(project, "cmd", "watermelon-nfqd")
	if err := os.MkdirAll(nfqdPackage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module "+watermelonModulePath+"\n\ngo 1.24.4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nfqdPackage, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	t.Setenv("WATERMELON_NFQD_BINARY", "")

	dest := filepath.Join(t.TempDir(), "watermelon-nfqd")
	err = ensureNfqdBinaryAtPath(dest)
	if err == nil || !strings.Contains(err.Error(), "sidecar not found") {
		t.Fatalf("ensureNfqdBinaryAtPath() error = %v, want no-release-sidecar guidance", err)
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("working-directory source was built or destination otherwise published: stat error = %v", statErr)
	}
}

func TestEnsureNfqdBinaryUsesLimaArchitectureAndValidatesPackagedRelease(t *testing.T) {
	const version = "v1.2.3"
	for _, test := range []struct {
		name           string
		sidecarVersion string
		wantContents   string
	}{
		{name: "matching", sidecarVersion: version, wantContents: "arm64 sidecar"},
		{name: "stale downloads replacement", sidecarVersion: "v1.2.2", wantContents: "downloaded matching sidecar"},
	} {
		t.Run(test.name, func(t *testing.T) {
			installDir := t.TempDir()
			armSidecar := filepath.Join(installDir, "watermelon-nfqd-linux-arm64")
			amdSidecar := filepath.Join(installDir, "watermelon-nfqd-linux-amd64")
			if err := os.WriteFile(armSidecar, []byte("arm64 sidecar"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(amdSidecar, []byte("wrong amd64 sidecar"), 0700); err != nil {
				t.Fatal(err)
			}

			oldHostArch, oldReadCLI, oldExecutable, oldNewRelease := cliNfqdHostArch, cliReadBuildInfo, cliExecutable, cliNewNfqdRelease
			hostArchCalls := 0
			cliNfqdHostArch = func() (string, error) {
				hostArchCalls++
				return "arm64", nil
			}
			cliReadBuildInfo = func() (*runtimedebug.BuildInfo, bool) {
				return testCLIReleaseBuildInfo(version), true
			}
			cliExecutable = func() (string, error) { return filepath.Join(installDir, "watermelon"), nil }
			readSidecarBuildInfo := func(path string) (*runtimedebug.BuildInfo, error) {
				if path != armSidecar {
					return testNfqdReleaseBuildInfo(version, "arm64"), nil
				}
				return testNfqdReleaseBuildInfo(test.sidecarVersion, "arm64"), nil
			}
			releaseSource := nfqdReleaseSource{readBuildInfo: readSidecarBuildInfo}
			if test.sidecarVersion != version {
				assetBody := []byte(test.wantContents)
				digest := sha256.Sum256(assetBody)
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					switch req.URL.Path {
					case "/releases/tags/" + version:
						writeReleaseMetadata(t, w, version, githubReleaseAsset{
							Name:               "watermelon-nfqd-linux-arm64",
							BrowserDownloadURL: server.URL + "/asset",
							Digest:             "sha256:" + hex.EncodeToString(digest[:]),
							Size:               int64(len(assetBody)),
						})
					case "/asset":
						_, _ = w.Write(assetBody)
					default:
						http.NotFound(w, req)
					}
				}))
				t.Cleanup(server.Close)
				releaseSource.client = server.Client()
				releaseSource.releaseAPIURL = server.URL + "/releases/tags"
			}
			cliNewNfqdRelease = func() nfqdReleaseSource { return releaseSource }
			t.Cleanup(func() {
				cliNfqdHostArch, cliReadBuildInfo, cliExecutable, cliNewNfqdRelease = oldHostArch, oldReadCLI, oldExecutable, oldNewRelease
			})
			t.Setenv("WATERMELON_NFQD_BINARY", "")

			dest := filepath.Join(t.TempDir(), "watermelon-nfqd")
			if err := ensureNfqdBinaryAtPath(dest); err != nil {
				t.Fatal(err)
			}
			if hostArchCalls != 1 {
				t.Fatalf("Lima host architecture calls = %d, want 1", hostArchCalls)
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.wantContents {
				t.Fatalf("installed sidecar = %q, want %q", got, test.wantContents)
			}
		})
	}
}

func TestVerifyRegisteredNfqdBinaryRejectsReleaseVersionSkew(t *testing.T) {
	const version = "v1.2.3"
	path := filepath.Join(t.TempDir(), "watermelon-nfqd")
	if err := os.WriteFile(path, []byte("registered sidecar"), 0700); err != nil {
		t.Fatal(err)
	}

	oldHostArch, oldReadCLI, oldReadNfqd := cliNfqdHostArch, cliReadBuildInfo, readNfqdBuildInfoFile
	cliNfqdHostArch = func() (string, error) { return "arm64", nil }
	cliReadBuildInfo = func() (*runtimedebug.BuildInfo, bool) {
		return testCLIReleaseBuildInfo(version), true
	}
	sidecarVersion := "v1.2.2"
	readNfqdBuildInfoFile = func(string) (*runtimedebug.BuildInfo, error) {
		return testNfqdReleaseBuildInfo(sidecarVersion, "arm64"), nil
	}
	t.Cleanup(func() {
		cliNfqdHostArch, cliReadBuildInfo, readNfqdBuildInfoFile = oldHostArch, oldReadCLI, oldReadNfqd
	})

	if err := verifyRegisteredNfqdBinary(path); err == nil || !strings.Contains(err.Error(), `does not match Watermelon v1.2.3`) {
		t.Fatalf("version-skew verification error = %v", err)
	}
	sidecarVersion = version
	if err := verifyRegisteredNfqdBinary(path); err != nil {
		t.Fatalf("matching registered sidecar error = %v", err)
	}
}

func TestEnsureNfqdBinaryRejectsMismatchedExplicitReleaseOverride(t *testing.T) {
	const version = "v1.2.3"
	override := filepath.Join(t.TempDir(), "watermelon-nfqd")
	if err := os.WriteFile(override, []byte("stale explicit sidecar"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATERMELON_NFQD_BINARY", override)

	oldHostArch, oldReadCLI, oldNewRelease := cliNfqdHostArch, cliReadBuildInfo, cliNewNfqdRelease
	cliNfqdHostArch = func() (string, error) { return "arm64", nil }
	cliReadBuildInfo = func() (*runtimedebug.BuildInfo, bool) {
		return testCLIReleaseBuildInfo(version), true
	}
	cliNewNfqdRelease = func() nfqdReleaseSource {
		return nfqdReleaseSource{readBuildInfo: func(path string) (*runtimedebug.BuildInfo, error) {
			if path != override {
				t.Fatalf("validated path = %q, want override %q", path, override)
			}
			return testNfqdReleaseBuildInfo("v1.2.2", "arm64"), nil
		}}
	}
	t.Cleanup(func() {
		cliNfqdHostArch, cliReadBuildInfo, cliNewNfqdRelease = oldHostArch, oldReadCLI, oldNewRelease
	})

	dest := filepath.Join(t.TempDir(), "watermelon-nfqd")
	err := ensureNfqdBinaryAtPath(dest)
	if err == nil || !strings.Contains(err.Error(), `module version is "v1.2.2"`) || !strings.Contains(err.Error(), override) {
		t.Fatalf("mismatched explicit override error = %v", err)
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched explicit override was published: %v", statErr)
	}
}

func TestCopyExecutableRejectsSymlinkSourceWithoutPublishingDestination(t *testing.T) {
	sourceDir := t.TempDir()
	target := filepath.Join(sourceDir, "target")
	if err := os.WriteFile(target, []byte("do not copy through link"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "watermelon-nfqd")
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "installed-watermelon-nfqd")

	err := copyExecutable(source, dest)
	if err == nil || !strings.Contains(err.Error(), "without following symlinks") {
		t.Fatalf("copyExecutable() error = %v, want no-follow rejection", err)
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("symlink source published destination: stat error = %v", statErr)
	}
}

func testCLIReleaseBuildInfo(version string) *runtimedebug.BuildInfo {
	return &runtimedebug.BuildInfo{
		Path: watermelonCLIPath,
		Main: runtimedebug.Module{
			Path:    watermelonModulePath,
			Version: version,
		},
	}
}

func testNfqdReleaseBuildInfo(version, arch string) *runtimedebug.BuildInfo {
	return &runtimedebug.BuildInfo{
		Path: watermelonNfqdPath,
		Main: runtimedebug.Module{
			Path:    watermelonModulePath,
			Version: version,
		},
		Settings: []runtimedebug.BuildSetting{
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: arch},
			{Key: "CGO_ENABLED", Value: "0"},
		},
	}
}

func setBuildSetting(info *runtimedebug.BuildInfo, key, value string) {
	for index := range info.Settings {
		if info.Settings[index].Key == key {
			info.Settings[index].Value = value
			return
		}
	}
	info.Settings = append(info.Settings, runtimedebug.BuildSetting{Key: key, Value: value})
}

func writeReleaseMetadata(t *testing.T, w http.ResponseWriter, version string, asset githubReleaseAsset) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(githubReleaseMetadata{TagName: version, Assets: []githubReleaseAsset{asset}}); err != nil {
		t.Errorf("encoding test release metadata: %v", err)
	}
}

func TestNfqdReleaseSourceRejectsInvalidInputsBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network must not be used")
	})}
	source := nfqdReleaseSource{
		client:        client,
		releaseAPIURL: "https://example.test/releases/tags",
		readBuildInfo: func(string) (*runtimedebug.BuildInfo, error) {
			return nil, fmt.Errorf("build information must not be read")
		},
	}
	for _, test := range []struct {
		name    string
		version string
		arch    string
		wantErr string
	}{
		{name: "pseudo-version", version: "v1.2.4-0.20260811192051-d3e5b3a7ffe0", arch: "amd64", wantErr: "not a release tag"},
		{name: "dirty version", version: "v1.2.3+dirty", arch: "amd64", wantErr: "not a release tag"},
		{name: "unsupported architecture", version: "v1.2.3", arch: "riscv64", wantErr: "not available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "watermelon-nfqd")
			err := source.install(dest, test.version, test.arch)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("install() error = %v, want containing %q", err, test.wantErr)
			}
			if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("invalid request published destination: stat error = %v", statErr)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
