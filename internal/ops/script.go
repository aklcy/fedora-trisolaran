package ops

import (
	"fmt"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var scriptLog = logging.WithSource("ops/script")

// EnsureScripts runs userspace.scripts, in order, as the specified user
// (via RunCommandAsUser) so they execute in the target user's own $HOME —
// not root's. Unlike the other Acts, these are opaque one-liners:
// Trisolaran has no way to check-diff arbitrary shell, so making a given
// line idempotent (safe to rerun) is the script author's responsibility,
// not this function's.
func EnsureScripts(scripts []string, username string) error {
	for _, s := range scripts {
		scriptLog.Infof("Running script as %s: %s", username, s)

		if err := utils.RunCommandAsUser(username, "sh", "-c", s); err != nil {
			return fmt.Errorf("script failed: %q: %w", s, err)
		}
	}

	return nil
}
