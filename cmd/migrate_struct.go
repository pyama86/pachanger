package cmd

import (
	"log/slog"
	"os"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/spf13/cobra"
)

var (
	suffix    string
	testFile  string
	targetPkg string
)

// migrate struct コマンドのサブコマンド
var migrateStructCmd = &cobra.Command{
	Use:   "struct",
	Short: "Migrate structs in the test file and add suffix to struct names",
	Run: func(cmd *cobra.Command, args []string) {
		if testFile == "" {
			slog.Error("Test file is required")
			os.Exit(1)
		}

		if suffix == "" {
			suffix = "ForTest"
		}

		ms := pachanger.NewMigrateStruct(workDir, targetPkg, suffix)
		if err := ms.Migrate(testFile); err != nil {
			slog.Error("Failed to migrate struct", slog.String("test_file", testFile), slog.Any("error", err))
			os.Exit(1)
		}

		slog.Info("Refactor completed", slog.String("test_file", testFile))
	},
}

func init() {
	cdir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	// migrate struct サブコマンドを rootCmd に追加
	rootCmd.AddCommand(migrateStructCmd)

	// オプション引数をサブコマンドに追加
	migrateStructCmd.Flags().StringVar(&suffix, "suffix", "ForTest", "Suffix to add to struct names")
	migrateStructCmd.Flags().StringVar(&testFile, "file", "", "Path to the test file (required)")
	migrateStructCmd.Flags().StringVar(&targetPkg, "pkg", "", "Target package name (required)")
	migrateStructCmd.Flags().StringVar(&workDir, "workdir", cdir, "Working directory (default: current directory)")
}
