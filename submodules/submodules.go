// Package submodules compiles files from the git submodules under this
// directory into the tri binary, so rehydra can install them without
// fetching anything. go:embed can't reach outside a package's own
// directory, so the embeds live here rather than in internal/.
package submodules

import _ "embed"

// KDEConnectAutorestartName is the installed script's filename and the
// systemd --user unit's base name.
const KDEConnectAutorestartName = "kdeconnect-autorestart"

// KDEConnectAutorestartScript restarts kdeconnectd after screen unlock or
// resume, since GNOME drops its remote input (EIS) session on lock and
// kdeconnectd never reconnects.
//
//go:embed kdeconnect-autorestart/bin/kdeconnect-autorestart
var KDEConnectAutorestartScript string

// KDEConnectAutorestartUnit runs KDEConnectAutorestartScript from
// ~/.local/bin/, bound to graphical-session.target.
//
//go:embed kdeconnect-autorestart/systemd/kdeconnect-autorestart.service
var KDEConnectAutorestartUnit string
