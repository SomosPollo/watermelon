package project

import (
	"reflect"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestDetectGoAndRust(t *testing.T) {
	for _, tt := range []struct {
		name                                           string
		files                                          map[string][]byte
		profile, runtime, wantRuntime, wantRequirement string
		commands                                       [][]string
	}{
		{"go module", map[string][]byte{"go.mod": []byte("module example.com/app\ngo 1.25.0\n")}, "go", "", config.DefaultGoRuntime, ">=1.25.0", [][]string{{"go", "mod", "download", "all"}}},
		{"go toolchain", map[string][]byte{"go.mod": []byte("module example.com/app\ngo 1.25.0\ntoolchain go1.26.2\n")}, "go", "", "1.26.2", ">=1.25.0", [][]string{{"go", "mod", "download", "all"}}},
		{"go workspace precedence", map[string][]byte{"go.mod": []byte("module example.com/app\ngo 1.25.0\ntoolchain go1.25.3\n"), "go.work": []byte("go 1.26.0\ntoolchain go1.26.2\nuse .\n")}, "go", "", "1.26.2", ">=1.26.0", [][]string{{"go", "mod", "download", "all"}}},
		{"go version pin", map[string][]byte{"go.mod": []byte("module example.com/app\ngo 1.25.0\n"), ".go-version": []byte("go1.26.2\n")}, "go", "", "1.26.2", ">=1.25.0", [][]string{{"go", "mod", "download", "all"}}},
		{"go workspace only", map[string][]byte{"go.work": []byte("go 1.26.0\nuse (\n ./a\n ./b\n)\n")}, "go", "1.26", "1.26", ">=1.26.0", [][]string{{"go", "mod", "download", "all"}}},
		{"rust crate", map[string][]byte{"Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\nrust-version='1.85'\n")}, "rust", "", config.DefaultRustRuntime, ">=1.85", [][]string{{"cargo", "fetch"}}},
		{"rust locked", map[string][]byte{"Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\n"), "Cargo.lock": []byte("version=4\n"), "rust-toolchain.toml": []byte("[toolchain]\nchannel='1.85.0'\n")}, "rust", "", "1.85.0", "", [][]string{{"cargo", "fetch", "--locked"}}},
		{"rust shorthand pin", map[string][]byte{"Cargo.toml": []byte("[workspace]\nmembers=['a']\n"), "rust-toolchain": []byte("1.85\n")}, "rust", "", "1.85.0", "", [][]string{{"cargo", "fetch"}}},
		{"rust development components", map[string][]byte{"Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\n"), "rust-toolchain": []byte("# Toolchain used for development\n[toolchain]\nchannel='stable'\ncomponents=['rustfmt', 'clippy']\n")}, "rust", "", config.DefaultRustRuntime, "", [][]string{{"cargo", "fetch"}}},
		{"rust inherited minimum", map[string][]byte{"Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\nrust-version.workspace=true\n[workspace.package]\nrust-version='1.85'\n")}, "rust", "", config.DefaultRustRuntime, ">=1.85", [][]string{{"cargo", "fetch"}}},
		{"rust workspace minimum", map[string][]byte{"Cargo.toml": []byte("[package]\nname='app'\nversion='0.1.0'\nrust-version='1.80'\n[workspace.package]\nrust-version='1.85'\n")}, "rust", "", config.DefaultRustRuntime, ">=1.85", [][]string{{"cargo", "fetch"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Detect(tt.files, "", tt.runtime)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Profile != tt.profile || plan.Runtime != tt.wantRuntime || plan.Requirement != tt.wantRequirement || !reflect.DeepEqual(plan.Commands, tt.commands) {
				t.Fatalf("unexpected plan: %+v", plan)
			}
		})
	}
}

func TestUnsupportedGoAndRustMetadata(t *testing.T) {
	goMod := []byte("module example.com/app\ngo 1.25.0\n")
	cargo := []byte("[package]\nname='app'\nversion='0.1.0'\n")
	for _, files := range []map[string][]byte{
		{"go.mod": []byte("go 1.25.0\n")},
		{"go.mod": []byte("module example.com/app\ngo 1.25; echo bad\n")},
		{"go.mod": goMod, ".go-version": []byte("1.26; touch sentinel")},
		{"go.work": []byte("go 1.26.0\nuse ../other\n")},
		{"go.work": []byte("go 1.26.0\nuse /private/other\n")},
		{"go.work": []byte("go 1.26.0\n")},
		{"Cargo.toml": []byte("not toml")},
		{"Cargo.toml": []byte("[package]\nrust-version.workspace=true\n")},
		{"Cargo.toml": []byte("[workspace]\nmembers=['../outside']\n")},
		{"Cargo.toml": []byte("[package]\nrust-version='1.85'\n[workspace.package]\nrust-version='invalid'\n")},
		{"Cargo.toml": cargo, "rust-toolchain": []byte("nightly-2026-10-01")},
		{"Cargo.toml": cargo, "rust-toolchain.toml": []byte("[toolchain]\nchannel='stable'\ntargets=['wasm32-unknown-unknown']\n")},
		{"Cargo.toml": cargo, "rust-toolchain.toml": []byte("[toolchain]\nchannel='stable'\ncomponents=['rust-src']\n")},
		{"Cargo.toml": cargo, "rust-toolchain": []byte("1.85.0"), "rust-toolchain.toml": []byte("[toolchain]\nchannel='1.86.0'\n")},
	} {
		if plan, err := Detect(files, "", ""); err == nil {
			t.Fatalf("accepted unsupported metadata: %+v => %+v", files, plan)
		}
	}
	plan, err := Detect(map[string][]byte{"go.mod": goMod, "Cargo.toml": cargo}, "go", "1.26")
	if err != nil || plan.Profile != "go" {
		t.Fatalf("explicit mixed selection failed: %+v %v", plan, err)
	}
}

func TestGoAndRustLockfilesInvalidateReadiness(t *testing.T) {
	for _, tt := range []struct{ manifest, body, lock string }{
		{"go.mod", "module example.com/app\ngo 1.25.0\n", "go.sum"},
		{"go.work", "go 1.26.0\nuse ./app\n", "go.work.sum"},
		{"Cargo.toml", "[package]\nname='app'\nversion='0.1.0'\n", "Cargo.lock"},
	} {
		files := map[string][]byte{tt.manifest: []byte(tt.body)}
		first, err := Detect(files, "", "")
		if err != nil {
			t.Fatal(err)
		}
		files[tt.lock] = []byte("new dependency checksums")
		second, err := Detect(files, "", "")
		if err != nil || first.Fingerprint == second.Fingerprint {
			t.Fatalf("%s not fingerprinted: %v", tt.lock, err)
		}
		if tt.lock == "Cargo.lock" && !strings.Contains(strings.Join(second.Commands[0], " "), "--locked") {
			t.Fatal("existing Rust lockfile can be rewritten")
		}
	}
}
