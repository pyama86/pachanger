package pachanger

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
)

var processedObjects = make(map[types.Object]bool)

type ExposeRenamer struct {
	workDir    string
	fs         *token.FileSet
	targetFile string
	buildFlags []string
	execute    bool
}

func NewExposeRenamer(workDir, targetFile, tagsFlag string, execute bool) (*ExposeRenamer, error) {
	fs := token.NewFileSet()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(targetFile) {
		targetFile = filepath.Join(absWorkDir, targetFile)
	}

	buildFlags := []string{}
	if tagsFlag != "" {
		buildFlags = append(buildFlags, "-tags", tagsFlag)
	}

	return &ExposeRenamer{
		workDir:    absWorkDir,
		fs:         fs,
		targetFile: targetFile,
		buildFlags: buildFlags,
		execute:    execute,
	}, nil
}

func (g *ExposeRenamer) Generate() error {
	pkgs, err := loadPackages(g.fs, g.workDir, g.buildFlags)
	if err != nil {
		return err
	}
	// エラーがあっても解析を続ける場合
	if packages.PrintErrors(pkgs) > 0 {
		slog.Warn("Some packages contain errors")
	}

	// ターゲットファイルを含むパッケージと AST を探す
	var targetPkg *packages.Package
	var targetFile *ast.File
	for _, pkg := range pkgs {
		// pkg.CompiledGoFiles には解析対象のファイル名が入っています
		for _, cf := range pkg.CompiledGoFiles {
			absCF, err := filepath.Abs(cf)
			if err != nil {
				continue
			}
			if absCF == g.targetFile {
				targetPkg = pkg
				// pkg.Syntax 内からターゲットファイルに該当する AST を探す
				for _, f := range pkg.Syntax {
					filename := pkg.Fset.File(f.Pos()).Name()
					absFilename, err := filepath.Abs(filename)
					if err != nil {
						continue
					}
					if absFilename == g.targetFile {
						targetFile = f
						break
					}
				}
				break
			}
		}
		if targetPkg != nil {
			break
		}
	}
	if targetPkg == nil || targetFile == nil {
		return fmt.Errorf("target file not found: %s", g.targetFile)
	}

	info := targetPkg.TypesInfo

	// パッケージ全体の使用状況から、「ターゲットファイル以外」で使われているオブジェクトを記録
	usedOutside := make(map[types.Object]bool)
	for ident, obj := range info.Uses {
		pos := targetPkg.Fset.Position(ident.Pos())
		absPos, err := filepath.Abs(pos.Filename)
		if err != nil {
			continue
		}
		if absPos != g.targetFile {
			usedOutside[obj] = true
		}
	}

	// パッケージ内の全 AST から宣言ノードをマップとして作成
	declMap := buildDeclMap(targetPkg.Syntax)

	// ターゲットファイルの AST を走査し、対象となる識別子を探索
	ast.Inspect(targetFile, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := info.Uses[ident]
		if obj == nil {
			obj = info.Defs[ident]
		}
		if obj == nil {
			return true
		}

		// 定義が同じパッケージ内かチェック
		if obj.Pkg() == nil || obj.Pkg() != targetPkg.Types {
			return true
		}

		// 非エクスポートであることと、パッケージスコープの識別子であることをチェック
		if obj.Pkg() == nil || !isUnexported(obj.Name()) {
			return true
		}
		// ターゲットファイル外で使われていなければスキップ
		if !usedOutside[obj] {
			return true
		}
		g.processObject(obj, info, declMap, usedOutside)
		return true
	})
	return nil
}

// buildDeclMap は、与えられた AST ファイル群から宣言ノードのマップを作成します。
// キーは識別子の NamePos (token.Pos) です。
func buildDeclMap(files []*ast.File) map[token.Pos]ast.Node {
	declMap := make(map[token.Pos]ast.Node)
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name != nil {
					declMap[d.Name.NamePos] = d
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range s.Names {
							declMap[name.NamePos] = s
						}
					case *ast.TypeSpec:
						// 型そのもの
						declMap[s.Name.NamePos] = s
						// struct 型の場合、フィールドを走査する
						if structType, ok := s.Type.(*ast.StructType); ok {
							for _, field := range structType.Fields.List {
								// 名前が指定されていない場合、匿名フィールドとして扱う
								if len(field.Names) == 0 {
									switch t := field.Type.(type) {
									case *ast.Ident:
										declMap[t.NamePos] = field
									case *ast.StarExpr:
										// ポインタの場合、内部が *ast.Ident または *ast.SelectorExpr なら処理
										switch x := t.X.(type) {
										case *ast.Ident:
											declMap[x.NamePos] = field
										case *ast.SelectorExpr:
											declMap[x.Sel.NamePos] = field
										}
									case *ast.SelectorExpr:
										// 例: pkg.businessProfileHelper
										declMap[t.Sel.NamePos] = field
									}
								} else {
									// 名前が指定されている場合は、従来通り処理
									for _, name := range field.Names {
										declMap[name.NamePos] = field
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return declMap
}

func isUnexported(name string) bool {
	if name == "" || name == "_" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return !unicode.IsUpper(r)
}

func capitalizeFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (g *ExposeRenamer) processObject(obj types.Object, info *types.Info, declMap map[token.Pos]ast.Node, usedOutside map[types.Object]bool) {
	if processedObjects[obj] {
		return
	}
	processedObjects[obj] = true

	pos := g.fs.Position(obj.Pos())
	exportedName := capitalizeFirst(obj.Name())
	if exportedName == obj.Name() {
		return
	}
	goplsCmd := fmt.Sprintf("gopls rename -w %s:%d:%d %s", pos.Filename, pos.Line, pos.Column, exportedName)
	slog.Info("gopls rename command", "command", goplsCmd)
	if g.execute {
		// 実行する場合は、gopls コマンドを実行
		_, err := exec.Command("gopls", "rename", "-w", fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column), exportedName).Output()
		if err != nil {
			slog.Error("gopls rename command failed", "error", err)
			return
		}
	}

	if decl, ok := declMap[obj.Pos()]; ok {
		ast.Inspect(decl, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			innerObj := info.Uses[ident]
			if innerObj == nil {
				return true
			}
			defPos := g.fs.Position(innerObj.Pos())
			absDef, err := filepath.Abs(defPos.Filename)
			if err != nil || absDef != g.targetFile {
				return true
			}
			// if innerObj.Pkg() != nil && innerObj.Pkg().Name() == obj.Pkg().Name() && isUnexported(innerObj.Name()) {
			if innerObj.Pkg() != nil && innerObj.Pkg().Name() == obj.Pkg().Name() {
				if usedOutside[innerObj] {
					g.processObject(innerObj, info, declMap, usedOutside)
				}
			}
			return true
		})
	}
}
