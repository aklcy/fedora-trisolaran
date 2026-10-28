package ops

import (
	"fmt"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var vscodeLog = logging.WithSource("ops/vscode")

// EnsureVSCodeExtensions installs the given VS Code extension IDs (as
// printed by `code --list-extensions`) as the target user. No separate
// check step: `code --install-extension` is itself idempotent, silently
// skipping anything already installed.
func EnsureVSCodeExtensions(extensions []string, username string) error {
	if len(extensions) == 0 {
		return nil
	}

	vscodeLog.Infof("Ensuring %d VS Code extension(s)...", len(extensions))

	args := []string{}
	for _, ext := range extensions {
		args = append(args, "--install-extension", ext)
	}

	if err := utils.RunCommandAsUser(username, "code", args...); err != nil {
		return fmt.Errorf("failed to install VS Code extensions: %w", err)
	}

	vscodeLog.Info("VS Code extensions ensured")
	return nil
}
