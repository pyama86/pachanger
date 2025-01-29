package pachanger

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const SHOULD_BE_DELETED = "SHOULD_BE_DELETED"

type Transformer struct {
	fs           *token.FileSet
	oldFile      string
	oldPkg       string
	oldPkgPath   string
	newPkgPath   string
	newPkg       string
	addPrefix    string
	deletePrefix string
	workDir      string
	doneIdent    map[*ast.Ident]bool
}

// NewTransformer は Transformer を生成
func NewTransformer(fs *token.FileSet, workDir, oldFile, oldPkg, oldPkgPath, newPkg, addPrefix, deletePrefix string) *Transformer {
	newPkgPath := ""
	pos := strings.LastIndex(oldPkgPath, oldPkg)
	if pos > 0 {
		newPkgPath = oldPkgPath[:pos] + newPkg
	}

	return &Transformer{
		fs:           fs,
		oldFile:      oldFile,
		oldPkg:       oldPkg,
		oldPkgPath:   oldPkgPath,
		newPkgPath:   newPkgPath,
		newPkg:       newPkg,
		addPrefix:    addPrefix,
		deletePrefix: deletePrefix,
		workDir:      workDir,
		doneIdent:    map[*ast.Ident]bool{},
	}
}

func LoadPackages(fs *token.FileSet, absWorkDir string, buildFlags []string) ([]*packages.Package, error) {
	slog.Debug("LoadPackages", slog.String("workDir", absWorkDir), slog.String("buildFlags", strings.Join(buildFlags, " ")))
	cfg := &packages.Config{
		Mode:       packages.LoadAllSyntax | packages.NeedForTest,
		Dir:        absWorkDir,
		Fset:       fs,
		Tests:      true,
		BuildFlags: buildFlags,
	}
	return packages.Load(cfg, "./...")
}

func FindPackageForFile(fs *token.FileSet, pkgs []*packages.Package, absTargetFile string) (*ast.File, *packages.Package, error) {
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			if fs.Position(file.Pos()).Filename == absTargetFile {
				return file, pkg, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("target file %s not found in packages", absTargetFile)
}

// writeFile はフォーマットしてインポートを整理し、指定ファイルへ出力する
func (t *Transformer) writeFile(node *ast.File, output string) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}
	defer func() {
		err := os.Chdir(originalDir)
		if err != nil {
			fmt.Printf("failed to change directory to %s: %v\n", originalDir, err)
		}
	}()

	dir := filepath.Dir(t.workDir)
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("failed to change directory to %s: %v", dir, err)
	}

	var buf bytes.Buffer
	config := &printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	if err := config.Fprint(&buf, t.fs, node); err != nil {
		return err
	}

	// SHOULD_BE_DELETED. が残っている場合は削除
	tmp := strings.ReplaceAll(buf.String(), SHOULD_BE_DELETED+".", "")
	buf = *bytes.NewBufferString(tmp)

	formatted, err := imports.Process(output, buf.Bytes(), &imports.Options{
		Comments: true, TabWidth: 8, Fragment: true, FormatOnly: false, AllErrors: true,
	})

	if err != nil {
		_ = os.WriteFile(output, buf.Bytes(), 0644)
		return fmt.Errorf("failed to format/imports: %v", err)
	}

	if err := os.WriteFile(output, formatted, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %v", output, err)
	}
	return nil
}

// TransformSymbolsInTargetFile はターゲットファイル用
func (t *Transformer) TransformSymbolsInTargetFile(node *ast.File, typeInfo *types.Info, output string) error {
	modified, err := t.transformFile(node, typeInfo, true)
	if err != nil {
		return err
	}
	if _, err := os.Stat(output); err != nil || modified {
		// import pathを追加
		if t.oldPkgPath != "" && !astutil.UsesImport(node, t.oldPkgPath) {
			astutil.AddImport(t.fs, node, t.oldPkgPath)
		}
		return t.writeFile(node, output)
	}
	return nil
}

// TransformSymbolsInOtherFile は他ファイル用
func (t *Transformer) TransformSymbolsInOtherFile(node *ast.File, typeInfo *types.Info, output string) error {
	modified, err := t.transformFile(node, typeInfo, false)
	if err != nil {
		return err
	}
	if modified {
		// import pathを追加
		if t.newPkgPath != "" && !astutil.UsesImport(node, t.newPkgPath) {
			astutil.AddImport(t.fs, node, t.newPkgPath)
		}

		return t.writeFile(node, output)
	}
	return nil
}

// ------------------------------
// transformFile (DRYにまとめる)
// ------------------------------
func (t *Transformer) transformFile(file *ast.File, typeInfo *types.Info, isTarget bool) (bool, error) {
	if isTarget {
		file.Name.Name = t.newPkg
	}
	modified := false
	filePkg := file.Name.Name

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
				modified = true
			}
		case *ast.ValueSpec:
			if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
				modified = true
			}
			for _, val := range node.Values {
				if t.updateExpr(val, typeInfo, filePkg, isTarget) {
					modified = true
				}
			}
			for _, name := range node.Names {
				if t.updateExpr(name, typeInfo, filePkg, isTarget) {
					modified = true
				}
			}

		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if t.updateExpr(p.Type, typeInfo, filePkg, isTarget) {
						modified = true
					}
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if t.updateExpr(r.Type, typeInfo, filePkg, isTarget) {
						modified = true
					}
				}
			}
			if node.Body != nil {
				ast.Inspect(node.Body, func(bodyNode ast.Node) bool {
					if ex, ok := bodyNode.(ast.Expr); ok {
						if t.updateExpr(ex, typeInfo, filePkg, isTarget) {
							modified = true
						}
					}
					return true
				})
			}

		case *ast.TypeSpec:
			if t.updateExpr(node.Name, typeInfo, filePkg, isTarget) {
				modified = true
			}
			if node.Assign != token.NoPos {
				if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
					modified = true
				}
			} else {
				if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
					modified = true
				}
			}

		case *ast.TypeAssertExpr:
			if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
				modified = true
			}

		case *ast.TypeSwitchStmt:
			for _, stmt := range node.Body.List {
				if cc, ok := stmt.(*ast.CaseClause); ok {
					for i, expr := range cc.List {
						if t.updateExpr(expr, typeInfo, filePkg, isTarget) {
							modified = true
							cc.List[i] = expr
						}
					}
				}
			}

		case *ast.CaseClause:
			for i, expr := range node.List {
				if t.updateExpr(expr, typeInfo, filePkg, isTarget) {
					modified = true
					node.List[i] = expr
				}
			}

		case *ast.CallExpr:
			if t.updateExpr(node.Fun, typeInfo, filePkg, isTarget) {
				modified = true
			}
			for i, arg := range node.Args {
				if t.updateExpr(arg, typeInfo, filePkg, isTarget) {
					modified = true
					node.Args[i] = arg
				}
			}

		case *ast.CompositeLit:
			if t.updateExpr(node.Type, typeInfo, filePkg, isTarget) {
				modified = true
			}
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if t.updateExpr(kv.Value, typeInfo, filePkg, isTarget) {
						modified = true
					}
				} else {
					if t.updateExpr(elt, typeInfo, filePkg, isTarget) {
						modified = true
					}
				}
			}
		case *ast.IndexExpr: // 単一のジェネリック型
			mod := t.updateExpr(node.X, typeInfo, filePkg, isTarget)
			if t.updateExpr(node.Index, typeInfo, filePkg, isTarget) {
				mod = true
			}
			return mod

		case *ast.IndexListExpr: // 複数のジェネリック型
			mod := t.updateExpr(node.X, typeInfo, filePkg, isTarget)
			for _, idx := range node.Indices {
				if t.updateExpr(idx, typeInfo, filePkg, isTarget) {
					mod = true
				}
			}
			return mod
		}

		return true
	})

	return modified, nil
}

// -----------------------------------------
// updateExpr: isTarget に応じて関数を切替
// -----------------------------------------
func (t *Transformer) updateExpr(n ast.Node, typeInfo *types.Info, filePkg string, isTarget bool) bool {
	if isTarget {
		return t.updateExprInTargetFile(n, typeInfo)
	}
	return t.updateExprInOtherFile(n, typeInfo, filePkg)
}

func (t *Transformer) updateExprInTargetFile(node ast.Node, typeInfo *types.Info) bool {
	switch n := node.(type) {
	case *ast.Field:
		return false
	case *ast.Ident:
		return t.updateIdentInTargetFile(n, typeInfo)
	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			pkgName, pos := getPkgNameAndPositionForIdent(n.Sel, t.fs, typeInfo)
			if pkgName == "" || pos.Filename == "" {
				slog.Debug(fmt.Sprintf("Pos NotFound SelectorExpr %s.%s for Target", ident.Name, n.Sel.Name), slog.String("oldPkg", t.oldPkg), slog.String("oldFile", t.oldFile), slog.String("newPkg", t.newPkg))
				return false
			}

			slog.Debug(fmt.Sprintf("SelectorExpr %s.%s for Target", ident.Name, n.Sel.Name), slog.String("oldPkg", t.oldPkg), slog.String("oldFile", t.oldFile), slog.String("newPkg", t.newPkg))
			if ident.Name == t.newPkg && pos.Filename == t.oldFile {
				// 探索したファイル内のパッケージが既に新しいパッケージで、
				// 今回変更する対象のファイルのAPIをコールしている場合、
				// パッケージ名を削除する必要がある
				ident.Name = SHOULD_BE_DELETED
				return true
			}
		}

	case *ast.StarExpr:
		return t.updateExprInTargetFile(n.X, typeInfo)
	case *ast.ArrayType:
		mod := t.updateExprInTargetFile(n.Elt, typeInfo)
		return mod
	case *ast.MapType:
		mod := t.updateExprInTargetFile(n.Key, typeInfo)
		if t.updateExprInTargetFile(n.Value, typeInfo) {
			mod = true
		}
		return mod
	case *ast.ChanType:
		return t.updateExprInTargetFile(n.Value, typeInfo)
	case *ast.CallExpr:
		mod := t.updateExprInTargetFile(n.Fun, typeInfo)
		for _, arg := range n.Args {
			if t.updateExprInTargetFile(arg, typeInfo) {
				mod = true
			}
		}
		return mod
	case *ast.TypeSpec:
		return t.updateExprInTargetFile(n.Type, typeInfo)
	}
	return false
}

func (t *Transformer) updateExprInOtherFile(node ast.Node, typeInfo *types.Info, filePkg string) bool {
	switch n := node.(type) {
	case *ast.Ident:
		if n.Name == t.newPkg {
			return false
		}

		if t.doneIdent[n] {
			return false
		}
		return t.updateIdentInOtherFile(n, typeInfo, filePkg)
	case *ast.GenDecl:
		for _, spec := range n.Specs {
			if typeSpec, ok := spec.(*ast.TypeSpec); ok {
				if t.updateExprInOtherFile(typeSpec.Type, typeInfo, filePkg) {
					return true
				}
			}
		}
	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			pkgName, pos := getPkgNameAndPositionForIdent(n.Sel, t.fs, typeInfo)
			if pkgName == "" || pos.Filename == "" {
				slog.Debug(fmt.Sprintf("Pos Notfound SelectorExpr %s.%s for Other", ident.Name, n.Sel.Name), slog.String("oldPkg", t.oldPkg), slog.String("oldFile", t.oldFile), slog.String("newPkg", t.newPkg), slog.String("filePkg", filePkg))
				return false
			}

			slog.Debug(fmt.Sprintf("SelectorExpr %s.%s for Other", ident.Name, n.Sel.Name), slog.String("oldPkg", t.oldPkg), slog.String("oldFile", t.oldFile), slog.String("newPkg", t.newPkg), slog.String("filePkg", filePkg))
			if ident.Name == t.oldPkg && pos.Filename == t.oldFile {
				// 探索したファイル内のパッケージが既に新しいパッケージで、
				// 今回変更する対象のファイルのAPIをコールしている場合、
				// パッケージ名を削除する必要がある
				if t.newPkg == filePkg {
					ident.Name = SHOULD_BE_DELETED
					return true
				}
				ident.Name = t.newPkg
				t.doneIdent[n.Sel] = true
				return true
			}
		}
	case *ast.StarExpr:
		return t.updateExprInOtherFile(n.X, typeInfo, filePkg)
	case *ast.ArrayType:
		mod := t.updateExprInOtherFile(n.Elt, typeInfo, filePkg)
		return mod
	case *ast.MapType:
		mod := t.updateExprInOtherFile(n.Key, typeInfo, filePkg)
		if t.updateExprInOtherFile(n.Value, typeInfo, filePkg) {
			mod = true
		}
		return mod
	case *ast.ChanType:
		return t.updateExprInOtherFile(n.Value, typeInfo, filePkg)
	case *ast.CallExpr:
		mod := t.updateExprInOtherFile(n.Fun, typeInfo, filePkg)
		for _, arg := range n.Args {
			if t.updateExprInOtherFile(arg, typeInfo, filePkg) {
				mod = true
			}
		}
		return mod
	}
	return false
}

// ----------------------------------------
// Ident の置き換えロジック
// ----------------------------------------
func (t *Transformer) updateIdentInTargetFile(e *ast.Ident, typeInfo *types.Info) bool {
	pkgName, pos := getPkgNameAndPositionForIdent(e, t.fs, typeInfo)
	// ここで "pkgName == t.oldPkg" のみで判定し、
	// pos.Filename == t.oldFile を削除すれば
	// 同一パッケージ全体を変換できる。
	if pkgName == t.oldPkg && pos.Filename != t.oldFile {
		e.Name = fmt.Sprintf("%s.%s%s", t.oldPkg, t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(e *ast.Ident, typeInfo *types.Info, filePkg string) bool {
	pkgName, pos := getPkgNameAndPositionForIdent(e, t.fs, typeInfo)

	if pkgName == "" || pos.Filename == "" {
		return false
	}

	if pkgName == t.oldPkg && pos.Filename == t.oldFile {
		if filePkg != t.newPkg {
			e.Name = fmt.Sprintf("%s.%s%s", t.newPkg, t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		} else {
			e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		}
		return true
	}
	return false
}

func getPkgNameAndPositionForIdent(e *ast.Ident, fs *token.FileSet, typeInfo *types.Info) (string, token.Position) {
	if !isExported(e.Name) {
		return "", token.Position{}
	}

	var obj types.Object
	if o, ok := typeInfo.Defs[e]; ok && o != nil && o.Pkg() != nil && o.Pkg().Name() != "" && fs.Position(o.Pos()).Filename != "" {
		obj = o
	} else if o, ok := typeInfo.Uses[e]; ok && o != nil && o.Pkg() != nil && o.Pkg().Name() != "" && fs.Position(o.Pos()).Filename != "" {
		obj = o
	}

	if obj == nil {
		return "", token.Position{}
	}

	// **埋め込みフィールドの処理**
	if v, ok := obj.(*types.Var); ok && v.Embedded() {
		// `Var.Type()` から `Named` 型を取得（埋め込みフィールドの型情報）
		if named, ok := v.Type().(*types.Named); ok {
			obj = named.Obj() // 埋め込み構造体の定義オブジェクトに置き換え
		}
	}

	// 「obj.Parent() がパッケージスコープ (pkg.Scope()) と同じかどうか」で判定
	if obj.Parent() != obj.Pkg().Scope() {
		return "", token.Position{}
	}

	return obj.Pkg().Name(), fs.Position(obj.Pos())
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := []rune(name)
	return unicode.IsUpper(r[0])
}
