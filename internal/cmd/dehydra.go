package cmd

import (
	"fmt"
	"os"

	"github.com/aklcy/fedora-trisolaran/internal/artifact"
	"github.com/aklcy/fedora-trisolaran/internal/config"
	"github.com/aklcy/fedora-trisolaran/internal/utils"

	"github.com/spf13/cobra"
)

var dehydraOutput string

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

	paths := make([]string, len(blueprint.UserSpace.Dehydration.Paths))
	for i, p := range blueprint.UserSpace.Dehydration.Paths {
		paths[i] = utils.ExpandPath(p, homeDir)
	}

	fmt.Printf("🍂 Dehydrating %d paths...\n", len(paths))
	notices, err := artifact.Pack(paths, blueprintPath, dehydraOutput)
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
