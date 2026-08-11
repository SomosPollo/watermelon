package cli

import (
	"testing"
)

func TestBuildIDECommand(t *testing.T) {
	tests := []struct {
		name     string
		ideCmd   string
		sshHost  string
		workdir  string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "vscode",
			ideCmd:   "code",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "/project",
			wantCmd:  "code",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678", "/project"},
		},
		{
			name:     "cursor",
			ideCmd:   "cursor",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "/workspace/app",
			wantCmd:  "cursor",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678", "/workspace/app"},
		},
		{
			name:     "guest home",
			ideCmd:   "code",
			sshHost:  "lima-watermelon-test-12345678",
			workdir:  "",
			wantCmd:  "code",
			wantArgs: []string{"--wait", "--remote", "ssh-remote+lima-watermelon-test-12345678"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := buildIDECommand(tt.ideCmd, tt.sshHost, tt.workdir)
			if cmd != tt.wantCmd {
				t.Errorf("buildIDECommand() cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if len(args) != len(tt.wantArgs) {
				t.Errorf("buildIDECommand() args len = %d, want %d", len(args), len(tt.wantArgs))
				return
			}
			for i, arg := range args {
				if arg != tt.wantArgs[i] {
					t.Errorf("buildIDECommand() args[%d] = %q, want %q", i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}
