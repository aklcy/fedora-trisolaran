package ops

import (
	"fmt"
	"os/exec"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var systemdLog = logging.WithSource("ops/systemd")

// EnsureServices enables and starts systemd services.
// Follows Check-Diff-Act pattern for idempotency.
func EnsureServices(services []string) error {
	if len(services) == 0 {
		return nil
	}

	systemdLog.Infof("Processing %d systemd services...", len(services))

	for _, svc := range services {
		systemdLog.Infof("Checking service: %s", svc)

		// Check: Is service already enabled and active?
		isEnabledCmd := exec.Command("systemctl", "is-enabled", svc)
		isEnabled := isEnabledCmd.Run() == nil

		isActiveCmd := exec.Command("systemctl", "is-active", svc)
		isActive := isActiveCmd.Run() == nil

		// Diff: If both enabled and active, skip
		if isEnabled && isActive {
			systemdLog.Infof("Service %s already enabled and running. Skipping.", svc)
			continue
		}

		// Act: Enable and start service
		systemdLog.Infof("Enabling and starting service: %s", svc)
		cmd := exec.Command("systemctl", "enable", "--now", svc)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to enable service %s: %w", svc, err)
		}

		systemdLog.Infof("Service %s enabled and started", svc)
	}

	systemdLog.Info("All services verified")
	return nil
}
