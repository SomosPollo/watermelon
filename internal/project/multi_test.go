package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestDetectAllLanguagesAndNestedProjects(t *testing.T) {
	for _, nested := range []bool{false, true} {
		files := map[string][]byte{}
		for name, body := range map[string]string{"package.json": `{}`, "requirements.txt": "idna==3.10", "go.mod": "module example.com/app\ngo 1.25.0\n", "Cargo.toml": "[package]\nname='app'\nversion='0.1.0'\n"} {
			if nested {
				name = strings.Split(name, ".")[0] + "/" + name
			}
			files[name] = []byte(body)
		}
		plan, err := Detect(files, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if plan.Profile != "multi" || len(plan.Projects) != 4 || len(plan.Setup().Runtimes) != 4 || !strings.HasPrefix(plan.Image, "watermelon-project:") {
			t.Fatalf("incomplete composition: %+v", plan)
		}
		order := []string{"go", "rust", "python", "node"}
		for i, p := range plan.Projects {
			if p.Profile != order[i] || len(p.Commands) == 0 {
				t.Fatalf("unsafe install order: %+v", plan.Projects)
			}
		}
		again, err := DetectConfigured(files, plan.Setup())
		if err != nil || again.Fingerprint != plan.Fingerprint {
			t.Fatalf("unstable configured plan: %v", err)
		}
		files["requirements.txt"] = []byte("idna==3.11")
		changed, err := DetectConfigured(files, plan.Setup())
		if err != nil || changed.Fingerprint == plan.Fingerprint {
			t.Fatalf("nested metadata ignored: %v", err)
		}
	}
}

func TestCommonRuntimeSelection(t *testing.T) {
	for _, tt := range []struct {
		name          string
		files         map[string][]byte
		runtime, want string
		fail          bool
	}{
		{name: "common lower node", files: map[string][]byte{"a/package.json": []byte(`{"engines":{"node":"22"}}`), "b/package.json": []byte(`{}`)}, want: "22"},
		{name: "incompatible engines", files: map[string][]byte{"a/package.json": []byte(`{"engines":{"node":"22"}}`), "b/package.json": []byte(`{"engines":{"node":"24"}}`)}, fail: true},
		{name: "incompatible pins", files: map[string][]byte{"a/package.json": []byte(`{}`), "a/.nvmrc": []byte("22"), "b/package.json": []byte(`{}`), "b/.nvmrc": []byte("24")}, fail: true},
		{name: "explicit override", files: map[string][]byte{"a/package.json": []byte(`{}`), "a/.nvmrc": []byte("22"), "b/package.json": []byte(`{}`), "b/.nvmrc": []byte("24")}, runtime: "node=24", want: "24"},
		{name: "ancestor pin", files: map[string][]byte{".nvmrc": []byte("22"), "a/package.json": []byte(`{}`)}, want: "22"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Detect(tt.files, "", tt.runtime)
			if (err != nil) != tt.fail {
				t.Fatalf("plan=%+v err=%v", p, err)
			}
			if err == nil && p.Setup().Runtimes["node"] != tt.want {
				t.Fatalf("runtime=%+v", p.Setup())
			}
		})
	}
}

func TestWorkspacesIncludeMemberConstraintsAndFingerprints(t *testing.T) {
	for _, tt := range []struct {
		profile string
		files   map[string][]byte
		member  string
	}{
		{"node", map[string][]byte{"package.json": []byte(`{"workspaces":["packages/**"]}`), "packages/group/app/package.json": []byte(`{"engines":{"node":"22"}}`)}, "packages/group/app/package.json"},
		{"go", map[string][]byte{"go.work": []byte("go 1.25.0\nuse ./app\n"), "app/go.mod": []byte("module example.com/app\ngo 1.25.0\n")}, "app/go.mod"},
		{"rust", map[string][]byte{"Cargo.toml": []byte("[workspace]\nmembers=['app']\n[workspace.package]\nrust-version='1.85'\n"), "app/Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\nrust-version.workspace=true\n")}, "app/Cargo.toml"},
	} {
		t.Run(tt.profile, func(t *testing.T) {
			p, err := Detect(tt.files, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Projects) != 2 {
				t.Fatalf("missing member: %+v", p)
			}
			for _, part := range p.Parts() {
				if part.Directory != "." && (part.Workspace != "." || len(part.Commands) != 0 || part.Requirement == "") {
					t.Fatalf("member not covered/checked: %+v", part)
				}
			}
			tt.files[tt.member] = append(tt.files[tt.member], '\n')
			changed, err := DetectConfigured(tt.files, p.Setup())
			if err != nil || changed.Fingerprint == p.Fingerprint {
				t.Fatalf("member change ignored: %v", err)
			}
		})
	}
}

func TestConfiguredDetectionAddsLanguagesUnlessRestricted(t *testing.T) {
	files := map[string][]byte{"package.json": []byte(`{}`), "backend/requirements.txt": nil}
	p, err := DetectConfigured(files, config.SetupConfig{Profile: "node", Runtime: "22"})
	if err != nil || len(p.Projects) != 2 || p.Setup().Runtimes["node"] != "22" {
		t.Fatalf("expansion=%+v %v", p, err)
	}
	p, err = DetectConfigured(files, config.SetupConfig{Profile: "node", Runtime: "22", Only: "node"})
	if err != nil || p.Profile != "node" || p.Only != "node" {
		t.Fatalf("restriction lost: %+v %v", p, err)
	}
	for _, runtime := range []string{"node=", "node=22,node=24", "go=1.27;bad", "other=2", "node=22,python"} {
		if _, err := RuntimeOverrides(runtime); err == nil {
			t.Errorf("accepted invalid override %q", runtime)
		}
	}
	if _, err := Detect(files, "", "rust=1.99"); err == nil {
		t.Fatal("undetected override ignored")
	}
}

func TestNestedDiscoverySkipsGeneratedAndSymlinkDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"frontend", "backend", "node_modules/pkg", ".git/objects", "target/generated"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "package.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "requirements.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	files, err := ReadLocal(root)
	if err != nil || len(files) != 2 {
		t.Fatalf("unexpected traversal: %v %v", files, err)
	}
	deep := filepath.Join(root, strings.Repeat("d/", MaxDepth+1))
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocal(root); err == nil {
		t.Fatal("depth limit ignored")
	}
}

func TestConfiguredDetectionDropsRemovedLanguages(t *testing.T) {
	files := map[string][]byte{"package.json": []byte(`{}`), "requirements.txt": nil}
	old, err := Detect(files, "", "")
	if err != nil {
		t.Fatal(err)
	}
	delete(files, "package.json")
	next, err := DetectConfigured(files, old.Setup())
	if err != nil || next.Profile != "python" {
		t.Fatalf("cannot remove language: %+v %v", next, err)
	}
}

func TestGoWorkspaceOverridesMemberToolchainSuggestion(t *testing.T) {
	files := map[string][]byte{"go.work": []byte("go 1.26.0\ntoolchain go1.26.1\nuse ./app\n"), "app/go.mod": []byte("module example.com/app\ngo 1.25.0\ntoolchain go1.28.0\n")}
	p, err := Detect(files, "", "")
	if err != nil || p.Setup().Runtimes["go"] != "1.26.1" {
		t.Fatalf("workspace toolchain did not win: %+v %v", p, err)
	}
	files["app/go.mod"] = []byte("module example.com/app\ngo 1.28.0\n")
	if _, err := Detect(files, "", ""); err == nil {
		t.Fatal("workspace ignored newer member minimum")
	}
}

func TestUpRuntimeOverrideRetainsOtherApprovedLanguages(t *testing.T) {
	files := map[string][]byte{"package.json": []byte(`{}`), "requirements.txt": nil}
	old, err := Detect(files, "", "python=3.12")
	if err != nil {
		t.Fatal(err)
	}
	next, err := DetectConfiguredWithRuntime(files, old.Setup(), "node=22")
	if err != nil {
		t.Fatal(err)
	}
	if next.Setup().Runtimes["node"] != "22" || next.Setup().Runtimes["python"] != "3.12" {
		t.Fatalf("lost version choices: %+v", next.Setup())
	}
	if _, err := DetectConfiguredWithRuntime(files, old.Setup(), "22"); err == nil {
		t.Fatal("ambiguous override accepted")
	}
	if _, err := DetectConfiguredWithRuntime(files, old.Setup(), "rust=1.99"); err == nil {
		t.Fatal("undetected override accepted")
	}
}
