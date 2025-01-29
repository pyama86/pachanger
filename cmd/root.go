package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	targetFiles  []string
	newPkg       string
	outputPath   string
	workDir      string
	deletePrefix string
	tagsFlag     string
	debug        bool
)

var rootCmd = &cobra.Command{
	Use:   "pachanger",
	Short: "Rename a package and update references in other files",
	Run: func(cmd *cobra.Command, args []string) {
		if cmd.Name() == "version" {
			return
		}

		if len(targetFiles) == 0 || newPkg == "" {
			if err := cmd.Help(); err != nil {
				slog.Error("Failed to show help", slog.Any("error", err))
			}
			slog.Error("Required flag(s) not set")
			os.Exit(1)
		}

		if err := run(); err != nil {
			slog.Error("Failed to run command", slog.Any("error", err))
			os.Exit(1)
		}
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
	rootCmd.Flags().StringSliceVar(&targetFiles, "file", nil, "Target file(s) to rename package (required, or use '-')")
	rootCmd.Flags().StringVar(&newPkg, "new", "", "New package name (required)")
	rootCmd.Flags().StringVar(&outputPath, "output", "", "Output file path (default: same directory as target file)")
	rootCmd.Flags().StringVar(&workDir, "workdir", cdir, "Working directory (default: current directory)")
	rootCmd.Flags().StringVar(&deletePrefix, "delete-prefix", "", "Delete prefix from symbol name")
	rootCmd.Flags().StringVar(&tagsFlag, "tags", "", "Build tags (e.g. 'test,integration')")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "debug mode")
	rootCmd.AddCommand(versionCmd)
}

// determineOutputFile は、outputPath が空や相対パスの場合に正しい絶対パスを返し、
// ディレクトリが存在しない場合は作成します。
func determineOutputFile(
	absWorkDir string,
	absTargetFile string,
	outputPath string,
) (string, error) {

	// もし --output が指定されていない場合、ターゲットファイルと同じ場所 + 同名にする
	if outputPath == "" {
		outputPath = path.Join(filepath.Dir(absTargetFile), filepath.Base(absTargetFile))
	}

	// outputPath が相対パスなら、workDir を起点とした絶対パスにする
	if !filepath.IsAbs(outputPath) {
		outputPath = path.Join(absWorkDir, outputPath)
	}

	// 拡張子がない、または "." のみの場合は、ターゲットファイル名を付加する
	ext := filepath.Ext(outputPath)
	if ext == "" || ext == "." {
		outputPath = path.Join(outputPath, filepath.Base(absTargetFile))
	}

	// 最終的な絶対パス
	absOutputFile, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}

	// 出力先ディレクトリがない場合は作成
	if err := os.MkdirAll(filepath.Dir(absOutputFile), 0o755); err != nil {
		return "", err
	}

	return absOutputFile, nil
}

func run() error {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug

	}

	slog.SetDefault(
		slog.New(
			slog.NewTextHandler(
				os.Stdout,
				&slog.HandlerOptions{Level: level},
			),
		),
	)

	ctx := context.Background()
	buildFlags := []string{}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of workdir: %w", err)
	}

	// workDirにgo.modが存在するか確認
	if _, err := os.Stat(filepath.Join(absWorkDir, "go.mod")); err != nil {
		return fmt.Errorf("go.mod not found in workdir: %w", err)
	}

	if tagsFlag != "" {
		buildFlags = append(buildFlags, "-tags", tagsFlag)
	}

	// ターゲットファイルの絶対パス
	// targetFiles が空ならエラー
	if len(targetFiles) == 0 {
		return fmt.Errorf("target file(s) not set")
	}

	// 「-」が含まれていたら標準入力からファイルパスを読み込む
	var expanded []string
	for _, f := range targetFiles {
		if f == "-" {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				if !filepath.IsAbs(line) {
					line = path.Join(absWorkDir, line)
				}
				expanded = append(expanded, line)

			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("failed to read from stdin: %w", err)
			}
		} else {
			if !filepath.IsAbs(f) {
				f = path.Join(absWorkDir, f)
			}
			expanded = append(expanded, f)
		}
	}
	fs := token.NewFileSet()
	slog.InfoContext(ctx, "Loading packages", slog.String("workDir", absWorkDir))
	allPkgs, err := pachanger.LoadPackages(fs, absWorkDir, buildFlags)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	slog.InfoContext(ctx, "Loaded packages", slog.String("workDir", absWorkDir))

	for _, absTargetFile := range expanded {
		slog.InfoContext(ctx, "Processing target file", slog.String("file", absTargetFile))
		absOutputFile, err := determineOutputFile(absWorkDir, absTargetFile, outputPath)
		if err != nil {
			return fmt.Errorf("failed to determine output file: %w", err)
		}

		// ターゲットファイルと出力ファイルが異なる場合、既存の出力ファイルを削除
		if absTargetFile != absOutputFile {
			if err := os.Remove(absOutputFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to remove output file: %w", err)
			}
		}

		node, pkg, err := pachanger.FindPackageForFile(fs, allPkgs, absTargetFile)
		if err != nil {
			return fmt.Errorf("failed to find package for file: %w", err)
		}
		oldPkg := pkg.Name
		oldPkgPath := pkg.PkgPath

		if os.Getenv("PACHANGER_PKG_DEBUG") != "" {
			for _, pkg := range allPkgs {
				if pkg.Name != oldPkg && pkg.Name != newPkg {
					continue
				}
				fmt.Printf("Loaded Package: %s\n", pkg.PkgPath)
				if pkg.Types != nil {
					fmt.Printf("  Package Name: %s\n", pkg.Types.Name())
				}

				fmt.Println("  --- Defs ---")
				for id, obj := range pkg.TypesInfo.Defs {
					if obj != nil {
						fmt.Printf("  Def: %s (%s)\n", id.Name, obj.Type().String())
					} else {
						fmt.Printf("  Def: %s (invalid type)\n", id.Name)
					}
				}

				fmt.Println("  --- Uses ---")
				for id, obj := range pkg.TypesInfo.Uses {
					if obj != nil {
						fmt.Printf("  Use: %s (%s)\n", id.Name, obj.Type().String())
					} else {
						fmt.Printf("  Use: %s (invalid type)\n", id.Name)
					}
				}
			}
		}

		// pachangerパッケージで定義した構造体を使って、ターゲットファイルを変換
		transformer := pachanger.NewTransformer(
			fs,
			absWorkDir,
			absTargetFile,
			oldPkg,
			oldPkgPath,
			newPkg,
			deletePrefix,
		)

		if err := transformer.TransformSymbolsInTargetFile(node, pkg.TypesInfo, absOutputFile); err != nil {
			return fmt.Errorf("failed to transform symbols in target file: %w", err)
		}

		// 他ファイルを並列で変換
		g, ctx := errgroup.WithContext(ctx)
		sem := make(chan struct{}, runtime.NumCPU()/2)

		err = filepath.WalkDir(absWorkDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if os.Getenv("PACHANGER_FILTER_FILE") != "" {
				if !strings.Contains(path, os.Getenv("PACHANGER_FILTER_FILE")) {
					return nil
				}
			}

			// .go ファイルで、かつターゲット/出力ファイル以外が対象
			if !strings.HasSuffix(path, ".go") || path == absTargetFile || path == absOutputFile {
				return nil
			}

			slog.DebugContext(ctx, "Processing other file", slog.String("file", path))
			g.Go(func() error {
				sem <- struct{}{}
				defer func() { <-sem }()

				nodeOther, pkgOther, errFilter := pachanger.FindPackageForFile(fs, allPkgs, path)
				if errFilter != nil {
					// パースできないファイルならスキップ（警告のみ）
					slog.WarnContext(ctx, "Error finding package for file", slog.String("file", path), slog.Any("error", errFilter))
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
			return fmt.Errorf("failed to walk directory: %w", err)
		}

		if err := g.Wait(); err != nil {
			return fmt.Errorf("failed to transform symbols in other files: %w", err)
		}

		// ターゲットファイルを削除
		if absTargetFile != absOutputFile {
			if err := os.Remove(absTargetFile); err != nil {
				return fmt.Errorf("failed to remove target file: %w", err)
			}
		}
		slog.InfoContext(ctx, "Successfully updated file", slog.String("file", absOutputFile))
	}
	slog.InfoContext(ctx, "Successfully updated references", slog.String("newPkg", newPkg))
	return nil
}
