package main

import (
	"os/exec"
	"testing"
)

func TestMainBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}
}
