package ops

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var accountLog = logging.WithSource("ops/account")

// EnsureGroups ensures the given groups exist.
// Idempotent: skips groups that already exist.
func EnsureGroups(groups []config.GroupConfig) error {
	for _, g := range groups {
		if err := ensureGroup(g); err != nil {
			return err
		}
	}
	return nil
}

func ensureGroup(g config.GroupConfig) error {
	accountLog.Infof("Checking group: %s", g.Name)

	if _, err := user.LookupGroup(g.Name); err == nil {
		accountLog.Infof("Group %s already exists. Skipping.", g.Name)
		return nil
	}

	args := []string{}
	if g.System {
		args = append(args, "-r")
	}
	args = append(args, g.Name)

	accountLog.Infof("Creating group: %s", g.Name)
	if err := exec.Command("groupadd", args...).Run(); err != nil {
		return fmt.Errorf("failed to create group %s: %w", g.Name, err)
	}

	accountLog.Infof("Group %s created successfully", g.Name)
	return nil
}

// EnsureUsers ensures the given users exist with their group memberships.
// Idempotent: creates missing users, and reconciles group membership for
// existing ones. UID/GID are never trusted from the blueprint; they are
// always read back from the OS after creation. See ADR-0008.
func EnsureUsers(users []config.UserConfig) error {
	for _, u := range users {
		if err := ensureUser(u); err != nil {
			return err
		}
	}
	return nil
}

func ensureUser(u config.UserConfig) error {
	accountLog.Infof("Checking user: %s", u.Name)

	if _, err := user.Lookup(u.Name); err != nil {
		if err := createUser(u); err != nil {
			return err
		}
	} else {
		accountLog.Infof("User %s already exists. Skipping creation.", u.Name)
	}

	return ensureUserGroups(u.Name, u.Groups)
}

func createUser(u config.UserConfig) error {
	args := []string{"-m"}
	if u.System {
		args = append(args, "-r")
	}

	// If a group with the same name as the user already exists (e.g. it was
	// declared under system.groups), reuse it as the primary group instead
	// of letting useradd auto-create a same-named private group, which
	// fails because that group name is already taken.
	if _, err := user.LookupGroup(u.Name); err == nil {
		args = append(args, "-g", u.Name)
	}

	args = append(args, u.Name)

	accountLog.Infof("Creating user: %s", u.Name)
	if err := exec.Command("useradd", args...).Run(); err != nil {
		return fmt.Errorf("failed to create user %s: %w", u.Name, err)
	}

	created, err := user.Lookup(u.Name)
	if err != nil {
		return fmt.Errorf("failed to resolve newly created user %s: %w", u.Name, err)
	}

	accountLog.Infof("User %s created successfully (UID %s)", u.Name, created.Uid)
	return nil
}

func ensureUserGroups(username string, groups []string) error {
	if len(groups) == 0 {
		return nil
	}

	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("failed to lookup user %s: %w", username, err)
	}

	currentGIDs, err := u.GroupIds()
	if err != nil {
		return fmt.Errorf("failed to list current groups for %s: %w", username, err)
	}

	current := make(map[string]bool)
	for _, gid := range currentGIDs {
		if g, err := user.LookupGroupId(gid); err == nil {
			current[g.Name] = true
		}
	}

	var missing []string
	for _, g := range groups {
		if !current[g] {
			missing = append(missing, g)
		}
	}

	if len(missing) == 0 {
		accountLog.Infof("User %s already in all required groups. Skipping.", username)
		return nil
	}

	accountLog.Infof("Adding user %s to groups: %s", username, strings.Join(missing, ", "))
	if err := exec.Command("usermod", "-a", "-G", strings.Join(missing, ","), username).Run(); err != nil {
		return fmt.Errorf("failed to add user %s to groups: %w", username, err)
	}

	return nil
}
