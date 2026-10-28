package ops

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var coprLog = logging.WithSource("ops/copr")

// EnsureCoprRepos enables the given "owner/project" COPR repos, before
// EnsurePackages runs. No separate check step: `dnf copr enable` is
// itself idempotent, reporting already-enabled repos as a no-op.
func EnsureCoprRepos(repos []string) error {
	for _, repo := range repos {
		coprLog.Infof("Enabling COPR repo: %s", repo)

		cmd := exec.Command("dnf", "copr", "enable", "-y", repo)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to enable copr repo %s: %w", repo, err)
		}
	}

	return nil
}
