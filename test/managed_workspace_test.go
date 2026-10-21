//go:build e2e

package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const managedGoSource = `package main
import (
 "fmt"
 "net/http"
 "os"
 "github.com/google/uuid"
)
func main() {
 if uuid.MustParse("123e4567-e89b-12d3-a456-426614174000").String() != "123e4567-e89b-12d3-a456-426614174000" { panic("dependency failed") }
 if len(os.Args) == 1 { fmt.Println("go-ready"); return }
 http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "retained-edit") })
 if err := http.ListenAndServe("0.0.0.0:" + os.Args[1], nil); err != nil { panic(err) }
}
`

const managedRustSource = `use std::io::{Read, Write};
use std::net::TcpListener;
fn main() {
 assert_eq!(itoa::Buffer::new().format(42), "42");
 let port = match std::env::args().nth(1) { Some(value) => value, None => { println!("rust-ready"); return; } };
 for connection in TcpListener::bind(format!("0.0.0.0:{}", port)).unwrap().incoming() {
  let mut stream = connection.unwrap();
  let mut request = [0u8; 4096];
  let _ = stream.read(&mut request);
  let _ = stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 13\r\nConnection: close\r\n\r\nretained-edit");
 }
}
`

// Use a real public Git checkout, then create small dependency fixtures inside
// the guest. No fixture repository, package install, or setup hook runs on the
// host. The same boundary is tested for every supported language profile.
func TestE2EManagedWorkspaceProjects(t *testing.T) {
	testManagedWorkspaceProjects(t, false)
}

// Runtime coverage can be exercised independently of GitHub availability.
// The host harness explicitly creates its own trusted ownership fixture and
// seeds all project content inside the guest. This is not a production bypass.
func TestE2EManagedWorkspaceRuntime(t *testing.T) {
	testManagedWorkspaceProjects(t, true)
}

func TestE2EManagedWorkspaceLogs(t *testing.T) {
	requireNonShortRealVM(t)
	timings := realVMTimings(t)
	h := newHarness(t)
	destination := filepath.Join(h.root, "managed-logs")
	seedManagedWorkspace(t, h, destination)
	h.project = destination
	digest := sha256.Sum256([]byte(destination))
	disk := fmt.Sprintf("w%x", digest[:5])
	t.Cleanup(func() {
		h.destroyVM(timings.cleanup)
		command := exec.Command("limactl", "disk", "delete", disk)
		command.Env = h.env
		if out, err := command.CombinedOutput(); err != nil {
			t.Logf("disk cleanup: %v %s", err, out)
		}
	})
	h.run(timings.boot, "run", "--no-shell")
	h.run(timings.command, "exec", "sh", "-c", `test ! -e /mnt/watermelon/state; test -d "$1"`, "fixture", "/mnt/lima-"+disk+"/logs")
	h.runBlockedNetwork(timings.command, "exec", "bash", "-lc", blockedNetworkProbe)
	waitForLogLine(t, h, timings.logWait, timings.status, "watermelon-net", "DST=93.184.216.34", "DPT=80")
	h.run(timings.command, "exec", "sh", "-c", `printf '%s\n' managed-log-retained >> "$1"`, "fixture", "/mnt/lima-"+disk+"/logs/logs.log")
	// This is intentionally a manual/no-profile workspace. Rebuild must retain
	// source and guest logs without forcing automatic manifest detection.
	h.run(timings.boot, "rebuild", "--force")
	if out := h.run(timings.status, "logs"); !strings.Contains(out, "managed-log-retained") {
		t.Fatalf("log lost on rebuild: %s", out)
	}
	h.run(timings.status, "logs", "--clear")
	if out := h.run(timings.status, "logs"); strings.Contains(out, "managed-log-retained") {
		t.Fatalf("clear failed: %s", out)
	}
}

func testManagedWorkspaceProjects(t *testing.T, controlledSource bool) {
	requireNonShortRealVM(t)
	timings := realVMTimings(t)
	for _, profile := range []string{"node", "python", "go", "rust"} {
		t.Run(profile, func(t *testing.T) {
			h := newHarness(t)
			destination := filepath.Join(h.root, "managed")
			t.Cleanup(func() {
				h.project = destination
				if t.Failed() && os.Getenv("WATERMELON_E2E_KEEP_FAILED") == "1" {
					t.Logf("retained failed workspace for inspection: %s", h.root)
					return
				}
				h.destroyVM(timings.cleanup)
				digest := sha256.Sum256([]byte(destination))
				disk := fmt.Sprintf("w%x", digest[:5])
				command := exec.Command("limactl", "disk", "delete", disk)
				command.Env = h.env
				if out, err := command.CombinedOutput(); err != nil {
					t.Logf("managed disk cleanup: %v %s", err, out)
				}
			})
			if controlledSource {
				seedManagedWorkspace(t, h, destination)
				h.project = destination
				h.run(timings.boot, "run", "--no-shell")
			} else {
				// Reproduce a stale/unreachable answer in Lima's host resolver.
				// Public acquisition must use the managed DNS upstreams instead.
				configDir := filepath.Join(h.home, ".lima", "_config")
				if err := os.MkdirAll(configDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(configDir, "default.yaml"), []byte("hostResolver:\n  hosts:\n    github.com: 192.0.2.1\n"), 0600); err != nil {
					t.Fatal(err)
				}
				h.run(timings.boot, "clone", "--no-setup", "https://github.com/octocat/Hello-World", destination)
				h.project = destination
				h.run(timings.command, "exec", "sh", "-c", `git rev-parse --verify HEAD; for host in example.com api.github.com nested.github.com; do if getent ahostsv4 "$host" >/dev/null; then echo "unexpected DNS permission: $host" >&2; exit 1; fi; done`)
			}
			h.project = destination
			h.run(timings.command, "exec", "sh", "-c", `test ! -e /mnt/watermelon/state`)
			if _, err := os.Stat(filepath.Join(destination, "README")); !os.IsNotExist(err) {
				t.Fatal("repository was cloned onto the host")
			}
			// An attacker-authored guest policy must never replace the approved host
			// config, including during initial automatic detection and VM recreation.
			h.run(timings.command, "exec", "sh", "-c", `printf '%s\n' '[security]' 'enforcement = "log"' > .watermelon.toml; printf '%s\n' retained-edit > edit.txt`)
			switch profile {
			case "node":
				fixture := `{"name":"watermelon-guest-fixture","version":"1.0.0","private":true,"scripts":{"postinstall":"node -e \"require('fs').appendFileSync('installed.txt','installed\\n')\""},"dependencies":{"is-number":"7.0.0"}}`
				h.run(timings.command, "exec", "sh", "-c", `printf '%s' "$1" > package.json`, "fixture", fixture)
			case "python":
				h.run(timings.command, "exec", "sh", "-c", `printf '%s\n' 'idna==3.10' 'wheel==0.45.1' > requirements.txt`)
			case "go":
				h.run(timings.command, "exec", "sh", "-c", `mkdir -p app; printf '%s\n' 'module example.com/watermelon-fixture' 'go 1.25.0' 'require github.com/google/uuid v1.6.0' > app/go.mod; printf '%s\n' 'go 1.25.0' 'use ./app' > go.work; printf '%s' "$1" > app/main.go`, "fixture", managedGoSource)
			case "rust":
				h.run(timings.command, "exec", "sh", "-c", `printf '%s\n' '[package]' 'name="watermelon-fixture"' 'version="0.1.0"' 'edition="2024"' 'rust-version="1.85"' '[dependencies]' 'itoa="=1.0.15"' > Cargo.toml; mkdir -p src; printf '%s' "$1" > src/main.rs; printf '%s\n' 'fn main() { std::fs::write("build-ran", "built").unwrap(); }' > build.rs; printf '%s\n' '[toolchain]' 'channel="stable"' 'components=["rustfmt", "clippy"]' > rust-toolchain.toml`, "fixture", managedRustSource)
			}
			h.run(timings.boot, "up", "--yes")
			hostPolicy, err := os.ReadFile(filepath.Join(destination, ".watermelon.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(hostPolicy), `enforcement = "fail"`) {
				t.Fatalf("guest configuration affected host policy: %s", hostPolicy)
			}
			switch profile {
			case "node":
				h.run(timings.command, "exec", "node", "-e", `if (!require('is-number')(42)) process.exit(1); if (!require('fs').existsSync('installed.txt')) process.exit(2)`)
			case "python":
				h.run(timings.command, "exec", "python", "-c", `import idna; assert idna.encode('example.com') == b'example.com'`)
				h.run(timings.command, "exec", "wheel", "version")
			case "go":
				h.run(timings.boot, "exec", "go", "run", "-mod=readonly", "./app")
				h.run(timings.command, "exec", "sh", "-c", `test -d "$GOMODCACHE/github.com/google/uuid@v1.6.0"; test "$GOTOOLCHAIN" = local; test "$GOPROXY" = https://proxy.golang.org; test "$GOSUMDB" = sum.golang.org`)
			case "rust":
				h.run(timings.command, "exec", "test", "!", "-e", "build-ran")
				h.run(timings.boot, "exec", "cargo", "run", "--offline", "--locked", "--quiet")
				h.run(timings.command, "exec", "rustfmt", "--version")
				h.run(timings.boot, "exec", "cargo", "clippy", "--offline", "--locked", "--quiet")
				h.run(timings.command, "exec", "sh", "-c", `test -e build-ran; test "$RUSTUP_TOOLCHAIN" = "$RUST_VERSION"; test "$CARGO_HOME" = /workspace/home/.cargo`)
			}
			if out := h.run(timings.command, "up", "--yes"); !strings.Contains(out, "unchanged") {
				t.Fatalf("setup was repeated: %s", out)
			}
			h.runBlockedNetwork(timings.command, "exec", "bash", "-lc", blockedNetworkProbe)
			waitForLogLine(t, h, timings.logWait, timings.status, "watermelon-net", "DST=93.184.216.34", "DPT=80")
			h.run(timings.status, "logs", "--clear")
			if out := h.run(timings.status, "logs"); strings.Contains(out, "DST=93.184.216.34") {
				t.Fatal("managed log clear retained blocked event")
			}
			h.run(timings.command, "exec", "sh", "-c", `mkdir -p nested; cd nested; pwd | grep '/project/nested$'`)
			h.run(timings.stop, "stop")
			h.run(timings.boot, "exec", "cat", "edit.txt")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			policy, err := os.ReadFile(filepath.Join(destination, ".watermelon.toml"))
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(policy), "forward = []", fmt.Sprintf("forward = [%d]", port), 1)
			if changed == string(policy) {
				t.Fatal("could not configure test port")
			}
			if err := os.WriteFile(filepath.Join(destination, ".watermelon.toml"), []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			h.run(timings.boot, "rebuild", "--force", "--yes")
			if out := managedCommandStdout(t, h, timings.command, "exec", "cat", "edit.txt"); strings.TrimSpace(out) != "retained-edit" {
				t.Fatalf("edit lost or command output polluted: %s", out)
			}
			switch profile {
			case "node":
				h.run(timings.command, "exec", "node", "-e", `require('is-number')`)
			case "python":
				h.run(timings.command, "exec", "python", "-c", `import idna`)
			case "go":
				h.run(timings.boot, "exec", "env", "GOPROXY=off", "go", "run", "-mod=readonly", "./app")
			case "rust":
				h.run(timings.boot, "exec", "cargo", "run", "--offline", "--locked", "--quiet")
			}
			testManagedRuntimeHTTP(t, h, profile, port, timings.httpWait)
			output := h.run(timings.status, "status", "--json")
			var status struct {
				Version   int `json:"schema_version"`
				Workspace struct {
					Disk string `json:"disk"`
				} `json:"workspace"`
				Setup struct {
					Stage string `json:"stage"`
				} `json:"setup"`
			}
			if err := json.Unmarshal([]byte(output), &status); err != nil {
				t.Fatal(err)
			}
			if status.Version != 1 || status.Workspace.Disk == "" || status.Setup.Stage != "ready" {
				t.Fatalf("bad status: %s", output)
			}
			exported := filepath.Join(h.root, "exported.txt")
			h.run(timings.command, "copy", vmNameFromPath(destination)+":/workspace/project/edit.txt", exported)
			content, err := os.ReadFile(exported)
			if err != nil || strings.TrimSpace(string(content)) != "retained-edit" {
				t.Fatalf("export failed: %q %v", content, err)
			}
		})
	}
}

func seedManagedWorkspace(t *testing.T, h *e2eHarness, destination string) {
	t.Helper()
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(destination))
	disk := fmt.Sprintf("w%x", digest[:5])
	command := exec.Command("limactl", "disk", "create", disk, "--size", "10GB", "--format", "raw")
	command.Env = h.env
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture disk: %v %s", err, out)
	}
	source := "https://github.com/octocat/Hello-World"
	policy := fmt.Sprintf("[vm]\nmount_project = false\nworkdir = '/workspace/project'\n[workspace]\ndisk = %q\nsource = %q\n[security]\nenforcement = 'fail'\n", disk, source)
	if err := os.WriteFile(filepath.Join(destination, ".watermelon.toml"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	limaHome := filepath.Join(h.home, ".lima")
	namespace := sha256.Sum256([]byte(limaHome))
	stateDir := filepath.Join(h.home, ".config", "watermelon", "vms", fmt.Sprintf("lima-%x", namespace))
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"version": 1, "owner": destination, "disk": disk, "source": source, "disk_created": true, "cloned": true, "stage": "controlled guest fixture"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, fmt.Sprintf("workspace-%x.json", digest)), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func managedCommandStdout(t *testing.T, h *e2eHarness, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.wm, args...)
	cmd.Dir = h.project
	cmd.Env = h.env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command stdout check failed: %v %s", err, out)
	}
	return string(out)
}

func testManagedRuntimeHTTP(t *testing.T, h *e2eHarness, profile string, port int, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{"exec", "node", "-e", fmt.Sprintf(`require('http').createServer((q,r)=>r.end('retained-edit')).listen(%d,'0.0.0.0')`, port)}
	if profile == "python" {
		args = []string{"exec", "python", "-m", "http.server", strconv.Itoa(port), "--bind", "0.0.0.0"}
	} else if profile == "go" {
		args = []string{"exec", "go", "run", "-mod=readonly", "./app", strconv.Itoa(port)}
	} else if profile == "rust" {
		args = []string{"exec", "cargo", "run", "--offline", "--locked", "--quiet", "--", strconv.Itoa(port)}
	}
	server := exec.CommandContext(ctx, h.wm, args...)
	server.Dir = h.project
	server.Env = h.env
	var logs bytes.Buffer
	server.Stdout = &logs
	server.Stderr = &logs
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.Cancel = func() error { return syscall.Kill(-server.Process.Pid, syscall.SIGTERM) }
	server.WaitDelay = 5 * time.Second
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-done
		}
	}()
	client := http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			finished = true
			t.Fatalf("%s runtime server exited: %v\n%s", profile, err, logs.String())
		default:
		}
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/edit.txt", port))
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
			response.Body.Close()
			if response.StatusCode == 200 && strings.TrimSpace(string(body)) == "retained-edit" {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	cancel()
	<-done
	finished = true
	t.Fatalf("%s runtime server was not reachable through the approved localhost port\n%s", profile, logs.String())
}
