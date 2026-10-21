package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestManagedLogsStayOnBoundGuestDisk(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	t.Chdir(dir)
	oldOutput := managedLogOutput
	t.Cleanup(func() { managedLogOutput = oldOutput })
	calls := 0
	managedLogOutput = func(ctx context.Context, name, cwd string, args []string, limit int64) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("log read has no deadline")
		}
		if name != derivedVMName(dir) || cwd != "/" || limit != 4<<20 || args[0] != "/usr/bin/python3" || args[1] != "-I" || args[4] != "/mnt/lima-"+cfg.Workspace.Disk+"/logs/logs.log" {
			t.Fatalf("unbound log read: %s %s %v", name, cwd, args)
		}
		return []byte("blocked-policy-event\n"), nil
	}
	cmd := NewLogsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "blocked-policy-event\n" || calls != 1 {
		t.Fatalf("logs=%q calls=%d", out.String(), calls)
	}
	cliGetVMStatus = func(string) lima.VMStatus { return lima.StatusStopped }
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "workspace disk") || calls != 1 {
		t.Fatalf("stopped logging unexpectedly attached: %v calls=%d", err, calls)
	}
}

func TestManagedLogReaderBoundsAndConfinesReadsAndClear(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "logs.log")
	read := func(mode string) ([]byte, error) {
		return exec.Command(python, "-I", "-c", managedLogReader, file, mode).CombinedOutput()
	}
	data := append(bytes.Repeat([]byte("first-line\n"), 500000), []byte("final-line\n")...)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := read("read")
	if err != nil || len(output) > 4<<20 || !bytes.HasSuffix(output, []byte("final-line\n")) {
		t.Fatalf("unbounded or missing logs: %d %v", len(output), err)
	}
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := read("clear"); err != nil {
		t.Fatalf("clear: %v %s", err, output)
	}
	after, err := os.Stat(file)
	if err != nil || after.Size() != 0 || !os.SameFile(before, after) {
		t.Fatal("clear replaced the log inode")
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []func(string, string) error{os.Symlink, os.Link} {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := link(victim, file); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"read", "clear"} {
			if _, err := read(mode); err == nil {
				t.Fatalf("%s followed a link", mode)
			}
		}
		if data, err := os.ReadFile(victim); err != nil || string(data) != "retain" {
			t.Fatal("guest log operation changed another file")
		}
	}
}
