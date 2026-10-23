package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/skills"
	"golang.org/x/sys/unix"
)

func isolatedAgentHome(t *testing.T, executables ...string) string {
	t.Helper()
	home := t.TempDir()
	oldHome, oldLookPath := agentHomeDir, agentLookPath
	agentHomeDir = func() (string, error) { return home, nil }
	agentLookPath = func(name string) (string, error) {
		for _, executable := range executables {
			if name == executable {
				return filepath.Join(home, "bin", name), nil
			}
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { agentHomeDir, agentLookPath = oldHome, oldLookPath })
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func executeAgents(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := NewAgentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := executeCommandWithArgs(cmd, args...)
	return out.String(), err
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("unexpected contents of %s:\n%s", path, data)
	}
}

func TestAgentsInstallDetectsBothAssistants(t *testing.T) {
	for _, test := range []struct {
		name, executable, config, destination string
	}{
		{"codex executable", "codex", "", ".agents/skills/watermelon/SKILL.md"},
		{"codex desktop configuration", "", ".codex", ".agents/skills/watermelon/SKILL.md"},
		{"claude executable", "claude", "", ".claude/skills/watermelon/SKILL.md"},
		{"claude configuration", "", ".claude", ".claude/skills/watermelon/SKILL.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := isolatedAgentHome(t, test.executable)
			if test.config != "" {
				if err := os.Mkdir(filepath.Join(home, test.config), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := executeAgents(t, "install"); err != nil {
				t.Fatal(err)
			}
			assertFileContent(t, filepath.Join(home, test.destination), skills.Watermelon)
			other := ".claude/skills/watermelon/SKILL.md"
			if strings.HasPrefix(test.destination, ".claude/") {
				other = ".agents/skills/watermelon/SKILL.md"
			}
			if _, err := os.Stat(filepath.Join(home, other)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("installed undetected assistant: %v", err)
			}
		})
	}
}

func TestAgentsInstallExplicitSelectionAndReinstall(t *testing.T) {
	for _, args := range [][]string{
		{"install", "--agent", "all"},
		{"install", "--agent", "codex,claude"},
		{"install", "--agent", "codex", "--agent", "claude", "--agent", "codex"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := isolatedAgentHome(t)
			if _, err := executeAgents(t, args...); err != nil {
				t.Fatal(err)
			}
			paths := []string{
				filepath.Join(home, ".agents", "skills", "watermelon", "SKILL.md"),
				filepath.Join(home, ".claude", "skills", "watermelon", "SKILL.md"),
			}
			before := make([]os.FileInfo, len(paths))
			for i, path := range paths {
				assertFileContent(t, path, skills.Watermelon)
				var err error
				before[i], err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			out, err := executeAgents(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(out, "Already installed") != 2 {
				t.Fatalf("reinstall output: %s", out)
			}
			for i, path := range paths {
				after, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(before[i], after) || before[i].ModTime() != after.ModTime() {
					t.Fatalf("reinstall changed %s", path)
				}
			}
		})
	}
}

func TestAgentsInstallHonorsCustomClaudeDirectory(t *testing.T) {
	home := isolatedAgentHome(t)
	custom := filepath.Join(home, "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	if err := os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAgents(t, "install"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(custom, "skills", "watermelon", "SKILL.md"), skills.Watermelon)
	if _, err := os.Stat(filepath.Join(home, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created default Claude directory despite override: %v", err)
	}
}

func TestAgentsInstallDetectsCustomCodexHomeButUsesSharedUserSkills(t *testing.T) {
	home := isolatedAgentHome(t)
	custom := filepath.Join(home, "codex-config")
	t.Setenv("CODEX_HOME", custom)
	if err := os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAgents(t, "install"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(home, ".agents", "skills", "watermelon", "SKILL.md"), skills.Watermelon)
}

func TestAgentsInstallRejectsUnknownOrUndetectedAssistantsWithoutWrites(t *testing.T) {
	for _, args := range [][]string{
		{"install"}, {"install", "--agent", "unknown"},
		{"install", "--agent", "codex,unknown"}, {"install", "--agent", ""},
		{"install", "unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := isolatedAgentHome(t)
			if _, err := executeAgents(t, args...); err == nil {
				t.Fatal("invalid invocation succeeded")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid invocation changed home directory: %v, %v", entries, err)
			}
		})
	}
}

func TestAgentsInstallPreservesCustomSkillsAndPreflightsBoth(t *testing.T) {
	home := isolatedAgentHome(t)
	path := filepath.Join(home, ".claude", "skills", "watermelon", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("my customized skill\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAgents(t, "install", "--agent", "all"); err == nil {
		t.Fatal("overwrote custom skill")
	}
	assertFileContent(t, path, "my customized skill\n")
	if _, err := os.Stat(filepath.Join(home, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installed Codex before discovering Claude conflict: %v", err)
	}
}

func TestAgentsInstallDoesNotFollowSkillDirectorySymlinks(t *testing.T) {
	home := isolatedAgentHome(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, ".agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAgents(t, "install", "--agent", "codex"); err == nil {
		t.Fatal("followed symlinked skill directory")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("changed symlink target: %v, %v", entries, err)
	}
}

func TestAgentsInstallRejectsRelativeClaudeDirectoryBeforeWriting(t *testing.T) {
	home := isolatedAgentHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-config")
	if _, err := executeAgents(t, "install", "--agent", "all"); err == nil {
		t.Fatal("accepted a relative user configuration directory")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid configuration changed home: %v, %v", entries, err)
	}
	// An unrelated assistant's override must not prevent Codex installation.
	if _, err := executeAgents(t, "install", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
}

func TestInitPreservesBothAssistantsProjectInstructions(t *testing.T) {
	for _, template := range []string{"manual", "node"} {
		t.Run(template, func(t *testing.T) {
			dir := t.TempDir()
			if template == "node" {
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"agent-test","version":"1.0.0"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			agents := "# Team rules\r\n\r\nKeep our conventions."
			claude := "# Claude instructions\n\nUse the existing workflow.\n"
			for name, text := range map[string]string{"AGENTS.md": agents, "CLAUDE.md": claude} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0640); err != nil {
					t.Fatal(err)
				}
			}
			if err := runInitWithTemplate(dir, template, ""); err != nil {
				t.Fatal(err)
			}
			for name, prefix := range map[string]string{"AGENTS.md": agents, "CLAUDE.md": claude} {
				path := filepath.Join(dir, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(data, []byte(prefix)) || !bytes.Contains(data, []byte("watermelon exec --")) {
					t.Fatalf("instructions lost user content or routing guidance: %s", data)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("changed permissions: %v, %v", info, err)
				}
				if err := writeProjectInstructions(dir); err != nil {
					t.Fatal(err)
				}
				assertFileContent(t, path, string(data))
			}
		})
	}
}

func TestInitCreatesBothInstructionFilesAndPreservesExistingManagedBlock(t *testing.T) {
	dir := t.TempDir()
	custom := "# Existing rules\n" + agentInstructionsBegin + "\nOur customized Watermelon guidance.\n" + agentInstructionsEnd + "\nMore rules.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInit(dir); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dir, "AGENTS.md"), custom)
	assertFileContent(t, filepath.Join(dir, "CLAUDE.md"), projectAgentInstructions)
	other := t.TempDir()
	if err := runInit(other); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		assertFileContent(t, filepath.Join(other, name), projectAgentInstructions)
	}
}

func TestInitPreflightsUnsafeInstructionsBeforeCreatingConfig(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling symlink", "fifo", "hardlink", "malformed block", "invalid text"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "CLAUDE.md")
			target := filepath.Join(t.TempDir(), "target.md")
			if err := os.WriteFile(target, []byte("untouched\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink(target, path)
			case "dangling symlink":
				err = os.Symlink(filepath.Join(dir, "missing"), path)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "hardlink":
				err = os.Link(target, path)
			case "malformed block":
				err = os.WriteFile(path, []byte(agentInstructionsBegin+"\nmissing end"), 0600)
			case "invalid text":
				err = os.WriteFile(path, []byte{0xff, 0}, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := runInit(dir); err == nil {
				t.Fatal("init accepted unsafe instructions")
			}
			for _, name := range []string{projectConfigName, "AGENTS.md"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created %s before checking both instruction files: %v", name, err)
				}
			}
			assertFileContent(t, target, "untouched\n")
		})
	}
}

func TestInitInstructionsOnlyAdoptsExistingProjectWithoutChangingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	config := defaultConfig + "\n# Keep local policy and comments exactly as written.\n"
	if err := os.WriteFile(filepath.Join(dir, projectConfigName), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := executeCommandWithArgs(NewInitCmd(), "--instructions-only"); err != nil {
			t.Fatal(err)
		}
	}
	assertFileContent(t, filepath.Join(dir, projectConfigName), config)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		assertFileContent(t, filepath.Join(dir, name), projectAgentInstructions)
	}
}

func TestInitPreflightsEmptyReadOnlyInstructions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write read-only files")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), nil, 0400); err != nil {
		t.Fatal(err)
	}
	if err := runInit(dir); err == nil {
		t.Fatal("accepted read-only instructions that need an append")
	}
	for _, name := range []string{projectConfigName, "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created %s before checking instruction permissions: %v", name, err)
		}
	}
}

func TestInitInstructionsOnlyRejectsMissingConfigAndProfileFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--instructions-only"}, {"--instructions-only", "--template", "manual"},
		{"--instructions-only", "--runtime", "22"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := executeCommandWithArgs(NewInitCmd(), args...); err == nil {
				t.Fatal("invalid instructions-only initialization succeeded")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid initialization changed project: %v, %v", entries, err)
			}
		})
	}
}
