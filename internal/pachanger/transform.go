package pachanger

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

type Transformer struct {
	fs           *token.FileSet
	oldFile      string
	oldPkg       string
	newPkg       string
	deletePrefix string
	workDir      string
}

// NewTransformer は Transformer を生成
func NewTransformer(fs *token.FileSet, workDir, oldFile, oldPkg, newPkg, deletePrefix string) *Transformer {
	return &Transformer{
		fs:           fs,
		oldFile:      oldFile,
		oldPkg:       oldPkg,
		newPkg:       newPkg,
		deletePrefix: deletePrefix,
		workDir:      workDir,
	}
}

func LoadPackages(fs *token.FileSet, absWorkDir string, buildFlags []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode:       packages.LoadAllSyntax,
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

	formatted, err := imports.Process(output, buf.Bytes(), &imports.Options{
		Comments: true, TabWidth: 4, Fragment: false, FormatOnly: false,
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
	history := &([]ast.Node{})

	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(*history) > 0 {
				*history = (*history)[:len(*history)-1]
			}
			return true
		}

		*history = append(*history, n)

		switch node := n.(type) {
		case *ast.Field:
			if t.updateExpr(node.Type, typeInfo, history, isTarget) {
				modified = true
			}
		case *ast.ValueSpec:
			if t.updateExpr(node.Type, typeInfo, history, isTarget) {
				modified = true
			}
			for _, val := range node.Values {
				if t.updateExpr(val, typeInfo, history, isTarget) {
					modified = true
				}
			}
			for _, name := range node.Names {
				if t.updateExpr(name, typeInfo, history, isTarget) {
					modified = true
				}
			}

		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if t.updateExpr(p.Type, typeInfo, history, isTarget) {
						modified = true
					}
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if t.updateExpr(r.Type, typeInfo, history, isTarget) {
						modified = true
					}
				}
			}
			if node.Body != nil {
				ast.Inspect(node.Body, func(bodyNode ast.Node) bool {
					if ex, ok := bodyNode.(ast.Expr); ok {
						if t.updateExpr(ex, typeInfo, history, isTarget) {
							modified = true
						}
					}
					return true
				})
			}

		case *ast.TypeSpec:
			if t.updateExpr(node.Name, typeInfo, history, isTarget) {
				modified = true
			}
			if node.Assign != token.NoPos {
				if t.updateExpr(node.Type, typeInfo, history, isTarget) {
					modified = true
				}
			} else {
				if t.updateExpr(node.Type, typeInfo, history, isTarget) {
					modified = true
				}
			}

		case *ast.TypeAssertExpr:
			if t.updateExpr(node.Type, typeInfo, history, isTarget) {
				modified = true
			}

		case *ast.TypeSwitchStmt:
			for _, stmt := range node.Body.List {
				if cc, ok := stmt.(*ast.CaseClause); ok {
					for i, expr := range cc.List {
						if t.updateExpr(expr, typeInfo, history, isTarget) {
							modified = true
							cc.List[i] = expr
						}
					}
				}
			}

		case *ast.CaseClause:
			for i, expr := range node.List {
				if t.updateExpr(expr, typeInfo, history, isTarget) {
					modified = true
					node.List[i] = expr
				}
			}

		case *ast.CallExpr:
			if t.updateExpr(node.Fun, typeInfo, history, isTarget) {
				modified = true
			}
			for i, arg := range node.Args {
				if t.updateExpr(arg, typeInfo, history, isTarget) {
					modified = true
					node.Args[i] = arg
				}
			}

		case *ast.CompositeLit:
			if t.updateExpr(node.Type, typeInfo, history, isTarget) {
				modified = true
			}
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if t.updateExpr(kv.Value, typeInfo, history, isTarget) {
						modified = true
					}
				} else {
					if t.updateExpr(elt, typeInfo, history, isTarget) {
						modified = true
					}
				}
			}
		case *ast.IndexExpr: // 単一のジェネリック型
			mod := t.updateExpr(node.X, typeInfo, history, isTarget)
			if t.updateExpr(node.Index, typeInfo, history, isTarget) {
				mod = true
			}
			return mod

		case *ast.IndexListExpr: // 複数のジェネリック型
			mod := t.updateExpr(node.X, typeInfo, history, isTarget)
			for _, idx := range node.Indices {
				if t.updateExpr(idx, typeInfo, history, isTarget) {
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
func (t *Transformer) updateExpr(n ast.Node, typeInfo *types.Info, history *[]ast.Node, isTarget bool) bool {

	if isTarget {
		return t.updateExprInTargetFile(n, typeInfo, history)
	}
	return t.updateExprInOtherFile(n, typeInfo, history)
}

func (t *Transformer) updateExprInTargetFile(node ast.Node, typeInfo *types.Info, history *[]ast.Node) bool {
	switch n := node.(type) {
	case *ast.Field:
		return false
	case *ast.Ident:
		return t.updateIdentInTargetFile(n, typeInfo)
	case *ast.StarExpr:
		return t.updateExprInTargetFile(n.X, typeInfo, history)
	case *ast.ArrayType:
		mod := t.updateExprInTargetFile(n.Elt, typeInfo, history)
		return mod
	case *ast.MapType:
		mod := t.updateExprInTargetFile(n.Key, typeInfo, history)
		if t.updateExprInTargetFile(n.Value, typeInfo, history) {
			mod = true
		}
		return mod
	case *ast.ChanType:
		return t.updateExprInTargetFile(n.Value, typeInfo, history)
	case *ast.CallExpr:
		mod := t.updateExprInTargetFile(n.Fun, typeInfo, history)
		for _, arg := range n.Args {
			if t.updateExprInTargetFile(arg, typeInfo, history) {
				mod = true
			}
		}
		return mod
	case *ast.TypeSpec:
		return t.updateExprInTargetFile(n.Type, typeInfo, history)
	}
	return false
}

func (t *Transformer) updateExprInOtherFile(node ast.Node, typeInfo *types.Info, history *[]ast.Node) bool {
	switch n := node.(type) {
	case *ast.Ident:
		if len(*history) < 2 || !t.isSelectorExpr((*history)[len(*history)-2]) {
			if n.Name == t.newPkg {
				return false
			}
			return t.updateIdentInOtherFile(n, typeInfo)
		}
	case *ast.GenDecl:
		for _, spec := range n.Specs {
			if typeSpec, ok := spec.(*ast.TypeSpec); ok {
				if t.updateExprInOtherFile(typeSpec.Type, typeInfo, history) {
					return true
				}
			}
		}
	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			pkgName, pos := getPkgNameAndPositionForIdent(n.Sel, t.fs, typeInfo)
			if pkgName == "" || pos.Filename == "" {
				return false
			}
			if ident.Name == t.oldPkg && pos.Filename == t.oldFile {
				ident.Name = t.newPkg
				return true
			}
		}
	case *ast.StarExpr:
		return t.updateExprInOtherFile(n.X, typeInfo, history)
	case *ast.ArrayType:
		mod := t.updateExprInOtherFile(n.Elt, typeInfo, history)
		return mod
	case *ast.MapType:
		mod := t.updateExprInOtherFile(n.Key, typeInfo, history)
		if t.updateExprInOtherFile(n.Value, typeInfo, history) {
			mod = true
		}
		return mod
	case *ast.ChanType:
		return t.updateExprInOtherFile(n.Value, typeInfo, history)
	case *ast.CallExpr:
		mod := t.updateExprInOtherFile(n.Fun, typeInfo, history)
		for _, arg := range n.Args {
			if t.updateExprInOtherFile(arg, typeInfo, history) {
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
		e.Name = fmt.Sprintf("%s.%s", t.oldPkg, strings.TrimPrefix(e.Name, t.deletePrefix))
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(e *ast.Ident, typeInfo *types.Info) bool {
	pkgName, pos := getPkgNameAndPositionForIdent(e, t.fs, typeInfo)

	if pkgName == "" || pos.Filename == "" {
		return false
	}

	if pkgName == t.oldPkg && pos.Filename == t.oldFile {
		e.Name = strings.TrimPrefix(e.Name, t.deletePrefix)
		return true
	}
	return false
}
func (t *Transformer) isSelectorExpr(n ast.Node) bool {
	_, ok := n.(*ast.SelectorExpr)
	return ok
}

func getPkgNameAndPositionForIdent(e *ast.Ident, fs *token.FileSet, typeInfo *types.Info) (string, token.Position) {
	if !isExported(e.Name) {
		return "", token.Position{}
	}

	var obj types.Object
	if o, ok := typeInfo.Uses[e]; ok {
		obj = o
	} else if o, ok := typeInfo.Defs[e]; ok {
		obj = o
	}
	if obj == nil {
		return "", token.Position{}
	}

	pkg := obj.Pkg()
	if pkg == nil {
		return "", token.Position{}
	}

	// 「トップレベルに定義されている」かどうかを
	// 「obj.Parent() がパッケージスコープ (pkg.Scope()) と同じかどうか」で判定
	if obj.Parent() != pkg.Scope() {
		// トップレベルのオブジェクトではない (例: struct フィールドやメソッドなど)
		return "", token.Position{}
	}

	return pkg.Name(), fs.Position(obj.Pos())
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := []rune(name)
	return unicode.IsUpper(r[0])
}
