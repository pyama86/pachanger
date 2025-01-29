package changed_example

import (
	"github.com/pyama86/pachanger/internal/pachanger/testdata/example"
)

type ChangedExample struct {
	example   Example
	SubStruct struct {
		InnerData   Example
		ExampleData SomeExample // 参照
	}
	example.Example
}

// コンストラクタ的関数
func NewChangedExample(id MyInt, note string) Example {
	return Example{
		ID: id,
		ExampleData: SomeExample{
			ID:   id,
			Note: note,
		},
	}
}

func SomeFunc() {
	a := Example{}
}
