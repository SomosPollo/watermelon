package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicRepositoryURL(t *testing.T) {
	for _, raw := range []string{"https://github.com/acme/project", "https://github.com/acme/project.git", "https://github.com/acme/project/"} {
		if _, err := PublicRepositoryURL(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"git@github.com:acme/project", "file:///tmp/project", "https://token@github.com/acme/project", "https://github.com:443/acme/project", "https://github.com/acme/project?token=x", "https://github.com/acme/project/tree/main", "https://github.com/acme/%2e%2e", "https://evil.example/acme/project", "https://github.com/acme/project;id", "https://github.com/acme/.git"} {
		if _, err := PublicRepositoryURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestManagedWorkspaceValidation(t *testing.T) {
	fresh := func() *Config {
		cfg := NewConfig()
		mount := false
		cfg.VM.MountProject = &mount
		cfg.VM.Workdir = "/workspace/project"
		cfg.Workspace = WorkspaceConfig{Disk: "w0123456789", Source: "https://github.com/acme/project"}
		return cfg
	}
	if err := Validate(fresh()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.VM.MountProject = nil }, func(c *Config) { c.VM.Workdir = "/project" }, func(c *Config) { c.Workspace.Disk = "another-disk" }, func(c *Config) { c.Provision.Scripts = []string{"setup.sh"} }, func(c *Config) { c.VM.Name = "other" }, func(c *Config) { c.Mounts["/tmp/host"] = Mount{Target: "/mnt/watermelon/host"} }} {
		cfg := fresh()
		change(cfg)
		if err := Validate(cfg); err == nil {
			t.Fatalf("unsafe managed config accepted: %+v", cfg)
		}
	}
}

func TestProfilesAndSnapshotCompatibility(t *testing.T) {
	cfg := NewConfig()
	raw, err := json.Marshal(NormalizeAppliedConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"workspace"`) || strings.Contains(string(raw), `"setup"`) {
		t.Fatalf("legacy digest representation changed: %s", raw)
	}
	cfg.Setup = SetupConfig{Profile: "python", Runtime: "3.13"}
	image, err := SetupImage(cfg.Setup)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tools[image] = []string{"python", "python3", "pip"}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	digest, _ := AppliedConfigDigest(cfg)
	cfg.Setup.Runtime = "3.14"
	changed, _ := AppliedConfigDigest(cfg)
	if digest == changed {
		t.Fatal("runtime not bound to policy")
	}
	for _, setup := range []SetupConfig{{Profile: "node", Runtime: "20"}, {Profile: "node", Runtime: "24;id"}, {Profile: "python", Runtime: "3"}, {Profile: "unknown"}, {Runtime: "24"}} {
		if _, err := SetupImage(setup); err == nil {
			t.Fatalf("accepted %+v", setup)
		}
	}
}

func TestGoRustProfilesValidateImageAndTools(t *testing.T) {
	for _, profile := range []string{"go", "rust"} {
		cfg := NewConfig()
		cfg.Setup = SetupConfig{Profile: profile}
		image, err := SetupImage(cfg.Setup)
		if err != nil {
			t.Fatal(err)
		}
		commands := SetupCommands(profile)
		cfg.Tools[image] = commands
		if err := Validate(cfg); err != nil {
			t.Fatal(err)
		}
		cfg.Tools[image] = commands[1:]
		if err := Validate(cfg); err == nil {
			t.Fatalf("%s profile accepted missing required tool", profile)
		}
		for _, runtime := range []string{"nightly", "1", "2.0", "1.99;id", "1.99/other", "1.99.0-custom"} {
			if _, err := SetupImage(SetupConfig{Profile: profile, Runtime: runtime}); err == nil {
				t.Fatalf("accepted %s runtime %q", profile, runtime)
			}
		}
	}
}

func TestMultiSetupValidationAndDigest(t *testing.T) {
	setup := SetupConfig{Profile: "multi", Runtimes: map[string]string{"node": "24", "python": "3.13"}, Projects: []SetupProject{{Directory: "frontend", Profile: "node"}, {Directory: "backend", Profile: "python"}}}
	cfg := NewConfig()
	cfg.Setup = setup
	image, err := SetupImage(setup)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tools[image] = AllSetupCommands(setup)
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := AppliedConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Setup.Projects[0].Directory = "other"
	after, err := AppliedConfigDigest(cfg)
	if err != nil || before == after {
		t.Fatal("project directories not bound to approval digest")
	}
	for _, mutate := range []func(*SetupConfig){
		func(s *SetupConfig) { s.Runtime = "24" },
		func(s *SetupConfig) { s.Only = "node" },
		func(s *SetupConfig) { s.Projects[0].Directory = "../outside" },
		func(s *SetupConfig) { s.Projects[0].Directory = "a/../frontend" },
		func(s *SetupConfig) { s.Projects[0].Profile = "unknown" },
		func(s *SetupConfig) { s.Projects = append(s.Projects, s.Projects[0]) },
		func(s *SetupConfig) { s.Runtimes["node"] = "24; touch /tmp/bad" },
	} {
		bad := SetupConfig{Profile: "multi", Runtimes: map[string]string{"node": "24", "python": "3.13"}, Projects: []SetupProject{{Directory: "frontend", Profile: "node"}, {Directory: "backend", Profile: "python"}}}
		mutate(&bad)
		if _, err := SetupImage(bad); err == nil {
			t.Errorf("invalid multi config accepted: %+v", bad)
		}
	}
}

func TestExtendedRuntimeSelectorsAndManagerPolicy(t *testing.T) {
	for _, profile := range []string{"java", "dotnet", "ruby", "php", "cpp"} {
		setup := SetupConfig{Profile: profile}
		source, err := SetupSourceImage(setup)
		if err != nil || source == "" {
			t.Fatalf("%s source: %s %v", profile, source, err)
		}
		image, err := SetupImage(setup)
		if err != nil || !strings.HasPrefix(image, "watermelon-project:") {
			t.Fatalf("%s output: %s %v", profile, image, err)
		}
		setup.Runtime = "21; touch /tmp/unsafe"
		if _, err := SetupImage(setup); err == nil {
			t.Fatal("runtime shell fragment accepted")
		}
	}
	for _, manager := range []string{"pnpm@https://evil.example/tool.tgz", "uv@0.12.0;bad", "yarn@stable", "unknown@1.0.0"} {
		if err := ValidateManager("node", manager); err == nil {
			t.Fatalf("invalid manager %q accepted", manager)
		}
	}
	cfg := NewConfig()
	cfg.Setup = SetupConfig{Profile: "node", Runtime: "24", Manager: "pnpm@10.20.0"}
	image, _ := SetupImage(cfg.Setup)
	cfg.Tools[image] = AllSetupCommands(cfg.Setup)
	before, err := AppliedConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Setup.Manager = "pnpm@10.21.0"
	after, err := AppliedConfigDigest(cfg)
	if err != nil || before == after {
		t.Fatal("manager version not bound to approval")
	}
}
