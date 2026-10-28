package ops

import (
	"os/exec"
	"strings"
	"time"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var timeLog = logging.WithSource("ops/time")

// EnsureTimeSync enables NTP and waits briefly for the clock to actually
// synchronize. A wrong system clock (common right after a fresh install,
// before NTP catches up) would make comparing file timestamps during
// restore meaningless, so callers should only trust that comparison when
// this returns true. Never fatal: on failure or timeout it just returns
// false so the caller can fall back to a safer default.
func EnsureTimeSync() bool {
	timeLog.Info("Enabling NTP time sync...")

	if err := exec.Command("timedatectl", "set-ntp", "true").Run(); err != nil {
		timeLog.Warnf("Failed to enable NTP: %v", err)
		return false
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if isTimeSynced() {
			timeLog.Info("Time sync confirmed")
			return true
		}
		time.Sleep(1 * time.Second)
	}

	timeLog.Warn("Time did not sync within timeout")
	return false
}

func isTimeSynced() bool {
	out, err := exec.Command("timedatectl", "show", "-p", "NTPSynchronized", "--value").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "yes"
}
