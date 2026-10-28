package ops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/logging"
)

var pkgLog = logging.WithSource("ops/pkg")

// EnsureSystemUpdate brings all currently-installed packages up to date.
// Unconditional by design: dnf itself is idempotent here (a fully-updated
// system just reports nothing to do), so there's no separate check step.
func EnsureSystemUpdate() error {
	pkgLog.Info("Updating all installed packages...")

	cmd := exec.Command("dnf", "update", "-y", "--refresh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dnf update failed: %w", err)
	}

	pkgLog.Info("System update complete")
	return nil
}

// kernelReleaseToken is expanded, in EnsurePackages, to the running
// kernel's uname -r release string -- e.g. "kernel-devel-$(uname -r)" for
// a kernel-devel matching the running kernel exactly, as DKMS/module
// builds need. pkgs is a plain string list passed straight to
// exec.Command (no shell involved), so "$(...)" is never shell-expanded;
// this recognizes just that one literal token and substitutes a
// Go-native value instead of shelling out.
const kernelReleaseToken = "$(uname -r)"

// expandKernelRelease replaces kernelReleaseToken in pkg with the running
// kernel's release (via syscall.Uname, no shell). Left as-is (and let dnf
// fail loudly on it) if the release can't be read.
func expandKernelRelease(pkg string) string {
	if !strings.Contains(pkg, kernelReleaseToken) {
		return pkg
	}

	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		pkgLog.Warnf("Failed to read running kernel release: %v", err)
		return pkg
	}

	release := utsFieldToString(uts.Release[:])
	return strings.ReplaceAll(pkg, kernelReleaseToken, release)
}

// utsFieldToString converts a syscall.Utsname byte field (a fixed-size,
// NUL-terminated int8 array) to a Go string.
func utsFieldToString(field []int8) string {
	b := make([]byte, 0, len(field))
	for _, c := range field {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// EnsurePackages is the idempotent function to install packages.
// It filters out already installed packages using rpm -q for speed.
func EnsurePackages(pkgs []string) error {
	if len(pkgs) == 0 {
		return nil
	}

	expanded := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		expanded[i] = expandKernelRelease(pkg)
	}
	pkgs = expanded

	pkgLog.Infof("Checking status for %d packages...", len(pkgs))

	// Filter out already installed packages
	// Use rpm -q to check each package individually for idempotency
	var missingPkgs []string

	for _, pkg := range pkgs {
		// rpm -q returns exit code 0 if installed, non-zero if not
		cmd := exec.Command("rpm", "-q", pkg)
		if err := cmd.Run(); err != nil {
			missingPkgs = append(missingPkgs, pkg)
		}
	}

	if len(missingPkgs) == 0 {
		pkgLog.Info("All packages are already installed")
		return nil
	}

	pkgLog.Infof("Found %d missing packages: %v", len(missingPkgs), missingPkgs)

	// Construct DNF command
	// -y: assume yes
	// --refresh: force metadata update
	args := append([]string{"install", "-y", "--refresh"}, missingPkgs...)

	cmd := exec.Command("dnf", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	pkgLog.Info("Starting DNF transaction...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dnf install failed: %w", err)
	}

	pkgLog.Info("Packages installed successfully")
	return nil
}

// EnsurePkgRepos ensures each declared dnf repo matches its desired state.
// Follows the same Check-Diff-Act pattern as EnsurePackages: a repo file
// that doesn't exist yet is created (importing its GPG key first, if
// given); a repo file that already exists only has its enabled flag
// reconciled. See config.PkgRepoConfig.
func EnsurePkgRepos(repos []config.PkgRepoConfig) error {
	for _, repo := range repos {
		if err := ensurePkgRepo(repo); err != nil {
			return err
		}
	}
	return nil
}

func pkgRepoPath(id string) string {
	return fmt.Sprintf("/etc/yum.repos.d/%s.repo", id)
}

func ensurePkgRepo(repo config.PkgRepoConfig) error {
	pkgLog.Infof("Checking dnf repo: %s", repo.ID)

	repoPath := pkgRepoPath(repo.ID)
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		if repo.BaseURL == "" {
			return fmt.Errorf("dnf repo %s not found at %s and no baseurl given to create it", repo.ID, repoPath)
		}
		return createPkgRepo(repo, repoPath)
	}

	currentlyEnabled, err := isPkgRepoEnabled(repo.ID, repoPath)
	if err != nil {
		return fmt.Errorf("failed to read enabled state for repo %s: %w", repo.ID, err)
	}

	if currentlyEnabled == repo.Enabled {
		pkgLog.Infof("Repo %s already in desired state. Skipping.", repo.ID)
		return nil
	}

	pkgLog.Infof("Setting repo %s: enabled=%v", repo.ID, repo.Enabled)
	if err := setPkgRepoEnabled(repo.ID, repoPath, repo.Enabled); err != nil {
		return fmt.Errorf("failed to set enabled state for repo %s: %w", repo.ID, err)
	}

	return nil
}

// setPkgRepoEnabled rewrites the enabled= line for repo.ID's section
// within its .repo file directly, rather than shelling out to `dnf
// config-manager --set-enabled/--set-disabled` -- that flag pair is
// dnf4-only and dnf5's config-manager plugin rejects it outright, so
// editing the file ourselves avoids depending on either CLI's syntax.
func setPkgRepoEnabled(id, repoPath string, enabled bool) error {
	data, err := os.ReadFile(repoPath)
	if err != nil {
		return err
	}

	desired := "enabled=0"
	if enabled {
		desired = "enabled=1"
	}

	sectionHeader := fmt.Sprintf("[%s]", id)
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+1)
	inSection := false
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")

		if inSection && !found && isHeader {
			// Leaving the target section without an enabled= line seen: insert one.
			out = append(out, desired)
			found = true
		}

		if isHeader {
			inSection = trimmed == sectionHeader
			out = append(out, line)
			continue
		}

		if inSection && strings.HasPrefix(trimmed, "enabled") {
			out = append(out, desired)
			found = true
			continue
		}

		out = append(out, line)
	}

	if inSection && !found {
		out = append(out, desired)
	}

	return os.WriteFile(repoPath, []byte(strings.Join(out, "\n")), 0644)
}

func createPkgRepo(repo config.PkgRepoConfig, repoPath string) error {
	if repo.GPGKey != "" {
		pkgLog.Infof("Importing GPG key for repo %s", repo.ID)
		if err := exec.Command("rpm", "--import", repo.GPGKey).Run(); err != nil {
			return fmt.Errorf("failed to import gpg key for repo %s: %w", repo.ID, err)
		}
	}

	enabled := "0"
	if repo.Enabled {
		enabled = "1"
	}
	gpgcheck := "0"
	if repo.GPGKey != "" {
		gpgcheck = "1"
	}

	var content strings.Builder
	fmt.Fprintf(&content, "[%s]\n", repo.ID)
	fmt.Fprintf(&content, "name=%s\n", repo.ID)
	fmt.Fprintf(&content, "baseurl=%s\n", repo.BaseURL)
	fmt.Fprintf(&content, "enabled=%s\n", enabled)
	fmt.Fprintf(&content, "gpgcheck=%s\n", gpgcheck)
	if repo.GPGKey != "" {
		fmt.Fprintf(&content, "gpgkey=%s\n", repo.GPGKey)
	}

	pkgLog.Infof("Creating dnf repo file: %s", repoPath)
	if err := os.WriteFile(repoPath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to write repo file for %s: %w", repo.ID, err)
	}

	pkgLog.Infof("Repo %s created successfully", repo.ID)
	return nil
}

// isPkgRepoEnabled reads the current enabled= value for repo.ID's section
// within its .repo file. Assumes the file's section header matches the
// repo id, which holds for the common case of one repo per file (true for
// both dnf-managed third-party repos like Fedora's google-chrome.repo and
// files this function itself creates).
func isPkgRepoEnabled(id, repoPath string) (bool, error) {
	data, err := os.ReadFile(repoPath)
	if err != nil {
		return false, err
	}

	inSection := false
	sectionHeader := fmt.Sprintf("[%s]", id)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = line == sectionHeader
			continue
		}
		if inSection && strings.HasPrefix(line, "enabled") {
			return strings.HasSuffix(line, "=1"), nil
		}
	}

	// No explicit enabled= line means dnf's own default of enabled.
	return true, nil
}

// EnsurePinnedPackages installs and locks specific package versions.
// Follows Check-Diff-Act pattern for idempotency.
func EnsurePinnedPackages(pkgs []string) error {
	if len(pkgs) == 0 {
		return nil
	}

	pkgLog.Infof("Processing %d pinned packages...", len(pkgs))

	// Ensure versionlock plugin is installed
	pkgLog.Info("Ensuring dnf-plugin-versionlock is installed...")
	if err := EnsurePackages([]string{"python3-dnf-plugin-versionlock"}); err != nil {
		return fmt.Errorf("failed to install versionlock plugin: %w", err)
	}

	// Process each pinned package
	for _, pkg := range pkgs {
		pkgLog.Infof("Checking pinned package: %s", pkg)

		// Check: Is package already installed?
		checkCmd := exec.Command("rpm", "-q", pkg)
		isInstalled := checkCmd.Run() == nil

		// Check: Is package already locked?
		listCmd := exec.Command("dnf", "versionlock", "list")
		output, err := listCmd.Output()
		isLocked := err == nil && strings.Contains(string(output), pkg)

		// Diff: If both installed and locked, skip
		if isInstalled && isLocked {
			pkgLog.Infof("Package %s already installed and locked. Skipping.", pkg)
			continue
		}

		// Act: Install if needed
		if !isInstalled {
			pkgLog.Infof("Installing pinned package: %s", pkg)
			cmd := exec.Command("dnf", "install", "-y", pkg)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to install pinned package %s: %w", pkg, err)
			}
		}

		// Act: Lock if needed
		if !isLocked {
			pkgLog.Infof("Locking package version: %s", pkg)
			cmd := exec.Command("dnf", "versionlock", "add", pkg)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to lock version for %s: %w", pkg, err)
			}
		}
	}

	pkgLog.Info("All pinned packages verified")
	return nil
}
