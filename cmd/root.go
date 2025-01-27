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
	targetFile   string
	newPkg       string
	outputPath   string
	workDir      string
	deletePrefix string
	tagsFlag     string
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
	rootCmd.Flags().StringVar(&outputPath, "output", "", "Output file path (default: same directory as target file)")
	rootCmd.Flags().StringVar(&workDir, "workdir", cdir, "Working directory (default: current directory)")
	rootCmd.Flags().StringVar(&deletePrefix, "delete-prefix", "", "Delete prefix from symbol name")
	rootCmd.Flags().StringVar(&tagsFlag, "tags", "", "Build tags (e.g. 'test,integration')")

	rootCmd.AddCommand(versionCmd)
}

func run(cmd *cobra.Command, _ []string) {
	ctx := context.Background()
	buildFlags := []string{}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to determine absolute workdir path",
			slog.String("workDir", workDir), slog.Any("error", err))
		return
	}

	if tagsFlag != "" {
		buildFlags = append(buildFlags, "-tags", tagsFlag)
	}

	// ターゲットファイルの絶対パス
	absTargetFile := targetFile
	if !filepath.IsAbs(targetFile) {
		absTargetFile = path.Join(absWorkDir, targetFile)
	}

	// 出力ファイルの絶対パス
	if outputPath == "" {
		outputPath = path.Join(filepath.Dir(absTargetFile), filepath.Base(absTargetFile))
	}
	if !filepath.IsAbs(outputPath) {
		outputPath = path.Join(absWorkDir, outputPath)
	}

	ext := filepath.Ext(outputPath)
	if ext == "" || ext == "." {
		outputPath = path.Join(outputPath, filepath.Base(absTargetFile))
	}

	absOutputFile, err := filepath.Abs(outputPath)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to determine absolute output path",
			slog.String("outputPath", outputPath), slog.Any("error", err))
		return
	}

	// 出力先ディレクトリを作成しておく
	if err := os.MkdirAll(filepath.Dir(absOutputFile), 0755); err != nil {
		slog.ErrorContext(ctx, "Failed to create output directory",
			slog.String("dir", filepath.Dir(absOutputFile)), slog.Any("error", err))
		return
	}

	// ターゲットファイルと出力ファイルが異なる場合、既存の出力ファイルを削除
	if absTargetFile != absOutputFile {
		if err := os.Remove(absOutputFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.ErrorContext(ctx, "Failed to remove existing file",
				slog.String("file", absOutputFile), slog.Any("error", err))
			return
		}
	}

	fs := token.NewFileSet()
	allPkgs, err := pachanger.LoadPackages(fs, absWorkDir, buildFlags)
	if err != nil {
		slog.ErrorContext(ctx, "Error loading packages", slog.Any("error", err))
		return
	}

	node, pkg, err := pachanger.FindPackageForFile(fs, allPkgs, absTargetFile)
	if err != nil {
		slog.ErrorContext(ctx, "Error finding target file in packages", slog.Any("error", err))
		return
	}
	oldPkg := pkg.Name

	// pachangerパッケージで定義した構造体を使って、ターゲットファイルを変換
	transformer := pachanger.NewTransformer(
		fs,
		absWorkDir,
		absTargetFile,
		absOutputFile,
		oldPkg,
		newPkg,
		deletePrefix,
	)

	if err := transformer.TransformSymbolsInTargetFile(node, pkg.TypesInfo); err != nil {
		slog.ErrorContext(ctx, "Error transforming target file",
			slog.String("file", absTargetFile), slog.Any("error", err))
		return
	}

	// 他ファイルを並列で変換
	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, runtime.NumCPU()/2)

	err = filepath.WalkDir(absWorkDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// .go ファイルで、かつターゲット/出力ファイル以外が対象
		if !strings.HasSuffix(path, ".go") || path == absTargetFile || path == absOutputFile {
			return nil
		}

		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			nodeOther, pkgOther, errFilter := pachanger.FindPackageForFile(fs, allPkgs, path)
			if errFilter != nil {
				// パースできないファイルならスキップ（警告のみ）
				slog.WarnContext(ctx, "Error filtering package", slog.Any("error", errFilter))
				return nil
			}
			if nodeOther == nil || pkgOther == nil {
				return nil
			}

			// 同じ Transformer インスタンスでOK。
			// ただし、別ファイル用の *types.Info を渡す必要がある
			return transformer.TransformSymbolsInOtherFile(nodeOther, pkgOther.TypesInfo, path)
		})
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "Error walking directory", slog.Any("error", err))
		return
	}

	if err := g.Wait(); err != nil {
		slog.ErrorContext(ctx, "Error updating references", slog.Any("error", err))
		return
	}

	slog.InfoContext(ctx, "Successfully updated references", slog.String("outputPath", absOutputFile))
}
