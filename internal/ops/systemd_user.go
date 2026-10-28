package ops

import (
	"fmt"

	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var systemdUserLog = logging.WithSource("ops/systemd_user")

// EnsureSystemdUserUnits installs userspace.systemd_user_units under
// ~/.config/systemd/user/, then reloads and enables them in the target
// user's own systemd session. Everything runs via RunCommandAsUser (not as
// root) so `systemctl --user` reaches that user's session bus instead of
// root's -- see RunCommandAsUser's doc comment.
func EnsureSystemdUserUnits(units []config.SystemdUserUnitConfig, username string) error {
	if len(units) == 0 {
		return nil
	}

	systemdUserLog.Infof("Ensuring %d systemd --user unit(s)...", len(units))

	for _, unit := range units {
		script := fmt.Sprintf(`mkdir -p "$HOME/.config/systemd/user" && cat > "$HOME/.config/systemd/user/%s.service" <<'TRISOLARAN_UNIT_EOF'
%s
TRISOLARAN_UNIT_EOF`, unit.Name, unit.Content)

		if err := utils.RunCommandAsUser(username, "sh", "-c", script); err != nil {
			return fmt.Errorf("failed to write unit file %s.service: %w", unit.Name, err)
		}
	}

	if err := utils.RunCommandAsUser(username, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("failed to reload systemd --user daemon: %w", err)
	}

	for _, unit := range units {
		if err := utils.RunCommandAsUser(username, "systemctl", "--user", "enable", unit.Name+".service"); err != nil {
			return fmt.Errorf("failed to enable unit %s.service: %w", unit.Name, err)
		}
	}

	systemdUserLog.Info("Systemd user units ensured")
	return nil
}
