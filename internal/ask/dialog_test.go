package ask

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTerminalPromptFailsClosedWithoutControllingTerminal(t *testing.T) {
	var diagnostic bytes.Buffer
	called := 0
	verdict := showTerminalPromptWith(func() (*os.File, error) {
		called++
		return nil, errors.New("no controlling terminal")
	}, &diagnostic, "npm", "example.com", 443, "app")

	if verdict != VerdictBlock {
		t.Fatalf("terminal prompt verdict = %q, want %q", verdict, VerdictBlock)
	}
	if called != 1 {
		t.Fatalf("terminal opener called %d times, want 1", called)
	}
	if !strings.Contains(diagnostic.String(), "foreground controlling terminal") || !strings.Contains(diagnostic.String(), "blocking by default") {
		t.Fatalf("missing fail-closed terminal diagnostic: %q", diagnostic.String())
	}
}

func TestTerminalPromptRejectsAndClosesNonTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	verdict := showTerminalPromptWith(func() (*os.File, error) {
		return devNull, nil
	}, &diagnostic, "npm", "example.com", 443, "app")

	if verdict != VerdictBlock {
		t.Fatalf("terminal prompt verdict = %q, want %q", verdict, VerdictBlock)
	}
	if _, err := devNull.Stat(); err == nil {
		t.Fatal("terminal prompt did not close the rejected terminal file")
	}
	if !strings.Contains(diagnostic.String(), "foreground controlling terminal") {
		t.Fatalf("missing non-terminal diagnostic: %q", diagnostic.String())
	}
}

func TestDevNullIsNotAnInteractiveTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	if fileIsTerminal(devNull) {
		t.Fatal("the null device must not be treated as an interactive terminal")
	}
}

func TestBuildAppleScript(t *testing.T) {
	script := buildAppleScript("npm", "evil.com", 443, "my-app")

	checks := []string{
		`"npm"`,
		"evil.com:443",
		"my-app",
		"Block for Session",
		"Allow Once",
		"Always Allow and Save",
		"Always Allow saves this domain",
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

func TestDefaultDialogForGOOS(t *testing.T) {
	if defaultDialogForGOOS("darwin") == nil {
		t.Fatal("expected darwin dialog backend")
	}
	if defaultDialogForGOOS("linux") == nil {
		t.Fatal("expected linux dialog backend")
	}
	if defaultDialogForGOOS("freebsd") == nil {
		t.Fatal("expected fallback dialog backend")
	}
}

func TestNormalizeTerminalChoice(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"o", "once"},
		{"allow once", "once"},
		{"2", "once"},
		{"a", "always"},
		{"always allow", "always"},
		{"3", "always"},
		{"b", "block"},
		{"", "block"},
		{"anything else", "block"},
	}

	for _, tt := range tests {
		if got := normalizeTerminalChoice(tt.input); got != tt.want {
			t.Errorf("normalizeTerminalChoice(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReadTerminalPrompt(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"allow once", "o\n", VerdictAllowOnce},
		{"always allow", "a\n", VerdictAlwaysAllow},
		{"block default", "\n", VerdictBlock},
		{"eof default", "", VerdictBlock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			got := readTerminalPrompt(strings.NewReader(tt.input), &out, "npm", "example.com", 443, "app")
			if got != tt.want {
				t.Errorf("readTerminalPrompt() = %q, want %q", got, tt.want)
			}
			rendered := out.String()
			for _, want := range []string{"Watermelon network prompt", "npm", "example.com:443", "Project: app", "block for session", "always allow and save"} {
				if !strings.Contains(rendered, want) {
					t.Errorf("terminal prompt missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}
