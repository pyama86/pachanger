package pachanger

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// nodeToString は、与えられた ast.File を string に変換します。
func nodeToString(node *ast.File) (string, error) {
	var buf bytes.Buffer
	fs := token.NewFileSet()
	if err := printer.Fprint(&buf, fs, node); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// parseGoSource は与えられた src (Goソースコード) をパースし、ast.File と *token.FileSet を返します。
func parseGoSource(src string) (*ast.File, *token.FileSet, error) {
	fs := token.NewFileSet()
	node, err := parser.ParseFile(fs, "", src, parser.AllErrors)
	if err != nil {
		return nil, nil, err
	}
	return node, fs, nil
}

// mockTypesInfo はテスト用に簡易な types.Info を組み立て、
// シンボル (Example, ModelExample など) がどのファイルに属しているかを記録したマップを返します。
func mockTypesInfo(fs *token.FileSet, node *ast.File) (*types.Info, map[string]string) {
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	typeFileMap := make(map[string]string)

	exampleFile := fs.AddFile("model/example.go", -1, 1000)
	examplePos := exampleFile.Base() + 1

	modelExampleFile := fs.AddFile("model/model/example.go", -1, 1000)
	modelExamplePos := modelExampleFile.Base() + 1

	examplePkg := types.NewPackage("model", "model")
	modelPkg := types.NewPackage("model", "model")

	emptyStruct := types.NewStruct(nil, nil)

	// Example 用オブジェクト
	exampleObjType := types.NewTypeName(token.Pos(examplePos), examplePkg, "Example", nil)
	exampleNamedType := types.NewNamed(exampleObjType, emptyStruct, nil)
	exampleObjTypeWithType := types.NewTypeName(token.Pos(examplePos), examplePkg, "Example", exampleNamedType)

	// ModelExample 用オブジェクト
	modelObjType := types.NewTypeName(token.Pos(modelExamplePos), modelPkg, "ModelExample", nil)
	modelNamedType := types.NewNamed(modelObjType, emptyStruct, nil)
	modelObjTypeWithType := types.NewTypeName(token.Pos(modelExamplePos), modelPkg, "ModelExample", modelNamedType)

	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			pos := fs.Position(ident.Pos())

			// ダミーの objects
			obj := types.NewTypeName(ident.Pos(), modelPkg, ident.Name, nil)
			info.Defs[ident] = obj
			info.Uses[ident] = obj

			// Example
			if ident.Name == "Example" {
				typeFileMap[ident.Name] = "model/example.go"
				info.Defs[ident] = exampleObjTypeWithType
				info.Uses[ident] = exampleObjTypeWithType
			} else if ident.Name == "ModelExample" {
				typeFileMap[ident.Name] = "model/model/example.go"
				info.Defs[ident] = modelObjTypeWithType
				info.Uses[ident] = modelObjTypeWithType
			} else if pos.IsValid() {
				// そのほかの識別子は適当に pos.Filename へ
				typeFileMap[ident.Name] = pos.Filename
			}
		}
		return true
	})

	return info, typeFileMap
}

func TestTransformInTargetFile(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		oldPkg       string
		newPkg       string
		deletePrefix string
		oldFile      string
		expectMatch  string
	}{
		{
			name:   "Example 型のパッケージ移動",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type Example struct {}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
type Example struct {}
`,
		},
		{
			name:   "ModelExampleを model.ModelExample に変更, Example は同ファイル→そのまま",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type TestStruct struct {
	Example
	ModelExample
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
type TestStruct struct {
	Example
	model.ModelExample
}
`,
		},
		{
			name:   "関数の戻り値でも同様の変換を確認",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
func Create() Example {
	return Example{}
}
func CreateModel() ModelExample {
	return ModelExample{}
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
func Create() Example {
	return Example{}
}
func CreateModel() model.ModelExample {
	return model.ModelExample{}
}
`,
		},
		{
			name:   "メソッドの引数でも同様に変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
func (s TestStruct) SetExample(e Example) {
}
func (s TestStruct) SetModelExample(me ModelExample) {
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
func (s TestStruct) SetExample(e Example) {
}
func (s TestStruct) SetModelExample(me model.ModelExample) {
}
`,
		},
		{
			name:   "スライス内の ModelExample は model.ModelExample に変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examples []Example
var modelExamples []ModelExample
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
var examples []Example
var modelExamples []model.ModelExample
`,
		},
		{
			name:   "ポインタ型での変換確認",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examplePtr *Example
var modelExamplePtr *ModelExample
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
var examplePtr *Example
var modelExamplePtr *model.ModelExample
`,
		},
		{
			name:   "map のキーと値での変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleMap map[Example]ModelExample
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
var exampleMap map[Example]model.ModelExample
`,
		},
		{
			name:         "プレフィックス削除の確認",
			oldPkg:       "model",
			newPkg:       "example",
			deletePrefix: "E",
			input: `
package model
type Sample struct {
	modelExample Example
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
type Sample struct {
	modelExample xample
}
`,
		},

		// --------------------------------
		// Additional tests for coverage
		// --------------------------------

		{
			name:   "複数の埋め込み型 + ネストでの変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type Inner struct {
	ModelExample
}
type Outer struct {
	Inner
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
type Inner struct {
	model.ModelExample
}
type Outer struct {
	Inner
}
`,
		},
		{
			name:   "型エイリアスが含まれる場合のテスト（基本的には同様に model.ModelExample へ）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type Alias = ModelExample
type TestAlias struct {
	A Alias
}
`,
			oldFile: "model/example.go",
			expectMatch: `
package example
type Alias = model.ModelExample
type TestAlias struct {
	A Alias
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			typesInfo, _ := mockTypesInfo(fs, node)
			cdir, err := os.Getwd()
			if err != nil {
				log.Fatal(err)
			}

			// Transformerを作成（outputFileはテストでは未使用なのでダミーでOK）
			transformer := NewTransformer(fs, cdir, tt.oldFile, tt.oldPkg, tt.newPkg, tt.deletePrefix)
			_, err = transformer.transformInTargetFile(node, typesInfo)
			assert.NoError(t, err)

			// 期待値と比較
			expectedNode, _, err := parseGoSource(tt.expectMatch)
			assert.NoError(t, err)

			// パッケージ名
			assert.Equal(t, expectedNode.Name.Name, node.Name.Name)

			// AST全体の文字列比較
			nodeStr, err := nodeToString(node)
			assert.NoError(t, err)
			expectedStr, err := nodeToString(expectedNode)
			assert.NoError(t, err)
			assert.Equal(t, expectedStr, nodeStr)
		})
	}
}

// TestTransformSymbolsInOtherFile は他ファイルのシンボルをターゲットファイルの変更に合わせて変換する処理をテストします。
func TestTransformInOtherFile(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		oldPkg       string
		newPkg       string
		deletePrefix string
		// ターゲットファイル（= 変更元）のパス
		oldFile     string
		expectMatch string
	}{
		{
			name:   "ModelExample 型を model から example に移動",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type TestStruct struct {
	ModelExample
}
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
type TestStruct struct {
	example.ModelExample
}
`,
		},
		{
			name:   "ModelExample 型のポインタ変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examplePtr *ModelExample
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
var examplePtr *example.ModelExample
`,
		},
		{
			name:   "ModelExample 型スライスの変換",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examples []ModelExample
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
var examples []example.ModelExample
`,
		},
		{
			name:   "ModelExample 型を map のキーと値に含むパターン",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var keyValueMap map[ModelExample]ModelExample
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
var keyValueMap map[example.ModelExample]example.ModelExample
`,
		},
		{
			name:   "チャネルの例（受信専用, 送信専用など）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleChan chan ModelExample
var sendOnlyChan chan<- ModelExample
var receiveOnlyChan <-chan ModelExample
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
var exampleChan chan example.ModelExample
var sendOnlyChan chan<- example.ModelExample
var receiveOnlyChan <-chan example.ModelExample
`,
		},
		{
			name:         "ModelExampleのPrefixを削除",
			oldPkg:       "model",
			newPkg:       "example",
			deletePrefix: "M",
			input: `
package model
type TestStruct struct {
	ModelExample
}
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model
type TestStruct struct {
	example.odelExample
}
`,
		},

		// --------------------------------
		// Additional tests for coverage
		// --------------------------------

		{
			name:   "型キャスト・CallExpr の例",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model

func Convert(x interface{}) ModelExample {
	return ModelExample(x.(ModelExample))
}
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model

func Convert(x interface{}) example.ModelExample {
	return example.ModelExample(x.(example.ModelExample))
}
`,
		},
		{
			name:   "type switch での使用",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model

func Check(x interface{}) string {
	switch x.(type) {
	case ModelExample:
		return "model"
	default:
		return "other"
	}
}
`,
			oldFile: "model/model/example.go",
			expectMatch: `
package model

func Check(x interface{}) string {
	switch x.(type) {
	case example.ModelExample:
		return "model"
	default:
		return "other"
	}
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			cdir, err := os.Getwd()
			if err != nil {
				log.Fatal(err)
			}
			typesInfo, _ := mockTypesInfo(fs, node)

			// Transformerを作成
			transformer := NewTransformer(fs, cdir, tt.oldFile, tt.oldPkg, tt.newPkg, tt.deletePrefix)

			_, err = transformer.transformInOtherFile(node, typesInfo)
			assert.NoError(t, err)

			// AST 文字列比較
			expectedNode, _, err := parseGoSource(tt.expectMatch)
			assert.NoError(t, err)

			assert.Equal(t, expectedNode.Name.Name, node.Name.Name)

			nodeStr, err := nodeToString(node)
			assert.NoError(t, err)
			expectedStr, err := nodeToString(expectedNode)
			assert.NoError(t, err)
			assert.Equal(t, expectedStr, nodeStr)
		})
	}
}
