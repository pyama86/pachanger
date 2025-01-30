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
	"unicode"

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
	mu            sync.Mutex
}

// NewTransformer は Transformer を生成
func NewTransformer(fs *token.FileSet, workDir, oldFile, oldPkg, oldPkgPath, newPkg, addPrefix, deletePrefix string, targetSymbols map[string]bool) *Transformer {
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

func (t *Transformer) transformFile(file *ast.File, typeInfo *types.Info, isTarget bool) (bool, error) {
	modified := false
	if isTarget {
		if file.Name.Name == t.oldPkg {
			modified = true
		}
		file.Name.Name = t.newPkg
	}
	filePkg := file.Name.Name

	ast.Inspect(file, func(n ast.Node) bool {
		if t.updateExpr(n, typeInfo, filePkg, isTarget) {
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
func (t *Transformer) updateExpr(node ast.Node, typeInfo *types.Info, filePkg string, isTarget bool) bool {
	mod := false
	switch n := node.(type) {
	case *ast.Field:
		if n.Names != nil {
			for _, name := range n.Names {
				if t.updateExpr(name, typeInfo, filePkg, isTarget) {
					mod = true
				}
			}
		}
		if t.updateExpr(n.Type, typeInfo, filePkg, isTarget) {
			return true
		}
	case *ast.Ident:
		t.mu.Lock()
		if t.doneIdent[n] {
			t.mu.Unlock()
			return false
		}
		t.doneIdent[n] = true
		t.mu.Unlock()

		if isTarget {
			return t.updateIdentInTargetFile(n, typeInfo)
		} else {
			return t.updateIdentInOtherFile(n, typeInfo, filePkg)
		}

	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			if isTarget {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Target", ident.Name, n.Sel.Name))
				// 対象のファイルで、新しいパッケージを参照している場合
				// パッケージ名を削除する必要がある
				if ident.Name == t.newPkg {
					slog.Debug(fmt.Sprintf("Delete %s in %s.%s in %v", ident.Name, ident.Name, n.Sel.Name, isTarget))
					ident.Name = SHOULD_BE_DELETED
					slog.Debug(fmt.Sprintf("Add DoneIdent %s in %v", n.Sel.Name, isTarget))
					t.addDoneList(n.Sel)
					return true
				}
			} else {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Other", ident.Name, n.Sel.Name))
				// 変更前のパッケージ名でアクセスしている
				if t.targetSymbols[n.Sel.Name] && ident.Name == t.oldPkg {
					if t.newPkg == filePkg {
						slog.Debug(fmt.Sprintf("Delete %s in %s.%s in Other", ident.Name, ident.Name, n.Sel.Name))
						ident.Name = SHOULD_BE_DELETED
						return true
					}
					ident.Name = t.newPkg
					slog.Debug(fmt.Sprintf("Add DoneIdent %s in Other", n.Sel.Name))
					t.addDoneList(n.Sel)
					return true
				} else {
					slog.Debug(fmt.Sprintf("Skip %s in %s.%s in synbol %v in Other", ident.Name, ident.Name, n.Sel.Name, t.targetSymbols[n.Sel.Name]))
				}
			}
			// 無関係なパッケージは置き換えない
			if ident.Name != t.oldPkg && ident.Name != t.newPkg {
				slog.Debug(fmt.Sprintf("Add DoneIdent %s in %v", n.Sel.Name, isTarget))
				t.addDoneList(n.Sel)
			}

		}
	case *ast.ValueSpec:
		if t.updateExpr(n.Type, typeInfo, filePkg, isTarget) {
			mod = true
		}
		for _, val := range n.Values {
			if t.updateExpr(val, typeInfo, filePkg, isTarget) {
				mod = true
			}
		}
		for _, name := range n.Names {
			if t.updateExpr(name, typeInfo, filePkg, isTarget) {
				mod = true
			}
		}
	case *ast.StarExpr:
		return t.updateExpr(n.X, typeInfo, filePkg, isTarget)
	case *ast.ArrayType:
		return t.updateExpr(n.Elt, typeInfo, filePkg, isTarget)
	case *ast.MapType:
		mod = t.updateExpr(n.Key, typeInfo, filePkg, isTarget)
		if t.updateExpr(n.Value, typeInfo, filePkg, isTarget) {
			mod = true
		}
	case *ast.ChanType:
		return t.updateExpr(n.Value, typeInfo, filePkg, isTarget)
	case *ast.CallExpr:
		mod = t.updateExpr(n.Fun, typeInfo, filePkg, isTarget)
		for _, arg := range n.Args {
			if t.updateExpr(arg, typeInfo, filePkg, isTarget) {
				mod = true
			}
		}

	case *ast.TypeAssertExpr:
		return t.updateExpr(n.Type, typeInfo, filePkg, isTarget)

	case *ast.TypeSwitchStmt:
		for _, stmt := range n.Body.List {
			if cc, ok := stmt.(*ast.CaseClause); ok {
				for i, expr := range cc.List {
					if t.updateExpr(expr, typeInfo, filePkg, isTarget) {
						mod = true
						cc.List[i] = expr
					}
				}
			}
		}

	case *ast.CaseClause:
		for i, expr := range n.List {
			if t.updateExpr(expr, typeInfo, filePkg, isTarget) {
				mod = true
				n.List[i] = expr
			}
		}

	case *ast.TypeSpec:
		mod = t.updateExpr(n.Name, typeInfo, filePkg, isTarget)
		if n.Assign != token.NoPos {
			if t.updateExpr(n.Type, typeInfo, filePkg, isTarget) {
				mod = true
			}
		} else {
			if t.updateExpr(n.Type, typeInfo, filePkg, isTarget) {
				mod = true
			}
		}
	case *ast.CompositeLit:
		mod = t.updateExpr(n.Type, typeInfo, filePkg, isTarget)
		for _, elt := range n.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if t.updateExpr(kv.Value, typeInfo, filePkg, isTarget) {
					mod = true
				}
			} else {
				if t.updateExpr(elt, typeInfo, filePkg, isTarget) {
					mod = true
				}
			}
		}
	case *ast.FuncDecl:
		if n.Type.Params != nil {
			for _, p := range n.Type.Params.List {
				if t.updateExpr(p.Type, typeInfo, filePkg, isTarget) {
					mod = true
				}
			}
		}
		if n.Type.Results != nil {
			for _, r := range n.Type.Results.List {
				if t.updateExpr(r.Type, typeInfo, filePkg, isTarget) {
					mod = true
				}
			}
		}
		if n.Body != nil {
			ast.Inspect(n.Body, func(bodyNode ast.Node) bool {
				if ex, ok := bodyNode.(ast.Expr); ok {
					if t.updateExpr(ex, typeInfo, filePkg, isTarget) {
						mod = true
					}
				}
				return true
			})
		}
	case *ast.IndexExpr: // 単一のジェネリック型
		if t.updateExpr(n.X, typeInfo, filePkg, isTarget) {
			mod = true
		}
		if t.updateExpr(n.Index, typeInfo, filePkg, isTarget) {
			mod = true
		}
	case *ast.IndexListExpr: // 複数のジェネリック型
		if t.updateExpr(n.X, typeInfo, filePkg, isTarget) {
			mod = true
		}
		for _, idx := range n.Indices {
			if t.updateExpr(idx, typeInfo, filePkg, isTarget) {
				mod = true
			}
		}

	}

	return mod
}

// ----------------------------------------
// Ident の置き換えロジック
// ----------------------------------------
func (t *Transformer) updateIdentInTargetFile(e *ast.Ident, typeInfo *types.Info) bool {
	pkgName := getPkgNameForIdent(e, t.fs, typeInfo)
	if pkgName == "" {
		return false
	}
	// 同じパッケージで対象のファイルのシンボルではない場合
	if pkgName == t.oldPkg && !t.targetSymbols[e.Name] {
		e.Name = fmt.Sprintf("%s.%s", t.oldPkg, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	} else if pkgName == t.oldPkg && t.targetSymbols[e.Name] {
		e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(e *ast.Ident, typeInfo *types.Info, filePkg string) bool {
	pkgName := getPkgNameForIdent(e, t.fs, typeInfo)
	if pkgName == "" {
		return false
	}

	if pkgName == t.oldPkg && t.targetSymbols[e.Name] {
		if filePkg != t.newPkg {
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

func getPkgNameForIdent(e *ast.Ident, fs *token.FileSet, typeInfo *types.Info) string {
	if !isExported(e.Name) {
		return ""
	}

	var obj types.Object
	if o, ok := typeInfo.Defs[e]; ok && o != nil && o.Pkg() != nil && o.Pkg().Name() != "" && fs.Position(o.Pos()).Filename != "" {
		obj = o
	} else if o, ok := typeInfo.Uses[e]; ok && o != nil && o.Pkg() != nil && o.Pkg().Name() != "" && fs.Position(o.Pos()).Filename != "" {
		obj = o
	}

	if obj == nil {
		return ""
	}

	if v, ok := obj.(*types.Var); ok && v.Embedded() {
		if named, ok := v.Type().(*types.Named); ok {
			obj = named.Obj()
		}
	}

	if obj.Parent() != obj.Pkg().Scope() {
		return ""
	}

	return obj.Pkg().Name()
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := []rune(name)
	return unicode.IsUpper(r[0])
}
