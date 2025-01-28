package someother

import (
	"fmt"
)

// exampleパッケージにある定数を参照（example.MyConst, example.MyConst2など）
const (
	LocalConstInt    = changed_example.MyConst
	LocalConstString = changed_example.MyConst2
)

// AnotherStruct は exampleパッケージの型を多数フィールドとして埋め込む例
type AnotherStruct struct {
	Count  int
	Ex     changed_example.Example                             // 構造体
	Val    changed_example.MyInt                               // 別名型
	Box    changed_example.GenericBox[changed_example.Example] // ジェネリクス
	Nested struct {
		Key   string
		Value changed_example.MyInt
	}
}

// AnotherInterface は exampleパッケージの型を引数や戻り値に使うインターフェース
type AnotherInterface interface {
	Transform(e changed_example.Example) changed_example.MyInt
}

// AnotherAlias は example.MyInt の型エイリアス
type AnotherAlias = changed_example.MyInt

// いろいろなコレクション
var (
	SomeSlice []changed_example.Example
	SomeMap   map[changed_example.MyInt]changed_example.Example
	SomeChan  chan changed_example.Example
)

// init関数で定数や関数を参照
func init() {
	fmt.Println("someother package init, referencing example:", changed_example.MyConst2)
	changed_example.PopulateSlice() // example側の関数呼び出し
}

// UseModelStuff は exampleパッケージの様々な要素を利用する関数
func UseModelStuff() {
	// example.NewExample でインスタンスを作る
	ex := changed_example.NewExample(100)
	fmt.Println("ex:", ex.GetInfo())

	// さらに map, chanを使う例
	SomeMap = make(map[changed_example.MyInt]changed_example.Example)
	SomeMap[999] = ex

	SomeChan = make(chan changed_example.Example, 1)
	SomeChan <- ex

	// ジェネリクス
	box := changed_example.GenericBox[changed_example.MyInt]{Value: 321}
	box.Print()

	// type switchを使う関数を呼ぶ
	changed_example.CheckType(ex)
}

// AnotherCheck は独自の type switch で exampleパッケージの型を判定
func AnotherCheck(x interface{}) {
	switch val := x.(type) {
	case changed_example.Example:
		fmt.Println("Got example.Example with ID:", val.ID)
	case changed_example.MyInt:
		fmt.Println("Got example.MyInt:", val)
	default:
		fmt.Println("Got something else")
	}
}

// AnotherFunc は embedded struct のフィールドにアクセスする例
func AnotherFunc(as *AnotherStruct) {
	as.Ex.ID = 9999
	as.Box.Value = 8888
	fmt.Println("AnotherFunc updated as.Ex.ID and as.Box.Value")
}
