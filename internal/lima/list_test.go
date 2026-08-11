package lima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListWatermelonVMs(t *testing.T) {
	jsonOutput := limaStringRecord("watermelon-proj-a1b2c3d4", "Running", "/tmp/a") + "\n" +
		limaStringRecord("watermelon-proj2-e5f6g7h8", "Stopped", "/tmp/b") + "\n" +
		limaStringRecord("default", "Running", "/tmp/c")

	withFakeExec(t, jsonOutput, 0)

	vms, err := ListWatermelonVMs()
	if err != nil {
		t.Fatalf("ListWatermelonVMs() error = %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("ListWatermelonVMs() returned %d VMs, want 2", len(vms))
	}
	if vms[0].Name != "watermelon-proj-a1b2c3d4" {
		t.Errorf("vms[0].Name = %q, want %q", vms[0].Name, "watermelon-proj-a1b2c3d4")
	}
	if vms[0].Status != "Running" {
		t.Errorf("vms[0].Status = %q, want %q", vms[0].Status, "Running")
	}
	if vms[1].Name != "watermelon-proj2-e5f6g7h8" {
		t.Errorf("vms[1].Name = %q, want %q", vms[1].Name, "watermelon-proj2-e5f6g7h8")
	}
}

func TestListAllVMsIncludesCustomNames(t *testing.T) {
	jsonOutput := limaStringRecord("watermelon-derived-a1b2c3d4", "Running", "/tmp/a") + "\n" +
		limaStringRecord("team-dev", "Stopped", "/tmp/b")
	withFakeExec(t, jsonOutput, 0)

	vms, err := ListAllVMs()
	if err != nil {
		t.Fatalf("ListAllVMs() error = %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("ListAllVMs() returned %d VMs, want 2", len(vms))
	}
	if vms[1].Name != "team-dev" || vms[1].Status != "Stopped" {
		t.Fatalf("ListAllVMs()[1] = %+v, want custom team-dev instance", vms[1])
	}
	if vms[1].ProjectDir != "" {
		t.Fatalf("unregistered custom VM project = %q, want no inferred ownership", vms[1].ProjectDir)
	}
}

func TestListAllVMsUsesNarrowTemplateOutput(t *testing.T) {
	var captured []string
	old := execCommand
	execCommand = fakeExecCommandCapture(&captured, "")
	t.Cleanup(func() { execCommand = old })

	if _, err := ListAllVMs(); err != nil {
		t.Fatalf("ListAllVMs() error = %v", err)
	}
	want := "limactl list --format " + vmListFormat
	if len(captured) != 1 || captured[0] != want {
		t.Fatalf("ListAllVMs() commands = %q, want [%q]", captured, want)
	}
}

func TestListAllVMsParsesRecordLargerThanScannerLimit(t *testing.T) {
	dir := "/tmp/" + strings.Repeat("d", 70*1024)
	withFakeExec(t, limaStringRecord("custom-dev", "Running", dir), 0)

	vms, err := ListAllVMs()
	if err != nil {
		t.Fatalf("ListAllVMs() error = %v", err)
	}
	if len(vms) != 1 || vms[0].Dir != dir {
		t.Fatalf("ListAllVMs() = %+v, want one VM with a %d-byte directory", vms, len(dir))
	}
}

func TestParseProjectDirFromLimaConfig(t *testing.T) {
	data := `mounts:
  - location: "/Users/dev/my app"
    mountPoint: /project
  - location: "/Users/dev/.gitconfig"
    mountPoint: /home/dev/.gitconfig
`

	got := parseProjectDirFromLimaConfig(data)
	if got != "/Users/dev/my app" {
		t.Errorf("parseProjectDirFromLimaConfig() = %q, want %q", got, "/Users/dev/my app")
	}
}

func TestProjectDirFromInstanceDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lima.yaml"), []byte(`mounts:
  - location: /tmp/project
    mountPoint: /project
`), 0644); err != nil {
		t.Fatal(err)
	}

	got := projectDirFromInstanceDir(dir)
	if got != "/tmp/project" {
		t.Errorf("projectDirFromInstanceDir() = %q, want %q", got, "/tmp/project")
	}
}

func TestListWatermelonVMsEmpty(t *testing.T) {
	withFakeExec(t, "", 0)

	vms, err := ListWatermelonVMs()
	if err != nil {
		t.Fatalf("ListWatermelonVMs() error = %v", err)
	}
	if vms != nil {
		t.Errorf("ListWatermelonVMs() = %v, want nil", vms)
	}
}

func TestListWatermelonVMsError(t *testing.T) {
	withFakeExec(t, "", 1)

	_, err := ListWatermelonVMs()
	if err == nil {
		t.Error("ListWatermelonVMs() expected error when limactl fails")
	}
}
