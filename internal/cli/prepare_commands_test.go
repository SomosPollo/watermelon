package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
)

func forbidValidationVMAccess(t *testing.T) {
	t.Helper()
	oldCompatible, oldStatus := cliRequireCompatibleLima, cliGetVMStatus
	t.Cleanup(func() { cliRequireCompatibleLima, cliGetVMStatus = oldCompatible, oldStatus })
	cliRequireCompatibleLima = func() error { t.Fatal("config validation inspected Lima installation"); return nil }
	cliGetVMStatus = func(string) lima.VMStatus { t.Fatal("config validation inspected a VM"); return lima.StatusUnknown }
}

func TestConfigValidateDiscoversRootAndDoesNotCreateState(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "JSON"}[jsonOutput], func(t *testing.T) {
			dir, limaHome, userConfig := setupNamedVMIdentityTest(t)
			forbidValidationVMAccess(t)
			cfg := config.NewConfig()
			cfg.Provision.Scripts = []string{"setup.sh"}
			// Validation must inspect this script as data, never execute it.
			if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte("#!/bin/sh\ntouch validation-executed-script\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := writeProjectConfig(dir, cfg, true); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, projectConfigName)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(dir, "src", "nested")
			if err := os.MkdirAll(nested, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(nested)
			cmd := NewConfigCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			args := []string{"validate"}
			if jsonOutput {
				args = append(args, "--json")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if jsonOutput {
				var got map[string]any
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("invalid JSON: %v: %s", err, &out)
				}
				if len(got) != 3 || got["schema_version"] != float64(1) || got["valid"] != true || got["project"] != dir {
					t.Fatalf("validation schema changed: %v", got)
				}
			} else if got, want := out.String(), "Valid configuration: "+configPath+"\n"; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("validation rewrote project configuration")
			}
			for _, path := range []string{limaHome, userConfig} {
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != 0 {
					t.Fatalf("validation created runtime state in %s: %v, %v", path, entries, err)
				}
			}
			if _, err := os.Stat(filepath.Join(nested, "validation-executed-script")); !os.IsNotExist(err) {
				t.Fatal("validation executed provision script")
			}
		})
	}
}

func TestConfigValidateRejectsInvalidInputsWithoutVMAccess(t *testing.T) {
	for _, tt := range []struct {
		name, content, want string
	}{
		{"missing config", "", "no .watermelon.toml"},
		{"malformed TOML", "[security", "parsing config"},
		{"unknown key", "[security]\nenforcment = 'fail'", "unknown key"},
		{"invalid policy", "[security]\nenforcement = 'invalid'", "enforcement"},
		{"missing provision script", "[provision]\nscripts = ['missing.sh']", "provision script"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, _, _ := setupNamedVMIdentityTest(t)
			forbidValidationVMAccess(t)
			if tt.content != "" {
				if err := os.WriteFile(filepath.Join(dir, projectConfigName), []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			cmd := NewConfigCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs([]string{"validate", "--json"})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want %q", err, tt.want)
			}
			if strings.Contains(out.String(), `"valid":true`) {
				t.Fatalf("invalid input reported success: %s", &out)
			}
		})
	}
}

func TestConfigValidateRequiresManagedWorkspaceOwnership(t *testing.T) {
	dir, cfg, _, _ := projectSetupFixture(t)
	forbidValidationVMAccess(t)
	t.Chdir(dir)
	validate := func() error {
		cmd := NewConfigCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetArgs([]string{"validate"})
		return cmd.Execute()
	}
	if err := validate(); err != nil {
		t.Fatalf("registered workspace rejected: %v", err)
	}
	state, err := loadWorkspaceState(dir)
	if err != nil {
		t.Fatal(err)
	}
	state.Source = "https://github.com/another/project"
	if err := saveWorkspaceState(dir, state); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("workspace source mismatch accepted: disk=%s err=%v", cfg.Workspace.Disk, err)
	}
}
