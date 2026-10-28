package session

import (
	"github.com/aklcy/fedora-trisolaran/internal/config"
)

// Session holds all runtime state for a single provision execution.
// This is created locally in runProvision() and passed to Acts as needed.
//
// Design: Local instance, not global singleton. Explicit parameter passing.
type Session struct {
	// Configuration (loaded from files)
	Blueprint *config.Blueprint
	Secrets   *config.Secrets

	// User Identity (discovered at runtime)
	Username string // Real user who invoked sudo (e.g., "ack")
	UID      int    // User's UID
	GID      int    // User's GID
	UserHome string // User's home directory path (e.g., "/home/ack")

	// Infrastructure State (from Block II)
	LuksMapperName string // LUKS mapper name (e.g., "company_data")
	LuksMountPoint string // LUKS mount point (e.g., "/mnt/company_data")
	LuksUnlocked   bool   // Whether LUKS device is currently unlocked
	LuksMounted    bool   // Whether LUKS device is currently mounted

	// System State (from Block III)
	// TimeSynced reports whether EnsureTimeSync confirmed NTP sync. Block
	// IV's artifact restore only trusts a file-timestamp comparison
	// (newer wins) when this is true; otherwise it keeps whatever's
	// already on disk rather than compare against a clock that might be
	// wrong.
	TimeSynced bool

	// Temporary Variables
	ArtifactPath string // Path to artifact tgz (explicit positional arg, or auto-detected default)
}
