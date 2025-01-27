package pachanger

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

func parseGoSource(src string) (*ast.File, *token.FileSet, error) {
	fs := token.NewFileSet()
	node, err := parser.ParseFile(fs, "", src, parser.AllErrors)
	if err != nil {
		return nil, nil, err
	}
	return node, fs, nil
}
func mockTypesInfo(fs *token.FileSet, node *ast.File, exampleDefFile string) (*types.Info, map[string]string) {
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	typeFileMap := make(map[string]string)

	file := fs.AddFile(exampleDefFile, -1, 1000)
	examplePos := token.Pos(file.Base() + 1)

	examplePkg := types.NewPackage("example", "example")
	modelPkg := types.NewPackage("model", "model")

	emptyStruct := types.NewStruct(nil, nil)

	exampleObjType := types.NewTypeName(examplePos, examplePkg, "Example", nil)
	exampleNamedType := types.NewNamed(exampleObjType, emptyStruct, nil)
	exampleObjTypeWithType := types.NewTypeName(examplePos, examplePkg, "Example", exampleNamedType)

	modelObjType := types.NewTypeName(examplePos, modelPkg, "ModelExample", nil)
	modelNamedType := types.NewNamed(modelObjType, emptyStruct, nil)
	modelObjTypeWithType := types.NewTypeName(examplePos, modelPkg, "ModelExample", modelNamedType)

	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			pos := fs.Position(ident.Pos())
			obj := types.NewTypeName(ident.Pos(), modelPkg, ident.Name, nil)
			info.Defs[ident] = obj
			info.Uses[ident] = obj

			if ident.Name == "Example" {
				typeFileMap[ident.Name] = exampleDefFile
				info.Defs[ident] = exampleObjTypeWithType
				info.Uses[ident] = exampleObjTypeWithType
				fmt.Printf("Mocked type: %s, Pos: %v, File: %s\n", ident.Name, fs.Position(token.Pos(examplePos)), exampleDefFile)
			} else if ident.Name == "ModelExample" {
				typeFileMap[ident.Name] = "model/model_example.go"
				info.Defs[ident] = modelObjTypeWithType
				info.Uses[ident] = modelObjTypeWithType
				fmt.Printf("Mocked type: %s, Pos: %v, File: %s\n", ident.Name, fs.Position(token.Pos(examplePos)), "model/model_example.go")
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
		name        string
		input       string
		oldPkg      string
		newPkg      string
		expectMatch string
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
			name:   "Example は example.Example に変換され、ModelExample は model.ModelExample のまま（構造体の埋め込み）",
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
			name:   "Example は example.Example に変換されるが ModelExample は変換されない（関数の戻り値）",
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
			name:   "Example は example.Example に変換され、ModelExample は model.ModelExample のまま（メソッドの引数）",
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
			name:   "Example は example.Example に変換されるが ModelExample は変換されない（スライス）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examples []Example
var modelExamples []ModelExample
`,
			expectMatch: `
package example
var examples []example.Example
var modelExamples []model.ModelExample
`,
		},
		{
			name:   "Example は example.Example に変換されるが ModelExample は変換されない（ポインタ型）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var examplePtr *Example
var modelExamplePtr *ModelExample
`,
			expectMatch: `
package example
var examplePtr *example.Example
var modelExamplePtr *model.ModelExample
`,
		},
		{
			name:   "Example は example.Example に変換されるが ModelExample は変換されない（mapのキーと値）",
			oldPkg: "model",
			newPkg: "example",
			input: `
package model
var exampleMap map[Example]ModelExample
`,
			expectMatch: `
package example
var exampleMap map[example.Example]model.ModelExample
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			typesInfo, _ := mockTypesInfo(fs, node, "model/example/example.go")

			_, err = transformTargetAST(fs, node, tt.newPkg, "model/example.go", typesInfo)
			assert.NoError(t, err)

			expectedNode, _, err := parseGoSource(tt.expectMatch)
			assert.NoError(t, err)

			assert.Equal(t, expectedNode.Name.Name, node.Name.Name)
		})
	}
}

func TestTransformOtherFileAST(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		oldPkg      string
		newPkg      string
		exampleDef  string
		expectMatch string
	}{
		{
			name:       "Example 型を model から example に移動",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
type TestStruct struct {
	Example
}
`,
			expectMatch: `
package model
type TestStruct struct {
	example.Example
}
`,
		},
		{
			name:       "Example 型のポインタの修正",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var examplePtr *Example
`,
			expectMatch: `
package model
var examplePtr *example.Example
`,
		},
		{
			name:       "Example 型のスライスの修正",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var examples []Example
`,
			expectMatch: `
package model
var examples []example.Example
`,
		},
		{
			name:       "Example 型を含む map の修正",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var exampleMap map[string]Example
`,
			expectMatch: `
package model
var exampleMap map[string]example.Example
`,
		},
		{
			name:       "Example 型を map のキーに含むパターン",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var keyMap map[Example]string
`,
			expectMatch: `
package model
var keyMap map[example.Example]string
`,
		},
		{
			name:       "Example 型を map のキーと値に含むパターン",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var keyValueMap map[Example]Example
`,
			expectMatch: `
package model
var keyValueMap map[example.Example]example.Example
`,
		},
		{
			name:       "Example 型を含むチャネル",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var exampleChan chan Example
`,
			expectMatch: `
package model
var exampleChan chan example.Example
`,
		},
		{
			name:       "Example 型を含む送信専用チャネル",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var sendOnlyChan chan<- Example
`,
			expectMatch: `
package model
var sendOnlyChan chan<- example.Example
`,
		},
		{
			name:       "Example 型を含む受信専用チャネル",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var receiveOnlyChan <-chan Example
`,
			expectMatch: `
package model
var receiveOnlyChan <-chan example.Example
`,
		},
		{
			name:       "Example 型を map のキーに持つチャネル",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var mapChan map[Example]chan Example
`,
			expectMatch: `
package model
var mapChan map[example.Example]chan example.Example
`,
		},
		{
			name:       "Example 型のスライスを持つチャネル",
			oldPkg:     "model",
			newPkg:     "example",
			exampleDef: "model/testfile.go",
			input: `
package model
var sliceChan chan []Example
`,
			expectMatch: `
package model
var sliceChan chan []example.Example
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, fs, err := parseGoSource(tt.input)
			assert.NoError(t, err)

			typesInfo, _ := mockTypesInfo(fs, node, tt.exampleDef)

			modified, err := transformOtherFileAST(fs, node, tt.newPkg, "model/testfile.go", typesInfo)
			assert.NoError(t, err)
			assert.True(t, modified)

			expectedNode, _, err := parseGoSource(tt.expectMatch)
			assert.NoError(t, err)

			assert.Equal(t, expectedNode.Name.Name, node.Name.Name)
		})
	}
}
