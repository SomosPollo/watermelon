package lima

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// Installed-Lima contracts are explicit so the ordinary suite has the same
// behavior whether or not a developer happens to have Lima on PATH. Opting in
// makes a missing executable a failure, rather than silently losing coverage.
func realLimaBinaryForTest(t *testing.T) string {
	t.Helper()
	if os.Getenv("WATERMELON_TEST_REAL_LIMA") != "1" && os.Getenv("WATERMELON_TEST_REAL_LIMA_INFO") != "1" {
		t.Skip("set WATERMELON_TEST_REAL_LIMA=1 to test an installed limactl")
	}
	path, err := exec.LookPath("limactl")
	if err != nil {
		t.Fatalf("installed-Lima contracts requested but limactl is unavailable: %v", err)
	}
	return path
}

// TestRealLimaInfoContract exercises the installed limactl rather than the
// subprocess seam used by unit tests. It is opt-in so ordinary development
// does not require Lima; the release compatibility matrix enables it for each
// supported boundary version.
func TestRealLimaInfoContract(t *testing.T) {
	realLimaBinaryForTest(t)

	info, err := InspectInstallation()
	if err != nil {
		t.Fatalf("InspectInstallation() with real limactl: %v", err)
	}
	if info.ExecutablePath == "" {
		t.Fatal("real limactl inspection did not report the selected executable path")
	}
	if !hasStableReleaseSyntax(info.Version) {
		t.Fatalf("real limactl reported non-release version %q", info.Version)
	}
	if expected := os.Getenv("WATERMELON_EXPECT_LIMA_VERSION"); expected != "" && strings.TrimPrefix(info.Version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real limactl version = %q, want %q", info.Version, expected)
	}
	if info.HostOS == "" || info.HostOS != runtime.GOOS {
		t.Fatalf("real limactl hostOS = %q, Go runtime host = %q", info.HostOS, runtime.GOOS)
	}
	goArch, err := goHostArchitecture(info.HostArch)
	if err != nil {
		t.Fatalf("real limactl hostArch: %v", err)
	}
	if goArch != runtime.GOARCH {
		t.Fatalf("real limactl hostArch = %q (%s), Go runtime host = %q", info.HostArch, goArch, runtime.GOARCH)
	}

	var requiredVMType string
	switch info.HostOS {
	case "linux":
		requiredVMType = "qemu"
	case "darwin":
		requiredVMType = "vz"
	default:
		t.Fatalf("real limactl reported unsupported hostOS %q", info.HostOS)
	}
	if !hasVMType(info, requiredVMType) {
		t.Fatalf("real limactl vmTypes=%v vmTypesEx=%v, want %q backend", info.VMTypes, info.VMTypesEx, requiredVMType)
	}
}
