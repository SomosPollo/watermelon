package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
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

func TestLogsNameClearsRegisteredVMLogNotProjectFallback(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.VM.Name = "named-logs"
	if err := os.WriteFile(filepath.Join(project, ".watermelon.toml"), []byte("[vm]\nname = \"named-logs\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	instance, err := reserveNamedVMIdentity(project, cfg.VM.Name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Paths.GuestNetworkLogPath, []byte("named\n"), 0600); err != nil {
		t.Fatal(err)
	}
	projectLog := filepath.Join(project, ".watermelon", "logs.log")
	if err := os.MkdirAll(filepath.Dir(projectLog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectLog, []byte("project\n"), 0600); err != nil {
		t.Fatal(err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cmd := NewLogsCmd()
	if err := cmd.Flags().Set("name", cfg.VM.Name); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("clear", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := os.ReadFile(instance.Paths.GuestNetworkLogPath)
	if err != nil {
		t.Fatalf("reading cleared registered VM log: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("registered VM log was not cleared: %q", cleared)
	}
	if _, err := os.Stat(projectLog); err != nil {
		t.Fatalf("project fallback log was incorrectly cleared: %v", err)
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
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading cleared log: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("logs --clear left data behind: %q", data)
	}
}
