package project

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/saeta-eth/watermelon/internal/config"
	"golang.org/x/sys/unix"
)

const MaxDepth = 8
const MaxEntries = 20000
const MaxDirectories = 2048

var ExcludedDirectories = []string{"node_modules", "vendor", "target", "venv", "dist", "build", "__pycache__", "bin", "obj", "out", "CMakeFiles"}

func excludedDirectory(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, excluded := range ExcludedDirectories {
		if name == excluded {
			return true
		}
	}
	return false
}

func ValidManifestPath(name string) bool {
	if !utf8.ValidString(name) || !config.ValidProjectDirectory(name) || len(strings.Split(name, "/")) > MaxDepth+1 {
		return false
	}
	for _, candidate := range ManifestNames {
		if ok, _ := path.Match(candidate, path.Base(name)); ok {
			return true
		}
	}
	return false
}

// ReadLocal never follows a directory symlink and bounds both traversal and
// metadata. os.Root confines all opens even if a directory is swapped mid-read.
func ReadLocal(dir string) (map[string][]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := map[string][]byte{}
	total, entries, dirs := 0, 0, 0
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		dirs++
		if dirs > MaxDirectories {
			return fmt.Errorf("project discovery exceeds %d directories", MaxDirectories)
		}
		d, err := root.OpenFile(directory, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer d.Close()
		for {
			batch, readErr := d.ReadDir(128)
			for _, entry := range batch {
				entries++
				if entries > MaxEntries {
					return fmt.Errorf("project discovery exceeds %d entries", MaxEntries)
				}
				name := path.Join(directory, entry.Name())
				if entry.IsDir() && !excludedDirectory(entry.Name()) {
					if depth >= MaxDepth {
						return fmt.Errorf("project discovery exceeds depth %d at %q", MaxDepth, name)
					}
					if err := walk(name, depth+1); err != nil {
						return err
					}
				} else if ValidManifestPath(name) {
					f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
					if err != nil {
						return fmt.Errorf("reading %s: %w", name, err)
					}
					info, err := f.Stat()
					if err != nil {
						f.Close()
						return err
					}
					if !info.Mode().IsRegular() || info.Size() > MaxFileSize {
						f.Close()
						return fmt.Errorf("%s must be a regular file of at most %d bytes", name, MaxFileSize)
					}
					data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
					f.Close()
					if err != nil {
						return err
					}
					total += len(data)
					if len(data) > MaxFileSize || total > MaxMetadataSize {
						return fmt.Errorf("project metadata is too large")
					}
					files[name] = data
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	if err := walk(".", 0); err != nil {
		return nil, err
	}
	return files, nil
}
