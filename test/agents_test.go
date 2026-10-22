package test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/skills"
)

// Exercise the release binary with the assistants' real discovery locations.
// Isolated homes protect installed skills, credentials and project instructions.
// These checks do not invoke an assistant model or require a VM.
func TestAgentIntegrations(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "watermelon")
	build := exec.Command("go", "build", "-o", binary, "../cmd/watermelon")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Watermelon: %v\n%s", err, output)
	}
	for _, agent := range []struct {
		name, skill, instructions string
	}{
		{"codex", ".agents/skills/watermelon/SKILL.md", "AGENTS.md"},
		{"claude", ".claude/skills/watermelon/SKILL.md", "CLAUDE.md"},
	} {
		t.Run(agent.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "user")
			project := filepath.Join(root, "project")
			for _, dir := range []string{home, project} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			env := []string{}
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch key {
				case "HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR":
					continue
				}
				env = append(env, entry)
			}
			env = append(env, "HOME="+home)
			run := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.Env = project, env
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("watermelon %v: %v\n%s", args, err, output)
				}
				return string(output)
			}
			run("agents", "install", "--agent", agent.name)
			installed, err := os.ReadFile(filepath.Join(home, agent.skill))
			if err != nil || string(installed) != skills.Watermelon {
				t.Fatalf("packaged skill not installed at %s: %v", agent.skill, err)
			}
			if !strings.Contains(run("agents", "install", "--agent", agent.name), "Already installed") {
				t.Fatal("reinstallation did not recognize existing skill")
			}
			instructionPath := filepath.Join(project, agent.instructions)
			original := []byte("# Existing team instructions\n\nPreserve these rules exactly.")
			if err := os.WriteFile(instructionPath, original, 0644); err != nil {
				t.Fatal(err)
			}
			run("init", "--template", "manual")
			instructions, err := os.ReadFile(instructionPath)
			if err != nil || !bytes.HasPrefix(instructions, original) || !bytes.Contains(instructions, []byte("watermelon exec --")) {
				t.Fatalf("%s did not preserve project guidance and add execution routing: %v", agent.name, err)
			}
			configPath := filepath.Join(project, ".watermelon.toml")
			config, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			run("init", "--instructions-only")
			after, err := os.ReadFile(instructionPath)
			if err != nil || !bytes.Equal(instructions, after) {
				t.Fatalf("repeated adoption changed %s: %v", agent.instructions, err)
			}
			after, err = os.ReadFile(configPath)
			if err != nil || !bytes.Equal(config, after) {
				t.Fatalf("instructions-only changed project policy: %v", err)
			}
			if err := os.WriteFile(filepath.Join(home, agent.skill), []byte("customized skill\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "agents", "install", "--agent", agent.name)
			cmd.Dir, cmd.Env = project, env
			if output, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("kept unchanged")) {
				t.Fatalf("reinstallation did not protect a customized skill: %v\n%s", err, output)
			}
			customized, err := os.ReadFile(filepath.Join(home, agent.skill))
			if err != nil || string(customized) != "customized skill\n" {
				t.Fatalf("customized skill was changed: %v", err)
			}
		})
	}
}
