package example

import (
	some_example "github.com/pyama86/pachanger/internal/pachanger/testdata/example"
	"github.com/pyama86/pachanger/internal/pachanger/testdata/output/changed_example"
)

// TestAliasAccess はエイリアスで移動しないシンボルへアクセスするテスト
func TestAliasAccess() some_example.SomeExample {
	return some_example.SomeExample{
		ID:   1,
		Note: "test",
	}
}

// TestAliasTargetAccess はエイリアスで移動するシンボルへアクセスするテスト
func TestAliasTargetAccess() changed_example.Example {
	return changed_example.NewExample(1, "test")
}
