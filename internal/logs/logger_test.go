package logs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalizing temporary directory: %v", err)
	}
	return dir
}

func TestLogPath(t *testing.T) {
	got := LogPath("/some/project")
	want := filepath.Join("/some/project", ".watermelon", "logs.log")
	if got != want {
		t.Errorf("LogPath = %q, want %q", got, want)
	}
}

func TestReadNoFile(t *testing.T) {
	dir := canonicalTempDir(t)
	lines, err := Read(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil lines, got %v", lines)
	}
}

func TestReadWithContent(t *testing.T) {
	dir := canonicalTempDir(t)
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

func TestReadPathReturnsOnlyCompleteBoundedTail(t *testing.T) {
	dir := canonicalTempDir(t)
	logFile := filepath.Join(dir, "logs.log")
	content := strings.Repeat("x", int(maxLogReadBytes)) + "\ndiscarded-boundary-line\nkept-one\nkept-two\n"
	if err := os.WriteFile(logFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadPath(logFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"discarded-boundary-line", "kept-one", "kept-two"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("bounded tail lines = %#v, want %#v", lines, want)
	}
}

func TestReadPathSkipsOversizedPartialLineWithoutFailing(t *testing.T) {
	dir := canonicalTempDir(t)
	logFile := filepath.Join(dir, "logs.log")
	if err := os.WriteFile(logFile, []byte(strings.Repeat("x", int(maxLogReadBytes)+100)), 0600); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadPath(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines != nil {
		t.Fatalf("oversized unterminated line returned %#v, want no complete entries", lines)
	}
}

func TestClearNoFile(t *testing.T) {
	dir := canonicalTempDir(t)
	err := Clear(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
}

func TestClearTruncatesFile(t *testing.T) {
	dir := canonicalTempDir(t)
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

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading cleared log: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("cleared log contains %q, want empty", data)
	}
}

func TestClearKeepsLiveAppendWriterVisible(t *testing.T) {
	dir := canonicalTempDir(t)
	logDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "logs.log")
	writer, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if _, err := writer.WriteString("before clear\n"); err != nil {
		t.Fatal(err)
	}
	before, err := writer.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := ClearPath(logFile); err != nil {
		t.Fatalf("clearing log with a live writer: %v", err)
	}
	after, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("statting cleared log: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("clear replaced the log inode while a writer had it open")
	}

	if _, err := writer.WriteString("after clear\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadPath(logFile)
	if err != nil {
		t.Fatalf("reading log after append: %v", err)
	}
	if len(lines) != 1 || lines[0] != "after clear" {
		t.Fatalf("log lines after clear and append = %q, want [\"after clear\"]", lines)
	}
}

func TestClearRejectsSymlink(t *testing.T) {
	dir := canonicalTempDir(t)
	target := filepath.Join(dir, "target.log")
	if err := os.WriteFile(target, []byte("must remain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "logs.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := ClearPath(link); err == nil {
		t.Fatal("ClearPath accepted a symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must remain\n" {
		t.Fatalf("symlink target changed to %q", data)
	}
}

func TestClearRejectsHardLink(t *testing.T) {
	dir := canonicalTempDir(t)
	target := filepath.Join(dir, "target.log")
	if err := os.WriteFile(target, []byte("must remain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "logs.log")
	if err := os.Link(target, link); err != nil {
		t.Fatal(err)
	}

	if err := ClearPath(link); err == nil {
		t.Fatal("ClearPath accepted a multiply linked file")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must remain\n" {
		t.Fatalf("hard-link target changed to %q", data)
	}
}

func TestLogOperationsRejectSymlinkedParent(t *testing.T) {
	dir := canonicalTempDir(t)
	outside := filepath.Join(dir, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "logs.log")
	if err := os.WriteFile(target, []byte("must remain private\n"), 0644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, ".watermelon")); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(project, ".watermelon", "logs.log")

	if _, err := ReadPath(logPath); err == nil {
		t.Fatal("ReadPath followed a symlinked parent directory")
	}
	if err := ClearPath(logPath); err == nil {
		t.Fatal("ClearPath followed a symlinked parent directory")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must remain private\n" {
		t.Fatalf("symlink-parent target changed to %q", data)
	}
}
