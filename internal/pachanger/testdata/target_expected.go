package changed_example

import (
	"fmt"
)

// 別名型
type MyInt int
type Alias = MyInt

var (
	IntSlice    []MyInt
	StringMap   map[string]MyInt
	ChanOfInt   chan MyInt
	ReceiveOnly <-chan MyInt
)

type Example struct {
	ID           MyInt
	Data         *Example
	ExampleData  example.SomeExample   // 参照: some_example.go
	exampleSlice []example.SomeExample // 参照: some_example.go
	exampleMap   map[string][]example.SomeExample
}

type ComplexStruct struct {
	Example
	Name      string
	SubStruct struct {
		InnerID     int
		InnerData   Example
		ExampleData example.SomeExample // 参照
	}
}

type GreatStruct struct {
	ComplexStruct
	AliasValue          Alias
	example.SomeExample // 埋め込み (from some_example.go)
}

type Info interface {
	GetInfo() string
	GetExample() example.SomeExample
}

// Example が Info を実装
func (e Example) GetInfo() string {
	return fmt.Sprintf("Example(ID=%d, Note=%s)", e.ID, e.ExampleData.Note)
}
func (e Example) GetExample() example.SomeExample {
	return e.ExampleData
}

// コンストラクタ的関数
func NewExample(id MyInt, note string) Example {
	return Example{
		ID: id,
		ExampleData: example.SomeExample{
			ID:   id,
			Note: note,
		},
	}
}

// ネストしたジェネリクスを使う
func UseAnotherBox[T any](val T) {
	a := example.AnotherGenericBox[T]{
		Value: val,
		Box:   example.GenericBox[T]{Value: val},
	}
	a.Summarize()
}

func CheckType(x interface{}) {
	switch v := x.(type) {
	case Example:
		fmt.Println("Example:", v.ID, v.ExampleData.Note)
	case MyInt:
		fmt.Println("MyInt:", v)
	case example.SomeExample:
		fmt.Println("SomeExample ID:", v.ID, "Note:", v.Note)
	default:
		fmt.Println("Unknown")
	}
}

func DoEmbeddingTest() {
	var c ComplexStruct
	c.ID = 10
	c.Example.Data = &Example{ID: 999}
	c.Name = "complex"
	c.SubStruct.InnerID = 50
	c.SubStruct.InnerData.ID = 888
	c.SubStruct.ExampleData = example.SomeExample{ID: 101, Note: "SubStruct data"}

	info := c.GetInfo()
	fmt.Println("DoEmbeddingTest info:", info)

	var g GreatStruct
	g.ID = 77
	g.Name = "great"
	g.AliasValue = 123
	g.SomeExample = example.SomeExample{ID: 777, Note: "Embedded example"}
	fmt.Println("GreatStruct alias:", g.AliasValue, ", Embedded Note=", g.SomeExample.Note)
}

// 追加: スライス操作
func PopulateSlice() {
	IntSlice = append(IntSlice, 10, 20, 30)
}

// 追加: チャンネル
func SendToChan(ch chan<- MyInt, val MyInt) {
	ch <- val
}

// Closure で Example を使用
func UseClosure(e Example) {
	fn := func() {
		fmt.Println("Closure: ID=", e.ID, ", Note=", e.ExampleData.Note)
	}
	fn()
}

// init
func init() {
	fmt.Println("Initializing from model package in example!")
}

// UseEnumTest は iota定数 KindFoo, KindBar, KindBaz を使ったコード例
func UseEnumTest(k int) {
	switch k {
	case example.KindFoo:
		fmt.Println("Enum is KindFoo")
	case example.KindBar:
		fmt.Println("Enum is KindBar")
	case example.KindBaz:
		fmt.Println("Enum is KindBaz")
	default:
		fmt.Println("Enum unknown")
	}
}

// Another usage: variables
var MyKind = example.KindFoo

func PrintMyKind() {
	if MyKind == example.KindFoo {
		fmt.Println("MyKind is KindFoo")
	}
}

// 既存ジェネリクス
type WillChangeGenericBox[T any] struct {
	Value T
}

func (g WillChangeGenericBox[T]) Print() {
	fmt.Printf("WillChangeGenericBox: %+v\n", g.Value)
}
