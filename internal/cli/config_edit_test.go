package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/spf13/cobra"
)

func stubConfigEditor(t *testing.T, edit func(string) error) {
	t.Helper()
	old := runConfigEditor
	t.Cleanup(func() { runConfigEditor = old })
	runConfigEditor = func(_ *cobra.Command, path string) error { return edit(path) }
}

func executeConfigEdit(t *testing.T) (string, error) {
	t.Helper()
	cmd := NewConfigCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"edit"})
	err := cmd.Execute()
	return output.String(), err
}

func TestConfigEditValidatesDraftAndDiscoversRootWithoutVMAccess(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	forbidValidationVMAccess(t)
	original := "# My sandbox\n[resources]\nmemory = '8GB' # for builds\ncpus = 2\n"
	path := filepath.Join(dir, projectConfigName)
	if err := os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "src")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	stubConfigEditor(t, func(draft string) error {
		active, _ := os.ReadFile(path)
		if string(active) != original || draft == path {
			t.Fatal("editor received the active policy")
		}
		return os.WriteFile(draft, []byte(strings.Replace(original, "cpus = 2", "cpus = 4", 1)), 0600)
	})
	out, err := executeConfigEdit(t)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != strings.Replace(original, "cpus = 2", "cpus = 4", 1) || !strings.Contains(out, "watermelon rebuild") {
		t.Fatalf("unexpected edit: %s\n%s", got, out)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: %o", info.Mode().Perm())
	}
}

func TestConfigEditRejectsBadDraftsAndRetainsEditorWork(t *testing.T) {
	for name, draft := range map[string]string{
		"syntax":                   "[resources",
		"unknown key":              "[resources]\nmemmory = '8GB'\n",
		"invalid resource":         "[resources]\ncpus = 0\n",
		"missing provision script": "[provision]\nscripts = ['missing.sh']\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			forbidValidationVMAccess(t)
			original := []byte("# Keep my policy\n")
			path := filepath.Join(dir, projectConfigName)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			var editedPath string
			stubConfigEditor(t, func(path string) error {
				editedPath = path
				return os.WriteFile(path, []byte(draft), 0600)
			})
			_, err := executeConfigEdit(t)
			if err == nil || !strings.Contains(err.Error(), "draft retained") {
				t.Fatalf("bad draft accepted: %v", err)
			}
			active, _ := os.ReadFile(path)
			recovered, _ := os.ReadFile(editedPath)
			if !bytes.Equal(active, original) || string(recovered) != draft {
				t.Fatal("active policy changed or editor work lost")
			}
		})
	}
}

func TestConfigEditSupportsRepairAndHostOnlyChanges(t *testing.T) {
	for _, original := range []string{"[broken", "# comment\n[ide]\ncommand='code'\n"} {
		t.Run(original, func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			forbidValidationVMAccess(t)
			path := filepath.Join(dir, projectConfigName)
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			stubConfigEditor(t, func(path string) error {
				return os.WriteFile(path, []byte("# comment\n[ide]\ncommand='cursor'\n"), 0600)
			})
			out, err := executeConfigEdit(t)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(original, "[ide]") && !strings.Contains(out, "No VM rebuild required") {
				t.Fatalf("host-only edit requests rebuild: %s", out)
			}
		})
	}
}

func TestConfigEditRefusesConcurrentChanges(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	forbidValidationVMAccess(t)
	path := filepath.Join(dir, projectConfigName)
	if err := os.WriteFile(path, []byte("# original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	stubConfigEditor(t, func(draft string) error {
		if err := os.WriteFile(path, []byte("# another writer\n"), 0600); err != nil {
			return err
		}
		return os.WriteFile(draft, []byte("# my edit\n"), 0600)
	})
	_, err := executeConfigEdit(t)
	if err == nil || !strings.Contains(err.Error(), "changed while") || !strings.Contains(err.Error(), "draft retained") {
		t.Fatalf("concurrent write was overwritten: %v", err)
	}
	active, _ := os.ReadFile(path)
	if string(active) != "# another writer\n" {
		t.Fatal("concurrent edit lost")
	}
	drafts, _ := filepath.Glob(filepath.Join(dir, ".watermelon-edit-*", projectConfigName))
	if len(drafts) != 1 {
		t.Fatalf("expected one recovery draft, got %v", drafts)
	}
	recovered, _ := os.ReadFile(drafts[0])
	if string(recovered) != "# my edit\n" {
		t.Fatal("editor changes lost after conflict")
	}
}

func TestConfigEditEditorFailureAndSymlinkLeavePolicyUntouched(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "editor failure", true: "symlink draft"}[symlink], func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			forbidValidationVMAccess(t)
			original := []byte("# original\n")
			path := filepath.Join(dir, projectConfigName)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			stubConfigEditor(t, func(draft string) error {
				if !symlink {
					return errors.New("editor exited unsuccessfully")
				}
				if err := os.Remove(draft); err != nil {
					return err
				}
				return os.Symlink(path, draft)
			})
			if _, err := executeConfigEdit(t); err == nil {
				t.Fatal("unsafe edit succeeded")
			}
			active, _ := os.ReadFile(path)
			if !bytes.Equal(active, original) {
				t.Fatal("active config changed")
			}
		})
	}
}

func TestConfigEditCannotRetargetManagedWorkspace(t *testing.T) {
	dir, _, _, _ := projectSetupFixture(t)
	forbidValidationVMAccess(t)
	t.Chdir(dir)
	stubConfigEditor(t, func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		cfg, err := config.Parse(data)
		if err != nil {
			return err
		}
		cfg.Workspace = config.WorkspaceConfig{}
		cfg.VM.MountProject = new(true)
		cfg.VM.Workdir = ""
		updated, err := config.UpdateDocument(data, cfg)
		if err != nil {
			return err
		}
		return os.WriteFile(path, updated, 0600)
	})
	if _, err := executeConfigEdit(t); err == nil || !strings.Contains(err.Error(), "generated identity") {
		t.Fatalf("workspace identity changed: %v", err)
	}
}

func TestConfigEditorUsesVisualWithQuotedPathAndArguments(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	forbidValidationVMAccess(t)
	path := filepath.Join(dir, projectConfigName)
	if err := os.WriteFile(path, []byte("# original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(t.TempDir(), "my editor")
	script := "#!/bin/sh\n[ \"$1\" = '--wait' ] || exit 4\nprintf '# edited through VISUAL\\n' > \"$2\"\n"
	if err := os.WriteFile(editor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", strconv.Quote(editor)+" --wait")
	t.Setenv("EDITOR", "missing-editor")
	t.Chdir(dir)
	out, err := executeConfigEdit(t)
	if err != nil || !strings.Contains(out, "No VM rebuild required") {
		t.Fatalf("editor failed: %v %s", err, out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "# edited through VISUAL\n" {
		t.Fatalf("editor did not update draft: %s", data)
	}
}

func TestConfigEditAskChangesExplainForegroundRecreation(t *testing.T) {
	dir, _, _ := setupNamedVMIdentityTest(t)
	forbidValidationVMAccess(t)
	path := filepath.Join(dir, projectConfigName)
	original := "[security]\nenforcement = 'ask'\n[resources]\ncpus = 1\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	stubConfigEditor(t, func(path string) error {
		return os.WriteFile(path, []byte(strings.Replace(original, "cpus = 1", "cpus = 2", 1)), 0600)
	})
	out, err := executeConfigEdit(t)
	if err != nil || !strings.Contains(out, "foreground recreation") || !strings.Contains(out, "watermelon status") {
		t.Fatalf("ask-mode instructions missing: %v %s", err, out)
	}
}
