package cli

import (
	"os"
	"testing"
)

func TestDestroyCommandNoVM(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewDestroyCmd()
	cmd.Flags().Set("force", "true")
	err = cmd.RunE(cmd, nil)
	if err == nil {
		t.Error("expected error when no VM exists")
	}
}
