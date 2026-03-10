package ask

import (
	"strings"
	"testing"
)

func TestBuildAppleScript(t *testing.T) {
	script := buildAppleScript("npm", "evil.com", 443, "my-app")

	checks := []string{
		`"npm"`,
		"evil.com:443",
		"my-app",
		"Block",
		"Allow Once",
		"Always Allow",
		"default button",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Errorf("expected script to contain %q, got:\n%s", check, script)
		}
	}
}

func TestBuildAppleScriptEscapesQuotes(t *testing.T) {
	script := buildAppleScript(`proc"name`, `do"main.com`, 443, `proj"ect`)
	// The raw unescaped quote pattern should not appear
	if strings.Contains(script, `proc"name`) {
		t.Error("expected quotes in process name to be escaped")
	}
}

func TestBuildAppleScriptEmptyProcess(t *testing.T) {
	script := buildAppleScript("", "evil.com", 443, "my-app")
	if strings.Contains(script, `"" is trying`) {
		t.Error("expected empty process name to be replaced with fallback")
	}
	if !strings.Contains(script, "A process") {
		t.Error("expected fallback process name in script")
	}
}

func TestParseDialogResult(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"button returned:Block", VerdictBlock},
		{"button returned:Allow Once", VerdictAllowOnce},
		{"button returned:Always Allow", VerdictAlwaysAllow},
		{"button returned:Unknown", VerdictBlock},
		{"", VerdictBlock},
	}

	for _, tt := range tests {
		got := parseDialogResult(tt.output)
		if got != tt.want {
			t.Errorf("parseDialogResult(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}
