package example

// ジェネリクス型のテスト
type GenericType[T any] struct {
	Value T
}

// 型パラメータに複数の型を持つケース
type MultiGeneric[K, V any] map[K]V

// 配列/スライスのインデックスとして使用される識別子
func UseArrayIndex() {
	var arr [10]int
	var example int = 5

	// この例では、example はパッケージ名ではなくローカル変数
	// example[example] は変換されるべきではない
	var val = arr[example]
}

// 別のパッケージの型を使用するジェネリクス
type ExampleGeneric[T any] struct {
	Value T
}

// ジェネリクスを使用する関数
func UseGenericTypes() {
	// ジェネリクス型の使用
	var g GenericType[MyInt]
	var m MultiGeneric[string, MyInt]

	// 配列のインデックスとしてパッケージ名と同じ変数を使用
	var exampleArr [5]int
	var example int = 2
	_ = exampleArr[example] // ここでは example はローカル変数
}

// 型パラメータ内でパッケージ名を使用
func UseGenericWithPackage[T any]() {
	var g GenericType[OtherExample]
}
