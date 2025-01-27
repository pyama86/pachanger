package pachanger

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

func nodeToString(node *ast.File) (string, error) {
	var buf bytes.Buffer
	fs := token.NewFileSet()
	if err := printer.Fprint(&buf, fs, node); err != nil {
		return "", err
	}
	return buf.String(), nil
}
func parseGoSource(src string) (*ast.File, *token.FileSet, error) {
	fs := token.NewFileSet()
	node, err := parser.ParseFile(fs, "", src, parser.AllErrors)
	if err != nil {
		return nil, nil, err
	}
	return node, fs, nil
}
func mockTypesInfo(fs *token.FileSet, node *ast.File) (*types.Info, map[string]string) {
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	typeFileMap := make(map[string]string)

	exampleFile := fs.AddFile("model/example.go", -1, 1000)
	examplePos := token.Pos(exampleFile.Base() + 1)

	modelExampleFile := fs.AddFile("model/model/example.go", -1, 1000)
	modelExamplePos := token.Pos(modelExampleFile.Base() + 1)

	examplePkg := types.NewPackage("model", "model")
	modelPkg := types.NewPackage("model", "model")

	emptyStruct := types.NewStruct(nil, nil)

	exampleObjType := types.NewTypeName(examplePos, examplePkg, "Example", nil)
	exampleNamedType := types.NewNamed(exampleObjType, emptyStruct, nil)
	exampleObjTypeWithType := types.NewTypeName(examplePos, examplePkg, "Example", exampleNamedType)

	modelObjType := types.NewTypeName(modelExamplePos, modelPkg, "ModelExample", nil)
	modelNamedType := types.NewNamed(modelObjType, emptyStruct, nil)
	modelObjTypeWithType := types.NewTypeName(modelExamplePos, modelPkg, "ModelExample", modelNamedType)

	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			pos := fs.Position(ident.Pos())
			obj := types.NewTypeName(ident.Pos(), modelPkg, ident.Name, nil)
			info.Defs[ident] = obj
			info.Uses[ident] = obj

			if ident.Name == "Example" {
				typeFileMap[ident.Name] = "model/example.go"
				info.Defs[ident] = exampleObjTypeWithType
				info.Uses[ident] = exampleObjTypeWithType
			} else if ident.Name == "ModelExample" {
				typeFileMap[ident.Name] = "model/model/example.go"
				info.Defs[ident] = modelObjTypeWithType
				info.Uses[ident] = modelObjTypeWithType
			} else if pos.IsValid() {
				typeFileMap[ident.Name] = pos.Filename
			}
		}
		return true
	})

	return info, typeFileMap
}

func TestTransformTargetAST(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		oldPkg       string
		newPkg       string
		deletePrefix string
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
			expectMatch: `
package example
type Example struct {}
`,
		},
		{
			name:   "ModelExampleは mode.ModelExample に変換され、Example は model.Example のまま（構造体の埋め込み）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
type TestStruct struct {
	Example
	ModelExample
}
`,
			expectMatch: `
package example
type TestStruct struct {
	Example
	model.ModelExample
}
`,
		},
		{
			name:   "ModelExampleは mode.ModelExample に変換されるが ModelExample は変換されない（関数の戻り値）",
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
			name:   "ModelExampleは mode.ModelExample に変換され、ModelExample は model.ModelExample のまま（メソッドの引数）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
func (s TestStruct) SetExample(e Example) {
}
func (s TestStruct) SetModelExample(me ModelExample) {
}
`,
			expectMatch: `
package example
func (s TestStruct) SetExample(e Example) {
}
func (s TestStruct) SetModelExample(me model.ModelExample) {
}
`,
		},
		{
			name:   "ModelExample は model.Example に変換されるが Example は変換されない（スライス）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examples []Example
var modelExamples []ModelExample
`,
			expectMatch: `
package example
var examples []Example
var modelExamples []model.ModelExample
`,
		},
		{
			name:   "ModelExample は model.Example に変換されるが Example は変換されない（ポインタ型）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examplePtr *Example
var modelExamplePtr *ModelExample
`,
			expectMatch: `
package example
var examplePtr *Example
var modelExamplePtr *model.ModelExample
`,
		},
		{
			name:   "ModelExample は model.ModelExample に変換されるが Example は変換されない（mapのキーと値）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleMap map[Example]ModelExample
`,
			expectMatch: `
package example
var exampleMap map[Example]model.ModelExample
`,
		},

		{
			name:         "Example 型のパッケージ移動とプレフィックス削除",
			oldPkg:       "model",
			newPkg:       "example",
			deletePrefix: "E",
			input: `
package model
type Sample struct {
	modelExample Example
}
`,
			expectMatch: `
package example
type Sample struct {
	modelExample xample
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			typesInfo, _ := mockTypesInfo(fs, node)

			_, err = transformTargetAST(fs, node, tt.newPkg, "model/example.go", tt.deletePrefix, typesInfo)
			assert.NoError(t, err)

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

func TestTransformOtherFileAST(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		oldPkg       string
		newPkg       string
		deletePrefix string
		expectMatch  string
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
			expectMatch: `
package model
type TestStruct struct {
	example.ModelExample
}
`,
		},
		{
			name:   "ModelExample 型のポインタの修正",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examplePtr *ModelExample
`,
			expectMatch: `
package model
var examplePtr *example.ModelExample
`,
		},
		{
			name:   "ModelExample 型のスライスの修正",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examples []ModelExample
`,
			expectMatch: `
package model
var examples []example.ModelExample
`,
		},
		{
			name:   "ModelExample 型を含む map の修正",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleMap map[string]ModelExample
`,
			expectMatch: `
package model
var exampleMap map[string]example.ModelExample
`,
		},
		{
			name:   "ModelExample 型を map のキーに含むパターン",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var keyMap map[ModelExample]string
`,
			expectMatch: `
package model
var keyMap map[example.ModelExample]string
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
			expectMatch: `
package model
var keyValueMap map[example.ModelExample]example.ModelExample
`,
		},
		{
			name:   "ModelExample 型を含むチャネル",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleChan chan ModelExample
`,
			expectMatch: `
package model
var exampleChan chan example.ModelExample
`,
		},
		{
			name:   "ModelExample 型を含む送信専用チャネル",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var sendOnlyChan chan<- ModelExample
`,
			expectMatch: `
package model
var sendOnlyChan chan<- example.ModelExample
`,
		},
		{
			name:   "Example 型を含む受信専用チャネル",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var receiveOnlyChan <-chan ModelExample
`,
			expectMatch: `
package model
var receiveOnlyChan <-chan example.ModelExample
`,
		},
		{
			name:   "Example 型を map のキーに持つチャネル",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var mapChan map[ModelExample]chan ModelExample
`,
			expectMatch: `
package model
var mapChan map[example.ModelExample]chan example.ModelExample
`,
		},
		{
			name:   "Example 型のスライスを持つチャネル",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var sliceChan chan []ModelExample
`,
			expectMatch: `
package model
var sliceChan chan []example.ModelExample
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
			expectMatch: `
package model
type TestStruct struct {
	example.odelExample
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			typesInfo, _ := mockTypesInfo(fs, node)

			modified, err := transformOtherFileAST(fs, node, "model/model/example.go", tt.oldPkg, tt.newPkg, tt.deletePrefix, typesInfo)
			assert.NoError(t, err)
			assert.True(t, modified)

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
