package utils

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var userLog = logging.WithSource("utils/user")

// GetRealUser identifies the original user who invoked sudo.
// It supports both X11 and Wayland sessions by checking multiple sources.
//
// Detection order:
// 1. SUDO_USER environment variable (most reliable for sudo context)
// 2. XAUTHORITY file ownership (X11 sessions)
// 3. XDG_RUNTIME_DIR ownership (Wayland sessions)
//
// Returns the username, UID, and GID of the real user.
func GetRealUser() (username string, uid int, gid int, err error) {
	userLog.Info("Detecting real user identity...")

	// Strategy 1: SUDO_USER (primary method)
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		userLog.Infof("Found SUDO_USER: %s", sudoUser)
		u, err := user.Lookup(sudoUser)
		if err != nil {
			return "", 0, 0, fmt.Errorf("failed to lookup SUDO_USER: %w", err)
		}
		uidInt, _ := strconv.Atoi(u.Uid)
		gidInt, _ := strconv.Atoi(u.Gid)
		return u.Username, uidInt, gidInt, nil
	}

	// Strategy 2: XAUTHORITY file ownership (X11)
	if xauth := os.Getenv("XAUTHORITY"); xauth != "" {
		userLog.Infof("Checking XAUTHORITY file: %s", xauth)
		if stat, err := os.Stat(xauth); err == nil {
			if uid, ok := getFileOwnerUID(stat); ok {
				if u, err := user.LookupId(fmt.Sprintf("%d", uid)); err == nil {
					gidInt, _ := strconv.Atoi(u.Gid)
					userLog.Infof("Resolved from XAUTHORITY: %s (UID %d)", u.Username, uid)
					return u.Username, uid, gidInt, nil
				}
			}
		}
	}

	// Strategy 3: XDG_RUNTIME_DIR ownership (Wayland)
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		userLog.Infof("Checking XDG_RUNTIME_DIR: %s", xdgRuntime)
		if stat, err := os.Stat(xdgRuntime); err == nil {
			if uid, ok := getFileOwnerUID(stat); ok {
				if u, err := user.LookupId(fmt.Sprintf("%d", uid)); err == nil {
					gidInt, _ := strconv.Atoi(u.Gid)
					userLog.Infof("Resolved from XDG_RUNTIME_DIR: %s (UID %d)", u.Username, uid)
					return u.Username, uid, gidInt, nil
				}
			}
		}
	}

	return "", 0, 0, fmt.Errorf("unable to determine real user: no SUDO_USER, XAUTHORITY, or XDG_RUNTIME_DIR available")
}

// getFileOwnerUID extracts the UID from file stat info.
func getFileOwnerUID(stat os.FileInfo) (int, bool) {
	if sysStat, ok := stat.Sys().(*syscall.Stat_t); ok {
		return int(sysStat.Uid), true
	}
	return 0, false
}
