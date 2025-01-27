package pachanger

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/tools/imports"
)

// WriteFile は変更後の AST をフォーマットし、インポート整理しつつ指定パスへ書き出す。
func WriteFile(outputPath string, fs *token.FileSet, node *ast.File) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}
	defer func() {
		err := os.Chdir(originalDir)
		if err != nil {
			slog.Warn("failed to change directory to original", slog.Any("error", err))
		}
	}()

	dir := filepath.Dir(outputPath)
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("failed to change directory to %s: %v", dir, err)
	}

	var buf bytes.Buffer
	config := &printer.Config{
		Mode:     printer.UseSpaces,
		Tabwidth: 4,
	}
	if err := config.Fprint(&buf, fs, node); err != nil {
		return err
	}

	formatted, err := imports.Process(outputPath, buf.Bytes(), &imports.Options{
		Comments:   true,
		TabWidth:   4,
		Fragment:   false,
		FormatOnly: false,
	})

	if err != nil {
		_ = os.WriteFile(outputPath, buf.Bytes(), 0644)

		return fmt.Errorf("failed to format and manage imports: %v", err)
	}

	if err := os.WriteFile(outputPath, formatted, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %v", outputPath, err)
	}

	return nil
}
