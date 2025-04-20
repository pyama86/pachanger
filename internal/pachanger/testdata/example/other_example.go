package example

import (
	"fmt"
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
	ID       MyInt
	Note     string
	example  OtherExample
	example2 OtherExample
	OtherExample
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
		ID   MyInt
		Name string
		E    Example
		S    SomeExample
	}

	some := SomeExample{
		ID: 1,
	}

	fmt.Println(some)

	type SameNameStruct struct {
		Example Example
	}

	var s SameNameStruct
	fmt.Println(s)
}
