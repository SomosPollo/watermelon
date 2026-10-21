package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// Keep default lifecycle and policy state inside temporary directories.
	stateDir, err := os.MkdirTemp("", "watermelon-cli-tests-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, subdir := range map[string]string{
		"XDG_CONFIG_HOME": "config",
		"LIMA_HOME":       "lima",
	} {
		if err := os.Setenv(name, filepath.Join(stateDir, subdir)); err != nil {
			_ = os.RemoveAll(stateDir)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Unit tests replace individual Lima lifecycle seams and must not depend on
	// a host Lima installation. Compatibility-specific tests override this
	// no-op explicitly.
	cliRequireCompatibleLima = func() error { return nil }
	code := m.Run()
	if err := os.RemoveAll(stateDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
