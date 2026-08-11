package cli

import (
	"fmt"
	"runtime"

	"github.com/saeta-eth/watermelon/internal/lima"
)

var cliRequireCompatibleLima = inspectCompatibleLimaForHost

func inspectCompatibleLimaForHost() error {
	if err := requireSupportedWorkloadHost(runtime.GOOS, runtime.GOARCH, readMacOSProductVersion); err != nil {
		return err
	}
	info, err := lima.InspectCompatibleInstallation()
	if err != nil {
		return err
	}
	return requireLimaHostOS(info, runtime.GOOS)
}

func requireLimaHostOS(info lima.InstallationInfo, goos string) error {
	if info.HostOS == goos {
		return nil
	}
	return fmt.Errorf("Lima reports host OS %q, but Watermelon is running on %q", info.HostOS, goos)
}

func requireSupportedWorkloadHost(goos, goarch string, macOSVersion func() (string, error)) error {
	check, supported := doctorPlatformCheck(doctorDeps{
		goos:         goos,
		goarch:       goarch,
		macOSVersion: macOSVersion,
	})
	if supported {
		return nil
	}
	if check.Remediation != "" {
		return fmt.Errorf("%s; %s", check.Message, check.Remediation)
	}
	return fmt.Errorf("%s", check.Message)
}

// requireCompatibleLima is used only by commands that create, start, attach
// to, or copy through a VM. Recovery commands deliberately do not call it so
// an older but still functional Lima can stop or delete a sandbox.
func requireCompatibleLima() error {
	if err := cliRequireCompatibleLima(); err != nil {
		return fmt.Errorf("environment preflight failed: %w; run 'watermelon doctor' for installation guidance", err)
	}
	return nil
}
