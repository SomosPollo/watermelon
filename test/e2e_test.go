//go:build e2e
// +build e2e

package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func newHarness(t *testing.T) *e2eHarness {
	t.Helper()
	requireLimactl(t)

	root := t.TempDir()
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

func (h *e2eHarness) runErr(timeout time.Duration, args ...string) string {
	h.t.Helper()
	out, err := h.command(timeout, args...)
	if err == nil {
		h.t.Fatalf("watermelon %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return out
}

func (h *e2eHarness) command(timeout time.Duration, args ...string) (string, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.wm, args...)
	cmd.Dir = h.project
	cmd.Env = h.env

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
	_, _ = h.command(timeout, "destroy", "--force")

	// Fall back to limactl directly in case the CLI cannot read config or the
	// destroy command exits early after a partially-created instance.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "limactl", "delete", "--force", vmNameFromPath(h.project))
	cmd.Env = h.env
	_ = cmd.Run()
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

	out = h.runErr(30*time.Second, "init")
	if !strings.Contains(out, ".watermelon.toml already exists") {
		t.Fatalf("duplicate init returned unexpected output:\n%s", out)
	}

	out = h.run(30*time.Second, "status")
	if !strings.Contains(out, "Status:  Not found") {
		t.Fatalf("status before VM creation should be Not found:\n%s", out)
	}

	out = h.runErr(30*time.Second, "exec", "true")
	if !strings.Contains(out, "no sandbox VM found") {
		t.Fatalf("exec before VM creation returned unexpected output:\n%s", out)
	}

	if err := os.WriteFile(configPath, []byte(`[vm]
image = "ubuntu-24.04"
`), 0644); err != nil {
		t.Fatalf("writing invalid config: %v", err)
	}
	out = h.runErr(30*time.Second, "run", "--no-shell")
	if !strings.Contains(out, "unsupported vm.image") {
		t.Fatalf("invalid config returned unexpected output:\n%s", out)
	}
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
%q = { target = "/mnt/wm-extra" }

[ports]
forward = [8765]

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"

[security]
enforcement = "fail"
`, extraMount)
	if err := os.WriteFile(filepath.Join(h.project, ".watermelon.toml"), []byte(config), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	h.run(timings.boot, "run", "--no-shell")

	out := h.run(timings.status, "status")
	if !strings.Contains(out, "Status:  Running") {
		t.Fatalf("status after run should be Running:\n%s", out)
	}

	out = h.run(timings.command, "exec", "pwd")
	if strings.TrimSpace(out) != "/project" {
		t.Fatalf("expected pwd to be /project, got:\n%s", out)
	}

	out = h.run(timings.command, "exec", "cat", "/project/host.txt")
	if strings.TrimSpace(out) != "project-mount-ok" {
		t.Fatalf("project mount did not round-trip, got:\n%s", out)
	}

	out = h.run(timings.command, "exec", "cat", "/mnt/wm-extra/extra.txt")
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

	h.run(timings.command, "exec", "sh", "-lc", "printf port-forward-ok > index.html; nohup python3 -m http.server 8765 --bind 0.0.0.0 >/tmp/wm-e2e-http.log 2>&1 &")
	waitForHTTP(t, "http://127.0.0.1:8765/", "port-forward-ok", timings.httpWait)

	h.run(timings.stop, "stop")
	out = h.run(timings.status, "status")
	if !strings.Contains(out, "Status:  Stopped") {
		t.Fatalf("status after stop should be Stopped:\n%s", out)
	}

	h.run(timings.boot, "exec", "true")
	out = h.run(timings.status, "status")
	if !strings.Contains(out, "Status:  Running") {
		t.Fatalf("exec should restart stopped VM:\n%s", out)
	}
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
