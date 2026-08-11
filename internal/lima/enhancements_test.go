package lima

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/sys/unix"
)

func boolPointer(value bool) *bool { return &value }

func TestGenerateConfigNoMountHasNoOperationalProjectDependency(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*config.Config)
		opts      GenerateOptions
		wants     []string
	}{
		{
			name: "strict logging",
			wants: []string{
				"mounts: []",
				"/var/log/watermelon/logs.log",
			},
		},
		{
			name: "tools smart wrappers and process policy",
			configure: func(cfg *config.Config) {
				cfg.Tools = map[string][]string{"node:20-slim": {"node", "npm"}}
				cfg.Network.Process = map[string][]string{"node": {"api.nodejs.org"}}
			},
			wants: []string{
				`_WM_WORKDIR=$(/bin/pwd -P)`,
				`-v "$_WM_WORKDIR:$_WM_WORKDIR" -w "$_WM_WORKDIR"`,
				"# watermelon-image: node:20-slim",
			},
		},
		{
			name: "ask bootstrap",
			configure: func(cfg *config.Config) {
				cfg.Security.Enforcement = config.EnforcementAsk
			},
			opts: GenerateOptions{
				NfqdSHA256:       testNfqdSHA256,
				VerdictAuthKey:   testVerdictAuthKey,
				BootstrapHostDir: "/host/watermelon/bootstrap",
			},
			wants: []string{
				`location: "/host/watermelon/bootstrap"`,
				"mountPoint: /mnt/watermelon/bootstrap",
				"writable: false",
				"/mnt/watermelon/bootstrap/watermelon-nfqd",
				"bootstrap source must be a regular, non-symlink file",
			},
		},
		{
			name: "dedicated host log state",
			opts: GenerateOptions{LogHostDir: "/host/watermelon/state"},
			wants: []string{
				`location: "/host/watermelon/state"`,
				"mountPoint: /mnt/watermelon/state",
				"/mnt/watermelon/state/logs.log",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.VM.MountProject = boolPointer(false)
			if tt.configure != nil {
				tt.configure(cfg)
			}
			generated, err := GenerateConfigForInstance(cfg, "/host/project", nil, tt.opts)
			if err != nil {
				t.Fatalf("GenerateConfigForInstance() error = %v", err)
			}
			if strings.Contains(generated, "/project") {
				t.Fatalf("no-mount config retained an operational /project dependency:\n%s", generated)
			}
			for _, want := range tt.wants {
				if !strings.Contains(generated, want) {
					t.Errorf("generated config missing %q", want)
				}
			}
			if tt.name == "tools smart wrappers and process policy" {
				for name, body := range map[string]string{
					// The unquoted outer heredoc consumes these escapes while
					// publishing the actual helper.
					"container helper": strings.ReplaceAll(extractHeredocBody(t, generated, "<< CONTAINERHELPER", "CONTAINERHELPER"), `\$`, `$`),
					"smart wrapper":    extractHeredocBody(t, generated, "<< 'WATERMELON_SMART_WRAPPER_npm'", "WATERMELON_SMART_WRAPPER_npm"),
				} {
					cmd := exec.Command("bash", "-n")
					cmd.Stdin = strings.NewReader(body)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Errorf("generated %s is invalid Bash: %v\n%s\n%s", name, err, output, body)
					}
				}
			}
		})
	}
}

func TestGenerateConfigNoMountValidatesWithLimaWhenAvailable(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl is not installed")
	}
	cfg := config.NewConfig()
	cfg.VM.MountProject = boolPointer(false)
	cfg.Security.Enforcement = config.EnforcementAsk
	cfg.Tools = map[string][]string{"node:20-slim": {"node", "npm"}}
	cfg.Network.Process = map[string][]string{"node": {"api.nodejs.org"}}
	bootstrapDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bootstrapDir, "watermelon-nfqd"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateConfigForInstance(cfg, t.TempDir(), nil, GenerateOptions{
		NfqdSHA256:       testNfqdSHA256,
		VerdictAuthKey:   testVerdictAuthKey,
		BootstrapHostDir: bootstrapDir,
		LogHostDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("GenerateConfigForInstance() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "no-mount.yaml")
	if err := os.WriteFile(path, []byte(generated), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(limactl, "validate", "--tty=false", path).CombinedOutput(); err != nil {
		t.Fatalf("limactl validate rejected no-mount config: %v\n%s", err, output)
	}
}

func TestGenerateConfigUsesConfiguredGuestWorkdirSafely(t *testing.T) {
	cfg := config.NewConfig()
	cfg.VM.MountProject = boolPointer(false)
	cfg.VM.Workdir = "/home/watermelon/work space"
	cfg.Tools = map[string][]string{"alpine:3.20": {"sh"}}

	generated, err := GenerateConfig(cfg, "/host/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	for _, want := range []string{
		`-v "/home/watermelon/work space:/home/watermelon/work space" -w "/home/watermelon/work space"`,
		shellQuote(`[ -d '/home/watermelon/work space' ] && cd '/home/watermelon/work space'`),
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated config missing safely quoted workdir form %q", want)
		}
	}
	if strings.Contains(generated, `_WM_WORKDIR=$(/bin/pwd -P)`) {
		t.Error("an explicit VM workdir unexpectedly used the runtime working directory")
	}
}

func TestGenerateConfigAskBootstrapIsIndependentAndFailClosed(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	tests := []struct {
		name string
		opts GenerateOptions
		want string
	}{
		{
			name: "missing bootstrap directory",
			opts: GenerateOptions{NfqdSHA256: testNfqdSHA256, VerdictAuthKey: testVerdictAuthKey},
			want: "dedicated watermelon-nfqd host bootstrap directory",
		},
		{
			name: "project overlap",
			opts: GenerateOptions{NfqdSHA256: testNfqdSHA256, VerdictAuthKey: testVerdictAuthKey, BootstrapHostDir: "/host/project/runtime"},
			want: "must not overlap the project directory",
		},
		{
			name: "writable custom mount overlap",
			opts: GenerateOptions{NfqdSHA256: testNfqdSHA256, VerdictAuthKey: testVerdictAuthKey, BootstrapHostDir: "/host/shared/bootstrap"},
			want: "must not overlap writable mount",
		},
		{
			name: "writable log alias",
			opts: GenerateOptions{
				NfqdSHA256:       testNfqdSHA256,
				VerdictAuthKey:   testVerdictAuthKey,
				BootstrapHostDir: "/host/runtime",
				LogHostDir:       "/host/runtime/logs",
			},
			want: "must not overlap the read-only watermelon-nfqd bootstrap directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Mounts = nil
			mountSources := map[string]string(nil)
			if tt.name == "writable custom mount overlap" {
				cfg.Mounts = map[string]config.Mount{"/configured": {Target: "/mnt/watermelon/shared", Mode: "rw"}}
				mountSources = map[string]string{"/configured": "/host/shared"}
			}
			_, err := GenerateConfigForInstance(cfg, "/host/project", mountSources, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("GenerateConfigForInstance() error = %v, want %q", err, tt.want)
			}
		})
	}

	t.Run("no-mount bootstrap still cannot expose project", func(t *testing.T) {
		cfg := config.NewConfig()
		mountProject := false
		cfg.VM.MountProject = &mountProject
		cfg.Security.Enforcement = config.EnforcementAsk
		_, err := GenerateConfigForInstance(cfg, "/host/project", nil, GenerateOptions{
			NfqdSHA256:       testNfqdSHA256,
			VerdictAuthKey:   testVerdictAuthKey,
			BootstrapHostDir: "/host/project/bootstrap",
		})
		if err == nil || !strings.Contains(err.Error(), "must not overlap the project directory") {
			t.Fatalf("GenerateConfigForInstance() error = %v, want no-mount project containment rejection", err)
		}
	})

	t.Run("log state cannot expose project", func(t *testing.T) {
		cfg := config.NewConfig()
		mountProject := false
		cfg.VM.MountProject = &mountProject
		_, err := GenerateConfigForInstance(cfg, "/host/project", nil, GenerateOptions{
			BootstrapHostDir: "/host/runtime/bootstrap",
			LogHostDir:       "/host/project/logs",
		})
		if err == nil || !strings.Contains(err.Error(), "log host directory must not overlap the project directory") {
			t.Fatalf("GenerateConfigForInstance() error = %v, want log-state project containment rejection", err)
		}
	})
}

func TestGenerateConfigMountsBootstrapIdentityOutsideAskMode(t *testing.T) {
	cfg := config.NewConfig()
	generated, err := GenerateConfigForInstance(cfg, "/host/project", nil, GenerateOptions{
		BootstrapHostDir: "/host/watermelon/bootstrap",
	})
	if err != nil {
		t.Fatalf("GenerateConfigForInstance() error = %v", err)
	}
	for _, want := range []string{
		`location: "/host/watermelon/bootstrap"`,
		"mountPoint: /mnt/watermelon/bootstrap",
		"writable: false",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("identity bootstrap mount missing %q", want)
		}
	}
	if strings.Contains(generated, "watermelon-nfqd.service") {
		t.Error("non-ask identity bootstrap unexpectedly configured nfqd")
	}

	_, err = GenerateConfigForInstance(cfg, "/host/project", nil, GenerateOptions{
		BootstrapHostDir: "/host/watermelon/bootstrap",
		NfqdSHA256:       testNfqdSHA256,
	})
	if err == nil || !strings.Contains(err.Error(), "only valid with ask enforcement") {
		t.Fatalf("non-ask nfqd digest error = %v, want rejection", err)
	}

	_, err = GenerateConfigForInstance(cfg, "/host/project", nil, GenerateOptions{
		BootstrapHostDir: "/host/one",
		NfqdHostDir:      "/host/two",
	})
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("conflicting bootstrap aliases error = %v, want rejection", err)
	}
}

func TestGenerateConfigSelectsRequestedUbuntuImage(t *testing.T) {
	for _, version := range []string{"22.04", "24.04"} {
		t.Run(version, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.VM.Image = "ubuntu-" + version
			generated, err := GenerateConfig(cfg, "/host/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}
			if !strings.Contains(generated, "/releases/"+version+"/") {
				t.Errorf("generated image list does not contain Ubuntu %s", version)
			}
			other := "22.04"
			if version == other {
				other = "24.04"
			}
			if strings.Contains(generated, "/releases/"+other+"/") {
				t.Errorf("generated image list unexpectedly contains Ubuntu %s", other)
			}
		})
	}
}

func TestReadProvisionScriptsPreservesBytesAndRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	content := "#!/bin/sh\nprintf 'hello'\n\n  "
	regular := filepath.Join(dir, "setup.sh")
	if err := os.WriteFile(regular, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareProvisionScripts(dir, []string{"setup.sh"})
	if err != nil {
		t.Fatalf("PrepareProvisionScripts() error = %v", err)
	}
	if len(prepared.Contents) != 1 || prepared.Contents[0] != content {
		t.Fatalf("PrepareProvisionScripts() = %#v, want byte-preserved content %#v", prepared.Contents, content)
	}
	sum := sha256.Sum256([]byte(content))
	if len(prepared.SHA256) != 1 || prepared.SHA256[0] != hex.EncodeToString(sum[:]) {
		t.Fatalf("prepared digest = %#v, want exact content SHA-256", prepared.SHA256)
	}

	symlink := filepath.Join(dir, "linked.sh")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readProvisionScripts(dir, []string{"linked.sh"}); err == nil || !strings.Contains(err.Error(), "without following symlinks") {
		t.Fatalf("symlink read error = %v, want no-follow rejection", err)
	}

	outside := t.TempDir()
	outsideScript := filepath.Join(outside, "outside.sh")
	if err := os.WriteFile(outsideScript, []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := readProvisionScripts(dir, []string{"outside/outside.sh"}); err == nil || !strings.Contains(err.Error(), "without following symlinks") {
		t.Fatalf("outside-project symlink error = %v, want no-follow rejection", err)
	}

	fifo := filepath.Join(dir, "setup.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProvisionScripts(dir, []string{"setup.fifo"}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("FIFO read error = %v, want non-regular rejection", err)
	}

	oversized := filepath.Join(dir, "oversized.sh")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", maxProvisionScriptSize+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProvisionScripts(dir, []string{"oversized.sh"}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized read error = %v, want size rejection", err)
	}
}

func TestReadProvisionScriptsCapsTotalBytes(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for index := 0; index < 5; index++ {
		name := filepath.Join(dir, "script-"+string(rune('a'+index))+".sh")
		size := maxProvisionScriptSize
		if index == 4 {
			size = 1
		}
		if err := os.WriteFile(name, []byte(strings.Repeat("x", size)), 0600); err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(name))
	}
	if _, err := readProvisionScripts(dir, names); err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("readProvisionScripts() error = %v, want total-size rejection", err)
	}
}

func TestGenerateConfigRendersProvisionScriptAndIdempotencyWarning(t *testing.T) {
	dir := t.TempDir()
	content := "#!/bin/sh\nset -eu\nprintf '%s' '{{.User}} kept trailing space '"
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig()
	cfg.Provision.Scripts = []string{"setup.sh"}

	generated, err := GenerateConfig(cfg, dir)
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	for _, want := range []string{
		"Configured provision scripts must be idempotent: Lima may run provisions again.",
		`_WM_PAYLOAD=$(/usr/bin/mktemp /usr/local/libexec/watermelon/.provision-script-00000000.XXXXXX)`,
		`trap wm_cleanup_provision_payload EXIT`,
		`/run/watermelon-provisioning/script-00000000.complete`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated provision script missing %q", want)
		}
	}
	for _, encodedLine := range strings.Split(encodeProvisionScript(content), "\n") {
		if !strings.Contains(generated, encodedLine) {
			t.Errorf("generated provision script missing encoded payload line %q", encodedLine)
		}
	}

	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl is not installed")
	}
	configPath := filepath.Join(t.TempDir(), "provision-literal.yaml")
	if err := os.WriteFile(configPath, []byte(generated), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(limactl, "template", "yq", configPath, `.provision[] | select(.script | contains(".provision-script-00000000.XXXXXX")) | .script | @base64`).CombinedOutput()
	if err != nil {
		t.Fatalf("resolving provision script through Lima templating: %v\n%s", err, out)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("decoding Lima-rendered provision script %q: %v", out, err)
	}
	const payloadStart = "<<'WATERMELON_PROVISION_PAYLOAD'\n"
	const payloadEnd = "\nWATERMELON_PROVISION_PAYLOAD\n"
	start := strings.Index(string(decoded), payloadStart)
	end := strings.Index(string(decoded), payloadEnd)
	if start < 0 || end < start {
		t.Fatalf("Lima-rendered provision wrapper does not contain its encoded payload:\n%s", decoded)
	}
	inner, err := base64.StdEncoding.DecodeString(string(decoded)[start+len(payloadStart) : end])
	if err != nil {
		t.Fatalf("decoding embedded provision payload: %v", err)
	}
	if string(inner) != content {
		t.Fatalf("Lima-rendered provision payload = %q, want exact bytes %q", inner, content)
	}
}

func TestGenerateConfigPublishesCompletionOnlyAfterEveryProvisionStage(t *testing.T) {
	dir := t.TempDir()
	contents := []string{
		"#!/bin/sh\nexit 0\n",
		"#!/usr/bin/env python3\nraise SystemExit(37)\n",
	}
	var scripts []string
	for index, content := range contents {
		name := "setup-" + string(rune('a'+index))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, name)
	}
	cfg := config.NewConfig()
	cfg.Provision.Scripts = scripts

	generated, err := GenerateConfig(cfg, dir)
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	for index, script := range extractSystemProvisionScripts(t, generated) {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated attested provision %d is not valid Bash: %v\n%s", index, err, output)
		}
	}
	if strings.Contains(generated, "mode: user") {
		t.Fatal("generated config contains a user provision that Lima would execute after system attestation")
	}
	for index, content := range contents {
		name := "script-0000000" + string(rune('0'+index))
		payloadAssignmentMarker := `_WM_PAYLOAD=$(/usr/bin/mktemp /usr/local/libexec/watermelon/.provision-` + name + `.XXXXXX)`
		stage := "/run/watermelon-provisioning/" + name + ".complete"
		for _, want := range []string{payloadAssignmentMarker, `"$_WM_PAYLOAD"`, `trap wm_cleanup_provision_payload EXIT`, stage} {
			if !strings.Contains(generated, want) {
				t.Errorf("generated provision wrapper %d missing %q", index, want)
			}
		}
		for _, encodedLine := range strings.Split(encodeProvisionScript(content), "\n") {
			if !strings.Contains(generated, encodedLine) {
				t.Errorf("generated provision wrapper %d missing encoded payload line %q", index, encodedLine)
			}
		}
		payloadAssignment := strings.Index(generated, payloadAssignmentMarker)
		payloadRun := -1
		if payloadAssignment >= 0 {
			if relative := strings.Index(generated[payloadAssignment:], "\n      \"$_WM_PAYLOAD\"\n"); relative >= 0 {
				payloadRun = payloadAssignment + relative
			}
		}
		stagePublish := strings.Index(generated, "/usr/bin/install -o root -g root -m 0600 /dev/null "+stage)
		stageCheck := strings.LastIndex(generated, "wm_require_provision_stage "+stage)
		if payloadRun < 0 || stagePublish < payloadRun || stageCheck < stagePublish {
			t.Fatalf("unsafe provision stage order for %s: payload=%d publish=%d check=%d", name, payloadRun, stagePublish, stageCheck)
		}
	}

	reset := strings.Index(generated, "/bin/rm -f -- /run/watermelon-provisioning-complete")
	policy := strings.Index(generated, "touch /run/watermelon-policy-applied")
	workdir := strings.Index(generated, "/usr/bin/install -o root -g root -m 0600 /dev/null /run/watermelon-provisioning/workdir.complete")
	complete := strings.LastIndex(generated, "/usr/bin/install -o root -g root -m 0600 /dev/null /run/watermelon-provisioning-complete")
	if reset < 0 || policy < reset || workdir < policy || complete < workdir {
		t.Fatalf("unsafe completion attestation order: reset=%d policy=%d workdir=%d complete=%d", reset, policy, workdir, complete)
	}
	for _, stage := range []string{
		"wm_require_provision_stage /run/watermelon-policy-applied",
		"wm_require_provision_stage /run/watermelon-provisioning/script-00000000.complete",
		"wm_require_provision_stage /run/watermelon-provisioning/script-00000001.complete",
		"wm_require_provision_stage /run/watermelon-provisioning/workdir.complete",
	} {
		if check := strings.LastIndex(generated, stage); check < 0 || check > complete {
			t.Errorf("final completion gate missing stage check %q", stage)
		}
	}
}

func TestGenerateConfigWithoutWorkdirDoesNotRequireWorkdirStage(t *testing.T) {
	cfg := config.NewConfig()
	cfg.VM.MountProject = boolPointer(false)

	generated, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	if strings.Contains(generated, "workdir.complete") {
		t.Fatal("dynamic login-directory config unexpectedly requires a workdir provision stage")
	}
	if !strings.Contains(generated, "/run/watermelon-provisioning-complete") {
		t.Fatal("dynamic login-directory config is missing its final completion marker")
	}
}
