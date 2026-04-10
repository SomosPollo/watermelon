package cli

import (
	"os"
	"testing"
)

func TestStatusCommand(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewStatusCmd()
	err = cmd.RunE(cmd, nil)
	if err != nil {
		t.Errorf("status command error = %v", err)
	}
}
