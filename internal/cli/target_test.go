package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/lima"
	"golang.org/x/sys/unix"
)

func TestResolveTargetsDiscoverAncestorProject(t *testing.T) {
	project := t.TempDir()
	configData := `[provision]
scripts = ["./setup.sh"]
`
	if err := os.WriteFile(filepath.Join(project, projectConfigName), []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "setup.sh"), []byte("echo ancestor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(project, "src", "package")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	canonicalProject, err := canonicalProjectRoot(project)
	if err != nil {
		t.Fatal(err)
	}

	configured, err := resolveConfiguredTarget(nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if configured.ProjectRoot != canonicalProject {
		t.Fatalf("configured project root = %q, want %q", configured.ProjectRoot, canonicalProject)
	}
	if configured.VMName != derivedVMName(canonicalProject) {
		t.Fatalf("configured VM = %q, want root-derived name", configured.VMName)
	}
	if configured.Workdir != "/project" || configured.IDEWorkdir != "/project" {
		t.Fatalf("configured workdirs = %q / %q, want /project", configured.Workdir, configured.IDEWorkdir)
	}
	if configured.PreparedProvisionScripts == nil || len(configured.PreparedProvisionScripts.Contents) != 1 || configured.PreparedProvisionScripts.Contents[0] != "echo ancestor\n" {
		t.Fatalf("prepared scripts = %+v, want ancestor-root script", configured.PreparedProvisionScripts)
	}

	management, err := resolveManagementTarget(nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if management.ProjectRoot != canonicalProject || management.VMName != configured.VMName {
		t.Fatalf("management target = %+v, want root %q and VM %q", management, canonicalProject, configured.VMName)
	}

	explicit, err := resolveConfiguredTarget(nested, "ancestor-override")
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ProjectRoot != canonicalProject || explicit.VMName != "ancestor-override" || explicit.Config.VM.Name != "ancestor-override" {
		t.Fatalf("explicit ancestor target = %+v", explicit)
	}
}

func TestResolveTargetUsesNearestAncestorConfig(t *testing.T) {
	outer, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, projectConfigName), []byte("[vm]\nname = \"outer-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "inner")
	nested := filepath.Join(inner, "src")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, projectConfigName), []byte("[vm]\nname = \"inner-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	target, err := resolveConfiguredTarget(nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.ProjectRoot != inner || target.VMName != "inner-vm" {
		t.Fatalf("nearest target = %+v, want inner config", target)
	}

	if err := os.WriteFile(filepath.Join(inner, projectConfigName), []byte("[vm\n"), 0600); err != nil {
		t.Fatal(err)
	}
	partial, err := resolveManagementTarget(nested, "")
	if err == nil || !strings.Contains(err.Error(), "parsing config") || !strings.Contains(err.Error(), filepath.Join(inner, projectConfigName)) {
		t.Fatalf("nearer malformed config error = %v, want selected path and parse failure", err)
	}
	if partial.ProjectRoot != inner {
		t.Fatalf("partial root = %q, want malformed config root %q", partial.ProjectRoot, inner)
	}
}

func TestResolveManagementTargetTreatsObservedDanglingConfigAsError(t *testing.T) {
	outer, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, projectConfigName), []byte("[vm]\nname = \"outer-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "inner")
	nested := filepath.Join(inner, "src")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(inner, "missing-config"), filepath.Join(inner, projectConfigName)); err != nil {
		t.Fatal(err)
	}

	target, err := resolveManagementTarget(nested, "")
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dangling config error = %v, want not-exist failure", err)
	}
	if target.ProjectRoot != inner || target.VMName != "" {
		t.Fatalf("dangling config target = %+v, want authoritative partial root %q", target, inner)
	}
	if !strings.Contains(err.Error(), filepath.Join(inner, projectConfigName)) {
		t.Fatalf("dangling config error does not identify selected path: %v", err)
	}
}

func TestLoadProjectConfigRejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, projectConfigName)
	if err := unix.Mkfifo(configPath, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := loadProjectConfig(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") || !strings.Contains(err.Error(), configPath) {
			t.Fatalf("FIFO config error = %v, want path and regular-file rejection", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("config loader blocked while opening a FIFO")
	}
}

func TestLoadProjectConfigRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, projectConfigName)
	file, err := os.Create(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxProjectConfigSize + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProjectConfig(dir); err == nil || !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), configPath) {
		t.Fatalf("oversized config error = %v, want size rejection with path", err)
	}
}

func TestLoadProjectConfigAcceptsSymlinkToRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "shared-config.toml")
	if err := os.WriteFile(target, []byte("[vm]\nname = \"linked-config\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, projectConfigName)); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VM.Name != "linked-config" {
		t.Fatalf("symlinked config VM name = %q", cfg.VM.Name)
	}
}

func TestLoadProjectConfigRejectsWorldWritableTarget(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, projectConfigName)
	if err := os.WriteFile(configPath, []byte("[network]\nallow = []\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProjectConfig(dir); err == nil || !strings.Contains(err.Error(), "untrusted ownership or permissions") || !strings.Contains(err.Error(), configPath) {
		t.Fatalf("world-writable config error = %v, want trust rejection with path", err)
	}
}

func TestDiscoverProjectRootHonorsGitBoundary(t *testing.T) {
	for _, markerKind := range []string{"directory", "file"} {
		t.Run(markerKind, func(t *testing.T) {
			outer, err := canonicalProjectRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outer, projectConfigName), []byte("[vm]\nname = \"outer-vm\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			repository := filepath.Join(outer, "repository")
			nested := filepath.Join(repository, "src")
			if err := os.MkdirAll(nested, 0700); err != nil {
				t.Fatal(err)
			}
			gitMarker := filepath.Join(repository, ".git")
			var markerErr error
			if markerKind == "directory" {
				markerErr = os.Mkdir(gitMarker, 0700)
			} else {
				markerErr = os.WriteFile(gitMarker, []byte("gitdir: elsewhere\n"), 0600)
			}
			if markerErr != nil {
				t.Fatal(markerErr)
			}

			root, found, err := discoverProjectRoot(nested)
			if err != nil {
				t.Fatal(err)
			}
			if found || root != nested {
				t.Fatalf("discovery across .git %s = %q, found=%v; want invocation fallback %q", markerKind, root, found, nested)
			}

			if err := os.WriteFile(filepath.Join(repository, projectConfigName), []byte("[vm]\nname = \"repository-vm\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			root, found, err = discoverProjectRoot(nested)
			if err != nil {
				t.Fatal(err)
			}
			if !found || root != repository {
				t.Fatalf("config at .git %s boundary = %q, found=%v; want %q", markerKind, root, found, repository)
			}
		})
	}
}

func TestDiscoverProjectRootDoesNotCrossFilesystemBoundary(t *testing.T) {
	outer, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, projectConfigName), []byte("[vm]\nname = \"outer-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	boundary := filepath.Join(outer, "mounted-project")
	nested := filepath.Join(boundary, "src")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	device := func(path string) (uint64, error) {
		if path == outer {
			return 1, nil
		}
		return 2, nil
	}

	root, found, err := discoverProjectRootWithDevice(nested, device)
	if err != nil {
		t.Fatal(err)
	}
	if found || root != nested {
		t.Fatalf("cross-filesystem discovery = %q, found=%v; want invocation fallback %q", root, found, nested)
	}

	if err := os.WriteFile(filepath.Join(boundary, projectConfigName), []byte("[vm]\nname = \"mounted-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, found, err = discoverProjectRootWithDevice(nested, device)
	if err != nil {
		t.Fatal(err)
	}
	if !found || root != boundary {
		t.Fatalf("boundary config discovery = %q, found=%v; want %q", root, found, boundary)
	}
}

func TestDiscoverProjectRootDoesNotEnterUntrustedWritableParent(t *testing.T) {
	outer, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, projectConfigName), []byte("[vm]\nname = \"injected-vm\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outer, 0777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outer, 0700) })
	trustedChild := filepath.Join(outer, "owned")
	nested := filepath.Join(trustedChild, "src")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}

	root, found, err := discoverProjectRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if found || root != nested {
		t.Fatalf("untrusted-parent discovery = %q, found=%v; want invocation fallback %q", root, found, nested)
	}

	if err := os.Chmod(trustedChild, 0770); err != nil {
		t.Fatal(err)
	}
	trustedConfig := filepath.Join(trustedChild, projectConfigName)
	if err := os.WriteFile(trustedConfig, []byte("[vm]\nname = \"trusted-vm\"\n"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(trustedConfig, 0660); err != nil {
		t.Fatal(err)
	}
	root, found, err = discoverProjectRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !found || root != trustedChild {
		t.Fatalf("trusted child discovery = %q, found=%v; want %q", root, found, trustedChild)
	}
}

func TestDiscoverProjectRootUsesPhysicalSymlinkAncestry(t *testing.T) {
	project, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, projectConfigName), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(project, "src")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-src")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}

	target, err := resolveConfiguredTarget(link, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.ProjectRoot != project || target.VMName != derivedVMName(project) {
		t.Fatalf("symlinked target = %+v, want physical project %q", target, project)
	}
}

func TestResolveManagementTargetKeepsConfiglessInvocationFallback(t *testing.T) {
	dir, err := canonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "unconfigured", "src")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	target, err := resolveManagementTarget(nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.ProjectRoot != nested || target.VMName != derivedVMName(nested) || target.Config != nil {
		t.Fatalf("configless management target = %+v, want invocation-directory fallback", target)
	}
	if _, err := resolveManagementTarget(nested, "explicit-vm"); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit management target without config error = %v, want missing-config failure", err)
	}
}

func TestResolveConfiguredTargetPrecedenceAndWorkdirs(t *testing.T) {
	dir := t.TempDir()
	data := `[vm]
name = "configured-vm"
mount_project = false
workdir = "/workspace"

[ide]
command = "code"
workdir = "/workspace/ide"
`
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	target, err := resolveConfiguredTarget(dir, "flag-vm")
	if err != nil {
		t.Fatal(err)
	}
	if target.VMName != "flag-vm" || target.Config.VM.Name != "flag-vm" {
		t.Fatalf("resolved name = %q, effective config name = %q", target.VMName, target.Config.VM.Name)
	}
	if target.Workdir != "/workspace" || target.IDEWorkdir != "/workspace/ide" {
		t.Fatalf("workdirs = %q / %q", target.Workdir, target.IDEWorkdir)
	}

	target, err = resolveConfiguredTarget(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.VMName != "configured-vm" {
		t.Fatalf("configured VM name = %q", target.VMName)
	}
}

func TestResolveManagementTargetRecordsExplicitNameInEffectiveConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := resolveManagementTarget(dir, "explicit-vm")
	if err != nil {
		t.Fatal(err)
	}
	if target.VMName != "explicit-vm" || target.Config.VM.Name != "explicit-vm" {
		t.Fatalf("management target = %q / config %q", target.VMName, target.Config.VM.Name)
	}
}

func TestResolveConfiguredTargetBindsExactProvisionScriptBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[provision]\nscripts = [\"./setup.sh\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "setup.sh")
	if err := os.WriteFile(scriptPath, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}

	first, err := resolveConfiguredTarget(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.PreparedProvisionScripts == nil || len(first.Config.Provision.ScriptSHA256) != 1 {
		t.Fatalf("target did not retain prepared script and digest: %+v", first)
	}
	firstDigest := first.Config.Provision.ScriptSHA256[0]

	if err := os.WriteFile(scriptPath, []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := resolveConfiguredTarget(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Config.Provision.ScriptSHA256[0] == firstDigest {
		t.Fatal("same-path script edit did not change resolved applied digest")
	}
}

func TestResolveConfiguredTargetNeverMasksConfigErrorsWithName(t *testing.T) {
	oldStatus := cliGetVMStatus
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusNotFound }
	t.Cleanup(func() { cliGetVMStatus = oldStatus })

	for _, test := range []struct {
		name    string
		prepare func(string) error
		want    string
	}{
		{
			name: "missing",
			prepare: func(string) error {
				return nil
			},
			want: "no .watermelon.toml",
		},
		{
			name: "malformed",
			prepare: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[vm\n"), 0600)
			},
			want: "parsing config",
		},
		{
			name: "invalid",
			prepare: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[security]\nenforcement = \"invalid\"\n"), 0600)
			},
			want: "invalid config",
		},
		{
			name: "wrong type",
			prepare: func(dir string) error {
				return os.Mkdir(filepath.Join(dir, ".watermelon.toml"), 0700)
			},
			want: "regular file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := test.prepare(dir); err != nil {
				t.Fatal(err)
			}
			_, err := resolveConfiguredTarget(dir, "explicit-vm")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestResolveConfiguredTargetRejectsInvalidFlagBeforeVMLookup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldStatus := cliGetVMStatus
	cliGetVMStatus = func(string) lima.VMStatus {
		t.Fatal("VM status must not be queried for an invalid name")
		return lima.StatusUnknown
	}
	t.Cleanup(func() { cliGetVMStatus = oldStatus })

	if _, err := resolveConfiguredTarget(dir, "bad:name"); err == nil || !strings.Contains(err.Error(), "invalid --name") {
		t.Fatalf("invalid name error = %v", err)
	} else if !IsUsageError(err) {
		t.Fatalf("invalid explicit name error = %T %v, want usage error", err, err)
	}
}

func TestResolveManagementTargetMarksOnlyExplicitInvalidNameAsUsageError(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveManagementTarget(dir, "bad:name"); err == nil {
		t.Fatal("invalid explicit name unexpectedly succeeded")
	} else if !IsUsageError(err) {
		t.Fatalf("invalid explicit name error = %T %v, want usage error", err, err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[vm]\nname = \"bad:name\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveManagementTarget(dir, ""); err == nil {
		t.Fatal("invalid configured name unexpectedly succeeded")
	} else if IsUsageError(err) {
		t.Fatalf("invalid configured name error = %T %v, must not be a usage error", err, err)
	}
}
