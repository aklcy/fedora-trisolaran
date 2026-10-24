package cmd

import (
	"fmt"
	"os"

	"github.com/acker1019/fedora-trisolaran/internal/artifact"
	"github.com/acker1019/fedora-trisolaran/internal/config"
	"github.com/acker1019/fedora-trisolaran/internal/ops"
	"github.com/acker1019/fedora-trisolaran/internal/session"
	"github.com/acker1019/fedora-trisolaran/internal/utils"

	"github.com/spf13/cobra"
)

// defaultArtifactPath is the conventional filename `tri dehydra` writes to
// when -o/--output isn't given (see dehydra.go). If rehydra isn't told
// which artifact to use, it looks for exactly this file next to where it's
// run, instead of silently skipping user space restore.
const defaultArtifactPath = "trisolaran-backup.tgz"

// rehydraCmd represents the rehydra command
var rehydraCmd = &cobra.Command{
	Use:   "rehydra [artifact]",
	Short: "Start the full rehydration protocol",
	Long: `Unlock LUKS, mount data, install packages, and restore user space from an artifact.

The artifact tgz (produced by 'tri dehydra') is given as a positional
argument. If omitted, rehydra looks for ` + defaultArtifactPath + ` next to
where it's run; if that isn't there either, user space restore is skipped
(e.g. for a first-ever provision with no prior artifact).`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runRehydra(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(rehydraCmd)
	// 如果 rehydra 有自己專屬的 flag，可以在這裡加
	// rehydraCmd.Flags().BoolP("dry-run", "d", false, "Preview changes only")
}

func runRehydra(cmd *cobra.Command, args []string) {
	// Resolve the artifact path: explicit positional arg always wins (and
	// must exist, or Restore below fails loudly). Otherwise, auto-detect
	// defaultArtifactPath next to where the command runs; if it's not
	// there, artifactPath stays "" and user space restore is skipped.
	var artifactPath string
	artifactExplicit := len(args) > 0
	if artifactExplicit {
		artifactPath = args[0]
	} else if _, err := os.Stat(defaultArtifactPath); err == nil {
		artifactPath = defaultArtifactPath
	}

	// 1. Validate Flags
	if secretsPath == "" {
		fmt.Println("❌ Error: --secrets flag is required.")
		fmt.Println("Usage: sudo tri rehydra --secrets=/path/to/secrets.yml")
		os.Exit(1)
	}

	// 2. Root Check
	if os.Geteuid() != 0 {
		fmt.Println("❌ Error: This command must be run as root (sudo).")
		os.Exit(1)
	}

	fmt.Println("💧 Initiating Rehydration Protocol...")

	// ============================================================================
	// Initialize Session
	// ============================================================================
	sess := &session.Session{}

	// 3. Real User Detection (supports X11 & Wayland)
	realUser, realUID, realGID, err := utils.GetRealUser()
	if err != nil {
		panic(fmt.Sprintf("Failed to detect real user: %v", err))
	}
	sess.Username = realUser
	sess.UID = realUID
	sess.GID = realGID
	sess.UserHome, err = ops.EnsureUserHome(sess.Username, sess.UID, sess.GID)
	if err != nil {
		panic(fmt.Sprintf("Failed to ensure home directory: %v", err))
	}
	fmt.Printf("✓ Detected real user: %s (UID %d, GID %d) -> %s\n", sess.Username, sess.UID, sess.GID, sess.UserHome)

	// ============================================================================
	// Block I: Identity & Configuration
	// ============================================================================
	fmt.Println("🔑 Step 1/5: Loading configuration...")

	// Load Blueprint. If --blueprint was not explicitly passed but an
	// artifact was given, prefer the blueprint embedded in that artifact
	// (see artifact.ExtractBlueprint) over the --blueprint default, so a
	// single artifact tgz can be self-sufficient for rehydration.
	sess.Blueprint = nil
	if !cmd.Flags().Changed("blueprint") && artifactPath != "" {
		if data, embedErr := artifact.ExtractBlueprint(artifactPath); embedErr == nil {
			sess.Blueprint, err = config.ParseBlueprint(data)
			if err != nil {
				panic(fmt.Sprintf("Failed to parse blueprint embedded in artifact: %v", err))
			}
			fmt.Println("📦 Using blueprint embedded in artifact (no --blueprint given)")
		} else if embedErr != artifact.ErrBlueprintNotEmbedded {
			panic(fmt.Sprintf("Failed to read artifact: %v", embedErr))
		}
	}
	if sess.Blueprint == nil {
		sess.Blueprint, err = config.LoadBlueprint(blueprintPath)
		if err != nil {
			panic(fmt.Sprintf("Failed to load blueprint: %v", err))
		}
	}

	// Load Secrets
	sess.Secrets, err = config.LoadSecrets(secretsPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to load secrets: %v", err))
	}

	hasLuks := sess.Blueprint.HasLuks()
	if hasLuks && sess.Secrets.LuksPassword == "" {
		panic("secrets.luks_password is required because infrastructure.luks is configured in the blueprint")
	}

	// Store artifact path
	sess.ArtifactPath = artifactPath

	// ============================================================================
	// Block II: Infrastructure
	// ============================================================================
	fmt.Println("🔧 Step 2/5: Setting up infrastructure...")

	if hasLuks {
		// Store infrastructure info in session
		sess.LuksMapperName = sess.Blueprint.Infrastructure.Luks.MapperName
		sess.LuksMountPoint = sess.Blueprint.Infrastructure.Luks.MountPoint

		// LUKS Unlock
		err = ops.UnlockLuks(
			sess.Blueprint.Infrastructure.Luks.Device,
			sess.LuksMapperName,
			sess.Secrets.LuksPassword,
		)
		if err != nil {
			panic(err)
		}
		sess.LuksUnlocked = true

		// Mount Device
		if err := ops.MountDevice(
			sess.LuksMapperName,
			sess.LuksMountPoint,
		); err != nil {
			panic(err)
		}
		sess.LuksMounted = true
	} else {
		fmt.Println("↷ No infrastructure.luks configured, skipping LUKS unlock/mount.")
	}

	// ============================================================================
	// Block III: System State
	// ============================================================================
	fmt.Println("📦 Step 3/5: Configuring system state...")

	var runNotices []string

	// Sync the clock first, before anything else touches package state or
	// user-space content -- dnf/HTTPS validation depends on a correct
	// clock, and so does Block IV's restore-vs-disk timestamp comparison
	// below (sess.TimeSynced gates whether that comparison is trusted).
	sess.TimeSynced = ops.EnsureTimeSync()
	if !sess.TimeSynced {
		runNotices = append(runNotices, "time sync did not complete -- Block IV kept on-disk files instead of comparing timestamps against the artifact")
	}

	// Check-only, never auto-fixed: a wrong boot parameter here needs a
	// bootloader edit + reboot, which this tool won't do unattended.
	if msg := ops.CheckIOMMUNotDisabled(); msg != "" {
		runNotices = append(runNotices, msg)
	}

	// Update all installed packages first, before anything else in this
	// block touches package state. Opt-out via system.skip_update; on by
	// default.
	if !sess.Blueprint.System.SkipUpdate {
		if err := ops.EnsureSystemUpdate(); err != nil {
			panic(err)
		}
	}

	// Ensure Groups (before Users, since users may reference them)
	if len(sess.Blueprint.System.Groups) > 0 {
		if err := ops.EnsureGroups(sess.Blueprint.System.Groups); err != nil {
			panic(err)
		}
	}

	// Ensure Users
	if len(sess.Blueprint.System.Users) > 0 {
		if err := ops.EnsureUsers(sess.Blueprint.System.Users); err != nil {
			panic(err)
		}
	}

	// Ensure Pkg Repos (before Pkgs, since packages may come from them)
	if len(sess.Blueprint.System.PkgRepos) > 0 {
		if err := ops.EnsurePkgRepos(sess.Blueprint.System.PkgRepos); err != nil {
			panic(err)
		}
	}

	// Ensure COPR Repos (before Pkgs, since packages may come from them)
	if len(sess.Blueprint.System.CoprRepos) > 0 {
		if err := ops.EnsureCoprRepos(sess.Blueprint.System.CoprRepos); err != nil {
			panic(err)
		}
	}

	// Install Packages
	if len(sess.Blueprint.System.Pkgs) > 0 {
		if err := ops.EnsurePackages(sess.Blueprint.System.Pkgs); err != nil {
			panic(err)
		}
	}

	// Install Pinned Packages
	if len(sess.Blueprint.System.PinnedPackages) > 0 {
		if err := ops.EnsurePinnedPackages(sess.Blueprint.System.PinnedPackages); err != nil {
			panic(err)
		}
	}

	// Enable Services
	if len(sess.Blueprint.System.Services) > 0 {
		if err := ops.EnsureServices(sess.Blueprint.System.Services); err != nil {
			panic(err)
		}
	}

	// Declare boot-time tmpfiles cleanup (e.g. stale app lock files)
	if len(sess.Blueprint.System.Tmpfiles) > 0 {
		if err := ops.EnsureTmpfiles(sess.Blueprint.System.Tmpfiles, sess.UserHome); err != nil {
			panic(err)
		}
	}

	// Set User Shell
	if sess.Blueprint.Identity.Shell != "" {
		if err := ops.EnsureUserShell(sess.Blueprint.Identity.Username, sess.Blueprint.Identity.Shell); err != nil {
			panic(err)
		}
	}

	// ============================================================================
	// Block IV: User Space
	// ============================================================================
	fmt.Println("👤 Step 4/5: Restoring user space...")

	// Declarative and unconditional, like System.Tmpfiles above: no
	// pipeline placement needed.
	if len(sess.Blueprint.UserSpace.SystemdUserUnits) > 0 {
		if err := ops.EnsureSystemdUserUnits(sess.Blueprint.UserSpace.SystemdUserUnits, sess.Blueprint.Identity.Username); err != nil {
			panic(err)
		}
	}
	if sess.Blueprint.UserSpace.KDEConnectAutorestart {
		if err := ops.EnsureKDEConnectAutorestart(sess.Blueprint.Identity.Username); err != nil {
			panic(err)
		}
	}

	restoreDehydration := func() {
		if sess.ArtifactPath != "" {
			notices, err := artifact.Restore(sess.ArtifactPath, sess.TimeSynced)
			if err != nil {
				panic(err)
			}
			runNotices = append(runNotices, notices...)
		} else {
			fmt.Printf("⚠️  No artifact given and %q not found, skipping user space restore (dotfiles/SSH keys/etc. will NOT be restored)\n", defaultArtifactPath)
		}
	}

	cloneRepos := func() {
		for _, repo := range sess.Blueprint.UserSpace.Repos {
			expandedDest := utils.ExpandPath(repo.Dest, sess.UserHome)
			if err := ops.GitClone(repo.URL, expandedDest, sess.Blueprint.Identity.Username); err != nil {
				panic(err)
			}
		}
	}

	// By default (no pipeline step explicitly places them), restore and
	// clone happen first, before any pipeline scripts -- see
	// config.UserSpaceConfig.Pipeline.
	hasExplicitDehydration := false
	hasExplicitRepos := false
	for _, step := range sess.Blueprint.UserSpace.Pipeline {
		if step.Run == config.RunDehydration {
			hasExplicitDehydration = true
		}
		if step.Run == config.RunRepos {
			hasExplicitRepos = true
		}
	}
	if !hasExplicitDehydration {
		restoreDehydration()
	}
	if !hasExplicitRepos {
		cloneRepos()
	}

	for _, step := range sess.Blueprint.UserSpace.Pipeline {
		switch {
		case step.Script != "":
			if err := ops.EnsureScripts([]string{step.Script}, sess.Blueprint.Identity.Username); err != nil {
				panic(err)
			}
		case step.Run == config.RunDehydration:
			restoreDehydration()
		case step.Run == config.RunRepos:
			cloneRepos()
		case step.Run == config.RunVSCodeExtensions:
			if err := ops.EnsureVSCodeExtensions(sess.Blueprint.UserSpace.VSCodeExtensions, sess.Blueprint.Identity.Username); err != nil {
				panic(err)
			}
		case step.Run == config.RunFlatpaks:
			if err := ops.EnsureFlatpaks(sess.Blueprint.UserSpace.Flatpaks, sess.Blueprint.Identity.Username); err != nil {
				panic(err)
			}
		}
	}

	fmt.Println("✨ Rehydration Complete. Welcome back, Commander.")

	if len(runNotices) > 0 {
		reportPath := "rehydra-notices.txt"
		if err := utils.WriteNoticesReport(runNotices, reportPath); err != nil {
			panic(fmt.Sprintf("Failed to write notices report: %v", err))
		}
		fmt.Printf("\n⚠️  %d note(s) worth a second look -- see %s\n", len(runNotices), reportPath)
	}
}
