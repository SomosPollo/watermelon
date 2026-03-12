package logs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogPath(t *testing.T) {
	got := LogPath("/some/project")
	want := filepath.Join("/some/project", ".watermelon", "logs.log")
	if got != want {
		t.Errorf("LogPath = %q, want %q", got, want)
	}
}

func TestReadNoFile(t *testing.T) {
	dir := t.TempDir()
	lines, err := Read(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil lines, got %v", lines)
	}
}

func TestReadWithContent(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(filepath.Join(logDir, "logs.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := Read(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	want := []string{"line one", "line two", "line three"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestClearNoFile(t *testing.T) {
	dir := t.TempDir()
	err := Clear(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
}

func TestClearRemovesFile(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}

	logFile := filepath.Join(logDir, "logs.log")
	if err := os.WriteFile(logFile, []byte("some log data\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Clear(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Errorf("expected log file to be removed, but it still exists")
	}
}
