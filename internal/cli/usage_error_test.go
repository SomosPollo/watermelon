package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUsageErrorMarkerIsTransparentAndIdempotent(t *testing.T) {
	cause := errors.New("invalid invocation")
	marked := NewUsageError(cause)
	if marked == cause {
		t.Fatal("NewUsageError returned the unmarked cause")
	}
	if marked.Error() != cause.Error() {
		t.Fatalf("marked error text = %q, want %q", marked.Error(), cause.Error())
	}
	if !errors.Is(marked, cause) || !IsUsageError(marked) {
		t.Fatalf("marked error = %T %v, want transparent usage marker", marked, marked)
	}
	if got := NewUsageError(marked); got != marked {
		t.Fatalf("marking twice changed error identity: got %T %v", got, got)
	}
	if got := NewUsageError(nil); got != nil {
		t.Fatalf("NewUsageError(nil) = %v, want nil", got)
	}
	if IsUsageError(cause) || IsUsageError(nil) {
		t.Fatal("unmarked or nil error reported as usage error")
	}
}

func TestInvalidRunWorkdirIsUsageErrorBeforeTargetLoading(t *testing.T) {
	t.Chdir(t.TempDir())
	err := runRunWithOptions(runOptions{OpenShell: false, Workdir: "relative"})
	if err == nil || !strings.Contains(err.Error(), "invalid --workdir") {
		t.Fatalf("invalid workdir error = %v", err)
	}
	if !IsUsageError(err) {
		t.Fatalf("invalid workdir error = %T %v, want usage error", err, err)
	}
	if strings.Contains(err.Error(), ".watermelon.toml") {
		t.Fatalf("target loading ran before workdir validation: %v", err)
	}
}

func TestDestructiveCommandsMarkInvalidExplicitNameAsUsageError(t *testing.T) {
	for _, test := range []struct {
		name string
		new  func() *cobra.Command
	}{
		{name: "stop", new: NewStopCmd},
		{name: "destroy", new: NewDestroyCmd},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cmd := test.new()
			if err := cmd.Flags().Set("name", "bad:name"); err != nil {
				t.Fatal(err)
			}
			err := cmd.RunE(cmd, nil)
			if err == nil || !strings.Contains(err.Error(), "invalid --name") {
				t.Fatalf("invalid name error = %v", err)
			}
			if !IsUsageError(err) {
				t.Fatalf("invalid name error = %T %v, want usage error", err, err)
			}
		})
	}
}
