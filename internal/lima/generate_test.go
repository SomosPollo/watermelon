package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

const testNfqdSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func generateConfigForTest(cfg *config.Config, projectDir string, verdictServerPort ...int) (string, error) {
	if cfg.Security.Enforcement != "ask" {
		return GenerateConfig(cfg, projectDir, verdictServerPort...)
	}
	opts := GenerateOptions{NfqdSHA256: testNfqdSHA256}
	if len(verdictServerPort) > 0 {
		opts.VerdictServerPort = verdictServerPort[0]
	}
	return GenerateConfigForInstance(cfg, projectDir, nil, opts)
}

func withHostGOOS(t *testing.T, goos string) {
	t.Helper()
	old := hostGOOS
	hostGOOS = goos
	t.Cleanup(func() {
		hostGOOS = old
	})
}

func TestGenerateConfigValidatesWithLimaWhenAvailable(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl is not installed")
	}

	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org", "*.github.com"}
	cfg.Network.Process = map[string][]string{
		"npm": {"api.npmjs.org"},
	}
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm", "npx"},
	}
	cfg.Ports.Forward = []int{3000}

	generated, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "watermelon.yaml")
	if err := os.WriteFile(path, []byte(generated), 0600); err != nil {
		t.Fatalf("writing generated Lima config: %v", err)
	}
	if output, err := exec.Command(limactl, "validate", "--tty=false", path).CombinedOutput(); err != nil {
		t.Fatalf("limactl validate error = %v:\n%s", err, output)
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid port 80", 80, false},
		{"valid port 443", 443, false},
		{"valid port 3000", 3000, false},
		{"valid port 1", 1, false},
		{"valid port 65535", 65535, false},
		{"invalid port 0", 0, true},
		{"invalid port negative", -1, true},
		{"invalid port too high", 65536, true},
		{"invalid port very high", 100000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestGenerateConfigValidation(t *testing.T) {
	t.Run("rejects invalid domain", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Network.Allow = []string{"github.com", "evil.com; rm -rf /"}

		_, err := GenerateConfig(cfg, "/test")
		if err == nil {
			t.Error("expected error for invalid domain, got nil")
		}
		if !strings.Contains(err.Error(), "invalid network allow domain") {
			t.Errorf("expected 'invalid network allow domain' in error, got: %v", err)
		}
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Ports.Forward = []int{80, 0}

		_, err := GenerateConfig(cfg, "/test")
		if err == nil {
			t.Error("expected error for invalid port, got nil")
		}
		if !strings.Contains(err.Error(), "invalid port forward") {
			t.Errorf("expected 'invalid port forward' in error, got: %v", err)
		}
	})
}

func TestGenerateConfigRejectsUnsafeProjectPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: "project"},
		{name: "not clean", path: "/tmp/project/../escape"},
		{name: "NUL", path: "/tmp/project\x00escape"},
		{name: "invalid UTF-8", path: string([]byte{'/', 't', 'm', 'p', '/', 0xff})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := GenerateConfig(config.NewConfig(), tt.path); err == nil {
				t.Fatalf("GenerateConfig() accepted unsafe project path %q", tt.path)
			}
		})
	}
}

func TestGenerateConfigQuotesHostPathsAndCanonicalizesMountTargets(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project\"\nmountPoint: /escape")
	canonicalSource := filepath.Join(t.TempDir(), "source\"\nwritable: true")
	cfg := config.NewConfig()
	cfg.Mounts = map[string]config.Mount{
		"/configured/source": {Target: "/mnt/watermelon/./cache", Mode: "rw"},
	}

	generated, err := GenerateConfigForInstance(cfg, projectDir, map[string]string{
		"/configured/source": canonicalSource,
	}, GenerateOptions{})
	if err != nil {
		t.Fatalf("GenerateConfigForInstance() error = %v", err)
	}

	for _, want := range []string{
		"location: " + strconv.Quote(projectDir),
		"location: " + strconv.Quote(canonicalSource),
		`mountPoint: "/mnt/watermelon/cache"`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated config missing safely quoted scalar %q", want)
		}
	}
	if strings.Contains(generated, "\nmountPoint: /escape\n") || strings.Contains(generated, "\nwritable: true\nwritable:") {
		t.Fatal("host path content escaped its YAML scalar")
	}

	limactl, err := exec.LookPath("limactl")
	if err != nil {
		return
	}
	path := filepath.Join(t.TempDir(), "hostile-paths.yaml")
	if err := os.WriteFile(path, []byte(generated), 0600); err != nil {
		t.Fatalf("writing generated Lima config: %v", err)
	}
	if output, err := exec.Command(limactl, "validate", "--tty=false", path).CombinedOutput(); err != nil {
		t.Fatalf("limactl rejected safely quoted hostile paths: %v\n%s", err, output)
	}
}

func TestGenerateConfigRejectsUnsafeMountSourceRepresentations(t *testing.T) {
	for _, source := range []string{
		"/tmp/source/../escape",
		"/tmp/source\x00escape",
		string([]byte{'/', 't', 'm', 'p', '/', 0xff}),
	} {
		t.Run(strconv.Quote(source), func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Mounts = map[string]config.Mount{
				source: {Target: "/mnt/watermelon/cache"},
			}
			if _, err := GenerateConfig(cfg, "/test/project"); err == nil {
				t.Fatalf("GenerateConfig() accepted unsafe mount source %q", source)
			}
		})
	}

	cfg := config.NewConfig()
	cfg.Mounts = map[string]config.Mount{
		"/configured/source": {Target: "/mnt/watermelon/cache"},
	}
	for _, canonical := range []string{"/tmp/source/../escape", "/tmp/source\x00escape", string([]byte{'/', 0xff})} {
		if _, err := GenerateConfigWithMountSources(cfg, "/test/project", map[string]string{"/configured/source": canonical}); err == nil {
			t.Fatalf("GenerateConfigWithMountSources() accepted unsafe canonical source %q", canonical)
		}
	}
}

func TestGenerateLimaConfig(t *testing.T) {
	withHostGOOS(t, "darwin")

	cfg := config.NewConfig()
	cfg.VM.Image = "ubuntu-22.04"
	cfg.Resources.Memory = "4GB"
	cfg.Resources.CPUs = 2
	cfg.Resources.Disk = "20GB"
	cfg.Network.Allow = []string{"registry.npmjs.org", "github.com"}
	cfg.Ports.Forward = []int{3000, 5173}

	projectDir := "/Users/test/myproject"

	yaml, err := GenerateConfig(cfg, projectDir)
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check key parts are present
	checks := []string{
		"vmType: vz",
		`memory: "4GiB"`,
		"cpus: 2",
		`disk: "20GiB"`,
		`name: "watermelon"`,
		`uid: 1000`,
		`home: "/home/watermelon"`,
		"/Users/test/myproject",
		"mountPoint: /project",
		"writable: true",
		"iptables",
		"registry.npmjs.org",
	}

	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("expected yaml to contain %q", check)
		}
	}
}

func TestGenerateConfigSelectsVMTypeForHostOS(t *testing.T) {
	tests := []struct {
		goos   string
		vmType string
	}{
		{goos: "darwin", vmType: "vz"},
		{goos: "linux", vmType: "qemu"},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			withHostGOOS(t, tt.goos)

			yaml, err := GenerateConfig(config.NewConfig(), "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			want := "vmType: " + tt.vmType
			if !strings.Contains(yaml, want) {
				t.Errorf("expected generated yaml to contain %q", want)
			}
		})
	}
}

func TestGenerateConfigRejectsUnsupportedHostOS(t *testing.T) {
	withHostGOOS(t, "windows")

	_, err := GenerateConfig(config.NewConfig(), "/test/project")
	if err == nil {
		t.Fatal("expected unsupported host OS error")
	}
	if !strings.Contains(err.Error(), "supports macOS and Linux") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGenerateConfigHasBashrcProvision(t *testing.T) {
	cfg := config.NewConfig()
	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for user-mode provision that sets up /project cd
	if !strings.Contains(yaml, "mode: user") {
		t.Error("expected user-mode provision in yaml")
	}
	if !strings.Contains(yaml, "cd /project") {
		t.Error("expected 'cd /project' in bashrc provision")
	}
}

func TestGenerateConfigWithNetworkProcess(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check that namespace creation is present
	wantNamespace := "wmns-" + processKernelID("claude")
	if !strings.Contains(yaml, wantNamespace) {
		t.Errorf("expected yaml to contain namespace name %q", wantNamespace)
	}
}

func TestGenerateConfigRejectsInvalidProcessDomain(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com", "evil.com; rm -rf /"},
	}

	_, err := GenerateConfig(cfg, "/test")
	if err == nil {
		t.Fatal("expected error for invalid process domain, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected error to mention 'invalid', got: %v", err)
	}
}

func TestGenerateConfigRejectsInvalidProcessName(t *testing.T) {
	for _, processName := range []string{
		"claude;evil",
		"x>(reboot)",
		"x\tcommand",
		"-leading-option",
	} {
		t.Run(processName, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Network.Process = map[string][]string{
				processName: {"api.anthropic.com"},
			}

			_, err := GenerateConfig(cfg, "/test")
			if err == nil {
				t.Fatalf("expected error for invalid process name %q, got nil", processName)
			}
		})
	}
}

func TestProcessKernelIdentifiersAreShortStableAndCollisionChecked(t *testing.T) {
	processName := "typescript-language-server-with-a-long-command-name"
	kernelID := processKernelID(processName)
	if len(kernelID) != 12 {
		t.Fatalf("processKernelID() length = %d, want 12", len(kernelID))
	}
	if got := processKernelID(processName); got != kernelID {
		t.Fatalf("processKernelID() is not stable: %q != %q", got, kernelID)
	}
	if len("wh-"+kernelID) > 15 || len("wn-"+kernelID) > 15 {
		t.Fatalf("generated veth identifiers exceed Linux IFNAMSIZ: %q / %q", "wh-"+kernelID, "wn-"+kernelID)
	}

	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		processName: {"*.example.com"},
	}
	yaml, err := GenerateConfig(cfg, "/test")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	for _, want := range []string{
		"ip netns add wmns-" + kernelID,
		"ip link add wh-" + kernelID + " type veth peer name wn-" + kernelID,
		"ipset create wm-" + kernelID + "-allow",
		"iptables -w -N WMF_" + kernelID,
		"/etc/watermelon/process-" + kernelID + "-dns.conf",
		"/run/watermelon-process-" + kernelID + "-dns.pid",
		"/usr/local/bin/" + processName,
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("generated config missing stable process identifier use %q", want)
		}
	}
	if strings.Contains(yaml, "veth-"+processName) || strings.Contains(yaml, "watermelon-"+processName+"-allow") {
		t.Error("raw process name leaked into a kernel identifier")
	}

	_, err = buildProcessKernelIDs([]string{"one", "two"}, func(string) string { return "collision" })
	if err == nil || !strings.Contains(err.Error(), "colliding kernel identifiers") {
		t.Fatalf("buildProcessKernelIDs() collision error = %v, want collision rejection", err)
	}
}

func TestGenerateConfigNetworkNamespaceSetup(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com", "*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for veth pair setup
	if !strings.Contains(yaml, "ip link add") {
		t.Error("expected yaml to contain veth pair creation")
	}

	// Check for namespace network config
	if !strings.Contains(yaml, "ip netns exec wmns-"+processKernelID("claude")) {
		t.Error("expected yaml to contain namespace execution")
	}

	// Check for iptables in namespace
	if !strings.Contains(yaml, "api.anthropic.com") {
		t.Error("expected yaml to contain process-specific domain")
	}

	// Check that wildcards are NOT passed directly to iptables (iptables doesn't support wildcard syntax)
	if strings.Contains(yaml, "iptables -A OUTPUT -d *.anthropic.com") {
		t.Error("wildcard domain should NOT appear in direct iptables rules")
	}
}

func TestGenerateConfigWrapperScripts(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for wrapper script creation
	if !strings.Contains(yaml, "/usr/local/bin/claude") {
		t.Error("expected yaml to contain wrapper script path")
	}

	// Check wrapper uses namespace
	if !strings.Contains(yaml, "ip netns exec wmns-"+processKernelID("claude")) {
		t.Error("expected wrapper to use network namespace")
	}
}

func TestGenerateConfigDnsmasqForWildcards(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check for dnsmasq config
	if !strings.Contains(yaml, "dnsmasq") {
		t.Error("expected yaml to contain dnsmasq setup")
	}

	// Check for ipset configuration in dnsmasq
	if !strings.Contains(yaml, "ipset=/*.anthropic.com/") {
		t.Error("expected yaml to contain ipset dnsmasq rule")
	}
}

func TestGenerateConfigNetworkProcessHeredocIndentation(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Heredoc bodies must be indented to remain valid YAML inside script: |
	if !strings.Contains(yaml, "cat > /etc/watermelon/process-"+processKernelID("claude")+"-dns.conf << 'DNSCONF'\n      # dnsmasq config for claude") {
		t.Error("expected DNS heredoc body to be indented in generated YAML")
	}
	// Wrapper heredoc uses NSWRAPPER (unquoted for variable expansion) or WRAPPER (quoted)
	if !strings.Contains(yaml, "/usr/local/bin/claude") {
		t.Error("expected wrapper to target /usr/local/bin/claude")
	}
}

func TestGenerateConfigEmptyNetworkProcess(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	// Network.Process is empty (default)

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should NOT contain namespace setup
	if strings.Contains(yaml, "ip netns add") {
		t.Error("expected yaml to NOT contain namespace setup when NetworkProcess is empty")
	}

	// Should still have regular iptables
	if !strings.Contains(yaml, "registry.npmjs.org") {
		t.Error("expected yaml to contain general network allow rules")
	}
}

func TestGenerateConfigEmptyAllowListStillInstallsDefaultDeny(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "-j LOG --log-prefix \"watermelon-net \"") {
		t.Error("expected fail mode to log unknown traffic even when allow list is empty")
	}
	if !strings.Contains(yaml, "iptables -w -A WM_OUTPUT -j REJECT") {
		t.Error("expected empty allow list to still install default-deny REJECT rule")
	}
}

func TestGenerateConfigEnforcementModes(t *testing.T) {
	tests := []struct {
		mode       string
		wantLog    bool
		wantReject bool
	}{
		{mode: "log", wantLog: true, wantReject: false},
		{mode: "fail", wantLog: true, wantReject: true},
		{mode: "silent", wantLog: false, wantReject: true},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = tt.mode
			cfg.Network.Allow = []string{"registry.npmjs.org"}

			yaml, err := GenerateConfig(cfg, "/test/project")
			if err != nil {
				t.Fatalf("failed to generate: %v", err)
			}

			hasLog := strings.Contains(yaml, "-j LOG --log-prefix \"watermelon-net \"")
			if hasLog != tt.wantLog {
				t.Errorf("LOG rule presence = %v, want %v", hasLog, tt.wantLog)
			}

			hasReject := strings.Contains(yaml, "iptables -w -A WM_OUTPUT -j REJECT")
			if hasReject != tt.wantReject {
				t.Errorf("REJECT rule presence = %v, want %v", hasReject, tt.wantReject)
			}

			hasLogWriter := strings.Contains(yaml, "watermelon-log-writer.service")
			if hasLogWriter != tt.wantLog {
				t.Errorf("log writer presence = %v, want %v", hasLogWriter, tt.wantLog)
			}
		})
	}
}

func TestGenerateConfigDomainWithPort(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"example.com:443"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "iptables -w -A WM_OUTPUT -p tcp --dport 443 -d example.com -j ACCEPT") {
		t.Error("expected domain-with-port to render as tcp dport + domain accept rule")
	}
	if strings.Contains(yaml, "-d example.com:443") {
		t.Error("domain-with-port must not be rendered as a raw iptables destination")
	}
}

func TestGenerateConfigMounts(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Mounts = map[string]config.Mount{
		"/Users/test/.gitconfig": {Target: "/mnt/watermelon/gitconfig"},
		"/Users/test/cache":      {Target: "/mnt/watermelon/./cache", Mode: "rw"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	checks := []string{
		`location: "/Users/test/.gitconfig"`,
		`mountPoint: "/mnt/watermelon/gitconfig"`,
		"writable: false",
		`location: "/Users/test/cache"`,
		`mountPoint: "/mnt/watermelon/cache"`,
		"writable: true",
	}

	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("expected yaml to contain %q", check)
		}
	}
}

func TestGenerateConfigWithProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim":     {"node", "npm"},
		"python:3.12-slim": {"python", "pip"},
	}
	cfg.Provision.Npm = []string{"@anthropic-ai/claude-code", "typescript"}
	cfg.Provision.Pip = []string{"aider-chat"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Check custom image build for npm
	if !strings.Contains(yaml, `nerdctl run --name watermelon-npm-build --network=host "node:20-slim"`) {
		t.Error("expected yaml to build custom npm image from base image")
	}
	if !strings.Contains(yaml, "npm install -g @anthropic-ai/claude-code typescript") {
		t.Error("expected yaml to install npm packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-npm-build watermelon-npm") {
		t.Error("expected yaml to commit custom npm image")
	}

	// Check custom image build for pip
	if !strings.Contains(yaml, `nerdctl run --name watermelon-pip-build --network=host "python:3.12-slim"`) {
		t.Error("expected yaml to build custom pip image from base image")
	}
	if !strings.Contains(yaml, "pip install aider-chat") {
		t.Error("expected yaml to install pip packages in custom image")
	}

	// Check tool wrappers use custom images
	if !strings.Contains(yaml, "watermelon-npm npm") {
		t.Error("expected npm wrapper to use custom watermelon-npm image")
	}
	if !strings.Contains(yaml, "watermelon-pip pip") {
		t.Error("expected pip wrapper to use custom watermelon-pip image")
	}

	// Check binary discovery section exists
	if !strings.Contains(yaml, "/usr/bin/comm -z -13") {
		t.Error("expected yaml to contain binary discovery logic")
	}
	if !strings.Contains(yaml, "wm_nerdctl()") {
		t.Error("expected yaml to define rootless nerdctl helper for provisioning")
	}

	// Ensure wrappers exist for provisioned package commands even if base image already has them
	if !strings.Contains(yaml, "for _bin in claude-code typescript; do") {
		t.Error("expected yaml to ensure wrappers for npm provisioned package commands")
	}
}

func TestNpmPackageToCommand(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"pnpm", "pnpm"},
		{"pnpm@10", "pnpm"},
		{"@scope/name", "name"},
		{"@scope/name@1.2.3", "name"},
		{"", ""},
	}

	for _, tc := range tests {
		got := npmPackageToCommand(tc.in)
		if got != tc.want {
			t.Errorf("npmPackageToCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateConfigSmartWrapperYamlIndentation(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Regression check: smart-wrapper heredoc body must remain indented
	// inside the YAML block scalar.
	if strings.Contains(yaml, "\n#!/bin/bash\n# Base images are pulled") {
		t.Error("expected smart wrapper script body to be indented in YAML")
	}
	if !strings.Contains(yaml, "\n      #!/bin/bash\n      # Base images are pulled") {
		t.Error("expected indented smart wrapper script body in YAML output")
	}
}

func TestGenerateConfigEmptyProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	// Provision is empty (default)

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should NOT contain any custom image builds or provision commands
	if strings.Contains(yaml, "watermelon-npm") {
		t.Error("expected yaml to NOT contain watermelon-npm when Provision.Npm is empty")
	}
	if strings.Contains(yaml, "watermelon-pip") {
		t.Error("expected yaml to NOT contain watermelon-pip when Provision.Pip is empty")
	}
	if strings.Contains(yaml, "watermelon-cargo") {
		t.Error("expected yaml to NOT contain watermelon-cargo when Provision.Cargo is empty")
	}
	if strings.Contains(yaml, "watermelon-go") {
		t.Error("expected yaml to NOT contain watermelon-go when Provision.Go is empty")
	}
	if strings.Contains(yaml, "watermelon-gem") {
		t.Error("expected yaml to NOT contain watermelon-gem when Provision.Gem is empty")
	}
}

func TestGenerateConfigWithCargoProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"rust:latest": {"cargo", "rustc"},
	}
	cfg.Provision.Cargo = []string{"ripgrep", "fd-find"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "cargo install ripgrep fd-find") {
		t.Error("expected yaml to install cargo packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-cargo-build watermelon-cargo") {
		t.Error("expected yaml to commit custom cargo image")
	}
	// Tool wrappers should use custom image
	if !strings.Contains(yaml, "watermelon-cargo cargo") {
		t.Error("expected cargo wrapper to use custom watermelon-cargo image")
	}
}

func TestGenerateConfigWithGoProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"golang:1.22": {"go"},
	}
	cfg.Provision.Go = []string{"github.com/junegunn/fzf@latest"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "go install github.com/junegunn/fzf@latest") {
		t.Error("expected yaml to install go packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-go-build watermelon-go") {
		t.Error("expected yaml to commit custom go image")
	}
}

func TestGenerateConfigWithGemProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"ruby:3.2": {"ruby", "gem"},
	}
	cfg.Provision.Gem = []string{"rails", "bundler"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(yaml, "gem install rails bundler") {
		t.Error("expected yaml to install gem packages in custom image")
	}
	if !strings.Contains(yaml, "nerdctl commit watermelon-gem-build watermelon-gem") {
		t.Error("expected yaml to commit custom gem image")
	}
}

func TestGenerateConfigProvisionRequiresToolImage(t *testing.T) {
	cfg := config.NewConfig()
	// No tools configured, but provision.npm is set
	cfg.Provision.Npm = []string{"pnpm"}

	_, err := GenerateConfig(cfg, "/test/project")
	if err == nil {
		t.Fatal("expected error when provision.npm is set without npm in [tools]")
	}
	if !strings.Contains(err.Error(), "provision.npm requires") {
		t.Errorf("expected error about missing npm tool, got: %v", err)
	}
}

func TestGenerateConfigProvisionRejectsInvalidPackageName(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"pnpm; rm -rf /"}

	_, err := GenerateConfig(cfg, "/test/project")
	if err == nil {
		t.Fatal("expected error for invalid package name")
	}
	if !strings.Contains(err.Error(), "invalid npm package") {
		t.Errorf("expected error about invalid package, got: %v", err)
	}
}

func TestGenerateConfigRejectsUnsafeDerivedProvisionCommand(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"@scope/.."}

	_, err := GenerateConfig(cfg, "/test/project")
	if err == nil || !strings.Contains(err.Error(), "invalid exposed npm command") {
		t.Fatalf("GenerateConfig() error = %v, want unsafe derived command rejection", err)
	}
}

func TestGenerateConfigValidatesDiscoveredProvisionCommandsBeforePathUse(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"typescript"}

	generated, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	validation := `case "$_bin" in`
	firstPathUse := `[ -f "/usr/local/bin/$_bin" ]`
	assertRenderedBefore(t, generated, validation, firstPathUse)
	for _, want := range []string{
		`read -r -d '' _bin`,
		`""|.|..|*[!A-Za-z0-9._+-]*`,
		`/usr/bin/comm -z -13`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated provision discovery missing %q", want)
		}
	}
}

func TestGenerateConfigNetworkProcessWithContainerizedTool(t *testing.T) {
	// Reproduces the flip-to-survive config: claude is installed via npm provision
	// and also has a network.process entry. The nerdctl wrapper must:
	// 1. Use the process's hashed network namespace (not --network=host)
	// 2. Mount the namespace's resolv.conf so DNS goes through dnsmasq
	// 3. Extract the container image from the existing nerdctl wrapper
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"*.anthropic.com"},
	}
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm", "npx"},
	}
	cfg.Provision.Npm = []string{"pnpm", "@anthropic-ai/claude-code"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}
	kernelID := processKernelID("claude")

	// The wrapper should detect containerized tools and extract the image name
	if !strings.Contains(yaml, "_WM_PROC_IMAGE") {
		t.Error("expected yaml to extract container image from existing wrapper")
	}

	// The wrapper should use rootful nerdctl with --network=ns:<path>
	if !strings.Contains(yaml, "/usr/local/libexec/watermelon/nerdctl --address /run/containerd/containerd.sock") {
		t.Error("expected fixed helper to use the verified rootful nerdctl")
	}
	if !strings.Contains(yaml, "--network=ns:/var/run/netns/wmns-"+kernelID) {
		t.Error("expected yaml to use --network=ns:<path> for namespace containers")
	}

	// FORWARD chain should have per-process filter chain
	if !strings.Contains(yaml, "iptables -w -I FORWARD 1 -i wh-"+kernelID) {
		t.Error("expected yaml to contain FORWARD chain rules for veth traffic")
	}

	// ipset and dnsmasq run in root namespace (ipset + nf_tables don't work in namespaces)
	if !strings.Contains(yaml, "ipset create wm-"+kernelID+"-allow") {
		t.Error("expected yaml to create ipset for wildcard domain matching")
	}
	if !strings.Contains(yaml, "dnsmasq --conf-file=/etc/watermelon/process-"+kernelID+"-dns.conf") {
		t.Error("expected yaml to start dnsmasq for wildcard DNS resolution")
	}

	// apt-get install must appear BEFORE iptables lockdown
	aptGetPos := strings.Index(yaml, "apt-get update && apt-get install -y dnsmasq-base ipset")
	iptablesRejectPos := strings.Index(yaml, "iptables -w -A WM_OUTPUT -j REJECT")
	if aptGetPos < 0 {
		t.Fatal("expected yaml to contain apt-get install for dnsmasq")
	}
	if iptablesRejectPos < 0 {
		t.Fatal("expected yaml to contain iptables REJECT rule")
	}
	if aptGetPos > iptablesRejectPos {
		t.Error("apt-get install must appear BEFORE iptables REJECT rule to avoid being blocked by firewall")
	}

	// Explicit image copy to rootful store (not dynamic discovery)
	if !strings.Contains(yaml, `wm_nerdctl save "watermelon-npm" | wm_root_nerdctl load`) {
		t.Error("expected yaml to explicitly copy watermelon-npm image to rootful store")
	}
	// Image copy is a required bootstrap step and must fail under pipefail.
	if strings.Contains(yaml, `|| echo "warning: failed to copy image`) {
		t.Error("required rootful image copy must not swallow failures")
	}

	// Should also have a fallback for non-containerized tools
	if !strings.Contains(yaml, "ip netns exec wmns-"+kernelID) {
		t.Error("expected yaml to have ip netns exec fallback for non-containerized tools")
	}
}

func TestGenerateConfigUsesNarrowRootOwnedProcessHelpers(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Network.Process = map[string][]string{
		"node": {"api.nodejs.org"},
	}

	generated, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	kernelID := processKernelID("node")
	helperPath := "/usr/local/libexec/watermelon/process-" + kernelID

	deny := `$_WM_USER ALL=(ALL:ALL) !ALL`
	grant := `$_WM_USER ALL=(root:root) NOPASSWD:NOSETENV: ` + helperPath
	assertRenderedBefore(t, generated, deny, grant)
	for _, forbidden := range []string{
		"NOPASSWD: ALL",
		"sudo nerdctl",
		"sudo tee",
		"sudo chmod",
		"sudo -iu",
		"exec sudo ",
	} {
		if strings.Contains(generated, forbidden) {
			t.Errorf("generated policy retained unsafe sudo form %q", forbidden)
		}
	}
	if got := strings.Count(generated, `/usr/bin/sudo -n -- `+helperPath+` "$@"`); got != 1 {
		t.Errorf("fixed public sudo wrapper count = %d, want 1", got)
	}

	for _, want := range []string{
		`/usr/bin/env -i HOME=/var/empty XDG_CONFIG_HOME=/var/empty PATH=/usr/sbin:/usr/bin:/sbin:/bin`,
		`/usr/local/libexec/watermelon/nerdctl --address /run/containerd/containerd.sock --namespace default`,
		`--pull=never --network=ns:/var/run/netns/wmns-` + kernelID,
		`--dns 10.200.1.1`,
		`--user $_WM_UID:$_WM_GID`,
		`--cap-drop=ALL`,
		`--security-opt=no-new-privileges`,
		`-v /project:/project -w /project -- "$_WM_PROC_IMAGE" "node" "\$@"`,
		`/usr/sbin/ip netns exec wmns-` + kernelID,
		`/usr/bin/setpriv --reuid=$_WM_UID --regid=$_WM_GID --init-groups`,
		`--no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all`,
		`/bin/mv -f "$_WM_HELPER_TMP" ` + helperPath,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("fixed process helper missing %q", want)
		}
	}

	helperInstalled := strings.LastIndex(generated, `/bin/mv -f "$_WM_HELPER_TMP" `+helperPath)
	visudoValidated := strings.LastIndex(generated, "/usr/sbin/visudo -cf /etc/sudoers")
	policyMarker := strings.LastIndex(generated, "touch /run/watermelon-policy-applied")
	if helperInstalled < 0 || visudoValidated < helperInstalled || policyMarker < visudoValidated {
		t.Fatalf("unsafe helper publication order: helper=%d visudo=%d marker=%d", helperInstalled, visudoValidated, policyMarker)
	}

	containerHelper := extractHeredocBody(t, generated, "<< CONTAINERHELPER", "CONTAINERHELPER")
	nativeHelper := extractHeredocBody(t, generated, "<< NATIVEHELPER", "NATIVEHELPER")
	for name, helper := range map[string]string{"container": containerHelper, "native": nativeHelper} {
		if !strings.HasPrefix(helper, "#!/bin/bash -p\n") {
			t.Errorf("%s helper does not use privileged Bash mode", name)
		}
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(helper)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated %s helper is not valid Bash: %v\n%s", name, err, output)
		}
	}

	if visudo, err := exec.LookPath("visudo"); err == nil {
		sudoers := extractHeredocBody(t, generated, "<< SUDOERS", "SUDOERS")
		sudoers = strings.ReplaceAll(sudoers, "$_WM_USER", "watermelon")
		path := filepath.Join(t.TempDir(), "99-watermelon")
		if err := os.WriteFile(path, []byte(sudoers), 0440); err != nil {
			t.Fatalf("writing generated sudoers fixture: %v", err)
		}
		if output, err := exec.Command(visudo, "-cf", path).CombinedOutput(); err != nil {
			t.Fatalf("generated sudoers policy is invalid: %v\n%s", err, output)
		}
	}
}

func TestGenerateConfigDeterminesFixedVMIdentityWithoutTools(t *testing.T) {
	generated, err := GenerateConfig(config.NewConfig(), "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	for _, want := range []string{
		`name: "watermelon"`,
		`home: "/home/watermelon"`,
		`_WM_USER=watermelon`,
		`_WM_UID=$(/usr/bin/id -u "$_WM_USER")`,
		`_WM_GID=$(/usr/bin/id -g "$_WM_USER")`,
		`if [ "$_WM_UID" != "1000" ] || [ "$_WM_GID" = 0 ]; then`,
		`_WM_HOME=$(/usr/bin/getent passwd "$_WM_USER"`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("fixed VM identity setup missing %q", want)
		}
	}
}

func TestGenerateConfigPrivilegedScriptsIgnoreToolNameCollisions(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Tools = map[string][]string{
		"alpine:3.20": {
			"awk", "cat", "iptables", "nerdctl", "sudo", "systemctl",
			"watermelon-log-writer", "watermelon-nfqd",
		},
	}
	cfg.Network.Process = map[string][]string{
		"cat": {"example.com"},
	}

	generated, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	systemScript := extractSystemProvisionScript(t, generated)
	trustedPath := "export PATH=/usr/sbin:/usr/bin:/sbin:/bin"
	if !strings.HasPrefix(systemScript, "#!/bin/bash\nset -e\nset -o pipefail\n"+trustedPath+"\n") {
		t.Fatalf("root provision does not establish its trusted PATH first:\n%s", systemScript[:min(len(systemScript), 240)])
	}
	earlyScript := extractHeredocBody(t, generated, "<< 'EARLYFIREWALL'", "EARLYFIREWALL")
	assertRenderedBefore(t, earlyScript, "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "iptables -w -P OUTPUT DROP")
	logWriter := extractHeredocBody(t, generated, "<< 'LOGWRITER'", "LOGWRITER")
	assertRenderedBefore(t, logWriter, trustedPath, "mkdir -p /project/.watermelon")

	for _, want := range []string{
		`_WM_NERDCTL=/usr/local/libexec/watermelon/nerdctl`,
		`ExecStart=/usr/local/libexec/watermelon/log-writer`,
		`User=watermelon`,
		`SupplementaryGroups=systemd-journal`,
		`exec /usr/bin/sudo -n --`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("privileged path hardening missing %q", want)
		}
	}
	if strings.Contains(generated, "ExecStart=/usr/local/bin/") {
		t.Error("a privileged systemd service still executes from the user-wrapper directory")
	}
	assertRenderedBefore(t, generated,
		`if [ -f /var/lib/watermelon/trusted-bootstrap-complete ]; then`,
		`for _WM_NERDCTL_CANDIDATE in /usr/local/bin/nerdctl /usr/bin/nerdctl`,
	)

	askCfg := config.NewConfig()
	askCfg.Security.Enforcement = "ask"
	askCfg.Tools = cfg.Tools
	askGenerated, err := generateConfigForTest(askCfg, "/test/project")
	if err != nil {
		t.Fatalf("ask GenerateConfigForInstance() error = %v", err)
	}
	if !strings.Contains(askGenerated, "ExecStart=/usr/local/libexec/watermelon/nfqd") {
		t.Error("NFQUEUE sidecar is not executed from the protected libexec directory")
	}
	if strings.Contains(askGenerated, "ExecStart=/usr/local/bin/watermelon-nfqd") {
		t.Error("NFQUEUE sidecar remains vulnerable to a tool-wrapper name collision")
	}
}

func TestGenerateConfigSmartWrappers(t *testing.T) {
	t.Run("npm wrapper detects -g/--global", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim": {"node", "npm"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_npm") {
			t.Error("expected yaml to contain smart npm wrapper heredoc")
		}
		if !strings.Contains(yaml, "-g|--global") {
			t.Error("expected npm smart wrapper to detect -g/--global flags")
		}
		if !strings.Contains(yaml, "nerdctl commit") {
			t.Error("expected smart wrapper to use nerdctl commit")
		}
		if !strings.Contains(yaml, `nerdctl tag "node:20-slim" "watermelon-npm"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("pip wrapper detects install subcommand", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"python:3.12-slim": {"python", "pip"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_pip") {
			t.Error("expected yaml to contain smart pip wrapper heredoc")
		}
		if !strings.Contains(yaml, `case "$1" in install)`) {
			t.Error("expected pip smart wrapper to detect install subcommand")
		}
		if !strings.Contains(yaml, `nerdctl tag "python:3.12-slim" "watermelon-pip"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("cargo wrapper with correct bin dirs", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"rust:latest": {"cargo", "rustc"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_cargo") {
			t.Error("expected yaml to contain smart cargo wrapper heredoc")
		}
		if !strings.Contains(yaml, "newly installed executables are not exposed automatically") {
			t.Error("expected cargo wrapper to explain how newly installed commands are exposed")
		}
	})

	t.Run("go wrapper with correct bin dirs", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"golang:1.22": {"go"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_go") {
			t.Error("expected yaml to contain smart go wrapper heredoc")
		}
		if !strings.Contains(yaml, "newly installed executables are not exposed automatically") {
			t.Error("expected go wrapper to explain how newly installed commands are exposed")
		}
	})

	t.Run("gem wrapper is generated", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"ruby:3.2": {"ruby", "gem"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_gem") {
			t.Error("expected yaml to contain smart gem wrapper heredoc")
		}
		if !strings.Contains(yaml, `nerdctl tag "ruby:3.2" "watermelon-gem"`) {
			t.Error("expected smart wrapper to reference base image for tagging")
		}
	})

	t.Run("all package managers get wrappers when present", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim":     {"node", "npm"},
			"python:3.12-slim": {"python", "pip"},
			"rust:latest":      {"cargo", "rustc"},
			"golang:1.22":      {"go"},
			"ruby:3.2":         {"ruby", "gem"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		for _, cmd := range []string{"npm", "pip", "cargo", "go", "gem"} {
			if !strings.Contains(yaml, "WATERMELON_SMART_WRAPPER_"+cmd) {
				t.Errorf("expected yaml to contain smart wrapper for %s", cmd)
			}
		}
	})

	t.Run("non-package-manager tools do not get smart wrappers", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Tools = map[string][]string{
			"node:20-slim": {"node"},
		}

		yaml, err := GenerateConfig(cfg, "/test/project")
		if err != nil {
			t.Fatalf("failed to generate: %v", err)
		}

		if strings.Contains(yaml, "WATERMELON_SMART_WRAPPER") {
			t.Error("expected yaml to NOT contain any smart wrappers for non-package-manager tools")
		}
	})
}

func TestGenerateConfigNoSmartWrapperWithoutPackageManagers(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Tools = map[string][]string{
		"alpine:latest": {"sh"},
	}

	yaml, err := generateConfigForTest(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if strings.Contains(yaml, "WATERMELON_SMART_WRAPPER") {
		t.Error("expected yaml to NOT contain smart wrappers when no package managers are in tools")
	}
}

func TestGenerateConfigAskMode(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "ask"
	cfg.Network.Allow = []string{"registry.npmjs.org"}

	yaml, err := generateConfigForTest(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should use NFQUEUE for TCP SYN catch-all
	if !strings.Contains(yaml, "NFQUEUE") {
		t.Error("expected yaml to contain NFQUEUE rules for ask mode")
	}

	// Should still ACCEPT allowed domains
	if !strings.Contains(yaml, "registry.npmjs.org") {
		t.Error("expected yaml to still accept allowed domains")
	}

	// Should install libnetfilter-queue
	if !strings.Contains(yaml, "libnetfilter-queue") {
		t.Error("expected yaml to install libnetfilter-queue package")
	}

	// Should set up nfqd systemd service
	if !strings.Contains(yaml, "watermelon-nfqd") {
		t.Error("expected yaml to set up watermelon-nfqd service")
	}

	// Should allow traffic to verdict server on gateway (fallback port)
	if !strings.Contains(yaml, "39285") {
		t.Error("expected yaml to allow traffic to verdict server port (fallback)")
	}

	// Should copy the nfqd binary
	if !strings.Contains(yaml, "/project/.watermelon/bin/watermelon-nfqd") {
		t.Error("expected yaml to copy nfqd binary from project mount")
	}
	for _, want := range []string{
		`/usr/bin/sha256sum "$_WM_NFQD_TMP"`,
		`if [ "$_WM_NFQD_ACTUAL" != "` + testNfqdSHA256 + `" ]; then`,
		`/usr/local/libexec/watermelon/nfqd`,
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("ask sidecar installation missing %q", want)
		}
	}
	assertRenderedBefore(t, yaml, `if [ "$_WM_NFQD_ACTUAL" !=`, `/bin/mv -f "$_WM_NFQD_TMP" /usr/local/libexec/watermelon/nfqd`)
	mismatch := strings.Index(yaml, `if [ "$_WM_NFQD_ACTUAL" !=`)
	move := strings.Index(yaml, `/bin/mv -f "$_WM_NFQD_TMP" /usr/local/libexec/watermelon/nfqd`)
	exitOnMismatch := -1
	if mismatch >= 0 && move > mismatch {
		exitOnMismatch = strings.Index(yaml[mismatch:move], "exit 1")
	}
	if exitOnMismatch < 0 {
		t.Error("an nfqd digest mismatch does not fail before publishing the privileged executable")
	}
}

func TestGenerateConfigAskModeRequiresValidNfqdDigest(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "ask"

	for _, digest := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		t.Run(strconv.Quote(digest), func(t *testing.T) {
			_, err := GenerateConfigForInstance(cfg, "/test/project", nil, GenerateOptions{NfqdSHA256: digest})
			if err == nil || !strings.Contains(err.Error(), "lowercase SHA-256") {
				t.Fatalf("GenerateConfigForInstance() error = %v, want digest rejection", err)
			}
		})
	}

	if _, err := GenerateConfig(cfg, "/test/project"); err == nil {
		t.Fatal("legacy convenience API generated ask mode without an authenticated sidecar")
	}
}

func TestGenerateConfigGlobalWildcardDomains(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org", "*.anthropic.com", "*.openai.com"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should create a global ipset
	if !strings.Contains(yaml, "ipset create watermelon-global-allow hash:ip") {
		t.Error("expected yaml to create global ipset for wildcard matching")
	}

	// Should configure dnsmasq with ipset rules for each wildcard
	if !strings.Contains(yaml, "ipset=/*.anthropic.com/watermelon-global-allow") {
		t.Error("expected yaml to configure dnsmasq ipset for anthropic.com wildcard")
	}
	if !strings.Contains(yaml, "ipset=/*.openai.com/watermelon-global-allow") {
		t.Error("expected yaml to configure dnsmasq ipset for openai.com wildcard")
	}

	// Should have iptables rule matching ipset
	if !strings.Contains(yaml, "iptables -w -A WM_OUTPUT -m set --match-set watermelon-global-allow dst -j ACCEPT") {
		t.Error("expected yaml to have iptables ipset match rule for global wildcards")
	}

	// Non-wildcard domains should still be direct iptables rules
	if !strings.Contains(yaml, "iptables -w -A WM_OUTPUT -d registry.npmjs.org -j ACCEPT") {
		t.Error("expected non-wildcard domains to still use direct iptables rules")
	}

	// Should install dnsmasq-base when no NetworkProcess
	if !strings.Contains(yaml, "apt-get update && apt-get install -y dnsmasq-base ipset") {
		t.Error("expected yaml to install dnsmasq-base and ipset for global wildcard domains")
	}
}

func TestGenerateConfigNoGlobalWildcardWithoutWildcards(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org", "github.com"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	// Should NOT set up global ipset when no wildcard domains exist
	if strings.Contains(yaml, "watermelon-global-allow") {
		t.Error("expected yaml to NOT set up global ipset when no wildcard domains exist")
	}
}

func TestGenerateConfigGlobalWildcardsInProcessChains(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org", "*.example.com"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	kernelID := processKernelID("claude")
	// Per-process dnsmasq should include global wildcard domains
	if !strings.Contains(yaml, "ipset=/*.example.com/wm-"+kernelID+"-allow") {
		t.Error("expected per-process dnsmasq to include global wildcard domain *.example.com")
	}

	// Non-wildcard global allows should still appear as direct iptables rules in FORWARD chain
	if !strings.Contains(yaml, "iptables -w -A WMF_"+kernelID+" -d registry.npmjs.org -j ACCEPT") {
		t.Error("expected per-process FORWARD chain to include non-wildcard global allow")
	}
}

func TestGenerateConfigDeterministicSubnets(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"aider":  {"api.openai.com"},
		"claude": {"api.anthropic.com"},
		"codex":  {"api.openai.com"},
	}

	// Generate twice and compare
	yaml1, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	yaml2, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}

	if yaml1 != yaml2 {
		t.Error("expected two generations of the same config to produce identical output")
	}
}

func TestGenerateConfigNonAskModeNoNFQUEUE(t *testing.T) {
	for _, mode := range []string{"log", "fail", "silent"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = mode
			cfg.Network.Allow = []string{"registry.npmjs.org"}

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("failed to generate: %v", err)
			}

			if strings.Contains(yaml, "NFQUEUE") {
				t.Errorf("expected yaml to NOT contain NFQUEUE for %q enforcement", mode)
			}
			if strings.Contains(yaml, "libnetfilter-queue") {
				t.Errorf("expected yaml to NOT install nfqueue packages for %q enforcement", mode)
			}
		})
	}
}

func TestGenerateConfigTrustedBootstrapPrecedesPolicyProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"typescript"}
	cfg.Network.Process = map[string][]string{
		"typescript": {"*.typescriptlang.org"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	pull := `wm_nerdctl pull "node:20-slim"`
	policy := `iptables -w -I OUTPUT 1 -j WM_OUTPUT`
	build := `wm_nerdctl run --name watermelon-npm-build --network=host "node:20-slim"`
	copyImage := `wm_nerdctl save "watermelon-npm" | wm_root_nerdctl load`
	processPolicy := `ip netns add wmns-` + processKernelID("typescript")
	assertRenderedBefore(t, yaml, pull, policy)
	assertRenderedBefore(t, yaml, policy, build)
	assertRenderedBefore(t, yaml, build, copyImage)
	assertRenderedBefore(t, yaml, copyImage, processPolicy)

	if strings.Contains(yaml, `wm_nerdctl pull "watermelon-npm"`) {
		t.Error("trusted bootstrap must pull the configured base image, not a not-yet-built custom tag")
	}
	if !strings.Contains(yaml, `wm_nerdctl run --rm --network=none "watermelon-npm"`) {
		t.Error("post-build binary inspection should not have network access")
	}
}

func TestGenerateConfigSeparatesFirstBootstrapFromPerBootPolicy(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"registry.npmjs.org", "*.example.com"}
	cfg.Network.Process = map[string][]string{
		"node": {"*.nodejs.org"},
	}
	cfg.Tools = map[string][]string{
		"node:20-slim": {"node", "npm"},
	}
	cfg.Provision.Npm = []string{"typescript"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	checks := []string{
		"if [ -f /run/watermelon-policy-applied ]; then",
		"if [ ! -f /var/lib/watermelon/bootstrap-complete ]; then",
		"if [ ! -f /var/lib/watermelon/trusted-bootstrap-complete ]; then",
		"systemctl stop watermelon-dns.service",
		"rm -f /etc/systemd/resolved.conf.d/watermelon.conf",
		"systemctl restart watermelon-dns.service",
		"touch /var/lib/watermelon/bootstrap-complete",
		"touch /run/watermelon-policy-applied",
	}
	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("per-boot provisioning guard missing %q", check)
		}
	}

	assertRenderedBefore(t, yaml,
		"rm -f /etc/systemd/resolved.conf.d/watermelon.conf",
		`wm_nerdctl pull "node:20-slim"`,
	)
	assertRenderedBefore(t, yaml,
		"systemctl restart watermelon-dns.service",
		`wm_nerdctl run --name watermelon-npm-build --network=host`,
	)
	assertRenderedBefore(t, yaml,
		`wm_nerdctl run --name watermelon-npm-build --network=host`,
		"touch /var/lib/watermelon/bootstrap-complete",
	)
	assertRenderedBefore(t, yaml,
		"ip netns add wmns-"+processKernelID("node"),
		"touch /run/watermelon-policy-applied",
	)

	if strings.Contains(yaml, "systemctl enable --now watermelon-dns.service") {
		t.Error("managed resolver must be explicitly restarted after its files are regenerated")
	}
}

func TestGenerateConfigEarlyFirewallLifecycle(t *testing.T) {
	for _, mode := range []string{"fail", "silent", "ask"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = mode

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			for _, want := range []string{
				"DefaultDependencies=no",
				"Wants=local-fs.target network-pre.target",
				"After=local-fs.target systemd-modules-load.service systemd-sysctl.service",
				"Before=network-pre.target shutdown.target",
				"ConditionPathExists=/var/lib/watermelon/trusted-bootstrap-complete",
				"WantedBy=sysinit.target",
				"systemd-networkd.service.d/watermelon-early-firewall.conf",
				"Requires=watermelon-early-firewall.service",
				"After=watermelon-early-firewall.service",
				"iptables -w -P OUTPUT DROP",
				"iptables -w -P FORWARD DROP",
				"ip6tables -w -P OUTPUT DROP",
				"ip6tables -w -P FORWARD DROP",
				"iptables -w -A WM_EARLY -o lo -j ACCEPT",
				"iptables -w -A WM_EARLY -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
				"iptables -w -A WM_EARLY -p udp --sport 68 --dport 67 -d 255.255.255.255 -j ACCEPT",
				"iptables -w -A WM_EARLY -j DROP",
				"systemctl enable watermelon-early-firewall.service",
				"systemctl restart watermelon-early-firewall.service",
			} {
				if !strings.Contains(yaml, want) {
					t.Errorf("enforcing mode missing early-firewall invariant %q", want)
				}
			}
			if strings.Contains(yaml, "systemctl enable --now watermelon-early-firewall.service") {
				t.Error("first trusted bootstrap must not auto-start the early firewall")
			}
			assertRenderedBefore(t, yaml,
				"systemctl restart watermelon-early-firewall.service",
				"iptables -w -I OUTPUT 1 -j WM_OUTPUT",
			)
		})
	}

	cfg := config.NewConfig()
	cfg.Security.Enforcement = "log"
	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	if strings.Contains(yaml, "watermelon-early-firewall") {
		t.Error("log mode must not install an enforcing early firewall")
	}
	if !strings.Contains(yaml, "iptables -w -P OUTPUT ACCEPT") {
		t.Error("log mode must retain an accepting built-in OUTPUT policy")
	}
}

func TestGenerateConfigScopesEveryDHCPException(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	generated, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	var dhcpRules []string
	for _, line := range strings.Split(generated, "\n") {
		if strings.Contains(line, "--sport 68 --dport 67") {
			dhcpRules = append(dhcpRules, strings.TrimSpace(line))
			if !strings.Contains(line, "-d 255.255.255.255") && !strings.Contains(line, `-d "$_WM_DEFAULT_GW"`) {
				t.Errorf("unscoped DHCP exception: %s", line)
			}
		}
	}
	if len(dhcpRules) != 3 {
		t.Fatalf("DHCP exception count = %d, want early broadcast plus steady broadcast/gateway: %v", len(dhcpRules), dhcpRules)
	}
	for _, forbidden := range []string{
		"iptables -w -A WM_EARLY -p udp --sport 68 --dport 67 -j ACCEPT",
		"iptables -w -A WM_OUTPUT -p udp --sport 68 --dport 67 -j ACCEPT",
	} {
		if strings.Contains(generated, forbidden) {
			t.Errorf("generated policy contains unscoped DHCP rule %q", forbidden)
		}
	}
	for _, want := range []string{
		`_WM_DEFAULT_GW=$(/usr/sbin/ip -4 route show default`,
		`if [ -n "$_WM_DEFAULT_GW" ]; then`,
		`-d "$_WM_DEFAULT_GW" -j ACCEPT`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("steady DHCP gateway scoping missing %q", want)
		}
	}
}

func TestGenerateConfigManagedDNSIsExplicitlyStartedAfterConfig(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"*.example.com"}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	start := strings.Index(yaml, "cat > /etc/systemd/system/watermelon-dns.service << 'DNSSERVICE'")
	if start < 0 {
		t.Fatal("generated config missing managed DNS unit")
	}
	endOffset := strings.Index(yaml[start:], "\n      DNSSERVICE")
	if endOffset < 0 {
		t.Fatal("generated config missing managed DNS unit terminator")
	}
	unit := yaml[start : start+endOffset]
	for _, forbidden := range []string{
		"After=network-online.target",
		"Before=systemd-resolved.service",
		"[Install]",
		"WantedBy=",
	} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("managed DNS unit retained cyclic/autostart directive %q", forbidden)
		}
	}
	if strings.Contains(yaml, "systemctl enable watermelon-dns.service") || strings.Contains(yaml, "systemctl enable --now watermelon-dns.service") {
		t.Error("managed DNS must never be enabled for automatic boot startup")
	}
	for _, want := range []string{
		"systemctl disable watermelon-dns.service 2>/dev/null || true",
		"systemctl restart watermelon-dns.service",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("managed DNS explicit lifecycle missing %q", want)
		}
	}
	assertRenderedBefore(t, yaml,
		"ipset flush watermelon-global-allow",
		"systemctl restart watermelon-dns.service",
	)
}

func TestGenerateConfigTrustedBootstrapIsDurableBeforeUserProvision(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Tools = map[string][]string{"node:20-slim": {"node", "npm"}}
	cfg.Provision.Npm = []string{"typescript"}
	cfg.Network.Process = map[string][]string{"node": {"*.nodejs.org"}}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	for _, want := range []string{
		"set -o pipefail",
		"if [ ! -f /var/lib/watermelon/trusted-bootstrap-complete ]; then",
		"_WM_DNS_READY=false",
		"trusted bootstrap DNS did not become ready",
		"if ! getent ahostsv4 \"$_WM_HOST\" | awk",
		"if [ ! -s \"$_WM_CAPTURE\" ]; then",
		"mktemp /etc/watermelon/allowed-hosts.XXXXXX",
		"chmod 0644 \"$_WM_ALLOWED_HOSTS\"",
		"mv -f \"$_WM_ALLOWED_HOSTS\" /etc/watermelon/allowed-hosts",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("trusted bootstrap hardening missing %q", want)
		}
	}

	trustedComplete := strings.LastIndex(yaml, "touch /var/lib/watermelon/trusted-bootstrap-complete")
	captureInstalled := strings.Index(yaml, "mv -f \"$_WM_ALLOWED_HOSTS\" /etc/watermelon/allowed-hosts")
	earlyClosed := strings.Index(yaml, "\n        /usr/local/sbin/watermelon-early-firewall\n")
	policyStart := strings.Index(yaml, "systemctl restart watermelon-early-firewall.service")
	userProvision := strings.Index(yaml, `wm_nerdctl run --name watermelon-npm-build --network=host`)
	finalComplete := strings.LastIndex(yaml, "touch /var/lib/watermelon/bootstrap-complete")
	if trustedComplete < 0 || captureInstalled < 0 || earlyClosed < 0 || policyStart < 0 || userProvision < 0 || finalComplete < 0 {
		t.Fatal("generated config missing trusted/full stage markers")
	}
	if !(captureInstalled < earlyClosed && earlyClosed < trustedComplete && trustedComplete < policyStart && policyStart < userProvision && userProvision < finalComplete) {
		t.Fatalf("stage order is unsafe: capture=%d early=%d trusted=%d policy=%d provision=%d final=%d", captureInstalled, earlyClosed, trustedComplete, policyStart, userProvision, finalComplete)
	}

	trustedBranch := strings.Index(yaml, "if [ ! -f /var/lib/watermelon/trusted-bootstrap-complete ]; then")
	openPolicy := strings.Index(yaml, "wm_delete_jump filter OUTPUT WM_EARLY")
	acceptPolicy := strings.Index(yaml, "iptables -w -P OUTPUT ACCEPT")
	if trustedBranch < 0 || openPolicy < trustedBranch || acceptPolicy < openPolicy || acceptPolicy > trustedComplete {
		t.Error("the intentional open-network reset must be confined to the unfinished trusted stage")
	}
	if strings.Contains(yaml, `|| echo "warning: failed to copy image`) {
		t.Error("required rootful image copies must fail bootstrap")
	}
	if !strings.Contains(yaml, `wm_nerdctl save "watermelon-npm" | wm_root_nerdctl load`) {
		t.Error("test setup did not render the required rootful image copy")
	}
}

func TestGenerateConfigReconcilesOwnedPolicyBeforeRetry(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Process = map[string][]string{"claude": {"*.anthropic.com"}}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	kernelID := processKernelID("claude")

	for _, want := range []string{
		"wm_delete_jump filter OUTPUT WM_OUTPUT",
		"wm_delete_jump nat OUTPUT WM_DNS_OUT",
		"iptables -w -F WM_OUTPUT 2>/dev/null || true",
		"iptables -w -t nat -F WM_DNS_OUT 2>/dev/null || true",
		"while iptables -w -D FORWARD -i wh-" + kernelID,
		"ip netns delete wmns-" + kernelID + " 2>/dev/null || true",
		"iptables -w -N WM_OUTPUT",
		"iptables -w -t nat -N WM_DNS_OUT",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("retry reconciliation missing %q", want)
		}
	}
	assertRenderedBefore(t, yaml,
		"ip netns delete wmns-"+kernelID,
		"ip netns add wmns-"+kernelID,
	)
	assertRenderedBefore(t, yaml,
		"iptables -w -A WM_OUTPUT -j REJECT",
		"iptables -w -I OUTPUT 1 -j WM_OUTPUT",
	)
}

func TestGenerateConfigSystemProvisionIsValidBash(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*config.Config)
	}{
		{name: "empty default fail"},
		{name: "empty log", configure: func(cfg *config.Config) {
			cfg.Security.Enforcement = "log"
		}},
		{name: "empty silent", configure: func(cfg *config.Config) {
			cfg.Security.Enforcement = "silent"
		}},
		{name: "ask digest without tools", configure: func(cfg *config.Config) {
			cfg.Security.Enforcement = "ask"
		}},
		{name: "native process without tools", configure: func(cfg *config.Config) {
			cfg.Network.Process = map[string][]string{
				"curl": {"example.com"},
			}
		}},
		{name: "provisioned container process", configure: func(cfg *config.Config) {
			cfg.Network.Allow = []string{"registry.npmjs.org", "*.example.com"}
			cfg.Network.Process = map[string][]string{
				"node": {"api.nodejs.org", "*.nodejs.org"},
			}
			cfg.Tools = map[string][]string{
				"node:20-slim": {"node", "npm"},
			}
			cfg.Provision.Npm = []string{"typescript"}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewConfig()
			if tt.configure != nil {
				tt.configure(cfg)
			}

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			script := extractSystemProvisionScript(t, yaml)
			if !strings.HasPrefix(script, "#!/bin/bash\n") {
				t.Fatalf("system provision script must select Bash before using pipefail:\n%s", script)
			}
			cmd := exec.Command("bash", "-n")
			cmd.Stdin = strings.NewReader(script)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated %s system provision is not valid bash: %v\n%s", tt.name, err, output)
			}
		})
	}
}

func TestGenerateConfigStrictManagedDNS(t *testing.T) {
	for _, mode := range []string{"fail", "silent"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = mode
			cfg.Network.Allow = []string{"registry.npmjs.org", "*.example.com", "8.8.8.8"}

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			for _, genericAccept := range []string{
				"iptables -w -A WM_OUTPUT -p tcp --dport 53 -j ACCEPT",
				"iptables -w -A WM_OUTPUT -p udp --dport 53 -j ACCEPT",
			} {
				if strings.Contains(yaml, genericAccept) {
					t.Errorf("strict mode rendered unscoped external DNS rule %q", genericAccept)
				}
			}

			checks := []string{
				`useradd --system --user-group`,
				`-m owner --uid-owner "$_WM_DNS_UID" -j ACCEPT`,
				`-m owner ! --uid-owner "$_WM_DNS_UID" -j REDIRECT --to-ports 5354`,
				"iptables -w -A WM_OUTPUT -p udp --dport 53 -j REJECT",
				"no-resolv",
				"addn-hosts=/etc/watermelon/allowed-hosts",
				`wm_capture_dns_host "registry.npmjs.org"`,
				"server=/*.example.com/8.8.8.8",
				"address=/#/",
			}
			for _, check := range checks {
				if !strings.Contains(yaml, check) {
					t.Errorf("strict managed DNS missing %q", check)
				}
			}

			if strings.Contains(yaml, "server=/registry.npmjs.org/") {
				t.Error("exact host must use a captured hosts entry, not suffix-based forwarding")
			}
			if strings.Contains(yaml, "server=/example.com/") {
				t.Error("wildcard DNS forwarding must not include the apex")
			}
			assertRenderedBefore(t, yaml,
				"iptables -w -A WM_OUTPUT -p udp --dport 53 -j REJECT",
				"iptables -w -A WM_OUTPUT -d 8.8.8.8 -j ACCEPT",
			)
		})
	}
}

func TestGenerateConfigDiscoveryDNSModes(t *testing.T) {
	for _, mode := range []string{"log", "ask"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = mode
			cfg.Network.Allow = []string{"registry.npmjs.org"}

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			if !strings.Contains(yaml, "server=8.8.8.8") {
				t.Error("discovery/interactive DNS must forward unknown names through the managed resolver")
			}
			if strings.Contains(yaml, "address=/#/") {
				t.Error("discovery/interactive DNS must not synthesize catch-all NXDOMAIN")
			}
			if !strings.Contains(yaml, "-j REDIRECT --to-ports 5354") {
				t.Error("direct DNS must still be mediated by the managed resolver")
			}
		})
	}
}

func TestGenerateConfigProcessDNSCannotBypassManagedResolver(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Security.Enforcement = "fail"
	cfg.Network.Allow = []string{"registry.npmjs.org"}
	cfg.Network.Process = map[string][]string{
		"claude": {"api.anthropic.com", "*.anthropic.com"},
	}

	yaml, err := GenerateConfig(cfg, "/test/project")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}

	checks := []string{
		"iptables -w -t nat -A PREROUTING -i wh-" + processKernelID("claude") + " -p udp --dport 53 -j DNAT --to-destination 10.200.1.1:53",
		"iptables -w -A WMF_" + processKernelID("claude") + " -p tcp -d 10.200.1.1 --dport 53 -j ACCEPT",
		"iptables -w -A WMF_" + processKernelID("claude") + " -p udp -d 10.200.1.1 --dport 53 -j ACCEPT",
		"addn-hosts=/etc/watermelon/process-" + processKernelID("claude") + "-allowed-hosts",
		"server=/*.anthropic.com/8.8.8.8",
		"ipset=/*.anthropic.com/wm-" + processKernelID("claude") + "-allow",
	}
	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("process managed DNS missing %q", check)
		}
	}
	if strings.Contains(yaml, "iptables -w -A WMF_"+processKernelID("claude")+" -p udp --dport 53 -j ACCEPT") {
		t.Error("process namespace must not have an unscoped DNS ACCEPT rule")
	}
}

func TestGenerateConfigDisablesIPv6InEnforcingModes(t *testing.T) {
	for _, tt := range []struct {
		mode        string
		wantDisable bool
	}{
		{mode: "fail", wantDisable: true},
		{mode: "silent", wantDisable: true},
		{mode: "ask", wantDisable: true},
		{mode: "log", wantDisable: false},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.Security.Enforcement = tt.mode

			yaml, err := generateConfigForTest(cfg, "/test/project")
			if err != nil {
				t.Fatalf("GenerateConfig() error = %v", err)
			}

			hasDisable := strings.Contains(yaml, "net.ipv6.conf.all.disable_ipv6 = 1")
			if hasDisable != tt.wantDisable {
				t.Errorf("IPv6 disable presence = %v, want %v", hasDisable, tt.wantDisable)
			}
		})
	}
}

func extractHeredocBody(t *testing.T, rendered, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(rendered, startMarker)
	if start < 0 {
		t.Fatalf("generated config has no heredoc marker %q", startMarker)
	}
	start = strings.Index(rendered[start:], "\n") + start + 1
	if start <= 0 {
		t.Fatalf("generated heredoc marker %q has no body", startMarker)
	}
	endToken := "\n      " + endMarker
	endOffset := strings.Index(rendered[start:], endToken)
	if endOffset < 0 {
		t.Fatalf("generated heredoc %q has no terminator %q", startMarker, endMarker)
	}
	body := rendered[start : start+endOffset]
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "      ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func extractSystemProvisionScript(t *testing.T, rendered string) string {
	t.Helper()
	const startMarker = "  - mode: system\n    script: |\n"
	const endMarker = "\n  - mode: user\n"
	start := strings.Index(rendered, startMarker)
	if start < 0 {
		t.Fatal("generated config has no system provision script")
	}
	body := rendered[start+len(startMarker):]
	end := strings.Index(body, endMarker)
	if end < 0 {
		t.Fatal("generated config has no user provision marker")
	}
	body = body[:end]

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "      ") {
			t.Fatalf("system provision line is outside YAML block indentation: %q", line)
		}
		lines[i] = strings.TrimPrefix(line, "      ")
	}
	return strings.Join(lines, "\n")
}

func assertRenderedBefore(t *testing.T, rendered, first, second string) {
	t.Helper()
	firstPos := strings.Index(rendered, first)
	if firstPos < 0 {
		t.Fatalf("generated config missing first marker %q", first)
	}
	secondPos := strings.Index(rendered, second)
	if secondPos < 0 {
		t.Fatalf("generated config missing second marker %q", second)
	}
	if firstPos >= secondPos {
		t.Errorf("expected %q before %q", first, second)
	}
}
