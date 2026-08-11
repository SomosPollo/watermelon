//go:build e2e
// +build e2e

package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type e2eHarness struct {
	t       *testing.T
	root    string
	home    string
	project string
	wm      string
	env     []string
}

type e2eTimings struct {
	boot     time.Duration
	command  time.Duration
	status   time.Duration
	stop     time.Duration
	cleanup  time.Duration
	logWait  time.Duration
	httpWait time.Duration
}

var e2eSharedCache string

func TestMain(m *testing.M) {
	cache := os.Getenv("WATERMELON_E2E_CACHE_HOME")
	removeCache := false
	if cache == "" {
		var err error
		cache, err = os.MkdirTemp("", "wm-e2e-cache-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "creating shared E2E cache: %v\n", err)
			os.Exit(1)
		}
		removeCache = true
	} else {
		var err error
		cache, err = filepath.Abs(cache)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolving shared E2E cache: %v\n", err)
			os.Exit(1)
		}
		if err := os.MkdirAll(cache, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "creating shared E2E cache %q: %v\n", cache, err)
			os.Exit(1)
		}
	}
	e2eSharedCache = cache

	code := m.Run()
	if removeCache {
		if err := os.RemoveAll(cache); err != nil {
			fmt.Fprintf(os.Stderr, "removing shared E2E cache %q: %v\n", cache, err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

func newHarness(t *testing.T) *e2eHarness {
	t.Helper()
	requireLimactl(t)

	// Keep Lima's instance directory short enough for its Unix-domain SSH
	// control socket. testing.T.TempDir includes the full test name, which can
	// push otherwise valid VM names beyond UNIX_PATH_MAX before boot begins.
	// macOS's default TMPDIR is also long, so use the conventional short temp
	// root on both hosts supported by the real-VM scenarios.
	tempBase := os.TempDir()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		tempBase = "/tmp"
	}
	root, err := os.MkdirTemp(tempBase, "wm-e2e-")
	if err != nil {
		t.Fatalf("creating short E2E root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("removing E2E root %q: %v", root, err)
		}
	})
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalizing short E2E root: %v", err)
	}
	root = canonicalRoot
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	wm := filepath.Join(root, "watermelon")
	build := exec.Command("go", "build", "-o", wm, "./cmd/watermelon")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building watermelon: %v\n%s", err, out)
	}

	env := append(os.Environ(),
		"HOME="+home,
		"USER=watermelon-e2e",
		"LIMA_HOME="+filepath.Join(home, ".lima"),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+e2eSharedCache,
	)

	return &e2eHarness{
		t:       t,
		root:    root,
		home:    home,
		project: project,
		wm:      wm,
		env:     env,
	}
}

func requireLimactl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("limactl"); err != nil {
		t.Skip("limactl is not installed")
	}
}

func (h *e2eHarness) run(timeout time.Duration, args ...string) string {
	h.t.Helper()
	out, err := h.command(timeout, args...)
	if err != nil {
		h.t.Fatalf("watermelon %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (h *e2eHarness) runInput(timeout time.Duration, input string, args ...string) string {
	h.t.Helper()
	out, err := h.commandInDirWithInput(h.project, timeout, input, args...)
	if err != nil {
		h.t.Fatalf("watermelon %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (h *e2eHarness) runErr(timeout time.Duration, args ...string) string {
	h.t.Helper()
	out, err := h.command(timeout, args...)
	if err == nil {
		h.t.Fatalf("watermelon %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return out
}

func (h *e2eHarness) runErrWithoutControllingTerminal(timeout time.Duration, args ...string) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.wm, args...)
	cmd.Dir = h.project
	cmd.Env = h.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		h.t.Fatalf("watermelon %s timed out after %s:\n%s", strings.Join(args, " "), timeout, combined.String())
	}
	if err == nil {
		h.t.Fatalf("watermelon %s unexpectedly succeeded without a controlling terminal:\n%s", strings.Join(args, " "), combined.String())
	}
	return combined.String()
}

func (h *e2eHarness) runExitCode(timeout time.Duration, want int, args ...string) string {
	h.t.Helper()
	out, err := h.command(timeout, args...)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		h.t.Fatalf("watermelon %s error = %T %v, want exit code %d\n%s", strings.Join(args, " "), err, err, want, out)
	}
	if got := exitErr.ExitCode(); got != want {
		h.t.Fatalf("watermelon %s exit code = %d, want %d\n%s", strings.Join(args, " "), got, want, out)
	}
	return out
}

func (h *e2eHarness) command(timeout time.Duration, args ...string) (string, error) {
	return h.commandInDir(h.project, timeout, args...)
}

func (h *e2eHarness) commandInDir(dir string, timeout time.Duration, args ...string) (string, error) {
	return h.commandInDirWithInput(dir, timeout, "", args...)
}

func (h *e2eHarness) commandInDirWithInput(dir string, timeout time.Duration, input string, args ...string) (string, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.wm, args...)
	cmd.Dir = dir
	cmd.Env = h.env
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return combined.String(), fmt.Errorf("timed out after %s", timeout)
	}
	return combined.String(), err
}

func (h *e2eHarness) destroyVM(timeout time.Duration) {
	h.t.Helper()
	if h.t.Failed() {
		h.logLimaDiagnostics(vmNameFromPath(h.project))
	}
	_, _ = h.command(timeout, "destroy", "--force")

	// Fall back to limactl directly in case the CLI cannot read config or the
	// destroy command exits early after a partially-created instance.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "limactl", "delete", "--force", vmNameFromPath(h.project))
	cmd.Env = h.env
	_ = cmd.Run()
}

func (h *e2eHarness) destroyNamedVM(name string, timeout time.Duration) {
	h.t.Helper()
	if h.t.Failed() {
		h.logLimaDiagnostics(name)
	}
	_, _ = h.command(timeout, "destroy", "--name", name, "--force")

	// Keep cleanup reliable even if the CLI is deliberately failing closed on
	// corrupt test state after the instance has partially been created.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "limactl", "delete", "--force", name)
	cmd.Env = h.env
	_ = cmd.Run()
}

func (h *e2eHarness) logLimaDiagnostics(name string) {
	h.t.Helper()
	instanceDir := filepath.Join(h.home, ".lima", name)
	for _, filename := range []string{"serial.log", "ha.stderr.log"} {
		path := filepath.Join(instanceDir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				h.t.Logf("reading failed-VM diagnostic %s: %v", path, err)
			}
			continue
		}
		const maxDiagnosticBytes = 256 << 10
		if len(data) > maxDiagnosticBytes {
			data = data[len(data)-maxDiagnosticBytes:]
		}
		h.t.Logf("failed-VM diagnostic %s (tail):\n%s", filename, data)
	}
}

func realVMTimings(t *testing.T) e2eTimings {
	t.Helper()

	timings := e2eTimings{
		boot:     12 * time.Minute,
		command:  45 * time.Second,
		status:   30 * time.Second,
		stop:     2 * time.Minute,
		cleanup:  3 * time.Minute,
		logWait:  30 * time.Second,
		httpWait: 90 * time.Second,
	}

	switch runtime.GOOS {
	case "darwin":
		return timings
	case "linux":
		requireQEMU(t)
		if err := requireUsableKVM(); err == nil {
			return timings
		} else if os.Getenv("WATERMELON_E2E_ALLOW_TCG") != "1" {
			t.Skipf("real Linux VM e2e requires usable /dev/kvm for reliable runtime (%v); set WATERMELON_E2E_ALLOW_TCG=1 to try slow QEMU TCG", err)
		} else {
			t.Logf("running real Linux VM e2e without KVM (%v); using slow QEMU TCG timeouts", err)
		}

		timings.boot = 45 * time.Minute
		timings.command = 3 * time.Minute
		timings.status = 90 * time.Second
		timings.stop = 5 * time.Minute
		timings.cleanup = 5 * time.Minute
		timings.logWait = 2 * time.Minute
		timings.httpWait = 3 * time.Minute
		return timings
	default:
		t.Skip("real Watermelon VM e2e requires a macOS or Linux host")
		return timings
	}
}

func requireQEMU(t *testing.T) {
	t.Helper()

	binary, ok := qemuSystemBinary()
	if !ok {
		t.Skipf("real Linux VM e2e does not know which QEMU binary to use for %s", runtime.GOARCH)
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("real Linux VM e2e requires %s: %v", binary, err)
	}
}

func qemuSystemBinary() (string, bool) {
	switch runtime.GOARCH {
	case "amd64":
		return "qemu-system-x86_64", true
	case "arm64":
		return "qemu-system-aarch64", true
	default:
		return "", false
	}
}

func requireUsableKVM() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func TestE2ECLIProjectWorkflow(t *testing.T) {
	h := newHarness(t)

	doctorOutput, err := h.commandInDir(h.root, 30*time.Second, "doctor", "--json")
	if err != nil {
		t.Fatalf("project-independent doctor failed: %v\n%s", err, doctorOutput)
	}
	var doctorReport struct {
		SchemaVersion int               `json:"schemaVersion"`
		OK            bool              `json:"ok"`
		Checks        []json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal([]byte(doctorOutput), &doctorReport); err != nil {
		t.Fatalf("doctor returned invalid JSON: %v\n%s", err, doctorOutput)
	}
	if doctorReport.SchemaVersion != 1 || !doctorReport.OK || len(doctorReport.Checks) == 0 {
		t.Fatalf("doctor returned an unhealthy or incomplete report: %+v", doctorReport)
	}

	out := h.run(30*time.Second, "init")
	if !strings.Contains(out, "Created") {
		t.Fatalf("init output did not mention created config:\n%s", out)
	}

	configPath := filepath.Join(h.project, ".watermelon.toml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config was not created: %v", err)
	}
	config := string(configBytes)
	for _, want := range []string{"[vm]", "[network]", "[mounts]", "[security]", "ask"} {
		if !strings.Contains(config, want) {
			t.Fatalf("default config missing %q:\n%s", want, config)
		}
	}
	if !strings.Contains(config, `enforcement = "fail"`) {
		t.Fatalf("default config is not strict:\n%s", config)
	}

	out = h.runErr(30*time.Second, "init")
	if !strings.Contains(out, ".watermelon.toml already exists") {
		t.Fatalf("duplicate init returned unexpected output:\n%s", out)
	}

	out = h.run(30*time.Second, "status")
	requireVMStatus(t, out, "Not found")

	out = h.runExitCode(30*time.Second, 1, "exec", "true")
	if !strings.Contains(out, "no sandbox VM found") {
		t.Fatalf("exec before VM creation returned unexpected output:\n%s", out)
	}

	if err := os.WriteFile(configPath, []byte(`[vm]
image = "ubuntu-26.04"
`), 0644); err != nil {
		t.Fatalf("writing invalid config: %v", err)
	}
	out = h.runErr(30*time.Second, "run", "--no-shell")
	if !strings.Contains(out, "unsupported vm.image") {
		t.Fatalf("invalid config returned unexpected output:\n%s", out)
	}

	if err := os.WriteFile(configPath, []byte("[vm\n"), 0644); err != nil {
		t.Fatalf("writing malformed config: %v", err)
	}
	out = h.runErr(30*time.Second, "status", "--name", "explicit-vm")
	if !strings.Contains(out, "parsing config") {
		t.Fatalf("--name masked malformed config:\n%s", out)
	}

	if err := os.WriteFile(configPath, []byte("[network]\nallow = []\n"), 0644); err != nil {
		t.Fatalf("restoring valid config: %v", err)
	}
	out = h.runErr(30*time.Second, "status", "--name", "bad:name")
	if !strings.Contains(out, "invalid --name") {
		t.Fatalf("invalid --name returned unexpected output:\n%s", out)
	}
}

func TestE2ENamedNoMountAskToolWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("real VM e2e is skipped in short mode")
	}
	timings := realVMTimings(t)
	h := newHarness(t)

	nameHash := sha256.Sum256([]byte(h.project))
	vmName := fmt.Sprintf("wm-e2e-nomount-%x", nameHash[:4])
	t.Cleanup(func() {
		h.destroyNamedVM(vmName, timings.cleanup)
	})

	nfqd := filepath.Join(h.root, "watermelon-nfqd")
	buildNFQD := exec.Command("go", "build", "-o", nfqd, "./cmd/watermelon-nfqd")
	buildNFQD.Dir = ".."
	buildNFQD.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := buildNFQD.CombinedOutput(); err != nil {
		t.Fatalf("building Linux nfqd sidecar: %v\n%s", err, out)
	}
	h.env = append(h.env, "WATERMELON_NFQD_BINARY="+nfqd)

	setupScript := filepath.Join(h.project, "setup.sh")
	if err := os.WriteFile(setupScript, []byte("#!/bin/sh\nset -eu\ninstall -d -o watermelon -g watermelon /home/watermelon/workspace\n"), 0600); err != nil {
		t.Fatalf("writing provision script: %v", err)
	}
	fakeIDE := filepath.Join(h.root, "fake-code")
	ideArgs := filepath.Join(h.root, "ide-args")
	if err := os.WriteFile(fakeIDE, []byte("#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$WM_E2E_IDE_ARGS\"\nprintf '%s\\n' \"$@\" | grep -Fx -- --wait >/dev/null\nsleep 1\n"), 0700); err != nil {
		t.Fatalf("writing fake IDE: %v", err)
	}
	h.env = append(h.env, "WM_E2E_IDE_ARGS="+ideArgs)

	configData := fmt.Sprintf(`[vm]
name = %q
image = "ubuntu-22.04"
mount_project = false
workdir = "/home/watermelon/workspace"

[ide]
command = %q
workdir = "/home/watermelon/workspace"

[tools]
"busybox:1.36" = ["busybox"]

[network]
allow = []

[security]
enforcement = "ask"

[provision]
scripts = ["./setup.sh"]

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
`, vmName, fakeIDE)
	if err := os.WriteFile(filepath.Join(h.project, ".watermelon.toml"), []byte(configData), 0600); err != nil {
		t.Fatalf("writing no-mount config: %v", err)
	}

	// Ask mode deliberately requires a foreground host process. Create the VM
	// through an interactive run and immediately exit that shell; later exec/code
	// commands each keep their own verdict server alive for their full lifetime.
	h.runInput(timings.boot, "exit\n", "run")

	out := h.run(timings.status, "status", "--name", vmName)
	requireVMStatus(t, out, "Running")
	out = h.run(timings.status, "list")
	if !strings.Contains(out, vmName) || !strings.Contains(out, h.project) {
		t.Fatalf("named no-mount VM missing from list:\n%s", out)
	}
	h.run(timings.command, "exec", "--name", vmName, "systemctl", "is-active", "--quiet", "watermelon-nfqd.service")

	// macOS intentionally uses a native dialog even when stdin is not a TTY;
	// avoid opening GUI prompts during an unattended E2E run. Linux uses the
	// terminal fallback, whose message proves the authenticated verdict request
	// reached the host and was answered with the fail-closed default.
	if runtime.GOOS != "darwin" {
		blockedOut := h.runErrWithoutControllingTerminal(timings.command, "exec", "--name", vmName, "timeout 30 bash -lc 'echo > /dev/tcp/93.184.216.34/80'")
		if !strings.Contains(blockedOut, "Watermelon network prompt requires a foreground controlling terminal; blocking by default") {
			t.Fatalf("ask-mode TCP attempt did not complete the authenticated non-interactive block flow:\n%s", blockedOut)
		}
	}

	out = h.run(timings.command, "exec", "--name", vmName, "pwd")
	if !hasOutputLine(out, "/home/watermelon/workspace") {
		t.Fatalf("configured no-mount workdir was not used:\n%s", out)
	}
	h.run(timings.command, "exec", "--name", vmName, "test", "!", "-e", "/project")
	h.run(timings.command, "exec", "--name", vmName, "sh", "-c", `test "$1" = --name && test "$2" = guest-web`, "sh", "--name", "guest-web")

	out = h.run(timings.command, "exec", "--name", vmName, "busybox", "pwd")
	if !hasOutputLine(out, "/home/watermelon/workspace") {
		t.Fatalf("no-mount tool wrapper used the wrong directory:\n%s", out)
	}
	h.run(timings.command, "exec", "--name", vmName, "busybox", "sh", "-c", "printf tool-ok > tool-output.txt")
	out = h.run(timings.command, "exec", "--name", vmName, "cat", "/home/watermelon/workspace/tool-output.txt")
	if !hasOutputLine(out, "tool-ok") {
		t.Fatalf("tool wrapper did not preserve no-mount workspace state:\n%s", out)
	}

	h.run(timings.command, "code", "--name", vmName)
	deadline := time.Now().Add(timings.command)
	for {
		data, err := os.ReadFile(ideArgs)
		if err == nil {
			rendered := string(data)
			if !strings.Contains(rendered, "--wait") || !strings.Contains(rendered, "ssh-remote+lima-"+vmName) || !strings.Contains(rendered, "/home/watermelon/workspace") {
				t.Fatalf("IDE did not receive named VM and configured workdir:\n%s", rendered)
			}
			break
		}
		if !os.IsNotExist(err) {
			t.Fatalf("reading fake IDE arguments: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fake IDE invocation")
		}
		time.Sleep(100 * time.Millisecond)
	}

	otherProject := filepath.Join(h.root, "other-project")
	if err := os.MkdirAll(otherProject, 0755); err != nil {
		t.Fatalf("creating colliding project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherProject, ".watermelon.toml"), []byte(fmt.Sprintf("[vm]\nname = %q\n", vmName)), 0600); err != nil {
		t.Fatalf("writing colliding config: %v", err)
	}
	collisionOut, collisionErr := h.commandInDir(otherProject, timings.status, "destroy", "--name", vmName, "--force")
	if collisionErr == nil || !strings.Contains(collisionOut, "owned by") {
		t.Fatalf("colliding project was not refused: err=%v\n%s", collisionErr, collisionOut)
	}
	out = h.run(timings.status, "status", "--name", vmName)
	requireVMStatus(t, out, "Running")

	h.run(timings.command, "logs", "--name", vmName, "--clear")
}

func TestE2ERealVMLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real VM e2e is skipped in short mode")
	}
	timings := realVMTimings(t)

	h := newHarness(t)
	t.Cleanup(func() {
		h.destroyVM(timings.cleanup)
	})

	extraMount := filepath.Join(h.root, "extra-mount")
	if err := os.MkdirAll(extraMount, 0755); err != nil {
		t.Fatalf("creating extra mount: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extraMount, "extra.txt"), []byte("extra-mount-ok\n"), 0644); err != nil {
		t.Fatalf("writing extra mount fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.project, "host.txt"), []byte("project-mount-ok\n"), 0644); err != nil {
		t.Fatalf("writing project fixture: %v", err)
	}

	config := fmt.Sprintf(`[vm]
image = "ubuntu-22.04"

[network]
allow = []

[mounts]
%q = { target = "/mnt/watermelon/wm-extra" }

[ports]
forward = [8765]

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"

`, extraMount)
	if err := os.WriteFile(filepath.Join(h.project, ".watermelon.toml"), []byte(config), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	h.run(timings.boot, "run", "--no-shell")

	out := h.run(timings.status, "status")
	requireVMStatus(t, out, "Running")

	out = h.run(timings.command, "exec", "pwd")
	if strings.TrimSpace(out) != "/project" {
		t.Fatalf("expected pwd to be /project, got:\n%s", out)
	}

	h.runExitCode(timings.command, 37, "exec", "--", "sh", "-c", "exit 37")
	h.runExitCode(timings.command, 255, "exec", "--", "sh", "-c", "exit 255")
	h.runExitCode(timings.command, 143, "exec", "--", "sh", "-c", "kill -TERM $$")

	out = h.run(timings.command, "exec", "cat", "/project/host.txt")
	if strings.TrimSpace(out) != "project-mount-ok" {
		t.Fatalf("project mount did not round-trip, got:\n%s", out)
	}

	out = h.run(timings.command, "exec", "cat", "/mnt/watermelon/wm-extra/extra.txt")
	if strings.TrimSpace(out) != "extra-mount-ok" {
		t.Fatalf("extra mount did not render, got:\n%s", out)
	}

	h.run(timings.command, "exec", "printf shell-ok > /project/from-shell.txt && printf ':compound-ok' >> /project/from-shell.txt")
	data, err := os.ReadFile(filepath.Join(h.project, "from-shell.txt"))
	if err != nil {
		t.Fatalf("reading shell-created file: %v", err)
	}
	if string(data) != "shell-ok:compound-ok" {
		t.Fatalf("compound exec wrote %q", string(data))
	}

	h.run(timings.command, "exec", "sh", "-lc", "printf argv-ok > /project/from-argv.txt")
	data, err = os.ReadFile(filepath.Join(h.project, "from-argv.txt"))
	if err != nil {
		t.Fatalf("reading argv-created file: %v", err)
	}
	if string(data) != "argv-ok" {
		t.Fatalf("argv exec wrote %q", string(data))
	}

	blockedOut := h.runErr(timings.command, "exec", "timeout 5 bash -lc 'echo > /dev/tcp/93.184.216.34/80'")
	if blockedOut == "" {
		t.Log("blocked network command failed with no output, as expected")
	}
	waitForLogLine(t, h, "watermelon-net", timings.logWait, timings.status)

	// The omitted [security] section must default to strict enforcement. Normal
	// resolution and attempts to select an external resolver must both receive
	// the managed resolver's negative response for an unlisted name.
	h.runErr(timings.command, "exec", "getent", "ahostsv4", "example.com")
	h.run(timings.command, "exec", "--", "python3", "-c", directDNSMediationProbe)

	out = h.run(timings.command, "exec", "cat", "/proc/sys/net/ipv6/conf/all/disable_ipv6")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("strict default must disable unfiltered IPv6, got:\n%s", out)
	}

	h.run(timings.command, "exec", "sh", "-lc", "printf port-forward-ok > index.html; nohup python3 -m http.server 8765 --bind 0.0.0.0 >/tmp/wm-e2e-http.log 2>&1 &")
	waitForHTTP(t, "http://127.0.0.1:8765/", "port-forward-ok", timings.httpWait)

	h.run(timings.stop, "stop")
	out = h.run(timings.status, "status")
	requireVMStatus(t, out, "Stopped")

	h.run(timings.boot, "exec", "true")
	out = h.run(timings.status, "status")
	requireVMStatus(t, out, "Running")

	restartedBlockedOut := h.runErr(timings.command, "exec", "timeout 5 bash -lc 'echo > /dev/tcp/93.184.216.34/80'")
	if restartedBlockedOut == "" {
		t.Log("blocked network command failed after restart with no output, as expected")
	}
}

const directDNSMediationProbe = `
import socket
import struct

name = "example.com"
question = b"".join(bytes([len(label)]) + label.encode() for label in name.split(".")) + b"\x00\x00\x01\x00\x01"
query = struct.pack("!HHHHHH", 0x574D, 0x0100, 1, 0, 0, 0) + question

udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
udp.settimeout(5)
udp.sendto(query, ("8.8.8.8", 53))
udp_response, _ = udp.recvfrom(4096)
assert udp_response[3] & 0x0F == 3, "direct UDP DNS bypassed the managed resolver"

def recv_exact(sock, size):
    data = b""
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise RuntimeError("unexpected EOF from DNS server")
        data += chunk
    return data

tcp = socket.create_connection(("8.8.8.8", 53), timeout=5)
tcp.sendall(struct.pack("!H", len(query)) + query)
tcp_size = struct.unpack("!H", recv_exact(tcp, 2))[0]
tcp_response = recv_exact(tcp, tcp_size)
assert tcp_response[3] & 0x0F == 3, "direct TCP DNS bypassed the managed resolver"
`

func requireVMStatus(t *testing.T, output, want string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "Status" {
			if got := strings.TrimSpace(value); got != want {
				t.Fatalf("VM status = %q, want %q:\n%s", got, want, output)
			}
			return
		}
	}
	t.Fatalf("status output has no Status field:\n%s", output)
}

func hasOutputLine(output, want string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func waitForLogLine(t *testing.T, h *e2eHarness, needle string, timeout, commandTimeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := h.run(commandTimeout, "logs")
		if strings.Contains(out, needle) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log line containing %q", needle)
}

func waitForHTTP(t *testing.T, url, want string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(resp.Body)
			_ = resp.Body.Close()
			if strings.Contains(body.String(), want) {
				return
			}
			lastErr = fmt.Errorf("response did not contain %q: %q", want, body.String())
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %v", url, lastErr)
}

func vmNameFromPath(projectPath string) string {
	base := filepath.Base(projectPath)
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")

	hash := sha256.Sum256([]byte(projectPath))
	shortHash := hex.EncodeToString(hash[:])[:8]

	return fmt.Sprintf("watermelon-%s-%s", base, shortHash)
}
