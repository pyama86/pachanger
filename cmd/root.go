package cmd

import (
	"context"
	"errors"
	"go/token"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var (
	targetFile string
	newPkg     string
	outputDir  string
	workDir    string
)

var rootCmd = &cobra.Command{
	Use:   "pachanger",
	Short: "Rename a package and update references in other files",
	Run: func(cmd *cobra.Command, args []string) {
		if cmd.Name() == "version" {
			return
		}

		if targetFile == "" || newPkg == "" {
			if err := cmd.Help(); err != nil {
				slog.Error("Failed to show help", slog.Any("error", err))
			}
			slog.Error("Required flag(s) not set")
			os.Exit(1)
		}

		run(cmd, args)
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {

	cdir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	rootCmd.Flags().StringVar(&targetFile, "file", "", "Target file to rename package (required)")
	rootCmd.Flags().StringVar(&newPkg, "new", "", "New package name (required)")
	rootCmd.Flags().StringVar(&outputDir, "output", "", "Output directory for modified files (default: same as input file)")
	rootCmd.Flags().StringVar(&workDir, "workdir", cdir, "Working directory(default: current directory)")

	rootCmd.AddCommand(versionCmd)
}

func run(cmd *cobra.Command, _ []string) {
	ctx := context.Background()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to determine absolute workdir path",
			slog.String("workDir", workDir), slog.Any("error", err))
		return
	}

	absTargetFile := targetFile
	if !filepath.IsAbs(targetFile) {
		absTargetFile = path.Join(absWorkDir, targetFile)
	}

	if outputDir == "" {
		outputDir = filepath.Dir(absTargetFile)
	}

	if !filepath.IsAbs(outputDir) {
		outputDir = path.Join(absWorkDir, outputDir)
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to determine absolute output path",
			slog.String("outputDir", outputDir), slog.Any("error", err))
		return
	}

	if err := os.MkdirAll(absOutputDir, os.ModePerm); err != nil {
		slog.ErrorContext(ctx, "Failed to create output directory",
			slog.String("outputDir", absOutputDir), slog.Any("error", err))
		return
	}

	absOutputFile := filepath.Join(outputDir, filepath.Base(absTargetFile))

	// 既にファイルがあれば削除
	if err := os.Remove(absOutputFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.ErrorContext(ctx, "Failed to remove existing file",
				slog.String("file", absOutputFile), slog.Any("error", err))
			return
		}
	}

	fs := token.NewFileSet()
	allPkg, err := pachanger.GetPackages(fs, absWorkDir, "./...")
	if err != nil {
		slog.ErrorContext(ctx, "Error getting packages", slog.Any("error", err))
		return
	}

	node, pkg, err := pachanger.FilterPackage(fs, allPkg, absTargetFile)
	if err != nil {
		slog.ErrorContext(ctx, "Error filtering package", slog.Any("error", err))
		return
	}

	err = pachanger.ProcessTargetFile(fs, node, pkg.TypesInfo, absTargetFile, absOutputFile, newPkg)
	if err != nil {
		slog.ErrorContext(ctx, "Error processing target file",
			slog.String("file", absTargetFile), slog.Any("error", err))
		return
	}

	g, ctx := errgroup.WithContext(ctx)
	// goimportsが結構重いので、CPUの半分だけ並列処理
	sem := make(chan struct{}, runtime.NumCPU()/2)

	err = filepath.WalkDir(absWorkDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !strings.HasSuffix(path, ".go") || path == absTargetFile || path == absOutputFile {
			return nil
		}

		node, pkg, err := pachanger.FilterPackage(fs, allPkg, path)
		if err != nil {
			slog.WarnContext(ctx, "Error filtering package", slog.Any("error", err))
			return nil
		}
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			return pachanger.ProcessOtherFiles(fs, node, pkg.TypesInfo, path, absTargetFile, absOutputFile, newPkg)
		})
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "Error walking directory", slog.Any("error", err))
		return
	}

	if err := g.Wait(); err != nil {
		slog.ErrorContext(ctx, "Error updating references", slog.Any("error", err))
	}
}
