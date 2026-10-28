package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aklcy/fedora-trisolaran/internal/artifact"
	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/utils"

	"github.com/spf13/cobra"
)

var (
	dehydraOutput   string
	dehydraStageDir string
)

// dehydraCmd represents the dehydra command
var dehydraCmd = &cobra.Command{
	Use:   "dehydra",
	Short: "Collect configured paths into a single artifact",
	Long:  `Reads userspace.dehydration.paths from the blueprint and packs them into an artifact tgz (ADR-0008).`,
	Run: func(cmd *cobra.Command, args []string) {
		runDehydra()
	},
}

func init() {
	rootCmd.AddCommand(dehydraCmd)
	dehydraCmd.Flags().StringVarP(&dehydraOutput, "output", "o", "trisolaran-backup.tgz", "Path to write the artifact")
	dehydraCmd.Flags().StringVar(&dehydraStageDir, "stage-dir", "", "Directory for the uncompressed staging copy (default: system temp dir; on Fedora /tmp is tmpfs, i.e. RAM)")
}

func runDehydra() {
	blueprint, err := config.LoadBlueprint(blueprintPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to load blueprint: %v", err))
	}

	if len(blueprint.UserSpace.Dehydration.Paths) == 0 {
		fmt.Println("❌ Error: no paths configured under userspace.dehydration.paths")
		os.Exit(1)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("Failed to determine home directory: %v", err))
	}

	// A leading "!" marks an exclude, gitignore-style: that path (and
	// everything under it) is left out even when a listed path covers it.
	var paths, excludes []string
	for _, p := range blueprint.UserSpace.Dehydration.Paths {
		if ex, ok := strings.CutPrefix(p, "!"); ok {
			excludes = append(excludes, filepath.Clean(utils.ExpandPath(ex, homeDir)))
			continue
		}
		paths = append(paths, utils.ExpandPath(p, homeDir))
	}

	if len(paths) == 0 {
		fmt.Println("❌ Error: userspace.dehydration.paths has only excludes, nothing to collect")
		os.Exit(1)
	}

	fmt.Printf("🍂 Dehydrating %d paths (%d excluded)...\n", len(paths), len(excludes))
	notices, err := artifact.Pack(paths, excludes, blueprintPath, dehydraOutput, dehydraStageDir)
	if err != nil {
		panic(fmt.Sprintf("Failed to pack artifact: %v", err))
	}

	fmt.Printf("✓ Artifact written to %s\n", dehydraOutput)

	if len(notices) > 0 {
		reportPath := dehydraOutput + ".notices.txt"
		if err := utils.WriteNoticesReport(notices, reportPath); err != nil {
			panic(fmt.Sprintf("Failed to write notices report: %v", err))
		}
		fmt.Printf("\n⚠️  %d path(s) worth a second look -- see %s\n", len(notices), reportPath)
	}
}
