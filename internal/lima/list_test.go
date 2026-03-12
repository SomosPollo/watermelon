package lima

import (
	"testing"
)

func TestListWatermelonVMs(t *testing.T) {
	jsonOutput := `{"name":"watermelon-proj-a1b2c3d4","status":"Running","dir":"/tmp/a"}
{"name":"watermelon-proj2-e5f6g7h8","status":"Stopped","dir":"/tmp/b"}
{"name":"default","status":"Running","dir":"/tmp/c"}`

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
