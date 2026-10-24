package ops

import (
	"fmt"

	"github.com/acker1019/fedora-trisolaran/internal/config"
	"github.com/acker1019/fedora-trisolaran/internal/logging"
	"github.com/acker1019/fedora-trisolaran/internal/utils"
	"github.com/acker1019/fedora-trisolaran/submodules"
)

var kdeconnectLog = logging.WithSource("ops/kdeconnect")

// EnsureKDEConnectAutorestart installs the bundled kdeconnect-autorestart
// script to ~/.local/bin/ and enables its systemd --user unit (via
// EnsureSystemdUserUnits), all as the target user.
func EnsureKDEConnectAutorestart(username string) error {
	kdeconnectLog.Info("Ensuring kdeconnect-autorestart...")

	script := fmt.Sprintf(`mkdir -p "$HOME/.local/bin" && cat > "$HOME/.local/bin/%[1]s" <<'TRISOLARAN_SCRIPT_EOF' && chmod 755 "$HOME/.local/bin/%[1]s"
%[2]s
TRISOLARAN_SCRIPT_EOF`, submodules.KDEConnectAutorestartName, submodules.KDEConnectAutorestartScript)

	if err := utils.RunCommandAsUser(username, "sh", "-c", script); err != nil {
		return fmt.Errorf("failed to install %s script: %w", submodules.KDEConnectAutorestartName, err)
	}

	unit := config.SystemdUserUnitConfig{
		Name:    submodules.KDEConnectAutorestartName,
		Content: submodules.KDEConnectAutorestartUnit,
	}
	return EnsureSystemdUserUnits([]config.SystemdUserUnitConfig{unit}, username)
}
