package ops

import (
	"fmt"
	"os"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var homeLog = logging.WithSource("ops/home")

// EnsureUserHome ensures the user's home directory exists with correct permissions.
// Returns the home directory path.
// Follows Check-Diff-Act pattern for idempotency.
func EnsureUserHome(username string, uid, gid int) (string, error) {
	homeDir := fmt.Sprintf("/home/%s", username)
	homeLog.Infof("Ensuring home directory: %s", homeDir)

	// Check: Does home directory exist?
	info, err := os.Stat(homeDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Act: Create home directory
			homeLog.Infof("Creating home directory: %s", homeDir)
			if err := os.MkdirAll(homeDir, 0700); err != nil {
				return "", fmt.Errorf("failed to create home directory: %w", err)
			}

			// Set ownership
			if err := os.Chown(homeDir, uid, gid); err != nil {
				return "", fmt.Errorf("failed to set ownership: %w", err)
			}

			homeLog.Infof("Home directory created successfully")
			return homeDir, nil
		}
		return "", fmt.Errorf("failed to stat home directory: %w", err)
	}

	// Check: Is it a directory?
	if !info.IsDir() {
		return "", fmt.Errorf("%s exists but is not a directory", homeDir)
	}

	// Diff: Check permissions (should be exactly 0700, so other users can't
	// read into it; unlike the old 0755 baseline, this can't be a "has at
	// least" bitmask check, since a looser mode like 0755 would already
	// satisfy that and never get tightened back down)
	mode := info.Mode().Perm()
	if mode != 0700 {
		// Act: Fix permissions
		homeLog.Warnf("Home directory has incorrect permissions: %o, fixing to 0700", mode)
		if err := os.Chmod(homeDir, 0700); err != nil {
			return "", fmt.Errorf("failed to fix permissions: %w", err)
		}
	}

	// Act: Verify ownership (always attempt to set correct ownership)
	if err := os.Chown(homeDir, uid, gid); err != nil {
		homeLog.Warnf("Failed to verify ownership, may already be correct: %v", err)
	}

	homeLog.Infof("Home directory verified: %s", homeDir)
	return homeDir, nil
}
