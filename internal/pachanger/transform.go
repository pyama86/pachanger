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

func GetPackages(fs *token.FileSet, absWorkDir string, absTargetFiles ...string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  absWorkDir,
		Fset: fs,
	}

	return packages.Load(cfg, absTargetFiles...)
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

func ProcessOtherFiles(fs *token.FileSet, node *ast.File, typesInfo *types.Info, filename, absTargetFile, absOutputFile, newPkg, deletePrefix string) error {
	slog.Info("Processing other file", slog.String("file", filename))
	modified, err := transformOtherFileAST(fs, node, newPkg, deletePrefix, absTargetFile, typesInfo)
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

		switch node := n.(type) {
		case *ast.Field:
			if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}
			history = append(history, node.Type)
		case *ast.ValueSpec:
			if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}
			history = append(history, node.Type)

			for _, val := range node.Values {
				if fixExprInTarget(fs, val, oldPkg, oldFile, deletePrefix, typesInfos, history) {
					modified = true
				}
				history = append(history, val)
			}
		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					if fixExprInTarget(fs, p.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
						modified = true
					}
					history = append(history, p.Type)
				}
			}
			if node.Type.Results != nil {
				for _, r := range node.Type.Results.List {
					if fixExprInTarget(fs, r.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
						modified = true
					}
					history = append(history, r.Type)
				}
			}
			if node.Body != nil {
				ast.Inspect(node.Body, func(nn ast.Node) bool {
					if ex, ok := nn.(ast.Expr); ok {
						if fixExprInTarget(fs, ex, oldPkg, oldFile, deletePrefix, typesInfos, history) {
							modified = true
						}
						history = append(history, ex)
					}
					return true
				})
			}

		case *ast.TypeSpec:
			if fixExprInTarget(fs, node.Type, oldPkg, oldFile, deletePrefix, typesInfos, history) {
				modified = true
			}
			history = append(history, node.Type)
		}
		return true
	})

	return modified, nil
}

func isSelectorExpr(n ast.Node) bool {
	_, ok := n.(*ast.SelectorExpr)
	return ok
}

func fixExprInTarget(fs *token.FileSet, node ast.Node, oldPkg, oldFile, deletePrefix string, typesInfos *types.Info, history []ast.Node) bool {
	mod := false
	switch node := node.(type) {
	case *ast.Ident:
		if len(history) < 2 || !isSelectorExpr(history[len(history)-2]) {
			updateTypeNameIfWant(true, typesInfos, node, fs, oldFile, oldPkg, func(node *ast.Ident) {
				nodeName := node.Name

				if deletePrefix != "" && len(nodeName) > len(deletePrefix) && strings.HasPrefix(nodeName, deletePrefix) {
					nodeName = nodeName[len(deletePrefix):]
				}
				node.Name = fmt.Sprintf("%s.%s", oldPkg, nodeName)
				mod = true
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
func transformOtherFileAST(fs *token.FileSet, node *ast.File, newPkg, oldFile, deletePrefix string, typeInfos *types.Info) (bool, error) {
	modified := false

	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if fixExpr(fs, n.Type, oldFile, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

		case *ast.FuncDecl:
			for _, p := range n.Type.Params.List {
				if fixExpr(fs, p.Type, oldFile, newPkg, deletePrefix, typeInfos) {
					modified = true
				}
			}
			if n.Type.Results != nil {
				for _, r := range n.Type.Results.List {
					if fixExpr(fs, r.Type, oldFile, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				}
			}

		case *ast.TypeAssertExpr:
			if fixExpr(fs, n.Type, oldFile, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

		case *ast.TypeSwitchStmt:
			for _, stmt := range n.Body.List {
				if cc, ok := stmt.(*ast.CaseClause); ok {
					for i, expr := range cc.List {
						if fixExpr(fs, expr, oldFile, newPkg, deletePrefix, typeInfos) {
							modified = true
							cc.List[i] = expr
						}
					}
				}
			}

		case *ast.ValueSpec:
			if fixExpr(fs, n.Type, oldFile, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

		case *ast.CaseClause:
			for i, expr := range n.List {
				if fixExpr(fs, expr, oldFile, newPkg, deletePrefix, typeInfos) {
					modified = true
					n.List[i] = expr
				}
			}

		case *ast.CallExpr:
			if fixExpr(fs, n.Fun, oldFile, newPkg, deletePrefix, typeInfos) {
				modified = true
			}
			for i, arg := range n.Args {
				if fixExpr(fs, arg, oldFile, newPkg, deletePrefix, typeInfos) {
					modified = true
					n.Args[i] = arg
				}
			}

		case *ast.CompositeLit:
			if fixExpr(fs, n.Type, oldFile, newPkg, deletePrefix, typeInfos) {
				modified = true
			}

			for _, elt := range n.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if fixExpr(fs, kv.Value, oldFile, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				} else {
					if fixExpr(fs, elt, oldFile, newPkg, deletePrefix, typeInfos) {
						modified = true
					}
				}
			}

		}
		return true
	})

	return modified, nil
}

func updateTypeNameIfWant(isTargetFile bool, typeInfos *types.Info, e *ast.Ident, fs *token.FileSet, oldFile, oldPkg string, updateFunc func(*ast.Ident)) bool {
	if isUpperCamelCase(e.Name) {
		uses, isUsed := typeInfos.Uses[e]
		defs, isDefined := typeInfos.Defs[e]
		if !isUsed && !isDefined {
			return false
		}

		var named *types.Named
		var ok bool
		if isDefined {
			named, ok = defs.Type().(*types.Named)
		} else if isUsed {
			named, ok = uses.Type().(*types.Named)
		}

		if ok {
			typeName := named.Obj()
			typePos := fs.Position(typeName.Pos())

			if (isTargetFile && oldFile != typePos.Filename && typeName.Pkg().Name() == oldPkg) || (!isTargetFile && oldFile == typePos.Filename) {
				updateFunc(e)
				return true
			}
		}
	}
	return false
}

func fixExpr(fs *token.FileSet, node ast.Node, oldFile, newPkg, deletePrefix string, typeInfos *types.Info) bool {
	if node == nil {
		return false
	}
	mod := false

	switch e := node.(type) {
	case *ast.Ident:
		mod = updateTypeNameIfWant(false, typeInfos, e, fs, oldFile, "", func(e *ast.Ident) {
			name := e.Name
			if deletePrefix != "" && len(e.Name) > len(deletePrefix) && strings.HasPrefix(e.Name, deletePrefix) {
				name = name[len(deletePrefix):]
			}

			e.Name = fmt.Sprintf("%s.%s", newPkg, name)
		})
	case *ast.SelectorExpr:
		if ident, ok := e.X.(*ast.Ident); ok {
			mod = updateTypeNameIfWant(false, typeInfos, ident, fs, oldFile, "", func(e *ast.Ident) {
				ident.Name = newPkg
			})
		}

	case *ast.StarExpr:
		// 例: *MyType, **PtrStruct, *SelectorExpr など。
		if fixExpr(fs, e.X, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.ArrayType:
		// []T, [...]T など
		if fixExpr(fs, e.Elt, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.MapType:
		// map[K]V
		if fixExpr(fs, e.Key, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}
		if fixExpr(fs, e.Value, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.ChanType:
		// chan T
		if fixExpr(fs, e.Value, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}

	case *ast.CallExpr:
		// ここに来る場合は型キャスト (MyType(...) ) などが可能性あり
		if fixExpr(fs, e.Fun, oldFile, newPkg, deletePrefix, typeInfos) {
			mod = true
		}
		for _, arg := range e.Args {
			if fixExpr(fs, arg, oldFile, newPkg, deletePrefix, typeInfos) {
				mod = true
			}
		}
	}

	return mod
}
