package pachanger

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

func GetPackages(fs *token.FileSet, absWorkDir string) ([]*packages.Package, error) {
	slog.Info("Getting packages", slog.String("dir", absWorkDir))
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   absWorkDir,
		Fset:  fs,
		Tests: true,
	}

	return packages.Load(cfg, "./...")
}

func ProcessTargetFile(fs *token.FileSet, node *ast.File, typesInfo *types.Info, absTargetFile, absOutputFile, newPkg, deletePrefix string) error {
	slog.Info("Processing target file", slog.String("file", absTargetFile))

	modified, err := transformTargetAST(fs, node, newPkg, absTargetFile, deletePrefix, typesInfo)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absOutputFile); err != nil || modified {
		if err := WriteFile(absOutputFile, fs, node); err != nil {
			return err
		}
	}

	return nil
}
func FilterPackage(fs *token.FileSet, pkgs []*packages.Package, absTargetFile string) (*ast.File, *packages.Package, error) {
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			if fs.Position(file.Pos()).Filename == absTargetFile {
				return file, pkg, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("target file %s not found in packages", absTargetFile)
}

func ProcessOtherFiles(fs *token.FileSet, node *ast.File, typesInfo *types.Info, filename, absTargetFile, oldPkg, absOutputFile, newPkg, deletePrefix string) error {
	slog.Info("Processing other file", slog.String("file", filename))
	modified, err := transformOtherFileAST(fs, node, absTargetFile, oldPkg, newPkg, deletePrefix, typesInfo)
	if err != nil {
		return err
	}
	if !modified {
		return nil
	}
	slog.Info("Updating file", slog.String("file", filename))
	return WriteFile(filename, fs, node)
}

func transformTargetAST(fs *token.FileSet, file *ast.File, newPkg, oldFile, deletePrefix string, typesInfos *types.Info) (bool, error) {

	oldPkg := file.Name.Name
	file.Name.Name = newPkg

	slog.Info("Updating package", slog.String("old", oldPkg), slog.String("new", file.Name.Name))
	modified := false
	history := []ast.Node{}
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(history) != 0 {
				history = history[:len(history)-1]
			}
			return false
		}
		history = append(history, n)

		switch node := n.(type) {
		case *ast.Field:
			if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}
		case *ast.ValueSpec:
			if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}

			for _, val := range node.Values {
				if fixExprInTarget(fs, val, oldPkg, oldFile, deletePrefix, typesInfos, history) {
					modified = true
				}
			}

			for _, name := range node.Names {
				if fixExprInTarget(fs, name, oldPkg, oldFile, deletePrefix, typesInfos, history) {
					modified = true
				}
			}
		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if fixExprInTarget(fs, p.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
						modified = true
					}
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if fixExprInTarget(fs, r.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
						modified = true
					}
				}
			}
			if node.Body != nil {
				ast.Inspect(node.Body, func(nn ast.Node) bool {
					if ex, ok := nn.(ast.Expr); ok {
						if fixExprInTarget(fs, ex, oldPkg, oldFile, deletePrefix, typesInfos, history) {
							modified = true
						}
					}
					return true
				})
			}

		case *ast.TypeSpec:
			if fixExprInTarget(fs, node.Name, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}
		}
		return true
	})

	return modified, nil
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

func fixExprInTarget(fs *token.FileSet, node ast.Node, oldPkg, oldFile, deletePrefix string, typesInfos *types.Info, history []ast.Node) bool {
	mod := false
	switch node := node.(type) {
	case *ast.Ident:
		if len(history) < 2 || !isSelectorExpr(history[len(history)-2], oldPkg) {
			mod = updateTypeNameTargetFile(fs, typesInfos, node, oldFile, oldPkg, func(node *ast.Ident, isSameFile bool) {
				if !isSameFile {
					node.Name = fmt.Sprintf("%s.%s", oldPkg, node.Name)
				} else {
					node.Name = strings.TrimPrefix(node.Name, deletePrefix)
				}
			})
		}

	case *ast.StarExpr:
		if pkgIdent, ok := node.X.(*ast.Ident); ok {
			if isUpperCamelCase(pkgIdent.Name) {
				if fixExprInTarget(fs, node.X, oldPkg, oldFile, deletePrefix, typesInfos, history) {
					mod = true
				}
			}
		}

	case *ast.ArrayType:
		if fixExprInTarget(fs, node.Elt, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}

	case *ast.MapType:
		if fixExprInTarget(fs, node.Key, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}
		if fixExprInTarget(fs, node.Value, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}

	case *ast.ChanType:
		if fixExprInTarget(fs, node.Value, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}

	case *ast.CallExpr:
		if fixExprInTarget(fs, node.Fun, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}
		for _, arg := range node.Args {
			if fixExprInTarget(fs, arg, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				mod = true
			}
		}
	case *ast.TypeSpec:
		if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
			mod = true
		}

	}
	return mod
}

func isUpperCamelCase(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsUpper([]rune(name)[0])
}

// transformOtherFileAST は他ファイル側の AST を歩き、oldPkg+symbols に該当する部分を newPkg に置き換える。
func transformOtherFileAST(fs *token.FileSet, node *ast.File, oldFile, oldPkg, newPkg, deletePrefix string, typeInfos *types.Info) (bool, error) {
	modified := false

	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if fixExpr(fs, n.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				modified = true
			}
		case *ast.FuncDecl:
			for _, p := range n.Type.Params.List {
				if fixExpr(fs, p.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
					modified = true
				}
			}
			if n.Type.Results != nil {
				for _, r := range n.Type.Results.List {
					if fixExpr(fs, r.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				}
			}

		case *ast.TypeAssertExpr:
			if fixExpr(fs, n.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

		case *ast.TypeSwitchStmt:
			for _, stmt := range n.Body.List {
				if cc, ok := stmt.(*ast.CaseClause); ok {
					for i, expr := range cc.List {
						if fixExpr(fs, expr, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
							modified = true
							cc.List[i] = expr
						}
					}
				}
			}

		case *ast.ValueSpec:
			if fixExpr(fs, n.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

		case *ast.CaseClause:
			for i, expr := range n.List {
				if fixExpr(fs, expr, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
					modified = true
					n.List[i] = expr
				}
			}

		case *ast.CallExpr:
			if fixExpr(fs, n.Fun, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				modified = true
			}
			for i, arg := range n.Args {
				if fixExpr(fs, arg, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
					modified = true
					n.Args[i] = arg
				}
			}

		case *ast.CompositeLit:
			if fixExpr(fs, n.Type, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

			for _, elt := range n.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if fixExpr(fs, kv.Value, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				} else {
					if fixExpr(fs, elt, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				}
			}

		}
		return true
	})

	return modified, nil
}

func getType(fs *token.FileSet, typeInfos *types.Info, e *ast.Ident) (string, token.Position) {
	if !isUpperCamelCase(e.Name) {
		return "", token.Position{}
	}

	// `types.Object` から `Pkg().Name()` と `Pos()` を取得するヘルパー関数
	getObject := func(obj types.Object) (string, token.Position) {
		if obj == nil {
			return "", token.Position{}
		}
		typePos := fs.Position(obj.Pos())
		if typePos.Filename == "" {
			return "", token.Position{}
		}
		if pkg := obj.Pkg(); pkg != nil {
			return pkg.Name(), typePos
		}
		return "", typePos
	}

	// `types.Const` の場合
	if obj, ok := typeInfos.Uses[e]; ok {
		if constObj, isConst := obj.(*types.Const); isConst {
			return getObject(constObj)
		}
	}
	if obj, ok := typeInfos.Defs[e]; ok {
		if constObj, isConst := obj.(*types.Const); isConst {
			return getObject(constObj)
		}
	}

	// `types.Named` の場合
	if obj, ok := typeInfos.Uses[e]; ok {
		if named, isNamed := obj.Type().(*types.Named); isNamed {
			return getObject(named.Obj())
		}
	}
	if obj, ok := typeInfos.Defs[e]; ok {
		if named, isNamed := obj.Type().(*types.Named); isNamed {
			return getObject(named.Obj())
		}
	}

	return "", token.Position{}
}
func updateTypeNameTargetFile(fs *token.FileSet, typeInfos *types.Info, e *ast.Ident, oldFile, oldPkg string, updateFunc func(*ast.Ident, bool)) bool {
	pkgName, typePos := getType(fs, typeInfos, e)
	if pkgName != "" {
		if pkgName == oldPkg {
			updateFunc(e, oldFile == typePos.Filename)
			return true
		}
	}
	return false
}

func updateTypeNameOtherFile(fs *token.FileSet, typeInfos *types.Info, e *ast.Ident, oldFile, oldPkg string, updateFunc func(*ast.Ident)) bool {
	pkgName, typePos := getType(fs, typeInfos, e)
	if pkgName != "" {
		if oldFile == typePos.Filename && pkgName == oldPkg {
			updateFunc(e)
			return true
		}
	}
	return false
}

func fixExpr(fs *token.FileSet, node ast.Node, oldFile, oldPkg, newPkg, deletePrefix string, typeInfos *types.Info) bool {
	if node == nil {
		return false
	}
	mod := false

	switch e := node.(type) {
	case *ast.Ident:
		mod = updateTypeNameOtherFile(fs, typeInfos, e, oldFile, oldPkg, func(e *ast.Ident) {
			e.Name = fmt.Sprintf("%s.%s", newPkg, strings.TrimPrefix(e.Name, deletePrefix))
		})

	case *ast.SelectorExpr:
		if ident, ok := e.X.(*ast.Ident); ok && ident.Name == oldPkg {
			mod = updateTypeNameOtherFile(fs, typeInfos, e.Sel, oldFile, oldPkg, func(e *ast.Ident) {
				ident.Name = newPkg
				e.Name = strings.TrimPrefix(e.Name, deletePrefix)
			})
		}

	case *ast.StarExpr:
		// 例: *MyType, **PtrStruct, *SelectorExpr など。
		if fixExpr(fs, e.X, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.ArrayType:
		// []T, [...]T など
		if fixExpr(fs, e.Elt, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.MapType:
		// map[K]V
		if fixExpr(fs, e.Key, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}
		if fixExpr(fs, e.Value, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.ChanType:
		// chan T
		if fixExpr(fs, e.Value, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.CallExpr:
		// ここに来る場合は型キャスト (MyType(...) ) などが可能性あり
		if fixExpr(fs, e.Fun, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
			mod = true
		}
		for _, arg := range e.Args {
			if fixExpr(fs, arg, oldFile, oldPkg, newPkg, deletePrefix, typeInfos) {
				mod = true
			}
		}
	}

	return mod
}
