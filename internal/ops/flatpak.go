package ops

import (
	"fmt"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var flatpakLog = logging.WithSource("ops/flatpak")

// flathubRepoURL is Flathub's own flatpakrepo file, the standard way to
// add it as a remote.
const flathubRepoURL = "https://flathub.org/repo/flathub.flatpakrepo"

// EnsureFlatpaks adds the Flathub remote (user-scope: no root needed,
// unlike Snap which requires a system-level /snap symlink on Fedora) and
// installs the given Flathub app IDs as the target user. No separate
// check step: both `flatpak remote-add --if-not-exists` and `flatpak
// install` are themselves idempotent.
func EnsureFlatpaks(appIDs []string, username string) error {
	if len(appIDs) == 0 {
		return nil
	}

	flatpakLog.Info("Ensuring Flathub remote...")
	if err := utils.RunCommandAsUser(username, "flatpak", "remote-add", "--if-not-exists", "--user", "flathub", flathubRepoURL); err != nil {
		return fmt.Errorf("failed to add flathub remote: %w", err)
	}

	flatpakLog.Infof("Ensuring %d Flatpak app(s)...", len(appIDs))
	args := append([]string{"install", "-y", "--user", "flathub"}, appIDs...)
	if err := utils.RunCommandAsUser(username, "flatpak", args...); err != nil {
		return fmt.Errorf("failed to install flatpaks: %w", err)
	}

	if err := ensureFlatpakDataDirs(username); err != nil {
		return err
	}

	flatpakLog.Info("Flatpaks ensured")
	return nil
}

// ensureFlatpakDataDirs makes user-scope Flatpak app launchers/icons
// discoverable by the desktop session. A --user install exports them
// under ~/.local/share/flatpak/exports/share, but that's not on
// XDG_DATA_DIRS by default, so GNOME Shell (etc.) can't find them.
// environment.d is read by systemd at session start, unlike shell rc
// files, which only apply to interactive shells -- not the desktop
// session that actually launches these apps.
func ensureFlatpakDataDirs(username string) error {
	const script = `mkdir -p "$HOME/.config/environment.d" && [ -f "$HOME/.config/environment.d/flatpak.conf" ] || printf '%s\n' 'XDG_DATA_DIRS=$HOME/.local/share/flatpak/exports/share:/var/lib/flatpak/exports/share:$XDG_DATA_DIRS' > "$HOME/.config/environment.d/flatpak.conf"`

	if err := utils.RunCommandAsUser(username, "sh", "-c", script); err != nil {
		return fmt.Errorf("failed to configure flatpak XDG_DATA_DIRS: %w", err)
	}
	return nil
}
