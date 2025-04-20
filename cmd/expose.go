package cmd

import (
	"log/slog"
	"os"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/spf13/cobra"
)

var (
	targetFile string
	execute    bool
)

// expose サブコマンド：未エクスポートなシンボルを外部に露出させるためのリネーム生成を行います。
var exposeCmd = &cobra.Command{
	Use:   "expose",
	Short: "Generate renaming commands to expose unexported symbols in the target file",
	Run: func(cmd *cobra.Command, args []string) {
		if targetFile == "" {
			slog.Error("Target file is required. Please specify the target file using the --file flag.")
			os.Exit(1)
		}

		renamer, err := pachanger.NewExposeRenamer(workDir, targetFile, tagsFlag, execute)
		if err != nil {
			slog.Error("Failed to initialize ExposeRenamer", slog.Any("error", err))
			os.Exit(1)
		}
		if err := renamer.Generate(); err != nil {
			slog.Error("Rename generation failed", slog.String("target_file", targetFile), slog.Any("error", err))
			os.Exit(1)
		}

		slog.Info("Rename generation completed successfully", slog.String("target_file", targetFile))
	},
}

func init() {
	cdir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	// サブコマンドをルートコマンドに追加
	rootCmd.AddCommand(exposeCmd)

	// コマンドラインオプションの定義
	exposeCmd.Flags().StringVar(&tagsFlag, "tags", "", "Specify build tags (e.g., 'test,integration')")
	exposeCmd.Flags().StringVar(&targetFile, "file", "", "Path to the target Go file (required)")
	exposeCmd.Flags().StringVar(&workDir, "workdir", cdir, "Working directory (default: current directory)")
	exposeCmd.Flags().BoolVar(&execute, "execute", false, "Execute the renaming (default: false)")
}
