package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/project"
)

func TestCloneReviewCustomizesDraftAndRequiresFinalApproval(t *testing.T) {
	dir, cfg, files, _ := projectSetupFixture(t)
	original, err := readProjectConfigBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := project.DetectConfigured(*files, cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "approve"}[accept], func(t *testing.T) {
			cmd, output := setupTestCommand()
			answer := "n"
			if accept {
				answer = "y"
			}
			cmd.SetIn(strings.NewReader("c\nnode=22\n8GB\n20GB\n4\n5173,8000\napi.example.com:443\n" + answer + "\n"))
			review, err := reviewCloneSetup(cmd, dir, original, *files, *cfg, plan)
			if !accept {
				if err == nil {
					t.Fatal("unapproved customization succeeded")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got := review.config
				if got.Setup.Runtime != "22" || got.Resources.Memory != "8GB" || got.Resources.Disk != "20GB" || got.Resources.CPUs != 4 || len(got.Ports.Forward) != 2 || len(got.Network.Allow) != 2 {
					t.Fatalf("customization lost: %+v", got)
				}
				if !strings.Contains(output.String(), "Resources: 8GB memory, 4 CPUs, 20GB VM disk") || !strings.Contains(output.String(), "node:22-bookworm") {
					t.Fatalf("customized plan was not reviewed: %s", output)
				}
				parsed, err := config.Parse(review.document)
				if err != nil || parsed.Setup.Runtime != "22" || parsed.Resources.Memory != "8GB" {
					t.Fatalf("draft differs from plan: %s %v", review.document, err)
				}
			}
			active, _ := readProjectConfigBytes(dir)
			if !bytes.Equal(active, original) || cfg.Setup.Runtime != "24" || cfg.Resources.Memory != "4GB" {
				t.Fatal("review mutated active policy")
			}
		})
	}
}

func TestCloneReviewEditorPreservesCommentsAndCustomSettings(t *testing.T) {
	dir, cfg, files, _ := projectSetupFixture(t)
	original, _ := readProjectConfigBytes(dir)
	plan, _ := project.DetectConfigured(*files, cfg.Setup)
	stubConfigEditor(t, func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, append([]byte("# Team sandbox settings\n"), bytes.Replace(data, []byte(`memory = "4GB"`), []byte(`memory = "8GB" # large builds`), 1)...), 0600)
	})
	cmd, _ := setupTestCommand()
	cmd.SetIn(strings.NewReader("e\ny\n"))
	review, err := reviewCloneSetup(cmd, dir, original, *files, *cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	if review.config.Resources.Memory != "8GB" || !bytes.Contains(review.document, []byte("# Team sandbox settings")) || !bytes.Contains(review.document, []byte("# large builds")) {
		t.Fatalf("editor changes lost: %s", review.document)
	}
	if err := replaceProjectConfigDocument(dir, original, review.document); err != nil {
		t.Fatal(err)
	}
	changedPlan, err := project.DetectConfiguredWithRuntime(*files, review.config.Setup, "node=22")
	if err != nil {
		t.Fatal(err)
	}
	updated := proposeProfile(&review.config, changedPlan)
	if err := writeProjectConfig(dir, &updated, false); err != nil {
		t.Fatal(err)
	}
	data, _ := readProjectConfigBytes(dir)
	if !bytes.Contains(data, []byte("# Team sandbox settings")) || !bytes.Contains(data, []byte(`memory = "8GB" # large builds`)) {
		t.Fatalf("subsequent update lost comments or settings: %s", data)
	}
}

func TestCloneReviewInvalidInputDoesNotChangePlan(t *testing.T) {
	dir, cfg, files, _ := projectSetupFixture(t)
	original, _ := readProjectConfigBytes(dir)
	plan, _ := project.DetectConfigured(*files, cfg.Setup)
	cmd, output := setupTestCommand()
	cmd.SetIn(strings.NewReader("c\nnode=invalid\ny\n"))
	review, err := reviewCloneSetup(cmd, dir, original, *files, *cfg, plan)
	if err != nil || review.config.Setup.Runtime != cfg.Setup.Runtime || !strings.Contains(output.String(), "Customization was not applied") {
		t.Fatalf("invalid customization changed plan: %+v %v %s", review.config, err, output)
	}
}

func TestCloneSetupCanBeDeferredAndYesSkipsCustomization(t *testing.T) {
	for _, deferSetup := range []bool{false, true} {
		t.Run(map[bool]string{false: "yes", true: "later"}[deferSetup], func(t *testing.T) {
			dir, _, _, calls := projectSetupFixture(t)
			original, _ := readProjectConfigBytes(dir)
			cmd, out := setupTestCommand()
			cmd.SetIn(strings.NewReader("s\n"))
			if err := runUp(cmd, dir, "", setupOptions{Customize: true, Yes: !deferSetup}); err != nil {
				t.Fatal(err)
			}
			if deferSetup {
				state, err := loadWorkspaceState(dir)
				if err != nil || state.Stage != "source cloned (setup pending)" || state.ApprovedConfig != "" || *calls != 0 {
					t.Fatalf("deferred setup executed: %+v %v calls=%d", state, err, *calls)
				}
			} else if *calls != 1 || strings.Contains(out.String(), "c=customize") {
				t.Fatalf("--yes prompted or skipped installation: calls=%d %s", *calls, out)
			}
			active, _ := readProjectConfigBytes(dir)
			if !bytes.Equal(active, original) {
				t.Fatal("unexpected config rewrite")
			}
		})
	}
}

func TestCloneCustomizationsAreAppliedBeforeDependencies(t *testing.T) {
	dir, _, _, calls := projectSetupFixture(t)
	oldDestroyStatus, oldStop, oldDelete, oldRelease := destroyGetStatus, destroyStop, destroyDelete, releaseWorkspaceDisk
	t.Cleanup(func() {
		destroyGetStatus, destroyStop, destroyDelete, releaseWorkspaceDisk = oldDestroyStatus, oldStop, oldDelete, oldRelease
	})
	status := lima.StatusRunning
	cliGetVMStatus = func(string) lima.VMStatus { return status }
	destroyGetStatus = func(string) lima.VMStatus { return status }
	destroyStop = func(string) error { status = lima.StatusStopped; return nil }
	destroyDelete = func(string) error { status = lima.StatusNotFound; return nil }
	releaseWorkspaceDisk = func(string, string) error { return nil }
	setupStartVM = func(options runOptions) error {
		if status != lima.StatusNotFound {
			return nil
		}
		cfg, err := loadProjectConfig(dir)
		if err != nil {
			return err
		}
		if cfg.Resources.Memory != "8GB" || len(cfg.Ports.Forward) != 1 || cfg.Ports.Forward[0] != 5173 {
			t.Fatalf("VM started without reviewed settings: %+v", cfg)
		}
		if _, err := reserveNamedVMIdentity(dir, options.ExpectedVMName, cfg); err != nil {
			return err
		}
		if err := saveAppliedPolicySnapshotForVM(dir, options.ExpectedVMName, cfg); err != nil {
			return err
		}
		status = lima.StatusRunning
		return nil
	}
	cliInstanceMount = func(vm, target string) (lima.LimaMount, error) {
		instance, err := loadOwnedNamedVMIdentity(dir, vm)
		return lima.LimaMount{Location: instance.Paths.BootstrapDir, MountPoint: target}, err
	}
	cmd, _ := setupTestCommand()
	cmd.SetIn(strings.NewReader("c\n\n8GB\n\n\n5173\n\ny\n"))
	if err := runUp(cmd, dir, "", setupOptions{Customize: true}); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("dependency installations=%d", *calls)
	}
	state, err := loadWorkspaceState(dir)
	if err != nil || state.Stage != "ready" {
		t.Fatalf("customized setup not ready: %+v %v", state, err)
	}
}

func TestCloneReviewCancellationAndNoninteractiveInput(t *testing.T) {
	dir, cfg, files, _ := projectSetupFixture(t)
	original, _ := readProjectConfigBytes(dir)
	plan, _ := project.DetectConfigured(*files, cfg.Setup)
	cmd, _ := setupTestCommand()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	if _, err := reviewCloneSetup(cmd, dir, original, *files, *cfg, plan); err == nil {
		t.Fatal("canceled review succeeded")
	}
	pipe, err := os.Open(filepath.Join(dir, projectConfigName))
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	cmd.SetContext(context.Background())
	cmd.SetIn(pipe)
	if _, err := reviewCloneSetup(cmd, dir, original, *files, *cfg, plan); err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("noninteractive approval accepted: %v", err)
	}
}

func TestUpRejectsOversizedPolicyBeforeReplacingVM(t *testing.T) {
	dir, _, files, calls := projectSetupFixture(t)
	path := filepath.Join(dir, projectConfigName)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// New project metadata needs more space than the almost-full source file.
	original = append(original, []byte("#"+strings.Repeat("x", maxProjectConfigSize-len(original)-2)+"\n")...)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	(*files)["backend/requirements.txt"] = []byte("idna==3.10\n")
	old := destroyGetStatus
	t.Cleanup(func() { destroyGetStatus = old })
	destroyGetStatus = func(string) lima.VMStatus {
		t.Fatal("oversized configuration reached VM replacement")
		return lima.StatusUnknown
	}
	cmd, _ := setupTestCommand()
	err = runUp(cmd, dir, "", setupOptions{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "byte limit") || *calls != 0 {
		t.Fatalf("oversized plan was not rejected before execution: err=%v calls=%d", err, *calls)
	}
	active, _ := os.ReadFile(path)
	if !bytes.Equal(active, original) {
		t.Fatal("oversized plan replaced the active policy")
	}
}
