package lima

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"gopkg.in/yaml.v3"
)

func TestTrustedBootstrapStopsOnAPTFailure(t *testing.T) {
	generated, err := GenerateConfig(config.NewConfig(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := extractSystemProvisionScript(t, generated)
	start := strings.Index(script, "# Trusted bootstrap prerequisites.")
	end := strings.Index(script, "if ! id -u watermelon-dns")
	if start < 0 || end <= start {
		t.Fatal("trusted prerequisite stage is missing")
	}
	prerequisites := script[start:end]
	for _, operation := range []string{"update", "install", "recover", "success"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "apt.log")
			fake := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$WM_APT_LOG\"\nif [ \"$WM_APT_FAIL_OPERATION\" = recover ] && [ \"$1\" = update ] && [ ! -f \"$WM_APT_LOG.failed\" ]; then : > \"$WM_APT_LOG.failed\"; exit 42; fi\nif [ \"$1\" = \"$WM_APT_FAIL_OPERATION\" ]; then exit 42; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "apt-get"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", "-e", "-c", prerequisites+"\nprintf completed")
			command.Env = append(os.Environ(), "PATH="+dir, "WM_APT_LOG="+log, "WM_APT_FAIL_OPERATION="+operation)
			out, err := command.CombinedOutput()
			if operation == "success" || operation == "recover" {
				if err != nil || !strings.HasSuffix(string(out), "completed") {
					t.Fatalf("successful prerequisites did not complete: %q, %v", out, err)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 42 || strings.Contains(string(out), "completed") {
					t.Fatalf("failed %s continued provisioning: %q, %v", operation, out, err)
				}
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := "update\ninstall\n"
			if operation == "update" {
				want = strings.Repeat("update\n", 3)
			} else if operation == "install" {
				want = strings.Repeat("update\ninstall\n", 3)
			} else if operation == "recover" {
				want = "update\nupdate\ninstall\n"
			}
			if string(calls) != want {
				t.Fatalf("APT calls = %q, want %q", calls, want)
			}
		})
	}
}

func TestBootstrapAPTSettingsPrecedeLimaDependencyResolution(t *testing.T) {
	generated, err := GenerateConfig(config.NewConfig(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Provision []struct {
			Mode                            string `yaml:"mode"`
			Script                          string `yaml:"script"`
			SkipDefaultDependencyResolution bool   `yaml:"skipDefaultDependencyResolution"`
		} `yaml:"provision"`
	}
	if err := yaml.Unmarshal([]byte(generated), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Provision) < 2 || document.Provision[0].Mode != "dependency" || document.Provision[0].SkipDefaultDependencyResolution {
		t.Fatal("APT configuration must run in Lima's dependency phase without bypassing its prerequisite installation")
	}
	script := document.Provision[0].Script
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(script)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("invalid dependency script: %v\n%s", err, out)
	}
	const marker = "/run/watermelon-bootstrap-apt-configured"
	assertRenderedBefore(t, script, "rm -f -- /run/watermelon-provisioning-complete "+marker, "cat > /etc/apt/apt.conf.d/")
	assertRenderedBefore(t, script, "chmod 0644 /etc/apt/apt.conf.d/", "install -o root -g root -m 0600 /dev/null "+marker)
	if strings.Contains(script, "iptables") || strings.Contains(script, "apt-get install") {
		t.Fatal("dependency configuration must not reopen network policy or replace Lima's package installation")
	}
	scripts := extractSystemProvisionScripts(t, generated)
	final := scripts[len(scripts)-1]
	assertRenderedBefore(t, final, "wm_require_provision_stage "+marker, "/dev/null /run/watermelon-provisioning-complete")

	settings := extractHeredocBody(t, generated, "<< 'WATERMELON_APT'", "WATERMELON_APT")
	for _, setting := range []string{
		`Acquire::ForceIPv4 "true";`,
		`Acquire::Retries "5";`,
		`Acquire::http::Timeout "30";`,
		`Acquire::https::Timeout "30";`,
		`APT::Update::Error-Mode "any";`,
	} {
		if !strings.Contains(settings, setting) {
			t.Errorf("bootstrap APT settings missing %s", setting)
		}
	}
	// Linux CI verifies the settings with APT's actual parser. macOS still
	// checks the generated YAML, phase ordering, shell syntax, and attestation.
	t.Run("APT parser", func(t *testing.T) {
		aptConfig, err := exec.LookPath("apt-config")
		if err != nil {
			t.Skip("apt-config is unavailable on this host")
		}
		path := filepath.Join(t.TempDir(), "apt.conf")
		if err := os.WriteFile(path, []byte(settings), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(aptConfig, "-c", path, "shell", "IPv4", "Acquire::ForceIPv4", "Retries", "Acquire::Retries", "Errors", "APT::Update::Error-Mode").CombinedOutput()
		if err != nil {
			t.Fatalf("APT rejected bootstrap settings: %v\n%s", err, out)
		}
		for _, want := range []string{"IPv4='true'", "Retries='5'", "Errors='any'"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("APT settings = %q; want %q", out, want)
			}
		}
	})
}
