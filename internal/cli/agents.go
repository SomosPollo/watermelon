package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/saeta-eth/watermelon/skills"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	agentHomeDir  = os.UserHomeDir
	agentLookPath = exec.LookPath
)

const (
	agentInstructionsBegin   = "<!-- watermelon:begin -->"
	agentInstructionsEnd     = "<!-- watermelon:end -->"
	maxAgentFileSize         = 1024 * 1024
	projectAgentInstructions = agentInstructionsBegin + `
## Development with Watermelon

This project uses Watermelon (.watermelon.toml). Use the watermelon skill when available.
Run project code through ` + "`watermelon exec -- <command>`" + `: dependency installs, builds,
tests, scripts, generators, migrations, and development servers. Read and edit mounted
source files normally; source that lives only in the guest must be read and edited in the VM.
For subdirectories use ` + "`watermelon exec --cwd <relative-directory> -- <command>`" + `.
Keep compound commands inside the guest, for example:
` + "`watermelon exec -- sh -c 'npm ci && npm test'`" + `.
If Watermelon fails, diagnose the failure; do not fall back to host execution.
Use ` + "`watermelon logs`" + ` for network failures. Ask before changing network policy or mounts.
Review the initial setup plan with the user before installing dependencies.
Do not stop or destroy the VM unless requested.
` + agentInstructionsEnd + "\n"
)

type agentIntegration struct {
	name, label, executable, configDir, skillPath string
}

func agentIntegrations(home string) []agentIntegration {
	codexDir := os.Getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home, ".codex")
	}
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	return []agentIntegration{
		{"codex", "Codex", "codex", codexDir, filepath.Join(home, ".agents", "skills", "watermelon", "SKILL.md")},
		{"claude", "Claude Code", "claude", claudeDir, filepath.Join(claudeDir, "skills", "watermelon", "SKILL.md")},
	}
}

func NewAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "agents", Short: "Integrate Watermelon with coding assistants", Args: cobra.NoArgs}
	var selected []string
	install := &cobra.Command{
		Use: "install", Short: "Install the Watermelon skill for Codex and Claude Code", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return installAgentIntegrations(cmd.OutOrStdout(), selected)
		},
	}
	install.Flags().StringSliceVar(&selected, "agent", nil, "Assistant to install for: codex, claude, or all (default: detect installed assistants; repeatable)")
	cmd.AddCommand(install)
	return cmd
}

func installAgentIntegrations(out io.Writer, selected []string) error {
	// Reject invalid selectors before consulting the host or touching files.
	wanted := map[string]bool{}
	for _, name := range selected {
		switch name {
		case "codex", "claude":
			wanted[name] = true
		case "all":
			wanted["codex"], wanted["claude"] = true, true
		default:
			return NewUsageError(fmt.Errorf("unknown agent %q; choose codex, claude, or all", name))
		}
	}
	home, err := agentHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("home directory must be an absolute path")
	}
	integrations := agentIntegrations(home)
	if len(selected) == 0 {
		for _, integration := range integrations {
			_, executableErr := agentLookPath(integration.executable)
			info, configErr := os.Stat(integration.configDir)
			wanted[integration.name] = executableErr == nil || (configErr == nil && info.IsDir())
		}
	}
	var installs []agentIntegration
	for _, integration := range integrations {
		if wanted[integration.name] {
			if !filepath.IsAbs(integration.configDir) {
				return fmt.Errorf("%s configuration directory must be an absolute path", integration.label)
			}
			installs = append(installs, integration)
		}
	}
	if len(installs) == 0 {
		return fmt.Errorf("no supported assistants detected; use 'watermelon agents install --agent codex', '--agent claude', or '--agent all'")
	}
	// Preflight every destination so a conflicting skill is never overwritten,
	// and a conflict in one integration does not install the other first.
	for _, integration := range installs {
		data, err := readAgentFile(integration.skillPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && string(data) != skills.Watermelon {
			return fmt.Errorf("%s already exists with different content; kept unchanged (move it aside or merge it before reinstalling)", integration.skillPath)
		}
	}
	for _, integration := range installs {
		if err := makeAgentDirectories(filepath.Dir(integration.skillPath)); err != nil {
			return err
		}
		created, err := createAgentFile(integration.skillPath, []byte(skills.Watermelon))
		if err != nil {
			return err
		}
		verb := "Already installed"
		if created {
			verb = "Installed"
		}
		fmt.Fprintf(out, "%s %s skill: %s\n", verb, integration.label, integration.skillPath)
	}
	fmt.Fprintln(out, "Start a new assistant session. In a project with .watermelon.toml, use Watermelon for project execution. Run 'watermelon init' to prepare a new project.")
	return nil
}

// Instruction files are append-only: preserve user text and any existing
// Watermelon block, including local edits. A malformed block needs manual
// repair rather than silently duplicating or replacing the user's guidance.
func projectInstructionSuffix(data []byte) ([]byte, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("instructions must be UTF-8 text without NUL bytes")
	}
	begin, end := bytes.Count(data, []byte(agentInstructionsBegin)), bytes.Count(data, []byte(agentInstructionsEnd))
	if begin != 0 || end != 0 {
		if begin != 1 || end != 1 || bytes.Index(data, []byte(agentInstructionsBegin)) > bytes.Index(data, []byte(agentInstructionsEnd)) {
			return nil, fmt.Errorf("incomplete or duplicate Watermelon instruction markers; repair them before running init")
		}
		return nil, nil
	}
	separator := ""
	if len(data) > 0 {
		separator = "\n"
		if data[len(data)-1] != '\n' {
			separator = "\n\n"
		}
	}
	suffix := []byte(separator + projectAgentInstructions)
	if len(data)+len(suffix) > maxAgentFileSize {
		return nil, fmt.Errorf("adding instructions would exceed %d bytes", maxAgentFileSize)
	}
	return suffix, nil
}

func checkProjectInstructions(dir string) error {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(dir, name)
		data, err := readAgentFile(path)
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		suffix, err := projectInstructionSuffix(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if exists && len(suffix) > 0 {
			file, err := openAgentFile(path, unix.O_RDWR|unix.O_APPEND)
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeProjectInstructions(dir string) error {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(dir, name)
		data, err := readAgentFile(path)
		if errors.Is(err, os.ErrNotExist) {
			if _, err := createAgentFile(path, []byte(projectAgentInstructions)); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		suffix, err := projectInstructionSuffix(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if len(suffix) == 0 {
			continue
		}
		file, err := openAgentFile(path, unix.O_RDWR|unix.O_APPEND)
		if err != nil {
			return err
		}
		// Recheck after opening, so an intervening edit is preserved rather than
		// appending instructions based on a stale snapshot.
		current, readErr := io.ReadAll(io.LimitReader(file, maxAgentFileSize+1))
		if readErr == nil && !bytes.Equal(current, data) {
			readErr = fmt.Errorf("instructions changed while initializing; retry after reviewing %s", path)
		}
		if readErr == nil {
			_, readErr = file.Write(suffix)
		}
		if err := errors.Join(readErr, file.Close()); err != nil {
			return fmt.Errorf("appending %s: %w", path, err)
		}
	}
	return nil
}

// Refuse links and special files instead of changing an unexpected destination.
// Directory opens pin each parent and refuse links below system root aliases.
func openAgentParent(path string, create bool) (int, error) {
	parent, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return -1, err
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for i, part := range strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if i == 0 {
			// macOS exposes /var and /tmp through system-owned root aliases.
			flags &^= unix.O_NOFOLLOW
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		if errors.Is(openErr, os.ErrNotExist) && create {
			if mkdirErr := unix.Mkdirat(fd, part, 0700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				_ = unix.Close(fd)
				return -1, mkdirErr
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, fmt.Errorf("opening directory for %s (symlinked directories are not supported): %w", path, openErr)
		}
		fd = next
	}
	return fd, nil
}

func makeAgentDirectories(dir string) error {
	fd, err := openAgentParent(filepath.Join(dir, "SKILL.md"), true)
	if err == nil {
		err = unix.Close(fd)
	}
	return err
}

func openAgentFile(path string, flags int) (*os.File, error) {
	parent, err := openAgentParent(path, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, filepath.Base(path), flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening %s without following symlinks: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Size() > maxAgentFileSize) {
		err = fmt.Errorf("must be a current-user-owned regular file no larger than %d bytes", maxAgentFileSize)
	}
	var stat unix.Stat_t
	if err == nil {
		err = unix.Fstat(fd, &stat)
		if err == nil && stat.Nlink != 1 {
			err = fmt.Errorf("must have exactly one hard link")
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

func readAgentFile(path string) ([]byte, error) {
	file, err := openAgentFile(path, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAgentFileSize+1))
	if err == nil && len(data) > maxAgentFileSize {
		err = fmt.Errorf("%s exceeds %d bytes", path, maxAgentFileSize)
	}
	return data, err
}

func createAgentFile(path string, data []byte) (bool, error) {
	file, err := openAgentFile(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := readAgentFile(path)
		if readErr == nil && !bytes.Equal(existing, data) {
			readErr = fmt.Errorf("%s already exists with different content; kept unchanged", path)
		}
		return false, readErr
	}
	if err != nil {
		return false, err
	}
	_, writeErr := file.Write(data)
	return true, errors.Join(writeErr, file.Close())
}
