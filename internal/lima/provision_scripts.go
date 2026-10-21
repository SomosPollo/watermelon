package lima

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/sys/unix"
)

// PreparedProvisionScripts binds the exact host bytes embedded into a Lima
// config to their ordered SHA-256 digests. The CLI carries this single read
// through generation, identity reservation, and applied-policy snapshots.
type PreparedProvisionScripts struct {
	Contents []string
	SHA256   []string
}

// PrepareProvisionScripts opens configured scripts beneath a real project
// directory without following any symlink component or blocking on a special
// file. Scripts run as root in the VM and Lima may retry them, so callers must
// author them to be idempotent.
func PrepareProvisionScripts(projectDir string, scripts []string) (PreparedProvisionScripts, error) {
	contents := make([]string, 0, len(scripts))
	digests := make([]string, 0, len(scripts))
	if len(scripts) == 0 {
		return PreparedProvisionScripts{Contents: contents, SHA256: digests}, nil
	}
	total := 0
	canonicalProjectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return PreparedProvisionScripts{}, fmt.Errorf("resolving project directory for provision scripts: %w", err)
	}
	projectFD, err := unix.Open(canonicalProjectDir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return PreparedProvisionScripts{}, fmt.Errorf("opening project directory for provision scripts: %w", err)
	}
	defer unix.Close(projectFD)
	for _, script := range scripts {
		if err := config.ValidateProvisionScript(script); err != nil {
			return PreparedProvisionScripts{}, err
		}
		fd, err := openProvisionScriptAt(projectFD, script)
		if err != nil {
			return PreparedProvisionScripts{}, err
		}
		file := os.NewFile(uintptr(fd), filepath.Join(canonicalProjectDir, script))
		if file == nil {
			_ = unix.Close(fd)
			return PreparedProvisionScripts{}, fmt.Errorf("opening provision script %q: invalid file descriptor", script)
		}

		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return PreparedProvisionScripts{}, fmt.Errorf("inspecting provision script %q: %w", script, statErr)
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q must be a regular file, got %s", script, info.Mode().Type())
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			_ = file.Close()
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q must be owned by the current user", script)
		}
		if info.Size() > maxProvisionScriptSize {
			_ = file.Close()
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q exceeds the %d-byte limit", script, maxProvisionScriptSize)
		}

		data, readErr := io.ReadAll(io.LimitReader(file, maxProvisionScriptSize+1))
		closeErr := file.Close()
		if readErr != nil {
			return PreparedProvisionScripts{}, fmt.Errorf("reading provision script %q: %w", script, readErr)
		}
		if closeErr != nil {
			return PreparedProvisionScripts{}, fmt.Errorf("closing provision script %q: %w", script, closeErr)
		}
		if len(data) > maxProvisionScriptSize {
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q exceeds the %d-byte limit", script, maxProvisionScriptSize)
		}
		if !utf8.Valid(data) {
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q must contain valid UTF-8", script)
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return PreparedProvisionScripts{}, fmt.Errorf("provision script %q contains a NUL byte", script)
		}
		total += len(data)
		if total > maxProvisionScriptsSize {
			return PreparedProvisionScripts{}, fmt.Errorf("provision scripts exceed the %d-byte total limit", maxProvisionScriptsSize)
		}
		contents = append(contents, string(data))
		digest := sha256.Sum256(data)
		digests = append(digests, fmt.Sprintf("%x", digest[:]))
	}
	return PreparedProvisionScripts{Contents: contents, SHA256: digests}, nil
}

func openProvisionScriptAt(projectFD int, script string) (int, error) {
	components := strings.Split(filepath.Clean(script), string(filepath.Separator))
	dirFD := projectFD
	ownedDirFD := -1
	defer func() {
		if ownedDirFD >= 0 {
			_ = unix.Close(ownedDirFD)
		}
	}()

	for _, component := range components[:len(components)-1] {
		nextFD, err := unix.Openat(dirFD, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return -1, fmt.Errorf("opening provision script %q directory component %q without following symlinks: %w", script, component, err)
		}
		if ownedDirFD >= 0 {
			_ = unix.Close(ownedDirFD)
		}
		ownedDirFD = nextFD
		dirFD = nextFD
	}

	fileFD, err := unix.Openat(dirFD, components[len(components)-1], unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("opening provision script %q without following symlinks: %w", script, err)
	}
	return fileFD, nil
}

func readProvisionScripts(projectDir string, scripts []string) ([]string, error) {
	prepared, err := PrepareProvisionScripts(projectDir, scripts)
	if err != nil {
		return nil, err
	}
	return prepared.Contents, nil
}

// encodeProvisionScript keeps the exact audited script bytes out of both the
// Watermelon and Lima template languages. The generated wrapper decodes the
// payload into a root-only per-boot path before executing it.
func encodeProvisionScript(value string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	const lineWidth = 76
	if len(encoded) <= lineWidth {
		return encoded
	}
	var lines []string
	for len(encoded) > lineWidth {
		lines = append(lines, encoded[:lineWidth])
		encoded = encoded[lineWidth:]
	}
	if encoded != "" {
		lines = append(lines, encoded)
	}
	return strings.Join(lines, "\n")
}

func validatePreparedProvisionScripts(cfg *config.Config, prepared *PreparedProvisionScripts) error {
	if prepared == nil {
		return errors.New("prepared provision scripts cannot be nil")
	}
	if len(prepared.Contents) != len(cfg.Provision.Scripts) || len(prepared.SHA256) != len(cfg.Provision.Scripts) {
		return fmt.Errorf("prepared provision script count does not match configured scripts")
	}
	total := 0
	for index, content := range prepared.Contents {
		data := []byte(content)
		if len(data) > maxProvisionScriptSize {
			return fmt.Errorf("prepared provision script %q exceeds the %d-byte limit", cfg.Provision.Scripts[index], maxProvisionScriptSize)
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return fmt.Errorf("prepared provision script %q is not valid NUL-free UTF-8", cfg.Provision.Scripts[index])
		}
		total += len(data)
		if total > maxProvisionScriptsSize {
			return fmt.Errorf("prepared provision scripts exceed the %d-byte total limit", maxProvisionScriptsSize)
		}
		digest := sha256.Sum256(data)
		actual := fmt.Sprintf("%x", digest[:])
		if prepared.SHA256[index] != actual {
			return fmt.Errorf("prepared provision script %q does not match its SHA-256 digest", cfg.Provision.Scripts[index])
		}
		if len(cfg.Provision.ScriptSHA256) != 0 && cfg.Provision.ScriptSHA256[index] != actual {
			return fmt.Errorf("prepared provision script %q does not match the applied configuration digest", cfg.Provision.Scripts[index])
		}
	}
	return nil
}
