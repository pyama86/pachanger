package example

import (
	some_example "github.com/pyama86/pachanger/internal/pachanger/testdata/example"
)

// TestAliasAccess はエイリアスでアクセスするテスト
func TestAliasAccess() some_example.SomeExample {
	return some_example.SomeExample{
		ID:   1,
		Note: "test",
	}
}
