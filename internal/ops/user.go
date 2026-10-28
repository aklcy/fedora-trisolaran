package ops

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/internal/utils"
)

var userLog = logging.WithSource("ops/user")

// EnsureUserShell changes the user's default shell if it doesn't match.
// Idempotent: checks /etc/passwd before executing usermod.
func EnsureUserShell(username, targetShell string) error {
	userLog.Infof("Checking shell for user: %s", username)

	// Read /etc/passwd to get current shell
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return fmt.Errorf("failed to open /etc/passwd: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			userLog.Warnf("failed to close /etc/passwd: %v", cerr)
		}
	}()

	var currentShell string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, username+":") {
			fields := strings.Split(line, ":")
			if len(fields) >= 7 {
				currentShell = fields[6]
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read /etc/passwd: %w", err)
	}

	// Check if shell already matches
	if currentShell == targetShell {
		userLog.Infof("User %s already has shell %s. Skipping.", username, targetShell)
		return nil
	}

	userLog.Infof("Changing shell for %s: %s -> %s", username, currentShell, targetShell)

	// Execute usermod
	cmd := exec.Command("usermod", "-s", targetShell, username)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to change shell for %s: %w", username, err)
	}

	userLog.Infof("Shell changed successfully for user %s", username)
	return nil
}

// EnsureSymlink creates a symlink from src to dest as the specified user.
// Idempotent: checks if symlink already exists and points to correct target.
func EnsureSymlink(src, dest, username string) error {
	userLog.Infof("Ensuring symlink: %s -> %s (as %s)", dest, src, username)

	// Check if dest exists and is correct
	if target, err := os.Readlink(dest); err == nil {
		if target == src {
			userLog.Infof("Symlink already correct. Skipping.")
			return nil
		}
	}

	// Create/update symlink using RunCommandAsUser
	if err := utils.RunCommandAsUser(username, "ln", "-sfn", src, dest); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	userLog.Info("Symlink created successfully")
	return nil
}

// GitClone clones a git repository to the destination as the specified user.
// Idempotent: checks if destination already exists.
func GitClone(url, dest, username string) error {
	userLog.Infof("Cloning %s to %s (as %s)", url, dest, username)

	// Check if destination exists
	if _, err := os.Stat(dest); err == nil {
		userLog.Infof("Destination %s already exists. Skipping clone.", dest)
		return nil
	}

	// Clone repository
	if err := utils.RunCommandAsUser(username, "git", "clone", url, dest); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	userLog.Info("Repository cloned successfully")
	return nil
}
