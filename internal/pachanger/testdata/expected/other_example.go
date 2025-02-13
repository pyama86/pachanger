package example

import (
	"fmt"

	"github.com/pyama86/pachanger/internal/pachanger/testdata/changed_example"
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
type SomeExample struct {
	ID      changed_example.MyInt
	Note    string
	example changed_example.OtherExample
	changed_example.OtherExample
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
