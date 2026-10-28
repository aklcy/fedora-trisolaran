package utils

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var execLog = logging.WithSource("utils/exec")

// RunCommandAsUser executes a command as the specified user.
// This is the core engine for Block IV (User Space) operations.
//
// It switches the process context to the target user's UID/GID
// before executing the command, preventing "root-owned files" in user space.
func RunCommandAsUser(username, name string, args ...string) error {
	execLog.Infof("Executing as %s: %s %v", username, name, args)

	// Lookup user information
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("failed to lookup user %s: %w", username, err)
	}

	// Parse UID and GID
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("invalid UID for user %s: %w", username, err)
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("invalid GID for user %s: %w", username, err)
	}

	// Create command
	cmd := exec.Command(name, args...)

	// Set process credentials to switch to target user
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}

	// Set HOME/XDG_RUNTIME_DIR/DBUS_SESSION_BUS_ADDRESS for the target
	// user. os.Environ() already contains root's own copies of these
	// (since this process runs as root); simply appending new entries
	// wouldn't override them, because getenv() on Linux/glibc scans
	// front-to-back and returns the FIRST match. Filter out the old
	// entries first.
	//
	// XDG_RUNTIME_DIR/DBUS_SESSION_BUS_ADDRESS matter for anything that
	// talks to the target user's own systemd user session or session
	// bus (`systemctl --user`, and tools like lemonade that go through
	// it) -- without them pointing at /run/user/<uid>, those commands
	// can't find that user's session and fail outright.
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	env := make([]string, 0, len(os.Environ())+3)
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "HOME="),
			strings.HasPrefix(e, "XDG_RUNTIME_DIR="),
			strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS="):
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env,
		fmt.Sprintf("HOME=%s", u.HomeDir),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", runtimeDir),
		fmt.Sprintf("DBUS_SESSION_BUS_ADDRESS=unix:path=%s/bus", runtimeDir),
	)

	// Connect stdout/stderr for visibility
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Execute command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}

	execLog.Infof("Command executed successfully as %s", username)
	return nil
}
