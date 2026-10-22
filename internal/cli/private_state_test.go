package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateStateReplacementIsAtomicAndBounded(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "record")
	const limit = 32
	original := []byte("original state")
	if err := replacePrivateStateFile(path, "test state", original, limit); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	replacement := bytes.Repeat([]byte("x"), limit)
	if err := replacePrivateStateFile(path, "test state", replacement, limit); err != nil {
		t.Fatal(err)
	}
	// An existing reader must retain the complete old record after publication.
	gotOld, err := io.ReadAll(old)
	if err != nil || !bytes.Equal(gotOld, original) {
		t.Fatalf("old reader = %q, %v", gotOld, err)
	}
	got, err := readPrivateStateFile(path, "test state", limit)
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("replacement at size limit = %q, %v", got, err)
	}
	assertFileMode(t, path, 0600)
	if err := replacePrivateStateFile(path, "test state", append(replacement, 'x'), limit); err == nil {
		t.Fatal("oversized write accepted")
	}
	got, err = readPrivateStateFile(path, "test state", limit)
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("failed replacement changed record: %q, %v", got, err)
	}
	if _, err := readPrivateStateFile(path, "test state", limit-1); err == nil {
		t.Fatal("oversized read accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "record" {
		t.Fatalf("temporary state files remain: %v, %v", entries, err)
	}
}

func TestPrivateStateRejectsUnsafeFilesWithoutChangingThem(t *testing.T) {
	for _, kind := range []string{"symlink", "hard link", "directory", "FIFO", "insecure mode"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path, victim := filepath.Join(dir, "record"), filepath.Join(dir, "victim")
			const original = "untouched"
			if err := os.WriteFile(victim, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(victim, path)
			case "hard link":
				err = os.Link(victim, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "FIFO":
				err = unix.Mkfifo(path, 0600)
			case "insecure mode":
				err = os.WriteFile(path, []byte(original), 0644)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readPrivateStateFile(path, "test state", 32); err == nil {
				t.Fatal("unsafe read accepted")
			}
			if err := replacePrivateStateFile(path, "test state", []byte("replacement"), 32); err == nil {
				t.Fatal("unsafe replacement accepted")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("rejected replacement changed target: %v", err)
			}
			got, err := os.ReadFile(victim)
			if err != nil || string(got) != original {
				t.Fatalf("rejected replacement changed victim: %q, %v", got, err)
			}
		})
	}
}

func TestPrivateStateReplacementRejectsUnsafeDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "insecure mode"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path := dir
			if kind == "symlink" {
				path = filepath.Join(privateTempDir(t), "alias")
				if err := os.Symlink(dir, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := replacePrivateStateFile(filepath.Join(path, "record"), "test state", []byte("new"), 32); err == nil {
				t.Fatal("unsafe directory accepted")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected write changed directory: %v, %v", entries, err)
			}
		})
	}
}

func TestPrivateStateMissingFilePreservesErrorIdentity(t *testing.T) {
	_, err := readPrivateStateFile(filepath.Join(privateTempDir(t), "missing"), "test state", 32)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing state error = %v", err)
	}
}
