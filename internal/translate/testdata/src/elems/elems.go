// Package elems は、配列・slice の要素、インタフェースのメソッド呼び出し、型の変換を確かめるためのサンプル。
package elems

import "fmt"

func Source() string { return "" }
func Sink(s string)  {}

// ArrayToSlice は報告される。配列から作った slice は配列と同じ要素を指す。
func ArrayToSlice() {
	var a [2]string
	a[1] = Source()
	s := a[:]
	Sink(s[0])
}

// SliceParam は報告される。slice の要素は 1 つのフィールドにまとめるので、添字は区別しない。
func SliceParam(xs []string) {
	xs[0] = Source()
	Sink(xs[1])
}

// ArrayClean は報告されない。
func ArrayClean() {
	var a [2]string
	a[0] = "x"
	Sink(a[1])
}

type Getter interface{ Get(k string) string }

// ViaInvoke は報告される。インタフェースのメソッドは本体のない宣言として呼ぶ。
func ViaInvoke(g Getter) { Sink(g.Get(Source())) }

type ID int

// Num は名前付きの型への変換。
func Num(n int) ID { return ID(n) }

// Unnamed は名前のない型への変換なので、値をそのまま使う。
func Unnamed(s ID) int { return int(s) }

// Bytes は Convert（string から []byte）。
func Bytes(s string) []byte { return []byte(s) }

// SprintfNoArgs は、可変長引数のない fmt.Sprintf（args は nil）。何も報告されない。
func SprintfNoArgs() { Sink(fmt.Sprintf("x")) }

// ToFloat は int から float64 への変換。Textual では __sil_cast にする。
func ToFloat(n int) float64 {
	x := float64(n)
	return x * 2
}
