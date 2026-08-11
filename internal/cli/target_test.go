package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

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
			want: "reading config",
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
	}
}
