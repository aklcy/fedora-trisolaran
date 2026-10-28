package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/logging"
	"github.com/aklcy/fedora-trisolaran/submodules"
	"gopkg.in/yaml.v3"
)

var blueprintLog = logging.WithSource("config/blueprint")

// Blueprint defines the schema for the trisolaran.yml configuration file.
// It represents the declarative system restoration plan.
type Blueprint struct {
	Version string `yaml:"version"`

	// Infrastructure: Hardware and storage configuration
	Infrastructure InfrastructureConfig `yaml:"infrastructure"`

	// System: OS-level packages and services
	System SystemConfig `yaml:"system"`

	// Identity: Target user configuration
	Identity IdentityConfig `yaml:"identity"`

	// UserSpace: User-level configuration (Block IV)
	UserSpace UserSpaceConfig `yaml:"userspace"`
}

// InfrastructureConfig defines storage and hardware mappings
type InfrastructureConfig struct {
	Luks LuksConfig `yaml:"luks"`
}

// LuksConfig defines LUKS partition configuration
type LuksConfig struct {
	Device     string `yaml:"device"`
	MapperName string `yaml:"mapper_name"`
	MountPoint string `yaml:"mount_point"`
}

// SystemConfig defines OS-level state
type SystemConfig struct {
	SkipUpdate     bool            `yaml:"skip_update"`
	Pkgs           []string        `yaml:"pkgs"`
	PinnedPackages []string        `yaml:"pinned_packages"`
	PkgRepos       []PkgRepoConfig `yaml:"pkg_repos"`
	// CoprRepos are "owner/project" COPR repos enabled via `dnf copr
	// enable` before Pkgs installs. Separate from PkgRepos: a COPR repo
	// is resolved by Copr's own API through the dnf copr plugin, not a
	// static baseurl+gpgkey we can write ourselves.
	CoprRepos []string `yaml:"copr_repos"`
	Services  []string `yaml:"services"`
	// Tmpfiles declares systemd-tmpfiles lines applied on every boot --
	// e.g. removing a stale app lock file left behind by an unclean
	// shutdown. Declarative: no custom systemd unit needed, since
	// systemd-tmpfiles-setup.service already ships enabled on any systemd
	// system.
	Tmpfiles []TmpfileEntry `yaml:"tmpfiles"`
	Users    []UserConfig   `yaml:"users"`
	Groups   []GroupConfig  `yaml:"groups"`
}

// Known keys for TmpfileEntry -- the systemd-tmpfiles line type code.
// Currently just TmpfileRemove; more of systemd-tmpfiles' own type codes
// (e.g. "f" create-if-missing, "w" write-content) can be added here later
// without changing TmpfileEntry's shape.
const TmpfileRemove = "r"

var knownTmpfileTypes = map[string]bool{
	TmpfileRemove: true,
}

// TmpfileEntry is a single systemd-tmpfiles line, written as a one-key
// map so the key can be any systemd-tmpfiles type code: {r: <path>} means
// "remove <path> (may use ~, expanded against the target user's home) if
// it exists". One entry, one key -- see validateBlueprint.
type TmpfileEntry map[string]string

// PkgRepoConfig declares the desired state of a single dnf repo, so
// packages that live outside Fedora's default repos (e.g. VS Code, Chrome)
// can be ensured before System.Pkgs installs them. Two shapes:
//   - id only: an already-shipped repo (e.g. Fedora's google-chrome.repo,
//     provided by fedora-workstation-repositories) whose `enabled=` flag
//     just needs flipping.
//   - id + baseurl: a repo file that doesn't exist yet and must be created
//     (GPGKey, if set, is imported via rpm --import).
type PkgRepoConfig struct {
	ID      string `yaml:"id"`
	BaseURL string `yaml:"baseurl,omitempty"`
	GPGKey  string `yaml:"gpgkey,omitempty"`
	Enabled bool   `yaml:"enabled"`
}

// UserConfig defines a user account to ensure exists.
// Only the name is recorded; UID is read back from the OS after creation.
type UserConfig struct {
	Name   string   `yaml:"name"`
	System bool     `yaml:"system"`
	Groups []string `yaml:"groups"`
}

// GroupConfig defines a group to ensure exists.
// Only the name is recorded; GID is read back from the OS after creation.
type GroupConfig struct {
	Name   string `yaml:"name"`
	System bool   `yaml:"system"`
}

// IdentityConfig defines target user characteristics
type IdentityConfig struct {
	Username string `yaml:"username"`
	Shell    string `yaml:"shell"`
}

// UserSpaceConfig defines user-level configuration (Block IV)
type UserSpaceConfig struct {
	Dehydration DehydrationConfig `yaml:"dehydration"`
	Repos       []RepoConfig      `yaml:"repos"`

	// VSCodeExtensions are extension IDs (as printed by `code
	// --list-extensions`) ensured installed via `code --install-extension`
	// when a RunVSCodeExtensions step runs. Not restored from the
	// dehydration path: like other network-reinstallable tools,
	// extensions are declared here and fetched fresh each time.
	VSCodeExtensions []string `yaml:"vscode_extensions"`

	// Flatpaks are Flathub app IDs (e.g. "md.obsidian.Obsidian") ensured
	// installed, user-scope (no root, unlike Snap which needs a
	// system-level /snap symlink on Fedora), when a RunFlatpaks step
	// runs. The Flathub remote itself is added as part of the same step.
	Flatpaks []string `yaml:"flatpaks"`

	// SystemdUserUnits are systemd --user unit files installed under
	// ~/.config/systemd/user/ and enabled, run as the target user's own
	// session (unlike System.Tmpfiles, which is root-owned and applied by
	// systemd-tmpfiles-setup.service at boot). Declarative and
	// unconditional, like Tmpfiles: no pipeline placement needed, and kept
	// as its own field (not a Pipeline script) since unit content is
	// structured text, not a shell one-liner.
	SystemdUserUnits []SystemdUserUnitConfig `yaml:"systemd_user_units"`

	// KDEConnectAutorestart installs the kdeconnect-autorestart script and
	// systemd --user unit compiled into tri (see submodules), which
	// restarts kdeconnectd after unlock/resume to restore remote input on
	// GNOME. Declarative and unconditional, like SystemdUserUnits.
	KDEConnectAutorestart bool `yaml:"kdeconnect_autorestart"`

	// Pipeline is the ordered sequence of Block IV steps, run as the
	// target user. By default (no step in Pipeline has a Run of
	// RunDehydration or RunRepos), restoring the artifact and cloning
	// Repos both happen automatically before Pipeline runs, and every
	// Script step then runs in order -- the original fixed "restore,
	// clone, then scripts last" behavior. Explicitly placing a
	// RunDehydration or RunRepos step in Pipeline opts out of that
	// default and lets you interleave custom commands around them (e.g. a
	// dotfile-clobbering installer that must run *before* the artifact
	// restore, so the restore has the last word instead of the
	// installer).
	//
	// A step's Run keyword is deliberately the same word as the section
	// it triggers (RunDehydration -> the Dehydration section above,
	// RunRepos -> the Repos section above) rather than a separate
	// "restore_x" verb, so there's only one name per concept to remember.
	// RunVSCodeExtensions and RunFlatpaks have no such default-run
	// behavior (nothing implicitly needs them), so a step for either only
	// runs where you place one.
	//
	// Script entries are opaque to Trisolaran: making a given line safe
	// to rerun is the script author's responsibility, not the engine's.
	Pipeline []PipelineStep `yaml:"pipeline"`
}

// Known values for PipelineStep.Run.
const (
	RunDehydration      = "dehydration"
	RunRepos            = "repos"
	RunVSCodeExtensions = "vscode_extensions"
	RunFlatpaks         = "flatpaks"
)

var knownRunValues = map[string]bool{
	RunDehydration:      true,
	RunRepos:            true,
	RunVSCodeExtensions: true,
	RunFlatpaks:         true,
}

// PipelineStep is exactly one of: a custom one-liner (Script), or a named
// built-in action (Run -- one of RunDehydration, RunRepos,
// RunVSCodeExtensions). Run is a keyword, not a bool, so future built-in
// actions can be added as new valid values without growing the struct
// every time.
type PipelineStep struct {
	Script string `yaml:"script,omitempty"`
	Run    string `yaml:"run,omitempty"`
}

// DehydrationConfig defines which paths are collected into an artifact.
// See ADR-0008 (Artifact Storage Format).
type DehydrationConfig struct {
	Paths []string `yaml:"paths"`
}

// RepoConfig defines a git repository to clone
type RepoConfig struct {
	URL  string `yaml:"url"`
	Dest string `yaml:"dest"`
}

// SystemdUserUnitConfig declares a single systemd --user unit file, its
// content written verbatim into the blueprint.
type SystemdUserUnitConfig struct {
	// Name is the unit's base filename, without a .service suffix (e.g.
	// "trisolaran-login-cleanup" -> ~/.config/systemd/user/trisolaran-login-cleanup.service).
	Name string `yaml:"name"`
	// Content is the full unit file text (INI sections such as [Unit],
	// [Service], [Install]).
	Content string `yaml:"content"`
}

// LoadBlueprint reads and parses the trisolaran.yml blueprint file.
func LoadBlueprint(path string) (*Blueprint, error) {
	blueprintLog.Infof("Loading blueprint from: %s", path)

	// 1. Check file existence
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("blueprint file not found at: %s", path)
	}

	// 2. Read file content
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read blueprint file: %w", err)
	}

	bp, err := ParseBlueprint(data)
	if err != nil {
		return nil, err
	}

	blueprintLog.Info("Blueprint loaded successfully")
	return bp, nil
}

// ParseBlueprint parses and validates blueprint YAML content regardless of
// where it came from — a file on disk, or one embedded in an artifact tgz
// (see artifact.ExtractBlueprint).
func ParseBlueprint(data []byte) (*Blueprint, error) {
	var bp Blueprint

	// KnownFields rejects unrecognized keys instead of silently dropping
	// them. Without this, a stale field left over from a schema rename
	// (e.g. system.packages -> system.pkgs) parses successfully but the
	// new field just never gets set, with no error anywhere.
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&bp); err != nil {
		return nil, fmt.Errorf("failed to parse YAML structure: %w", err)
	}

	if err := validateBlueprint(&bp); err != nil {
		return nil, fmt.Errorf("invalid blueprint: %w", err)
	}

	return &bp, nil
}

// HasLuks reports whether infrastructure.luks is configured in the blueprint.
// LUKS is optional: an absent `infrastructure` block or an empty `luks`
// sub-block means the rehydration protocol skips LUKS unlock/mount entirely.
func (bp *Blueprint) HasLuks() bool {
	l := bp.Infrastructure.Luks
	return l.Device != "" || l.MapperName != "" || l.MountPoint != ""
}

// validateBlueprint ensures critical fields are present
func validateBlueprint(bp *Blueprint) error {
	if bp.Version == "" {
		return fmt.Errorf("version field is required")
	}

	// Validate Infrastructure: LUKS is optional, but if any field is set,
	// all three are required (a partially configured LUKS block is an error).
	if bp.HasLuks() {
		if bp.Infrastructure.Luks.Device == "" {
			return fmt.Errorf("infrastructure.luks.device is required when infrastructure.luks is configured")
		}
		if bp.Infrastructure.Luks.MapperName == "" {
			return fmt.Errorf("infrastructure.luks.mapper_name is required when infrastructure.luks is configured")
		}
		if bp.Infrastructure.Luks.MountPoint == "" {
			return fmt.Errorf("infrastructure.luks.mount_point is required when infrastructure.luks is configured")
		}
	}

	// Validate Identity
	if bp.Identity.Username == "" {
		return fmt.Errorf("identity.username is required")
	}

	// Validate Pipeline: each step must set exactly one of Script/Run, and
	// a Run value must be a known keyword, so there's no ambiguity about
	// what a step does (or silent no-ops from an empty/misspelled step).
	for i, step := range bp.UserSpace.Pipeline {
		set := 0
		if step.Script != "" {
			set++
		}
		if step.Run != "" {
			set++
		}
		if set != 1 {
			return fmt.Errorf("userspace.pipeline[%d] must set exactly one of script or run", i)
		}
		if step.Run != "" && !knownRunValues[step.Run] {
			return fmt.Errorf("userspace.pipeline[%d]: unknown run value %q", i, step.Run)
		}
	}

	// Validate Tmpfiles: each entry must be exactly one key, and that key
	// must be a known systemd-tmpfiles type code.
	for i, entry := range bp.System.Tmpfiles {
		if len(entry) != 1 {
			return fmt.Errorf("system.tmpfiles[%d] must have exactly one key (a tmpfiles type code)", i)
		}
		for t := range entry {
			if !knownTmpfileTypes[t] {
				return fmt.Errorf("system.tmpfiles[%d]: unknown tmpfiles type %q", i, t)
			}
		}
	}

	// Validate SystemdUserUnits: each unit needs a name (used as the
	// filename, so no path separators) and non-empty content.
	seenUnitNames := make(map[string]bool, len(bp.UserSpace.SystemdUserUnits))
	for i, unit := range bp.UserSpace.SystemdUserUnits {
		if unit.Name == "" {
			return fmt.Errorf("userspace.systemd_user_units[%d].name is required", i)
		}
		if strings.ContainsAny(unit.Name, "/\\") {
			return fmt.Errorf("userspace.systemd_user_units[%d].name must not contain a path separator: %q", i, unit.Name)
		}
		if seenUnitNames[unit.Name] {
			return fmt.Errorf("userspace.systemd_user_units[%d]: duplicate name %q", i, unit.Name)
		}
		seenUnitNames[unit.Name] = true
		if unit.Content == "" {
			return fmt.Errorf("userspace.systemd_user_units[%d].content is required", i)
		}
	}
	if bp.UserSpace.KDEConnectAutorestart && seenUnitNames[submodules.KDEConnectAutorestartName] {
		return fmt.Errorf("userspace.systemd_user_units: %q is already installed by userspace.kdeconnect_autorestart", submodules.KDEConnectAutorestartName)
	}

	return nil
}
