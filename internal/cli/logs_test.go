package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogsCommandNoLogs(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewLogsCmd()
	err = cmd.RunE(cmd, nil)
	if err != nil {
		t.Errorf("logs command error = %v, want nil", err)
	}
}

func TestLogsCommandWithLogs(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".watermelon")
	os.MkdirAll(logDir, 0755)
	os.WriteFile(filepath.Join(logDir, "logs.log"), []byte("BLOCKED example.com\n"), 0644)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewLogsCmd()
	err = cmd.RunE(cmd, nil)
	if err != nil {
		t.Errorf("logs command error = %v", err)
	}
}

func TestLogsCommandClear(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".watermelon")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "logs.log")
	os.WriteFile(logPath, []byte("data\n"), 0644)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	os.Chdir(dir)

	cmd := NewLogsCmd()
	cmd.Flags().Set("clear", "true")
	err = cmd.RunE(cmd, nil)
	if err != nil {
		t.Errorf("logs --clear error = %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Error("logs --clear did not remove log file")
	}
}
