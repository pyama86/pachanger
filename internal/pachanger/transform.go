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
	"sync"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const SHOULD_BE_DELETED = "SHOULD_BE_DELETED"

type Transformer struct {
	fs            *token.FileSet
	oldFile       string
	oldPkg        string
	oldPkgPath    string
	newPkgPath    string
	newPkg        string
	addPrefix     string
	deletePrefix  string
	workDir       string
	doneIdent     map[*ast.Ident]bool
	targetSymbols map[string]bool
	otherSymbols  map[string]bool
	mu            sync.Mutex
}

// NewTransformer は Transformer を生成
func NewTransformer(fs *token.FileSet, workDir, oldFile, oldPkg, oldPkgPath, newPkg, addPrefix, deletePrefix string, targetSymbols, otherSymbols map[string]bool) *Transformer {
	newPkgPath := ""
	pos := strings.LastIndex(oldPkgPath, oldPkg)
	if pos > 0 {
		newPkgPath = oldPkgPath[:pos] + newPkg
	}

	return &Transformer{
		fs:            fs,
		oldFile:       oldFile,
		oldPkg:        oldPkg,
		oldPkgPath:    oldPkgPath,
		newPkgPath:    newPkgPath,
		newPkg:        newPkg,
		addPrefix:     addPrefix,
		deletePrefix:  deletePrefix,
		workDir:       workDir,
		doneIdent:     map[*ast.Ident]bool{},
		targetSymbols: targetSymbols,
		otherSymbols:  otherSymbols,
	}
}

func FilterDefSymbols(fs *token.FileSet, pkg *packages.Package, absTargetFile string) (map[string]bool, map[string]bool) {
	targetSymbols := map[string]bool{}
	otherSymbols := map[string]bool{}
	// pkgの中からエクスポートされているシンボルを抽出
	for _, d := range pkg.TypesInfo.Defs {
		if d != nil && d.Exported() && d.Parent() == d.Pkg().Scope() {
			if fs.Position(d.Pos()).Filename == absTargetFile {
				targetSymbols[d.Name()] = true
			} else {
				otherSymbols[d.Name()] = true
			}
		}
	}
	return targetSymbols, otherSymbols
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
func (t *Transformer) TransformSymbolsInTargetFile(node *ast.File, output string, typesInfo *types.Info) error {
	modified, err := t.transformFile(node, typesInfo, true)
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
func (t *Transformer) TransformSymbolsInOtherFile(node *ast.File, output string, typesInfo *types.Info) error {
	modified, err := t.transformFile(node, typesInfo, false)
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

func (t *Transformer) transformFile(file *ast.File, typesInfo *types.Info, isTarget bool) (bool, error) {
	modified := false
	filePkg := file.Name.Name
	if isTarget {
		if file.Name.Name == t.oldPkg {
			modified = true
		}
		file.Name.Name = t.newPkg
		filePkg = t.oldPkg
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if t.updateExpr(n, filePkg, typesInfo, isTarget) {
			modified = true
		}

		return true
	})

	return modified, nil
}
func (t *Transformer) addDoneList(e *ast.Ident) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.doneIdent[e] = true
}

// -----------------------------------------
// updateExpr: isTarget に応じて関数を切替
// -----------------------------------------
func (t *Transformer) updateExpr(node ast.Node, filePkg string, typesInfo *types.Info, isTarget bool) bool {
	mod := false
	switch n := node.(type) {
	case *ast.Field:
		if n.Names != nil {
			for _, name := range n.Names {
				// 構造体のフィールド名は変更しない
				t.addDoneList(name)
			}
		}
		if t.updateExpr(n.Type, filePkg, typesInfo, isTarget) {
			return true
		}
	case *ast.Ident:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.doneIdent[n] {
			return false
		}
		t.doneIdent[n] = true

		if isTarget {
			return t.updateIdentInTargetFile(n, filePkg, typesInfo)
		} else {
			return t.updateIdentInOtherFile(n, filePkg, typesInfo)
		}

	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			// 無関係なパッケージは置き換えない
			if ident.Name != t.oldPkg && ident.Name != t.newPkg {
				t.addDoneList(n.Sel)
				return false
			}

			if isTarget {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Target", ident.Name, n.Sel.Name))
				// 対象のファイルで、新しいパッケージを参照している場合
				// パッケージ名を削除する必要がある
				if ident.Name == t.newPkg {
					ident.Name = SHOULD_BE_DELETED
					t.addDoneList(n.Sel)
					return true
				}
			} else {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Other", ident.Name, n.Sel.Name))
				if t.targetSymbols[n.Sel.Name] {
					// 新しいパッケージのファイルが
					// 変更前か変更後のパッケージ名でアクセスしている
					if t.newPkg == filePkg && (ident.Name == t.oldPkg || ident.Name == t.newPkg) {
						slog.Debug(fmt.Sprintf("Delete %s in %s.%s in Other", ident.Name, ident.Name, n.Sel.Name))
						ident.Name = SHOULD_BE_DELETED
						return true
						// もとのパッケージのファイルが
						// 変更前のパッケージ名でアクセスしている

						// もしくは新旧関係ないパッケージのファイルが
						// 変更前か変更後のパッケージ名でアクセスしている
					} else if (t.oldPkg == filePkg || t.oldPkg != filePkg) && ident.Name == t.oldPkg {
						ident.Name = t.newPkg
						slog.Debug(fmt.Sprintf("Add DoneIdent %s in Other", n.Sel.Name))
						t.addDoneList(n.Sel)
						return true
					}
				} else {
					slog.Debug(fmt.Sprintf("Skip %s in %s.%s in synbol %v in Other", ident.Name, ident.Name, n.Sel.Name, t.targetSymbols[n.Sel.Name]))
				}
			}
		}
	case *ast.ValueSpec:
		if t.updateExpr(n.Type, filePkg, typesInfo, isTarget) {
			mod = true
		}
		for _, val := range n.Values {
			if t.updateExpr(val, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
		for _, name := range n.Names {
			if t.updateExpr(name, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.StarExpr:
		return t.updateExpr(n.X, filePkg, typesInfo, isTarget)
	case *ast.ArrayType:
		return t.updateExpr(n.Elt, filePkg, typesInfo, isTarget)
	case *ast.MapType:
		mod = t.updateExpr(n.Key, filePkg, typesInfo, isTarget)
		if t.updateExpr(n.Value, filePkg, typesInfo, isTarget) {
			mod = true
		}
	case *ast.KeyValueExpr:
		// 構造体のフィールド名は変更しない
		if ident, ok := n.Key.(*ast.Ident); ok {
			t.addDoneList(ident)
		}
	case *ast.ChanType:
		return t.updateExpr(n.Value, filePkg, typesInfo, isTarget)
	case *ast.CallExpr:
		mod = t.updateExpr(n.Fun, filePkg, typesInfo, isTarget)
		for _, arg := range n.Args {
			if t.updateExpr(arg, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.TypeAssertExpr:
		return t.updateExpr(n.Type, filePkg, typesInfo, isTarget)

	case *ast.TypeSwitchStmt:
		for _, stmt := range n.Body.List {
			if cc, ok := stmt.(*ast.CaseClause); ok {
				for i, expr := range cc.List {
					if t.updateExpr(expr, filePkg, typesInfo, isTarget) {
						mod = true
						cc.List[i] = expr
					}
				}
			}
		}

	case *ast.CaseClause:
		for i, expr := range n.List {
			if t.updateExpr(expr, filePkg, typesInfo, isTarget) {
				mod = true
				n.List[i] = expr
			}
		}

	case *ast.TypeSpec:
		if isTarget {
			mod = t.updateExpr(n.Name, filePkg, typesInfo, isTarget)
		} else {
			// 他ファイルの定義の場合は変更しない
			t.addDoneList(n.Name)
		}
		if n.Assign != token.NoPos {
			if t.updateExpr(n.Type, filePkg, typesInfo, isTarget) {
				mod = true
			}
		} else {
			if t.updateExpr(n.Type, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.CompositeLit:
		mod = t.updateExpr(n.Type, filePkg, typesInfo, isTarget)
		for _, elt := range n.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if t.updateExpr(kv.Value, filePkg, typesInfo, isTarget) {
					mod = true
				}
			} else {
				if t.updateExpr(elt, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
	case *ast.FuncDecl:
		if n.Type.Params != nil {
			for _, p := range n.Type.Params.List {
				if t.updateExpr(p.Type, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
		if n.Type.Results != nil {
			for _, r := range n.Type.Results.List {
				if t.updateExpr(r.Type, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
		if n.Body != nil {
			ast.Inspect(n.Body, func(bodyNode ast.Node) bool {
				if t.updateExpr(bodyNode, filePkg, typesInfo, isTarget) {
					mod = true
				}
				return true
			})
		}
	case *ast.IndexExpr: // 単一のジェネリック型
		if t.updateExpr(n.X, filePkg, typesInfo, isTarget) {
			mod = true
		}
		if t.updateExpr(n.Index, filePkg, typesInfo, isTarget) {
			mod = true
		}
	case *ast.IndexListExpr: // 複数のジェネリック型
		if t.updateExpr(n.X, filePkg, typesInfo, isTarget) {
			mod = true
		}
		for _, idx := range n.Indices {
			if t.updateExpr(idx, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	}

	return mod
}

func usesPackageName(e *ast.Ident, typesInfo *types.Info) string {
	if o, ok := typesInfo.Uses[e]; ok && o != nil && o.Pkg() != nil {
		return o.Pkg().Name()
	}
	return ""
}

func (t *Transformer) updateIdentInTargetFile(e *ast.Ident, filePkg string, typesInfo *types.Info) bool {
	// 変更前と同じパッケージのファイルで対象のファイルのシンボルではない場合
	if t.otherSymbols[e.Name] && usesPackageName(e, typesInfo) == t.oldPkg {
		e.Name = fmt.Sprintf("%s.%s", t.oldPkg, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	} else if filePkg == t.oldPkg && t.targetSymbols[e.Name] {
		e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(e *ast.Ident, filePkg string, typesInfo *types.Info) bool {
	// 変更前のパッケージ
	if filePkg == t.oldPkg && t.targetSymbols[e.Name] {
		// 変更前のファイルのシンボルを利用している
		if filePkg != t.newPkg && usesPackageName(e, typesInfo) == t.oldPkg {
			e.Name = fmt.Sprintf("%s.%s%s", t.newPkg, t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
			return true
		} else {
			before := e.Name
			e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
			if before != e.Name {
				return true
			}
		}
	}
	return false
}
