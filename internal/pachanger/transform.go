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
	"path"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const SHOULD_BE_DELETED = "SHOULD_BE_DELETED"

type astWithOutFile struct {
	node     *ast.File
	output   string
	modified bool
	pkgName  string
}

type Transformer struct {
	fs            *token.FileSet
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
	doneFile      map[string]*astWithOutFile
	allPkgs       []*packages.Package
	identMutex    sync.Mutex
	fileMutex     sync.Mutex
}

// NewTransformer は Transformer を生成
func NewTransformer(workDir, newPkg, addPrefix, deletePrefix string, buildFlags []string) (*Transformer, error) {
	fs := token.NewFileSet()
	slog.Info("Loading packages", slog.String("workDir", workDir))
	allPkgs, err := loadPackages(fs, workDir, buildFlags)
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}

	slog.Info("Loaded packages", slog.Int("count", len(allPkgs)))
	return &Transformer{
		fs:           fs,
		addPrefix:    addPrefix,
		deletePrefix: deletePrefix,
		workDir:      workDir,
		newPkg:       newPkg,
		doneIdent:    map[*ast.Ident]bool{},
		doneFile:     map[string]*astWithOutFile{},
		allPkgs:      allPkgs,
	}, nil
}

func (t *Transformer) getDoneFile(key string) *astWithOutFile {
	t.fileMutex.Lock()
	defer t.fileMutex.Unlock()
	return t.doneFile[key]
}

func (t *Transformer) setDoneFile(key string, value *astWithOutFile) {
	t.fileMutex.Lock()
	defer t.fileMutex.Unlock()
	t.doneFile[key] = value
}

func (t *Transformer) filterDefSymbols(pkg *packages.Package, absTargetFile string) (map[string]bool, map[string]bool) {
	targetSymbols := map[string]bool{}
	otherSymbols := map[string]bool{}
	// pkgの中からエクスポートされているシンボルを抽出
	for _, d := range pkg.TypesInfo.Defs {
		if d != nil && d.Exported() && d.Parent() == d.Pkg().Scope() {
			if t.fs.Position(d.Pos()).Filename == absTargetFile {
				targetSymbols[d.Name()] = true
			} else {
				otherSymbols[d.Name()] = true
			}
		}
	}
	return targetSymbols, otherSymbols
}

func loadPackages(fs *token.FileSet, absWorkDir string, buildFlags []string) ([]*packages.Package, error) {
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

func (t *Transformer) findPackageForFile(absTargetFile string) (*ast.File, *packages.Package, error) {
	for _, pkg := range t.allPkgs {
		for _, file := range pkg.Syntax {
			if t.fs.Position(file.Pos()).Filename == absTargetFile {
				if file != nil {
					return file, pkg, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("target file %s not found in packages", absTargetFile)
}

func (t *Transformer) Dump() error {
	eg := &errgroup.Group{}

	for _, v := range t.doneFile {
		v := v
		eg.Go(func() error {
			if _, err := os.Stat(v.output); err != nil || v.modified {
				// import pathを追加
				if t.oldPkgPath != "" && !astutil.UsesImport(v.node, t.oldPkgPath) {
					astutil.AddImport(t.fs, v.node, t.oldPkgPath)
				}
				if err := writeFile(t.fs, v.node, t.workDir, v.output); err != nil {
					return err
				}
			}
			return nil
		})

	}
	return eg.Wait()
}
func findGoModDir(startDir string) (string, error) {
	currentDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	for {
		goModPath := filepath.Join(currentDir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}

	return "", fmt.Errorf("go.mod not found in any parent directory of %s", startDir)
}

// TransformSymbolsInTargetFile はターゲットファイル用
func (t *Transformer) TransformSymbolsInTargetFile(target, output string) error {
	t.doneIdent = map[*ast.Ident]bool{}
	node, pkg, err := t.findPackageForFile(target)
	if err != nil {
		return fmt.Errorf("failed to find package for file: %w", err)
	}

	t.oldPkg = node.Name.Name
	t.oldPkgPath = pkg.PkgPath
	t.targetSymbols, t.otherSymbols = t.filterDefSymbols(pkg, target)
	if len(t.targetSymbols) == 0 && len(t.otherSymbols) == 0 {
		return fmt.Errorf("no symbols found in target file: %s target:%d other:%d may be having syntax errors", target, len(t.targetSymbols), len(t.otherSymbols))
	}

	outputDir := filepath.Dir(output)
	goDir, err := findGoModDir(t.workDir)
	if err != nil {
		return fmt.Errorf("failed to find go.mod directory: %w", err)
	}
	gomodStr, err := os.ReadFile(filepath.Join(goDir, "go.mod"))
	if err != nil {
		return fmt.Errorf("failed to read go.mod: %w", err)
	}
	gomod, err := modfile.Parse("go.mod", gomodStr, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}
	t.newPkgPath = path.Join(gomod.Module.Mod.Path, outputDir[len(goDir):])

	slog.Debug(fmt.Sprintf("load target symbol oldPkg: %s, newPkg: %s, oldPkgPath: %s, newPkgPath: %s", t.oldPkg, t.newPkg, t.oldPkgPath, t.newPkgPath))

	base := filepath.Base(target)
	modified, err := t.transformFile(base, node, pkg.TypesInfo, true)
	if err != nil {
		return err
	}

	t.setDoneFile(t.fs.Position(node.Pos()).Filename, &astWithOutFile{
		node:     node,
		output:   output,
		modified: modified,
		pkgName:  t.newPkg,
	})

	return nil
}

// TransformSymbolsInOtherFile は他ファイル用
func (t *Transformer) TransformSymbolsInOtherFile(target, output string) error {
	if t.oldPkg == "" {
		return fmt.Errorf("need to call TransformSymbolsInTargetFile first")
	}

	node, pkg, err := t.findPackageForFile(target)
	if err != nil {
		slog.Debug(fmt.Sprintf("failed to find package for file: %s", target))
		return nil
	}

	slog.Debug(fmt.Sprintf("load other symbol oldPkg: %s, newPkg: %s, oldPkgPath: %s, newPkgPath: %s", t.oldPkg, t.newPkg, t.oldPkgPath, t.newPkgPath))
	if node == nil {
		return fmt.Errorf("failed to find package for file: %w", err)
	}

	base := filepath.Base(target)
	modified, err := t.transformFile(base, node, pkg.TypesInfo, false)
	if err != nil {
		return err
	}

	if modified {
		slog.Debug(fmt.Sprintf("modified file: %s", output))
		// import pathを追加
		if t.newPkgPath != "" && !astutil.UsesImport(node, t.newPkgPath) {
			astutil.AddImport(t.fs, node, t.newPkgPath)
		}
		t.setDoneFile(t.fs.Position(node.Pos()).Filename, &astWithOutFile{
			node:     node,
			output:   output,
			modified: modified,
			pkgName:  node.Name.Name,
		})
	}
	return nil
}

func (t *Transformer) transformFile(target string, file *ast.File, typesInfo *types.Info, isTarget bool) (bool, error) {
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
		if t.updateExpr(target, n, filePkg, typesInfo, isTarget) {
			modified = true
		}

		return true
	})

	return modified, nil
}
func (t *Transformer) addDoneList(e *ast.Ident) {
	t.identMutex.Lock()
	defer t.identMutex.Unlock()
	slog.Debug(fmt.Sprintf("Add DoneIdent %s", e.Name))
	t.doneIdent[e] = true
}

// -----------------------------------------
// updateExpr: isTarget に応じて関数を切替
// -----------------------------------------
func (t *Transformer) updateExpr(target string, node ast.Node, filePkg string, typesInfo *types.Info, isTarget bool) bool {
	mod := false
	switch n := node.(type) {
	case *ast.Field:
		if n.Names != nil {
			for _, name := range n.Names {
				// 構造体のフィールド名は変更しない
				slog.Debug(fmt.Sprintf("Skip Field %s in synbol %v file:%s", name.Name, t.targetSymbols[name.Name], target))
				t.addDoneList(name)
			}
		}

		if t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget) {
			return true
		}
	case *ast.Ident:
		t.identMutex.Lock()
		defer t.identMutex.Unlock()
		if t.doneIdent[n] {
			slog.Debug(fmt.Sprintf("Skip Ident %s in synbol %v file:%s", n.Name, t.targetSymbols[n.Name], target))
			return false
		}

		if isTarget {
			slog.Debug(fmt.Sprintf("Processing Ident %s in Target filePkg:%s file:%s", n.Name, filePkg, target))
			return t.updateIdentInTargetFile(target, n, filePkg, typesInfo)
		} else {
			slog.Debug(fmt.Sprintf("Processing Ident %s in Other filePkg: %s file:%s", n.Name, filePkg, target))
			return t.updateIdentInOtherFile(target, n, filePkg, typesInfo)
		}

	case *ast.SelectorExpr:
		if nest, nestok := n.X.(*ast.SelectorExpr); nestok {
			slog.Debug(fmt.Sprintf("Processing Nest SelectorExpr %s.%s in file:%s", nest.Sel.Name, n.Sel.Name, target))
			t.addDoneList(nest.Sel)
			t.addDoneList(n.Sel)
			if t.updateExpr(target, nest, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}

		if ident, ok := n.X.(*ast.Ident); ok {
			// 無関係なパッケージは置き換えない
			if ident.Name != t.oldPkg && ident.Name != t.newPkg {
				slog.Debug(fmt.Sprintf("Skip Selector %s.%s in synbol %v file:%s", ident.Name, n.Sel.Name, t.targetSymbols[n.Sel.Name], target))

				t.addDoneList(n.Sel)
				return false
			}

			if isTarget {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Target file:%s", ident.Name, n.Sel.Name, target))
				// 対象のファイルで、新しいパッケージを参照している場合
				// パッケージ名を削除する必要がある
				if ident.Name == t.newPkg {
					ident.Name = SHOULD_BE_DELETED
					t.addDoneList(n.Sel)
					return true
				}

				// 対象のファイルで古いパッケージ名を参照している場合
				// 変数名がpackageと重複してるケースなので無視する
				if ident.Name == t.oldPkg {
					t.addDoneList(n.Sel)
					return false
				}

			} else {
				slog.Debug(fmt.Sprintf("Processing SelectorExpr %s.%s in Other file:%s", ident.Name, n.Sel.Name, target))
				if t.targetSymbols[n.Sel.Name] {
					// 新しいパッケージのファイルが
					// 変更前か変更後のパッケージ名でアクセスしている
					if t.newPkg == filePkg && (ident.Name == t.oldPkg || ident.Name == t.newPkg) {
						slog.Debug(fmt.Sprintf("Delete %s in %s.%s in Other file:%s", ident.Name, ident.Name, n.Sel.Name, target))
						ident.Name = SHOULD_BE_DELETED
						return true
						// もとのパッケージのファイルが
						// 変更前のパッケージ名でアクセスしている
						// もしくは新旧関係ないパッケージのファイルが
						// 変更前のパッケージ名でアクセスしている
					} else if (t.oldPkg == filePkg || t.newPkg != filePkg) && ident.Name == t.oldPkg {
						beforeIdent := ident.Name
						beforeSel := n.Sel.Name
						ident.Name = t.newPkg
						if len(t.deletePrefix) > 0 && len(t.deletePrefix) < len(n.Sel.Name) {
							n.Sel.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(n.Sel.Name, t.deletePrefix))
						} else {
							n.Sel.Name = fmt.Sprintf("%s%s", t.addPrefix, n.Sel.Name)
						}
						slog.Debug(fmt.Sprintf("Update %s.%s -> %s.%s in Other file:%s", beforeIdent, beforeSel, ident.Name, n.Sel.Name, target))
						return true
					}
				} else {
					slog.Debug(fmt.Sprintf("Skip %s in %s.%s in synbol %v in Other file:%s", ident.Name, ident.Name, n.Sel.Name, t.targetSymbols[n.Sel.Name], target))
				}
			}
		}
	case *ast.ValueSpec:
		if t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget) {
			mod = true
		}
		for _, val := range n.Values {
			if t.updateExpr(target, val, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
		for _, name := range n.Names {
			if t.updateExpr(target, name, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.StarExpr:
		return t.updateExpr(target, n.X, filePkg, typesInfo, isTarget)
	case *ast.ArrayType:
		return t.updateExpr(target, n.Elt, filePkg, typesInfo, isTarget)
	case *ast.MapType:
		mod = t.updateExpr(target, n.Key, filePkg, typesInfo, isTarget)
		if t.updateExpr(target, n.Value, filePkg, typesInfo, isTarget) {
			mod = true
		}
	case *ast.KeyValueExpr:
		// 構造体のフィールド名は変更しない
		if ident, ok := n.Key.(*ast.Ident); ok {
			slog.Debug(fmt.Sprintf("Skip KeyValueExpr %s in Target file:%s", ident.Name, target))
			t.addDoneList(ident)
		}
	case *ast.ChanType:
		return t.updateExpr(target, n.Value, filePkg, typesInfo, isTarget)
	case *ast.CallExpr:
		mod = t.updateExpr(target, n.Fun, filePkg, typesInfo, isTarget)
		for _, arg := range n.Args {
			if t.updateExpr(target, arg, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.TypeAssertExpr:
		return t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget)

	case *ast.TypeSwitchStmt:
		for _, stmt := range n.Body.List {
			if cc, ok := stmt.(*ast.CaseClause); ok {
				for i, expr := range cc.List {
					if t.updateExpr(target, expr, filePkg, typesInfo, isTarget) {
						mod = true
						cc.List[i] = expr
					}
				}
			}
		}

	case *ast.CaseClause:
		for i, expr := range n.List {
			if t.updateExpr(target, expr, filePkg, typesInfo, isTarget) {
				mod = true
				n.List[i] = expr
			}
		}

	case *ast.TypeSpec:
		mod = t.updateExpr(target, n.Name, filePkg, typesInfo, isTarget)
		if n.Assign != token.NoPos {
			if t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget) {
				mod = true
			}
		} else {
			if t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	case *ast.CompositeLit:
		mod = t.updateExpr(target, n.Type, filePkg, typesInfo, isTarget)
		for _, elt := range n.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if t.updateExpr(target, kv.Value, filePkg, typesInfo, isTarget) {
					mod = true
				}
			} else {
				if t.updateExpr(target, elt, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
	case *ast.FuncDecl:
		if n.Type.Params != nil {
			for _, p := range n.Type.Params.List {
				if t.updateExpr(target, p.Type, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
		if n.Type.Results != nil {
			for _, r := range n.Type.Results.List {
				if t.updateExpr(target, r.Type, filePkg, typesInfo, isTarget) {
					mod = true
				}
			}
		}
		if n.Body != nil {
			ast.Inspect(n.Body, func(bodyNode ast.Node) bool {
				if t.updateExpr(target, bodyNode, filePkg, typesInfo, isTarget) {
					mod = true
				}
				return true
			})
		}
	case *ast.IndexExpr: // 単一のジェネリック型
		if t.updateExpr(target, n.X, filePkg, typesInfo, isTarget) {
			mod = true
		}
		if t.updateExpr(target, n.Index, filePkg, typesInfo, isTarget) {
			mod = true
		}
	case *ast.IndexListExpr: // 複数のジェネリック型
		if t.updateExpr(target, n.X, filePkg, typesInfo, isTarget) {
			mod = true
		}
		for _, idx := range n.Indices {
			if t.updateExpr(target, idx, filePkg, typesInfo, isTarget) {
				mod = true
			}
		}
	}

	return mod
}

func (t *Transformer) usesPackageName(e *ast.Ident, typesInfo *types.Info) string {
	if o, ok := typesInfo.Uses[e]; ok && o != nil && o.Pkg() != nil {
		// 処理済みのファイルのパッケージは新しいパッケージ名を返す
		d := t.getDoneFile(t.fs.Position(o.Pos()).Filename)
		if d != nil {
			return d.pkgName
		}
		return o.Pkg().Name()
	}
	return ""
}

func (t *Transformer) isLocalDecl(e *ast.Ident, typesInfo *types.Info) bool {
	if obj := typesInfo.ObjectOf(e); obj != nil && obj.Parent() != obj.Pkg().Scope() {
		// この識別子はローカル定義なので、変換対象外とする
		return true
	}

	return false
}

func (t *Transformer) updateIdentInTargetFile(target string, e *ast.Ident, filePkg string, typesInfo *types.Info) bool {

	// 同じパッケージの接頭辞がついている場合は削除
	if strings.HasPrefix(e.Name, t.newPkg+".") {
		slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Target file:%s", e.Name, strings.TrimPrefix(e.Name, t.newPkg+"."), target))
		e.Name = strings.TrimPrefix(e.Name, t.newPkg+".")
	}

	// 変更前と同じパッケージのファイルで対象のファイルのシンボルではない場合
	if t.otherSymbols[e.Name] && t.usesPackageName(e, typesInfo) == t.oldPkg {
		// 変更前のパッケージと新しいパッケージが同じ場合(上書きなど)
		if t.newPkg == t.oldPkg && filePkg == t.oldPkg {
			return false
		} else {
			slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Target file:%s", e.Name, fmt.Sprintf("%s.%s", t.newPkg, e.Name), target))
			e.Name = fmt.Sprintf("%s.%s", t.oldPkg, e.Name)
		}
		return true
	} else if filePkg == t.oldPkg && t.targetSymbols[e.Name] {
		if len(t.deletePrefix) > 0 && len(t.deletePrefix) < len(e.Name) {
			e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
		} else {
			e.Name = fmt.Sprintf("%s%s", t.addPrefix, e.Name)
		}
		return true
	}
	return false
}

func (t *Transformer) updateIdentInOtherFile(target string, e *ast.Ident, filePkg string, typesInfo *types.Info) bool {
	usePkg := t.usesPackageName(e, typesInfo)
	if t.isLocalDecl(e, typesInfo) && usePkg == filePkg {
		return false
	}

	// ファイルと同じパッケージの接頭辞がついている場合は削除
	if strings.HasPrefix(e.Name, t.newPkg+".") && filePkg == t.newPkg {
		slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Other file:%s", e.Name, strings.TrimPrefix(e.Name, t.newPkg+"."), target))
		e.Name = strings.TrimPrefix(e.Name, t.newPkg+".")
	}

	// 変更前のパッケージで、現在のターゲットのシンボルにある場合、接頭辞を削除
	if strings.HasPrefix(e.Name, t.oldPkg+".") && t.targetSymbols[strings.TrimPrefix(e.Name, t.oldPkg+".")] {
		slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Other file:%s", e.Name, strings.TrimPrefix(e.Name, t.oldPkg+"."), target))
		e.Name = strings.TrimPrefix(e.Name, t.oldPkg+".")
	}

	if strings.HasPrefix(e.Name, t.oldPkg+".") && filePkg == t.oldPkg {
		slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Other file:%s", e.Name, fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix)), target))
		e.Name = strings.TrimPrefix(e.Name, t.oldPkg+".")
	}

	if t.targetSymbols[e.Name] {
		// 変更前のパッケージのファイル
		if filePkg == t.oldPkg && usePkg != "" {
			// ファイルのパッケージが新しいパッケージと異なるかつ、新しいパッケージ名で参照している場合
			if len(t.deletePrefix) > 0 && len(t.deletePrefix) < len(e.Name) {
				slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Other file:%s", e.Name, fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix)), target))
				e.Name = fmt.Sprintf("%s.%s%s", t.newPkg, t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
			} else {
				slog.Debug(fmt.Sprintf("Update Ident %s -> %s in Other file:%s", e.Name, fmt.Sprintf("%s%s", t.addPrefix, e.Name), target))
				e.Name = fmt.Sprintf("%s.%s", t.newPkg, e.Name)
			}
			return true
		} else {
			// 変更前のパッケージではないファイル
			before := e.Name
			if usePkg == t.oldPkg {
				if len(t.deletePrefix) > 0 && len(t.deletePrefix) < len(e.Name) {
					e.Name = fmt.Sprintf("%s%s", t.addPrefix, strings.TrimPrefix(e.Name, t.deletePrefix))
				} else {
					e.Name = fmt.Sprintf("%s%s", t.addPrefix, e.Name)
				}
				if before != e.Name {
					return true
				}
			}
		}

	}
	return false
}

func writeFile(fs *token.FileSet, node *ast.File, workDir, output string) error {
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

	dir := filepath.Dir(workDir)
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("failed to change directory to %s: %v", dir, err)
	}

	var buf bytes.Buffer
	config := &printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	if err := config.Fprint(&buf, fs, node); err != nil {
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
