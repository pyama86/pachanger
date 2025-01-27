package pachanger

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// Transformer はシンボル変換に必要な情報を保持する構造体です。
type Transformer struct {
	fs           *token.FileSet
	oldFile      string
	absOutput    string
	oldPkg       string
	newPkg       string
	deletePrefix string
}

// NewTransformer は Transformer 構造体を生成します。
func NewTransformer(
	fs *token.FileSet,
	oldFile, absOutput, oldPkg, newPkg, deletePrefix string,
) *Transformer {
	return &Transformer{
		fs:           fs,
		oldFile:      oldFile,
		absOutput:    absOutput,
		oldPkg:       oldPkg,
		newPkg:       newPkg,
		deletePrefix: deletePrefix,
	}
}

// LoadPackages は指定ディレクトリ以下の Go パッケージをすべて読み込みます。
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

// FindPackageForFile は、読み込んだパッケージリストから特定ファイルを探し返します。
// （旧FilterPackage）
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

// TransformSymbolsInTargetFile は、ターゲットファイル内のパッケージ名・シンボル名を変換します。
// 変換があった場合、あるいは出力先ファイルが存在しない場合は新たにファイルを書き込みます。
func (t *Transformer) TransformSymbolsInTargetFile(node *ast.File, typeInfo *types.Info) error {

	modified, err := t.transformInTargetFile(node, typeInfo)
	if err != nil {
		return err
	}

	// ファイルが存在しないか、modified == true の場合に書き出す
	if _, err := os.Stat(t.absOutput); err != nil || modified {
		if werr := WriteFile(t.absOutput, t.fs, node); werr != nil {
			return werr
		}
	}
	return nil
}

// TransformSymbolsInOtherFile はターゲットファイルのシンボルを参照している可能性のある他ファイルに対して、シンボルを変換します。
// 変換があった場合のみ上書き保存します。
func (t *Transformer) TransformSymbolsInOtherFile(node *ast.File, typeInfo *types.Info, filename string) error {
	modified, err := t.transformInOtherFile(node, typeInfo)
	if err != nil {
		return err
	}

	if modified {
		return WriteFile(filename, t.fs, node)
	}
	return nil
}

//------------------------------------------------------------------------------
// 以下、実際の変換ロジック
//------------------------------------------------------------------------------

func (t *Transformer) transformInTargetFile(file *ast.File, typeInfo *types.Info) (bool, error) {
	modified := false
	var history []ast.Node
	file.Name.Name = t.newPkg

	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(history) > 0 {
				history = history[:len(history)-1]
			}
			return false
		}
		history = append(history, n)

		switch node := n.(type) {
		case *ast.Field:
			if t.updateExprInTargetFile(node.Type, typeInfo, history) {
				modified = true
			}
		case *ast.ValueSpec:
			if t.updateExprInTargetFile(node.Type, typeInfo, history) {
				modified = true
			}
			for _, val := range node.Values {
				if t.updateExprInTargetFile(val, typeInfo, history) {
					modified = true
				}
			}
			for _, name := range node.Names {
				if t.updateExprInTargetFile(name, typeInfo, history) {
					modified = true
				}
			}
		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if t.updateExprInTargetFile(p.Type, typeInfo, history) {
						modified = true
					}
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if t.updateExprInTargetFile(r.Type, typeInfo, history) {
						modified = true
					}
				}
			}
			if node.Body != nil {
				ast.Inspect(node.Body, func(bodyNode ast.Node) bool {
					if ex, ok := bodyNode.(ast.Expr); ok {
						if t.updateExprInTargetFile(ex, typeInfo, history) {
							modified = true
						}
					}
					return true
				})
			}

		case *ast.TypeSpec:
			// 型エイリアスかどうかチェックして、再帰的に変換
			if t.updateExprInTargetFile(node.Name, typeInfo, history) {
				modified = true
			}
			if node.Assign != token.NoPos {
				// エイリアス
				if t.updateExprInTargetFile(node.Type, typeInfo, history) {
					modified = true
				}
			} else {
				if t.updateExprInTargetFile(node.Type, typeInfo, history) {
					modified = true
				}
			}

		}
		return true
	})

	return modified, nil
}

func (t *Transformer) transformInOtherFile(file *ast.File, typeInfo *types.Info) (bool, error) {
	modified := false

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			if t.updateExprInOtherFile(node.Type, typeInfo) {
				modified = true
			}

		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if t.updateExprInOtherFile(p.Type, typeInfo) {
						modified = true
					}
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if t.updateExprInOtherFile(r.Type, typeInfo) {
						modified = true
					}
				}
			}
		case *ast.TypeAssertExpr:
			if t.updateExprInOtherFile(node.Type, typeInfo) {
				modified = true
			}
		case *ast.TypeSwitchStmt:
			for _, stmt := range node.Body.List {
				if cc, ok := stmt.(*ast.CaseClause); ok {
					for i, expr := range cc.List {
						if t.updateExprInOtherFile(expr, typeInfo) {
							modified = true
							cc.List[i] = expr
						}
					}
				}
			}
		case *ast.ValueSpec:
			if t.updateExprInOtherFile(node.Type, typeInfo) {
				modified = true
			}
		case *ast.CaseClause:
			for i, expr := range node.List {
				if t.updateExprInOtherFile(expr, typeInfo) {
					modified = true
					node.List[i] = expr
				}
			}
		case *ast.CallExpr:
			if t.updateExprInOtherFile(node.Fun, typeInfo) {
				modified = true
			}
			for i, arg := range node.Args {
				if t.updateExprInOtherFile(arg, typeInfo) {
					modified = true
					node.Args[i] = arg
				}
			}
		case *ast.CompositeLit:
			if t.updateExprInOtherFile(node.Type, typeInfo) {
				modified = true
			}
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if t.updateExprInOtherFile(kv.Value, typeInfo) {
						modified = true
					}
				} else {
					if t.updateExprInOtherFile(elt, typeInfo) {
						modified = true
					}
				}
			}
		}
		return true
	})

	return modified, nil
}

//------------------------------------------------------------------------------
// 各種ヘルパー
//------------------------------------------------------------------------------

func (t *Transformer) updateExprInTargetFile(node ast.Node, typeInfo *types.Info, history []ast.Node) bool {
	if node == nil {
		return false
	}
	switch n := node.(type) {
	case *ast.Ident:
		// 親が SelectorExpr(ベースが oldPkg 以外) ならスキップ
		if len(history) >= 2 && isSelectorExpr(history[len(history)-2], t.oldPkg) == false {
			return t.updateIdentInTargetFile(n, typeInfo)
		}
	case *ast.StarExpr:
		return t.updateExprInTargetFile(n.X, typeInfo, history)
	case *ast.ArrayType:
		return t.updateExprInTargetFile(n.Elt, typeInfo, history)
	case *ast.MapType:
		m := false
		if t.updateExprInTargetFile(n.Key, typeInfo, history) {
			m = true
		}
		if t.updateExprInTargetFile(n.Value, typeInfo, history) {
			m = true
		}
		return m
	case *ast.ChanType:
		return t.updateExprInTargetFile(n.Value, typeInfo, history)
	case *ast.CallExpr:
		m := t.updateExprInTargetFile(n.Fun, typeInfo, history)
		for _, arg := range n.Args {
			if t.updateExprInTargetFile(arg, typeInfo, history) {
				m = true
			}
		}
		return m
	case *ast.TypeSpec:
		return t.updateExprInTargetFile(n.Type, typeInfo, history)
	}
	return false
}

func (t *Transformer) updateExprInOtherFile(node ast.Node, typeInfo *types.Info) bool {
	if node == nil {
		return false
	}
	switch n := node.(type) {
	case *ast.Ident:
		return t.updateIdentInOtherFile(n, typeInfo, true)
	case *ast.SelectorExpr:
		// oldPkg.シンボルの場合のみ
		if ident, ok := n.X.(*ast.Ident); ok && ident.Name == t.oldPkg {
			if t.updateIdentInOtherFile(n.Sel, typeInfo, false) {
				ident.Name = t.newPkg
				return true
			}
		}
	case *ast.StarExpr:
		return t.updateExprInOtherFile(n.X, typeInfo)
	case *ast.ArrayType:
		return t.updateExprInOtherFile(n.Elt, typeInfo)
	case *ast.MapType:
		m := t.updateExprInOtherFile(n.Key, typeInfo)
		if t.updateExprInOtherFile(n.Value, typeInfo) {
			m = true
		}
		return m
	case *ast.ChanType:
		return t.updateExprInOtherFile(n.Value, typeInfo)
	case *ast.CallExpr:
		m := t.updateExprInOtherFile(n.Fun, typeInfo)
		for _, arg := range n.Args {
			if t.updateExprInOtherFile(arg, typeInfo) {
				m = true
			}
		}
		return m
	}
	return false
}

func (t *Transformer) updateIdentInTargetFile(e *ast.Ident, typeInfo *types.Info) bool {
	pkgName, pos := getPkgNameAndPositionForIdent(e, t.fs, typeInfo)
	if pkgName == t.oldPkg {
		if pos.Filename == t.oldFile {
			e.Name = strings.TrimPrefix(e.Name, t.deletePrefix)
		} else {
			e.Name = fmt.Sprintf("%s.%s", t.oldPkg, e.Name)
		}
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(e *ast.Ident, typeInfo *types.Info, isIdent bool) bool {
	pkgName, pos := getPkgNameAndPositionForIdent(e, t.fs, typeInfo)
	if pkgName == t.oldPkg && pos.Filename == t.oldFile {
		if isIdent {
			e.Name = fmt.Sprintf("%s.%s", t.newPkg, strings.TrimPrefix(e.Name, t.deletePrefix))
		} else {
			e.Name = strings.TrimPrefix(e.Name, t.deletePrefix)
		}
		return true
	}
	return false
}

func isSelectorExpr(n ast.Node, oldPkg string) bool {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name != oldPkg
}

func getPkgNameAndPositionForIdent(e *ast.Ident, fs *token.FileSet, typeInfo *types.Info) (string, token.Position) {
	if !isExported(e.Name) {
		return "", token.Position{}
	}

	// Uses
	if obj, ok := typeInfo.Uses[e]; ok {
		if constObj, isConst := obj.(*types.Const); isConst {
			return extractPkgNameAndPos(fs, constObj)
		}
		if named, isNamed := obj.Type().(*types.Named); isNamed {
			return extractPkgNameAndPos(fs, named.Obj())
		}
	}
	// Defs
	if obj, ok := typeInfo.Defs[e]; ok {
		if constObj, isConst := obj.(*types.Const); isConst {
			return extractPkgNameAndPos(fs, constObj)
		}
		if named, isNamed := obj.Type().(*types.Named); isNamed {
			return extractPkgNameAndPos(fs, named.Obj())
		}
	}
	return "", token.Position{}
}

func extractPkgNameAndPos(fs *token.FileSet, obj types.Object) (string, token.Position) {
	if obj == nil {
		return "", token.Position{}
	}
	pos := fs.Position(obj.Pos())
	if pos.Filename == "" {
		return "", token.Position{}
	}
	if pkg := obj.Pkg(); pkg != nil {
		return pkg.Name(), pos
	}
	return "", pos
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := []rune(name)
	return unicode.IsUpper(r[0])
}
