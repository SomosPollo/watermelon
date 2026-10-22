package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestManagedWorkspaceGeneration(t *testing.T) {
	cfg := config.NewConfig()
	mount := false
	cfg.VM.MountProject = &mount
	cfg.VM.Workdir = "/workspace/project"
	cfg.Workspace = config.WorkspaceConfig{Disk: "w0123456789", Source: "https://github.com/example/project"}
	cfg.Setup = config.SetupConfig{Profile: "python", Runtime: "3.13"}
	cfg.Ports.Forward = []int{3000}
	image, _ := config.SetupImage(cfg.Setup)
	cfg.Tools[image] = []string{"python", "python3", "pip"}
	generated, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := GenerateConfigForInstance(cfg, t.TempDir(), nil, GenerateOptions{FormatWorkspaceDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(initial, "format: true") {
		t.Fatal("new empty disk is not initialized")
	}
	if !strings.Contains(generated, `/mnt/lima-w0123456789/logs/logs.log`) {
		t.Fatal("managed logs are not stored on the guest data disk")
	}
	withIdentity, err := GenerateConfigForInstance(cfg, t.TempDir(), nil, GenerateOptions{BootstrapHostDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withIdentity, "    writable: true") || strings.Contains(withIdentity, "mountPoint: /mnt/watermelon/state") {
		t.Fatal("managed VM has a writable host state mount")
	}
	if _, err := GenerateConfigForInstance(cfg, t.TempDir(), nil, GenerateOptions{LogHostDir: t.TempDir()}); err == nil {
		t.Fatal("managed generator accepts a writable host logging mount")
	}
	if !strings.Contains(generated, "propagateProxyEnv: false") {
		t.Fatal("profile inherits host proxy credentials")
	}
	for _, field := range []string{"forwardAgent", "forwardX11", "forwardX11Trusted", "loadDotSSHPubKeys"} {
		if !strings.Contains(generated, "  "+field+": false") {
			t.Fatalf("host SSH capability inherits defaults: %s", field)
		}
	}
	if !strings.Contains(generated, "hostResolver:\n  enabled: false") || strings.Contains(generated, "\ndns:\n") {
		t.Fatal("managed workspace bootstrap must disable the optional host resolver without pinning public DNS servers")
	}
	for _, want := range []string{"additionalDisks:", `name: "w0123456789"`, "format: false", "mounts: []", "/usr/bin/mountpoint -q", "watermelon-project", "--pull=never", `--env "PATH=/watermelon-runtime/bin:$_wm_venv/bin:`, "git python3", `hostIP: "127.0.0.1"`} {
		if !strings.Contains(generated, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, script := range extractSystemProvisionScripts(t, generated) {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid provision script: %v %s", err, out)
		}
	}
	t.Run("real Lima contract", func(t *testing.T) {
		binary := realLimaBinaryForTest(t)
		path := filepath.Join(t.TempDir(), "workspace.yaml")
		if err := os.WriteFile(path, []byte(generated), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(binary, "validate", "--tty=false", path).CombinedOutput(); err != nil {
			t.Fatalf("invalid Lima config: %v %s", err, out)
		}
	})
}

func TestGoRustRuntimeGeneration(t *testing.T) {
	for _, profile := range []string{"go", "rust"} {
		t.Run(profile, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Setup.Profile = profile
			image, err := config.SetupImage(cfg.Setup)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Tools[image] = config.SetupCommands(profile)
			generated, err := GenerateConfig(cfg, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, script := range extractSystemProvisionScripts(t, generated) {
				command := exec.Command("bash", "-n")
				command.Stdin = strings.NewReader(script)
				if out, err := command.CombinedOutput(); err != nil {
					t.Fatalf("invalid %s provision: %v %s", profile, err, out)
				}
			}
			checks := []string{`--env GOTOOLCHAIN=local`, `--env GOENV=off`, `--env GOSUMDB=sum.golang.org`, `--env GOVCS=off`, `$_wm_home/go/bin:/usr/local/go/bin:`}
			if profile == "rust" {
				checks = []string{`--env "CARGO_HOME=$_wm_home/.cargo"`, `$_wm_home/.cargo/bin:/usr/local/cargo/bin:`, `RUSTUP_TOOLCHAIN="${RUST_VERSION:?missing image toolchain version}"`, `--entrypoint /bin/sh`}
			}
			for _, check := range checks {
				if !strings.Contains(generated, check) {
					t.Errorf("%s runtime missing %q", profile, check)
				}
			}
		})
	}
}

func TestDiskLifecycleNeverForceDeletesOrAutoAdopts(t *testing.T) {
	old := execCommand
	t.Cleanup(func() { execCommand = old })
	var called []string
	var calls []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = append([]string{name}, args...)
		calls = append(calls, strings.Join(called, " "))
		return exec.Command("true")
	}
	if err := CreateWorkspaceDisk("wm-ws-test", "10GB"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(called, " "); !strings.Contains(got, "disk create wm-ws-test") || !strings.Contains(got, "--format raw") {
		t.Fatal(got)
	}
	if err := DeleteWorkspaceDisk("wm-ws-test"); err != nil {
		t.Fatal(err)
	}
	if calls[1] != "limactl disk delete wm-ws-test" || calls[2] != "limactl disk list --json" {
		t.Fatal(calls)
	}
}

func TestDiskDeletionVerifiesRemoval(t *testing.T) {
	old := execCommand
	t.Cleanup(func() { execCommand = old })
	execCommand = func(_ string, args ...string) *exec.Cmd {
		if len(args) > 1 && args[1] == "list" {
			return exec.Command("printf", "%s", `{"name":"w0123456789","instance":""}`)
		}
		return exec.Command("true")
	}
	if err := DeleteWorkspaceDisk("w0123456789"); err == nil {
		t.Fatal("skipped deletion reported success")
	}
}

type preparationRunner struct {
	t     *testing.T
	calls int
}

func (r *preparationRunner) Run(command *exec.Cmd) error {
	r.calls++
	if len(command.Args) < 3 || command.Args[1] != "--tty=false" {
		r.t.Fatalf("preparation may open Lima's editor: %v", command.Args)
	}
	return nil
}
func TestProjectPreparationDoesNotOpenLimaEditor(t *testing.T) {
	runner := &preparationRunner{t: t}
	if err := StartWithRunner("project-vm", "/tmp/generated.yaml", runner); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("startup stages=%d", runner.calls)
	}
}

func TestCombinedRuntimeAndNestedVenvGeneration(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Setup = config.SetupConfig{Profile: "multi", Runtimes: map[string]string{"node": "24", "python": "3.13", "go": "1.27", "rust": "1.99"}, Projects: []config.SetupProject{{Directory: "frontend", Profile: "node"}, {Directory: "backend", Profile: "python"}, {Directory: "service", Profile: "go"}, {Directory: "native", Profile: "rust"}}}
	image, err := config.SetupImage(cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tools[image] = config.AllSetupCommands(cfg.Setup)
	generated, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`wm_nerdctl pull "node:24-bookworm"`, `wm_nerdctl pull "python:3.13-bookworm"`, `wm_nerdctl pull "golang:1.27-bookworm"`, `wm_nerdctl pull "rust:1.99-bookworm"`, `--network=none -v "$_WM_COMPOSE_STAGE:/watermelon-toolchains"`, `cp -a /watermelon-toolchains/. /usr/local/ && /sbin/ldconfig`, `rustup default`, `GOTOOLCHAIN=local`, `CARGO_HOME=$_wm_home/.cargo`, `_wm_root${_wm_scan#"$_wm_real_root"}/.venv`, `/backend/.venv'`} {
		if !strings.Contains(generated, want) {
			t.Errorf("missing combined runtime %q", want)
		}
	}
	if strings.Contains(generated, `wm_nerdctl pull "`+image+`"`) {
		t.Fatal("attempts to pull guest-composed image")
	}
	for _, script := range extractSystemProvisionScripts(t, generated) {
		command := exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(script)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("invalid composed script: %v %s", err, out)
		}
	}
	// pwd -P resolves symlinks in a mounted workspace. Venv paths passed into the
	// container must use the logical bind target, which is the only path it sees.
	physical := t.TempDir()
	logical := filepath.Join(t.TempDir(), "logical")
	if err := os.MkdirAll(filepath.Join(physical, "backend", ".venv", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(physical, "backend", ".venv", "bin", "python"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(physical, "backend", ".venv", "pyvenv.cfg"), []byte("home=/usr/local/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(generated, "      _wm_root=")
	end := strings.Index(generated[start:], "      /bin/mkdir -p --") + start
	script := generated[start:end]
	realpath, err := exec.LookPath("realpath")
	if err != nil {
		t.Skip("realpath unavailable")
	}
	script = strings.ReplaceAll(script, "/usr/bin/realpath -e", realpath)
	script = strings.Replace(script, "_wm_root='/project'", "_wm_root='"+logical+"'", 1)
	command := exec.Command("bash", "-eu", "-c", script+"\nprintf '%s' \"$_wm_venv\"")
	command.Dir = filepath.Join(logical, "backend")
	out, err := command.CombinedOutput()
	if err != nil || string(out) != filepath.Join(logical, "backend", ".venv") {
		t.Fatalf("venv bind mapping: %q %v", out, err)
	}
	// Poetry can link to an interpreter that exists only in the image, so the
	// guest-side link is dangling even though it is valid inside the container.
	python := filepath.Join(physical, "backend", ".venv", "bin", "python")
	if err := os.Remove(python); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/local/bin/watermelon-image-only-python", python); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("bash", "-eu", "-c", script+`
printf '%s' "$_wm_venv"`)
	command.Dir = filepath.Join(logical, "backend")
	out, err = command.CombinedOutput()
	if err != nil || string(out) != filepath.Join(logical, "backend", ".venv") {
		t.Fatalf("image-only interpreter not selected: %q %v", out, err)
	}
}

func TestExtendedRuntimeAssemblyUsesOnlyTrustedDonors(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Setup = config.SetupConfig{Profile: "multi", Runtimes: map[string]string{"java": "21", "dotnet": "10.0", "ruby": "3.4", "php": "8.4", "cpp": "12", "node": "24", "python": "3.13"}, Projects: []config.SetupProject{{Directory: "java", Profile: "java"}, {Directory: "dotnet", Profile: "dotnet"}, {Directory: "ruby", Profile: "ruby"}, {Directory: "php", Profile: "php"}, {Directory: "cpp", Profile: "cpp"}, {Directory: "frontend", Profile: "node", Manager: "pnpm@10.20.0"}, {Directory: "backend", Profile: "python", Manager: "uv@0.12.0"}}}
	image, err := config.SetupImage(cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tools[image] = config.AllSetupCommands(cfg.Setup)
	generated, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"eclipse-temurin:21-jdk-jammy", "mcr.microsoft.com/dotnet/sdk:10.0-noble", "ruby:3.4-bookworm", "php:8.4-cli-bookworm", "gcc:12-bookworm", "/opt/java/openjdk/.", "/usr/share/dotnet/.", "--user 0:0 --network=none", "--env JAVA_HOME=/usr/local/java", "MAVEN_OPTS=-Duser.home=$_wm_home -Dmaven.repo.local=$_wm_home/.m2/repository", "--env DOTNET_ROOT=/usr/local/share/dotnet", "--env COMPOSER_ALLOW_SUPERUSER=1", "--env UV_PYTHON_DOWNLOADS=never", "/usr/local/share/watermelon-runtime:/watermelon-runtime:ro"} {
		if !strings.Contains(generated, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(generated, `wm_nerdctl pull "`+image+`"`) {
		t.Fatal("attempts to pull local assembly output")
	}
	for _, script := range extractSystemProvisionScripts(t, generated) {
		command := exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(script)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("invalid runtime script: %v %s", err, out)
		}
	}
	t.Run("real Lima contract", func(t *testing.T) {
		binary := realLimaBinaryForTest(t)
		file := filepath.Join(t.TempDir(), "extended.yaml")
		if err := os.WriteFile(file, []byte(generated), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(binary, "validate", "--tty=false", file).CombinedOutput(); err != nil {
			t.Fatalf("invalid extended Lima config: %v %s", err, out)
		}
	})
}
