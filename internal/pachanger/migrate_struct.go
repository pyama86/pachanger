package pachanger

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// パッケージ情報を取得
type StructDef struct {
	pkg       string
	filePath  string
	fields    map[string]string // フィールド名 -> 型
	fieldList []string          // フィールドの順番を保持
}

type MigrateStruct struct {
	workDir   string
	fs        *token.FileSet
	targetpkg string
	suffix    string
}

func NewMigrateStruct(workDir, targetpkg, suffix string) (*MigrateStruct, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}

	return &MigrateStruct{
		workDir:   absWorkDir,
		targetpkg: targetpkg,
		suffix:    suffix,
		fs:        token.NewFileSet(),
	}, nil
}

func (m *MigrateStruct) Migrate(testFile string) error {
	if !filepath.IsAbs(testFile) {
		testFile = path.Join(m.workDir, testFile)
	}

	StructDefs := m.FindStructDefinitions()
	usedStructs, err := m.FindUsedStructs(testFile)
	if err != nil {
		return err
	}

	// コンストラクタとパラメータ構造体がない場合は作成
	for structName, StructDef := range StructDefs {
		// 同一パッケージかどうかチェック
		if StructDef.pkg != m.targetpkg {
			continue
		}
		_, ok := usedStructs[structName]
		if ok {
			if !m.HasConstructor(StructDef.filePath, structName) {
				slog.Info("Adding constructor", slog.String("struct", structName))
				str, err := m.AddConstructorWithParamsStructRefactored(StructDef.filePath, structName, StructDefs)
				if err != nil {
					return err
				}

				if err := os.WriteFile(StructDef.filePath, []byte(str), 0644); err != nil {
					return err
				}

			}
		}
	}

	// テストファイルを書き換える
	n, err := m.RewriteTestFileRefactored(testFile, StructDefs, usedStructs)
	if err != nil {
		return err
	}
	if n != nil {
		if err := writeFile(m.fs, n, m.workDir, testFile); err != nil {
			return err
		}
	}
	return nil
}

// findUsedStructs：テストファイルから使用している構造体情報を取得
func (m *MigrateStruct) FindUsedStructs(testFile string) (map[string][]string, error) {
	src, err := os.ReadFile(testFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read test file: %v", err)
	}

	node, err := parser.ParseFile(m.fs, testFile, src, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("failed to parse test file: %v", err)
	}

	usedStructs := make(map[string][]string)

	ast.Inspect(node, func(n ast.Node) bool {
		var cl *ast.CompositeLit

		if unaryExpr, ok := n.(*ast.UnaryExpr); ok && unaryExpr.Op == token.AND {
			if compositeLit, ok := unaryExpr.X.(*ast.CompositeLit); ok {
				cl = compositeLit
			}
		} else if compositeLit, ok := n.(*ast.CompositeLit); ok {
			cl = compositeLit
		}

		if cl == nil {
			return true
		}

		se, ok := cl.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		pkgIdent, ok := se.X.(*ast.Ident)
		if !ok {
			return true
		}

		structName := fmt.Sprintf("%s.%s", pkgIdent.Name, se.Sel.Name)
		var fields []string

		for _, el := range cl.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				fields = append(fields, exprToString(kv.Key))
			}
		}

		if usedStructs[structName] != nil {
			usedStructs[structName] = append(usedStructs[structName], fields...)
		} else {
			usedStructs[structName] = fields
		}

		usedStructs[structName] = slices.Compact(usedStructs[structName])
		return true
	})

	return usedStructs, nil
}

// findStructDefinitions：パッケージ内の構造体定義をスキャンして情報化
func (m *MigrateStruct) FindStructDefinitions() map[string]StructDef {
	StructDefs := make(map[string]StructDef)

	_ = filepath.Walk(m.workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		node, err := parser.ParseFile(m.fs, path, src, parser.AllErrors)
		if err != nil {
			return nil
		}

		ast.Inspect(node, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok {
				structName := fmt.Sprintf("%s.%s", node.Name.Name, ts.Name.Name)
				switch t := ts.Type.(type) {
				case *ast.StructType:
					StructDef := StructDef{
						filePath:  path,
						fields:    make(map[string]string),
						fieldList: []string{},
						pkg:       node.Name.Name,
					}
					for _, field := range t.Fields.List {
						typeStr := exprToString(field.Type)
						unshifted := strings.TrimPrefix(typeStr, "*")

						if isUpperFirst(unshifted) && node.Name.Name == m.targetpkg {
							if strings.HasPrefix(typeStr, "*") {
								typeStr = fmt.Sprintf("*%s.%s", node.Name.Name, unshifted)
							} else {
								typeStr = fmt.Sprintf("%s.%s", node.Name.Name, unshifted)
							}
						}

						if len(field.Names) == 0 {
							StructDef.fieldList = append(StructDef.fieldList, typeStr)
						} else {
							for _, name := range field.Names {
								StructDef.fields[name.Name] = typeStr
								StructDef.fieldList = append(StructDef.fieldList, name.Name)
							}
						}
					}
					StructDefs[structName] = StructDef
				}
			}
			return true
		})
		return nil
	})

	return StructDefs
}

// hasConstructor：構造体にコンストラクタがあるか確認
func (m *MigrateStruct) HasConstructor(filePath, structName string) bool {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	node, err := parser.ParseFile(m.fs, filePath, src, parser.AllErrors)
	if err != nil {
		return false
	}

	nakedStructName := structName[strings.LastIndex(structName, ".")+1:]
	funcName := "New" + nakedStructName + m.suffix
	paramsStructName := nakedStructName + "Params" + m.suffix

	existsConstructor := false
	existsParams := false

	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == funcName {
			existsConstructor = true
		}
		if ts, ok := n.(*ast.TypeSpec); ok {
			if ts.Name.Name == paramsStructName {
				existsParams = true
			}
		}
		return true
	})

	return existsConstructor && existsParams
}

func (m *MigrateStruct) AddConstructorWithParamsStructRefactored(
	filePath, structName string,
	StructDefs map[string]StructDef,
) (string, error) {
	StructDef, exists := StructDefs[structName]
	if !exists {
		return "", fmt.Errorf("struct %s not found in StructDefs", structName)
	}
	if len(StructDef.fieldList) == 0 && len(StructDef.fields) == 0 {
		slog.Debug("no fields found", slog.String("struct", structName))
		return "", nil // フィールドがなければ何もしない
	}

	// ファイルの内容を読み取る
	src, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	// 構造体名とパラメータ構造体の名前を取得
	nakedStructName := structName[strings.LastIndex(structName, ".")+1:]
	paramsStructName := nakedStructName + "Params" + m.suffix
	constructorName := "New" + nakedStructName + m.suffix

	if strings.Contains(string(src), "type "+paramsStructName) &&
		strings.Contains(string(src), "func "+constructorName) {
		slog.Debug("constructor with params struct already exists", slog.String("struct", structName))
		return "", nil
	}

	var fieldsBuilder strings.Builder
	fieldsBuilder.WriteString("type " + paramsStructName + " struct {\n")

	for _, fieldName := range StructDef.fieldList {
		cleanFieldName := strings.TrimPrefix(fieldName, m.targetpkg+".")
		// ポインタ型の処理
		if strings.HasPrefix(fieldName, "*") {
			cleanFieldName = "*" + cleanFieldName
		}

		exportedName := cases.Title(language.English).String(cleanFieldName)
		fieldsBuilder.WriteString(fmt.Sprintf("    %s %s\n", exportedName, StructDef.fields[fieldName]))
	}

	fieldsBuilder.WriteString("}\n\n")

	var constructorBuilder strings.Builder
	constructorBuilder.WriteString(fmt.Sprintf("func %s(params *%s) *%s {\n", constructorName, paramsStructName, nakedStructName))
	constructorBuilder.WriteString(fmt.Sprintf("    return &%s{\n", nakedStructName))

	for _, fieldName := range StructDef.fieldList {
		exportedName := cases.Title(language.English).String(fieldName)
		constructorBuilder.WriteString(fmt.Sprintf("        %s: params.%s,\n", exportedName, exportedName))
	}
	constructorBuilder.WriteString("    }\n")
	constructorBuilder.WriteString("}\n")

	// 3) バッファに追加して返す
	var buf strings.Builder
	buf.Write(src)
	buf.WriteString("\n\n")
	buf.WriteString(fieldsBuilder.String())
	buf.WriteString(constructorBuilder.String())

	return buf.String(), nil
}

// RewriteTestFileRefactored：テストファイルの修正（バッファを使って書き込み）
func (m *MigrateStruct) RewriteTestFileRefactored(testFile string, StructDefs map[string]StructDef, usedStructs map[string][]string) (*ast.File, error) {
	src, err := os.ReadFile(testFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read test file: %v", err)
	}

	// AST（抽象構文木）を生成
	node, err := parser.ParseFile(m.fs, testFile, src, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse test file: %v", err)
	}

	var modified bool

	// ASTを巡回して修正を加える
	ast.Inspect(node, func(n ast.Node) bool {
		// 関数リテラルを再帰的にチェック
		if funcLit, ok := n.(*ast.FuncLit); ok {
			ast.Inspect(funcLit.Body, func(n ast.Node) bool {
				modified = m.RewriteHandlerStructs(n, StructDefs, usedStructs) || modified
				return true
			})
		}
		modified = m.RewriteHandlerStructs(n, StructDefs, usedStructs) || modified
		return true
	})

	if modified {
		return node, nil
	}

	return nil, nil
}

// RewriteHandlerStructs：構造体のリテラルを新しいコンストラクタに書き換え
func (m *MigrateStruct) RewriteHandlerStructs(n ast.Node, StructDefs map[string]StructDef, usedStructs map[string][]string) bool {
	modified := false

	// 1) var 宣言の解析
	if decl, ok := n.(*ast.GenDecl); ok && decl.Tok == token.VAR {
		for _, spec := range decl.Specs {
			if valueSpec, ok := spec.(*ast.ValueSpec); ok {
				for i, value := range valueSpec.Values {
					if compLit, ok := value.(*ast.CompositeLit); ok {
						if newCall := m.processCompositeLit(compLit, StructDefs); newCall != nil {
							valueSpec.Values[i] = newCall
							modified = true
						}
					}
				}
			}
		}
	}

	// 2) 代入式の処理
	if assign, ok := n.(*ast.AssignStmt); ok {
		for i, rhs := range assign.Rhs {
			if compLit, ok := rhs.(*ast.CompositeLit); ok {
				if newCall := m.processCompositeLit(compLit, StructDefs); newCall != nil {
					assign.Rhs[i] = newCall
					modified = true
				}
			}
		}
	}

	// 3) スライスや KeyValueExpr の処理
	if compLit, ok := n.(*ast.CompositeLit); ok {
		for _, el := range compLit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if rhs, ok := kv.Value.(*ast.CompositeLit); ok {
					if newCall := m.processCompositeLit(rhs, StructDefs); newCall != nil {
						kv.Value = newCall
						modified = true
					}
				}
			}
		}
	}

	return modified
}
func exprToString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), expr)
	return buf.String()
}

func isUpperFirst(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)
	return unicode.IsUpper(r[0])
}

// `pkg.XXX{...}` → `pkg.XXX<suffix>(pkg.XXXParams<suffix>{...})` に書き換える
func (m *MigrateStruct) processCompositeLit(cl *ast.CompositeLit, StructDefs map[string]StructDef) ast.Expr {
	se, ok := cl.Type.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := se.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if pkgIdent.Name != m.targetpkg {
		return nil
	}

	structName := fmt.Sprintf("%s.%s", pkgIdent.Name, se.Sel.Name)
	StructDef, found := StructDefs[structName]
	if !found {
		return nil
	}

	// --- フィールドを集める ---
	// 元のリテラル中の "key: value" で key が小文字の場合、大文字に変換してやる
	var newElts []ast.Expr
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if keyIdent, ok2 := kv.Key.(*ast.Ident); ok2 {
				// フィールド名を Title 化 (repo -> Repo)
				newKey := cases.Title(language.English).String(keyIdent.Name)
				newElts = append(newElts, &ast.KeyValueExpr{
					Key:   &ast.Ident{Name: newKey},
					Value: kv.Value,
				})
			} else {
				// もし map["x"] など特殊キーの場合はそのまま
				newElts = append(newElts, el)
			}
		} else {
			// key-value以外(配列リテラルなど)
			newElts = append(newElts, el)
		}
	}

	nakedStructName := structName[strings.LastIndex(structName, ".")+1:]
	paramsStructName := nakedStructName + "Params" + m.suffix

	// コンストラクタ (例: handler.NewForTestXxx)
	ctorFuncName := "New" + nakedStructName + m.suffix
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: StructDef.pkg}, // 例: "handler"
			Sel: &ast.Ident{Name: ctorFuncName},  // 例: "NewForTestAdminToolDPaymentOfficeContractStateGET"
		},
		Args: []ast.Expr{
			&ast.CompositeLit{
				Type: &ast.SelectorExpr{
					X:   &ast.Ident{Name: "&" + StructDef.pkg}, // "handler"
					Sel: &ast.Ident{Name: paramsStructName},
				},
				Elts: newElts, // 大文字化した KeyValue を詰める
			},
		},
	}

	return call
}
