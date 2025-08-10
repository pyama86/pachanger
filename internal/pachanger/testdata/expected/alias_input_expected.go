package example

import (
	"github.com/pyama86/pachanger/internal/pachanger/testdata/output/changed_example"
)

// TestAliasAccess はエイリアスでアクセスするテスト
func TestAliasAccess() changed_example.SomeExample {
	return changed_example.SomeExample{
		ID:   1,
		Note: "test",
	}
}
