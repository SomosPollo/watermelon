package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusCommand(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	if err := runStatus(&out, dir); err != nil {
		t.Errorf("status command error = %v", err)
	}
	if !strings.Contains(out.String(), "Config:   missing") {
		t.Errorf("status output should mention missing config:\n%s", out.String())
	}
}

func TestStatusShowsConfigSummary(t *testing.T) {
	dir := t.TempDir()
	config := `[vm]
image = "ubuntu-22.04"

[network]
allow = ["registry.npmjs.org"]

[tools]
"node:20-slim" = ["npm", "node"]

[ports]
forward = [5173, 3000]

[resources]
memory = "4GB"
cpus = 2
disk = "15GB"
`
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(dir, ".watermelon")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "logs.log"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(&out, dir); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	rendered := out.String()
	for _, want := range []string{
		"Config:   valid",
		"Network:  log enforcement, 1 allow rule, 0 process rules",
		"Tools:    node:20-slim [node, npm]",
		"Ports:    3000, 5173",
		"Resources: 4GB memory, 2 CPUs, 15GB disk",
		"Logs:     2 entries",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("status output missing %q:\n%s", want, rendered)
		}
	}
}

func TestStatusReportsUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".watermelon.toml"), []byte("not = [valid"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(&out, dir); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(out.String(), "Config:   unreadable") {
		t.Errorf("status output should mention unreadable config:\n%s", out.String())
	}
}
