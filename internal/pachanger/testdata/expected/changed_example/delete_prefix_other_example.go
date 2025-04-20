package changed_example

import (
	"fmt"

	"github.com/pyama86/pachanger/internal/pachanger/testdata/example"
)

// 追加のジェネリクス型（ネストで利用される）
type AnotherGenericBox[T any] struct {
	Value T
	Box   GenericBox[T]
}

func (a AnotherGenericBox[T]) Summarize() {
	fmt.Println("AnotherGenericBox Value =", a.Value)
	a.Box.Print()
}

// SomeExample 構造体
type Example struct {
	ID       example.MyInt
	Note     string
	example  example.OtherExample
	example2 example.OtherExample
	example.OtherExample
}

// iotaで列挙的定数
const (
	KindFoo = iota
	KindBar
	KindBaz
)

// 列挙チェック
func CheckKind(k int) {
	switch k {
	case KindFoo:
		fmt.Println("KindFoo")
	case KindBar:
		fmt.Println("KindBar")
	case KindBaz:
		fmt.Println("KindBaz")
	default:
		fmt.Println("UnknownKind")
	}
}

// 既存ジェネリクス
type GenericBox[T any] struct {
	Value T
}

func (g GenericBox[T]) Print() {
	fmt.Printf("GenericBox: %+v\n", g.Value)
}

// 関数内で同じ名前の構造体を定義したケース
func SameNameStruct() {
	type Example struct {
		ID   example.MyInt
		Name string
		E    example.Example
		S    Example
	}

	some := Example{
		ID: 1,
	}

	fmt.Println(some)

	type SameNameStruct struct {
		Example example.Example
	}

	var s SameNameStruct
	fmt.Println(s)
}
