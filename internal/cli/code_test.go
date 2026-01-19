package cli

import (
	"testing"
)

func TestBuildIDECommand(t *testing.T) {
	tests := []struct {
		name     string
		ideCmd   string
		sshHost  string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "vscode",
			ideCmd:   "code",
			sshHost:  "lima-watermelon-test-12345678",
			wantCmd:  "code",
			wantArgs: []string{"--remote", "ssh-remote+lima-watermelon-test-12345678", "/project"},
		},
		{
			name:     "cursor",
			ideCmd:   "cursor",
			sshHost:  "lima-watermelon-test-12345678",
			wantCmd:  "cursor",
			wantArgs: []string{"--remote", "ssh-remote+lima-watermelon-test-12345678", "/project"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := buildIDECommand(tt.ideCmd, tt.sshHost)
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
