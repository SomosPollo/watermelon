package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDetectProjects(t *testing.T) {
	tests := []struct {
		name                                                    string
		files                                                   map[string][]byte
		profile, runtime, wantProfile, wantRuntime, wantCommand string
		wantError                                               bool
	}{
		{name: "npm lock", files: map[string][]byte{"package.json": []byte(`{"engines":{"node":">=22"}}`), "package-lock.json": []byte(`{}`)}, wantProfile: "node", wantRuntime: "24", wantCommand: "ci"},
		{name: "node version", files: map[string][]byte{"package.json": []byte(`{}`), ".nvmrc": []byte("v22\n")}, wantProfile: "node", wantRuntime: "22", wantCommand: "install"},
		{name: "python requirements", files: map[string][]byte{"requirements.txt": []byte("requests==2.32.3\n")}, wantProfile: "python", wantRuntime: "3.13", wantCommand: "venv"},
		{name: "python constraint", files: map[string][]byte{"pyproject.toml": []byte("[project]\nrequires-python = '>=3.14'\n")}, wantProfile: "python", wantRuntime: "3.14", wantCommand: "venv"},
		{name: "mixed explicit", files: map[string][]byte{"package.json": []byte(`{}`), "requirements.txt": nil}, profile: "python", wantProfile: "python", wantRuntime: "3.13", wantCommand: "venv"},
		{name: "yarn selected", files: map[string][]byte{"package.json": []byte(`{"packageManager":"yarn@4.0.0"}`)}, wantProfile: "node", wantRuntime: "24", wantCommand: "install"},
		{name: "pnpm lock", files: map[string][]byte{"package.json": []byte(`{}`), "pnpm-lock.yaml": nil}, wantProfile: "node", wantRuntime: "24", wantCommand: "install"},
		{name: "uv lock", files: map[string][]byte{"pyproject.toml": nil, "uv.lock": nil}, wantProfile: "python", wantRuntime: "3.13", wantCommand: "sync"},
		{name: "bad json", files: map[string][]byte{"package.json": []byte(`evil`)}, wantError: true},
		{name: "runtime injection", files: map[string][]byte{"package.json": []byte(`{}`), ".nvmrc": []byte("24; touch /tmp/should-not-exist")}, wantError: true},
		{name: "empty", files: map[string][]byte{}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Detect(tt.files, tt.profile, tt.runtime)
			if (err != nil) != tt.wantError {
				t.Fatalf("plan=%+v error=%v", plan, err)
			}
			if err != nil {
				return
			}
			if plan.Profile != tt.wantProfile || plan.Runtime != tt.wantRuntime {
				t.Fatalf("plan=%+v", plan)
			}
			found := false
			for _, arg := range plan.Commands[0] {
				found = found || arg == tt.wantCommand
			}
			if !found {
				t.Fatalf("commands=%v", plan.Commands)
			}
			if len(plan.Fingerprint) != 64 {
				t.Fatal("missing fingerprint")
			}
		})
	}
}

func TestFingerprintChangesOnlyWithMetadata(t *testing.T) {
	files := map[string][]byte{"package.json": []byte(`{}`)}
	first, err := Detect(files, "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Detect(files, "", "")
	if err != nil || first.Fingerprint != second.Fingerprint {
		t.Fatal("unstable fingerprint")
	}
	files["package-lock.json"] = []byte(`{"lockfileVersion":3}`)
	third, err := Detect(files, "", "")
	if err != nil || first.Fingerprint == third.Fingerprint {
		t.Fatal("lockfile not included")
	}
}

func TestReadLocalRejectsSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "package.json")
			switch kind {
			case "symlink":
				victim := filepath.Join(t.TempDir(), "secret")
				if err := os.WriteFile(victim, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(MaxFileSize + 1); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			if _, err := ReadLocal(dir); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}

func TestInspectDoesNotExecuteScriptsOrReadHostPolicy(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "executed")
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"preinstall":"touch executed"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("[security]\nenforcement='log'"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := ReadLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[".watermelon.toml"]; ok {
		t.Fatal("repository policy was inspected")
	}
	if _, err := Detect(files, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("project code executed")
	}
}

func TestMatchesRuntime(t *testing.T) {
	for _, tt := range []struct {
		actual, constraint string
		want               bool
	}{
		{"24.1.2", ">=22 <25", true}, {"22.0.0", ">=24", false}, {"3.13.8", ">=3.12,<3.14", true}, {"22.12.0", "^22.0.0 || ^24.0.0", true}, {"24.1.0", "22.x", false}, {"24.0.1", "24", true}, {"3.14.0", "~=3.13", true},
		{"3.3.6", ">= 3.2, < 3.4", true}, {"0.3.0", "^0.2.1", false}, {"0.2.9", "^0.2.1", true}, {"0.0.2", "^0.0.1", false},
	} {
		ok, err := MatchesRuntime(tt.actual, tt.constraint)
		if err != nil || ok != tt.want {
			t.Errorf("%s %s: %t %v", tt.actual, tt.constraint, ok, err)
		}
	}
	if _, err := MatchesRuntime("24", "arbitrary shell text"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("unknown constraint guessed")
	}
	if _, err := MatchesRuntime("24", ">=9999999999999999999999999999999999999"); err == nil {
		t.Fatal("overflowing constraint guessed")
	}
}
