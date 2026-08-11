package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Unit tests replace individual Lima lifecycle seams and must not depend on
	// a host Lima installation. Compatibility-specific tests override this
	// no-op explicitly.
	cliRequireCompatibleLima = func() error { return nil }
	os.Exit(m.Run())
}
