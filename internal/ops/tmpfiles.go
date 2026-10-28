package ops

import (
	"fmt"
	"os"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var tmpfilesLog = logging.WithSource("ops/tmpfiles")

// tmpfilesConfPath is where EnsureTmpfiles writes its systemd-tmpfiles
// rules. systemd-tmpfiles-setup.service reads every file under
// /etc/tmpfiles.d/ on every boot, so no custom unit is needed to apply
// this on startup.
const tmpfilesConfPath = "/etc/tmpfiles.d/trisolaran.conf"

// EnsureTmpfiles declares systemd-tmpfiles lines applied on every boot
// (e.g. removing a stale app lock file). Idempotent: the desired file
// content is computed and only written if it differs from what's already
// on disk.
func EnsureTmpfiles(entries []config.TmpfileEntry, userHome string) error {
	if len(entries) == 0 {
		return nil
	}

	var desired strings.Builder
	for _, entry := range entries {
		for tmpfileType, path := range entry {
			fmt.Fprintf(&desired, "%s %s\n", tmpfileType, utils.ExpandPath(path, userHome))
		}
	}

	existing, err := os.ReadFile(tmpfilesConfPath)
	if err == nil && string(existing) == desired.String() {
		tmpfilesLog.Info("tmpfiles config already up to date. Skipping.")
		return nil
	}

	tmpfilesLog.Infof("Writing tmpfiles config: %s", tmpfilesConfPath)
	if err := os.WriteFile(tmpfilesConfPath, []byte(desired.String()), 0644); err != nil {
		return fmt.Errorf("failed to write tmpfiles config: %w", err)
	}

	return nil
}
