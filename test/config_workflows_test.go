//go:build e2e

package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/config"
)

// Drive the release CLI through a real terminal and real Lima guests. Editor
// fixtures run on the host; repository code and dependencies stay in the VM.
func TestE2ECloneCustomizationAndConfigEdit(t *testing.T) {
	requireNonShortRealVM(t)
	timings := realVMTimings(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		skipOrFailMissingRealVMPrerequisite(t, "interactive config E2E requires python3 for its standard-library PTY driver")
	}
	h := newHarness(t)
	destination := filepath.Join(h.root, "managed")
	t.Logf("Isolated Lima test root: %s", h.root)
	t.Cleanup(func() {
		h.project = destination
		if t.Failed() && os.Getenv("WATERMELON_E2E_KEEP_FAILED") == "1" {
			return
		}
		h.destroyVM(timings.cleanup)
		digest := sha256.Sum256([]byte(destination))
		command := exec.Command("limactl", "disk", "delete", fmt.Sprintf("w%x", digest[:5]))
		command.Env = h.env
		if out, err := command.CombinedOutput(); err != nil {
			t.Logf("workspace disk cleanup: %v %s", err, out)
		}
	})
	editor := filepath.Join(h.root, "config-editor.py")
	if err := os.WriteFile(editor, []byte(configWorkflowEditor), 0600); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "VISUAL="+strconv.Quote(python)+" "+strconv.Quote(editor))

	// Exercise the actual clone prompt, including customization and its editor,
	// then defer without allowing dependency installation or saving the draft.
	t.Log("Clone, customize, edit the draft, and defer setup")
	runWorkflowTerminal(t, h, python, timings.boot, [][2]string{
		{cloneReviewPrompt, "c"},
		{"Runtime override (e.g. node=22,python=3.13): ", "node=22"},
		{"Memory [4GB]: ", "3GB"},
		{"VM system disk [10GB]: ", "12GB"},
		{"CPUs [2]: ", "2"},
		{"Forwarded localhost ports [] (comma-separated): ", ""},
		{"Additional allowed destinations (comma-separated hosts or host:port): ", "example.com:443"},
		{cloneReviewPrompt, "e"},
		{cloneReviewPrompt, "s"},
	}, "clone", "--template", "node", "https://github.com/left-pad/left-pad", destination)
	h.project = destination
	path := filepath.Join(destination, ".watermelon.toml")
	deferred, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(deferred)
	if err != nil || cfg.Setup.Profile != "" || cfg.Resources.Memory != "2GB" || bytes.Contains(deferred, []byte("E2E editor note")) {
		t.Fatalf("deferred draft changed active policy: %v\n%s", err, deferred)
	}
	if _, err := os.Stat(filepath.Join(destination, "package.json")); !os.IsNotExist(err) {
		t.Fatal("cloned source escaped onto the host")
	}
	h.run(timings.command, "exec", "sh", "-c", "git rev-parse --verify HEAD && test -f package.json && test ! -d node_modules")

	// Resume the same clone review, approve it, and inspect the real guest.
	port := workflowFreePort(t)
	t.Log("Approve customized Node 22 setup and verify the guest runtime and resources")
	runWorkflowTerminal(t, h, python, timings.boot, [][2]string{
		{cloneReviewPrompt, "c"},
		{"Runtime override (e.g. node=22,python=3.13): ", "node=22"},
		{"Memory [4GB]: ", "3GB"},
		{"VM system disk [10GB]: ", "12GB"},
		{"CPUs [2]: ", "2"},
		{"Forwarded localhost ports [] (comma-separated): ", strconv.Itoa(port)},
		{"Additional allowed destinations (comma-separated hosts or host:port): ", "example.com:443"},
		{cloneReviewPrompt, "e"},
		{cloneReviewPrompt, "y"},
	}, "up")
	assertWorkflowConfig(t, path, "22", port)
	h.run(timings.command, "exec", "node", "-e", `if (!process.version.startsWith('v22.')) throw Error(process.version); require('tape'); require('fs').writeFileSync('edit.txt','retained-edit\n')`)
	h.run(timings.command, "exec", "sh", "-c", `test "$(getconf _NPROCESSORS_ONLN)" = 2 && awk '/MemTotal:/ {exit !($2 > 2500000 && $2 < 3500000)}' /proc/meminfo && getent ahostsv4 example.com`)
	testManagedRuntimeHTTP(t, h, "node", port, timings.httpWait)

	t.Log("Reject an invalid config edit without changing the running VM")
	before, _ := os.ReadFile(path)
	h.env = append(h.env, "WATERMELON_E2E_EDIT=invalid")
	out := h.runErr(timings.command, "config", "edit")
	after, _ := os.ReadFile(path)
	if !strings.Contains(out, "draft retained") || !bytes.Equal(before, after) {
		t.Fatalf("invalid draft damaged policy: %s", out)
	}
	requireVMStatus(t, h.run(timings.status, "status"), "Running")

	t.Log("Apply an editor-only change without rebuilding")
	h.env = append(h.env, "WATERMELON_E2E_EDIT=ide")
	if out := h.run(timings.command, "config", "edit"); !strings.Contains(out, "No VM rebuild required") {
		t.Fatalf("editor-only change requested rebuilding: %s", out)
	}
	h.run(timings.command, "exec", "cat", "edit.txt")

	t.Log("Update Node to 24 and preserve comments, custom policy, and source")
	h.run(timings.boot, "up", "--runtime", "node=24", "--yes")
	assertWorkflowConfig(t, path, "24", port)
	h.run(timings.command, "exec", "node", "-e", `if (!process.version.startsWith('v24.')) throw Error(process.version); require('tape')`)
	if out := managedCommandStdout(t, h, timings.command, "exec", "cat", "edit.txt"); strings.TrimSpace(out) != "retained-edit" {
		t.Fatalf("runtime update lost source: %s", out)
	}

	t.Log("Edit a forwarded port, reject stale policy, rebuild, and verify HTTP")
	newPort := workflowFreePort(t)
	h.env = append(h.env, "WATERMELON_E2E_EDIT=port", "WATERMELON_E2E_PORT="+strconv.Itoa(newPort))
	if out := h.run(timings.command, "config", "edit"); !strings.Contains(out, "watermelon rebuild") {
		t.Fatalf("VM-affecting edit lacked apply instructions: %s", out)
	}
	if out := h.runErr(timings.command, "exec", "true"); !strings.Contains(out, "stale") {
		t.Fatalf("stale policy accepted: %s", out)
	}
	requireVMStatus(t, h.run(timings.status, "status"), "Stopped")
	h.run(timings.boot, "rebuild", "--force", "--yes")
	assertWorkflowConfig(t, path, "24", newPort)
	testManagedRuntimeHTTP(t, h, "node", newPort, timings.httpWait)
	h.run(timings.command, "config", "validate")
	t.Log("Clone customization, draft editing, runtime update, and port rebuild passed")
}

const cloneReviewPrompt = "Prepare this project? [y/N, c=customize, e=edit TOML, s=setup later] "

func TestE2EWorkflowTerminalDriver(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("terminal driver self-check requires python3")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	h := &e2eHarness{t: t, root: root, project: root, wm: shell, env: os.Environ()}
	runWorkflowTerminal(t, h, python, 5*time.Second, [][2]string{{"first> ", "accepted"}, {"second> ", ""}}, "-c", `printf 'first> '; read -r answer; test "$answer" = accepted || exit 2; printf 'second> '; read -r answer; test -z "$answer"`)
}

func workflowFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func assertWorkflowConfig(t *testing.T, path, version string, port int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Setup.Runtime != version || cfg.Resources.Memory != "3GB" || cfg.Resources.CPUs != 2 || cfg.Resources.Disk != "12GB" || len(cfg.Ports.Forward) != 1 || cfg.Ports.Forward[0] != port || strings.Join(cfg.Network.Allow, ",") != "registry.npmjs.org:443,example.com:443" || !bytes.Contains(data, []byte("# E2E editor note")) || !bytes.Contains(data, []byte("# E2E resource note")) {
		t.Fatalf("custom configuration or comments lost:\n%s", data)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func runWorkflowTerminal(t *testing.T, h *e2eHarness, python string, timeout time.Duration, prompts [][2]string, args ...string) {
	t.Helper()
	steps, err := json.Marshal(prompts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+15*time.Second)
	defer cancel()
	arguments := append([]string{"-c", configWorkflowTerminal, string(steps), strconv.FormatFloat(timeout.Seconds(), 'f', 0, 64), h.wm}, args...)
	command := exec.CommandContext(ctx, python, arguments...)
	command.Dir, command.Env = h.project, h.env
	out, err := command.CombinedOutput()
	logPath := filepath.Join(h.root, "terminal-"+args[0]+".log")
	_ = os.WriteFile(logPath, out, 0600)
	if err != nil {
		t.Fatalf("interactive watermelon %v failed: %v\n%s", args, err, out)
	}
	t.Logf("Interactive %s passed (transcript: %s)", args[0], logPath)
}

// Python's standard library supplies a real controlling terminal on both
// supported hosts without adding a production Go dependency or shell parser.
const configWorkflowTerminal = `
import errno, json, os, pty, select, signal, sys, time
steps = json.loads(sys.argv[1])
deadline = time.monotonic() + float(sys.argv[2])
pid, terminal = pty.fork()
if pid == 0:
    os.execv(sys.argv[3], sys.argv[3:])
pending = b''
step = 0
status = None
try:
    while True:
        if time.monotonic() >= deadline:
            raise TimeoutError('terminal workflow exceeded deadline')
        readable, _, _ = select.select([terminal], [], [], 0.2)
        if readable:
            try:
                data = os.read(terminal, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b''
            if not data:
                break
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
            pending += data
            while step < len(steps):
                prompt, answer = steps[step]
                at = pending.find(prompt.encode())
                if at < 0:
                    break
                pending = pending[at + len(prompt):]
                os.write(terminal, (answer + '\n').encode())
                step += 1
            pending = pending[-1048576:]
    _, status = os.waitpid(pid, 0)
finally:
    os.close(terminal)
    if status is None:
        try:
            os.killpg(pid, signal.SIGTERM)
            time.sleep(1)
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        os.waitpid(pid, 0)
if step != len(steps):
    raise RuntimeError('process exited before prompt: ' + steps[step][0])
code = os.waitstatus_to_exitcode(status)
sys.exit(code if code >= 0 else 128 - code)
`

const configWorkflowEditor = `
import os, pathlib, re, sys
path = pathlib.Path(sys.argv[1])
text = path.read_text()
mode = os.environ.get('WATERMELON_E2E_EDIT', 'comment')
if mode == 'invalid':
    path.write_text('[broken')
elif mode == 'ide':
    text, count = re.subn(r'(?m)^([ \t]*command[ \t]*=[ \t]*)"code"', r'\1"code-e2e"', text)
    assert count == 1, 'editor command not found'
    path.write_text(text)
elif mode == 'port':
    text, count = re.subn(r'(?m)^([ \t]*forward[ \t]*=[ \t]*)\[[^\n]*\]', lambda m: m[1] + '[' + os.environ['WATERMELON_E2E_PORT'] + ']', text)
    assert count == 1, 'forwarded ports not found'
    path.write_text(text)
else:
    if '# E2E editor note' not in text:
        text = '# E2E editor note\n' + text
    text, count = re.subn(r'(?m)^([ \t]*memory[^\n]*)(?<!note)$', r'\1 # E2E resource note', text)
    path.write_text(text)
`
