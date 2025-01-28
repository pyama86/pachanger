package changed_example

import (
	"github.com/pyama86/pachanger/internal/pachanger/testdata/example"
)

type ChangedExample struct {
	example   example.Example
	SubStruct struct {
		InnerData   example.Example
		ExampleData SomeExample // 参照
	}
}

// コンストラクタ的関数
func NewChangedExample(id MyInt, note string) example.Example {
	return example.Example{
		ID: id,
		ExampleData: SomeExample{
			ID:   id,
			Note: note,
		},
	}
}
