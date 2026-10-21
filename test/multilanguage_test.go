//go:build e2e

package test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// All four languages must be available to the same process, including npm
// lifecycle hooks, with dependencies installed in their own project directory.
func TestE2EManagedWorkspaceMultiLanguage(t *testing.T) {
	requireNonShortRealVM(t)
	timings := realVMTimings(t)
	h := newHarness(t)
	destination := filepath.Join(h.root, "mixed")
	t.Cleanup(func() {
		h.project = destination
		if t.Failed() && os.Getenv("WATERMELON_E2E_KEEP_FAILED") == "1" {
			t.Logf("retained failed workspace for inspection: %s", h.root)
			return
		}
		h.destroyVM(timings.cleanup)
		digest := sha256.Sum256([]byte(destination))
		command := exec.Command("limactl", "disk", "delete", fmt.Sprintf("w%x", digest[:5]))
		command.Env = h.env
		if out, err := command.CombinedOutput(); err != nil {
			t.Logf("disk cleanup: %v %s", err, out)
		}
	})
	h.run(timings.boot, "clone", "--no-setup", "https://github.com/octocat/Hello-World", destination)
	h.project = destination
	// Hooks exercise shared PATH, the Python environment, and native compilers.
	pkg := `{"name":"mixed-fixture","version":"1.0.0","private":true,"scripts":{"postinstall":"node setup.cjs"},"dependencies":{"is-number":"7.0.0"}}`
	hook := `const cp = require('child_process'); for (const [command,args] of [['python',['-c','import idna; print(idna.__version__)']],['go',['version']],['rustc',['--version']],['rustfmt',['--version']]]) { const r=cp.spawnSync(command,args,{stdio:'inherit'}); if(r.status!==0) throw Error(command+' failed: '+r.error); } require('fs').writeFileSync('installed.txt','ready');`
	h.run(timings.command, "exec", "sh", "-c", `mkdir -p frontend backend service native/src; printf '%s' "$1" > frontend/package.json; printf '%s' "$2" > frontend/setup.cjs; printf '%s\n' 'idna==3.10' > backend/requirements.txt; printf '%s\n' 'module example.com/mixed' 'go 1.25.0' 'require github.com/google/uuid v1.6.0' > service/go.mod; printf '%s' "$3" > service/main.go; printf '%s\n' '[package]' 'name="mixed-fixture"' 'version="0.1.0"' 'edition="2024"' '[dependencies]' 'itoa="=1.0.15"' > native/Cargo.toml; printf '%s' "$4" > native/src/main.rs; printf '%s\n' retained-edit > edit.txt`, "fixture", pkg, hook, managedGoSource, managedRustSource)
	output := h.run(timings.boot, "up", "--yes")
	for _, want := range []string{`node 24 at "frontend"`, `python 3.13 at "backend"`, `go 1.27 at "service"`, `rust 1.99 at "native"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing combined plan %q: %s", want, output)
		}
	}
	h.run(timings.command, "exec", "--cwd", "frontend", "node", "-e", `if(!require('is-number')(42) || !require('fs').existsSync('installed.txt')) process.exit(1)`)
	h.run(timings.command, "exec", "--cwd", "backend", "python", "-c", `import idna; assert idna.__version__ == '3.10'`)
	h.run(timings.boot, "exec", "--cwd", "service", "env", "GOPROXY=off", "go", "run", "-mod=readonly", ".")
	h.run(timings.boot, "exec", "--cwd", "native", "cargo", "run", "--offline", "--locked", "--quiet")
	h.run(timings.command, "exec", "--cwd", "native", "rustfmt", "--version")
	h.run(timings.boot, "exec", "--cwd", "native", "cargo", "clippy", "--offline", "--locked", "--quiet")
	if out := h.run(timings.command, "up", "--yes"); !strings.Contains(out, "unchanged") {
		t.Fatalf("setup repeated: %s", out)
	}
	h.runBlockedNetwork(timings.command, "exec", "bash", "-lc", blockedNetworkProbe)
	// Nested manifest changes invalidate readiness; generated locks stay stable.
	h.run(timings.command, "exec", "sh", "-c", `printf '\n# changed\n' >> backend/requirements.txt`)
	if out := h.run(timings.boot, "up", "--yes"); strings.Contains(out, "unchanged") {
		t.Fatalf("nested change ignored: %s", out)
	}
	h.run(timings.stop, "stop")
	h.run(timings.boot, "exec", "--cwd", "frontend", "node", "setup.cjs")
	h.run(timings.boot, "rebuild", "--force", "--yes")
	if out := managedCommandStdout(t, h, timings.command, "exec", "cat", "edit.txt"); strings.TrimSpace(out) != "retained-edit" {
		t.Fatalf("edit lost: %q", out)
	}
	h.run(timings.command, "exec", "--cwd", "frontend", "node", "setup.cjs")
	h.run(timings.boot, "exec", "--cwd", "service", "env", "GOPROXY=off", "go", "run", "-mod=readonly", ".")
	h.run(timings.boot, "exec", "--cwd", "native", "cargo", "run", "--offline", "--locked", "--quiet")
}
