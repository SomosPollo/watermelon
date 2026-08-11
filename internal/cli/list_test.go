package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/saeta-eth/watermelon/internal/lima"
)

func TestListReportsLimaFailureWithDoctorGuidance(t *testing.T) {
	old := cliListAllVMs
	cliListAllVMs = func() ([]lima.VMInfo, error) {
		return nil, errors.New("limactl executable not found")
	}
	t.Cleanup(func() { cliListAllVMs = old })

	_, err := listOwnedWatermelonVMs()
	if err == nil {
		t.Fatal("listOwnedWatermelonVMs() succeeded")
	}
	for _, want := range []string{"limactl executable not found", "watermelon doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("listOwnedWatermelonVMs() error = %q, want %q", err, want)
		}
	}
}
