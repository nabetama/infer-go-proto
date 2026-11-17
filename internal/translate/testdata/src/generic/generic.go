// Package generic は、対象のパッケージの中のジェネリクスの関数と型の呼び出しを確かめるためのサンプル。
package generic

import "cmp"

// Source は .inferconfig で戻り値を入力元にする。
func Source() string { return "" }

// Sink は .inferconfig で引数を危険な関数の引数にする。
func Sink(q string) {}

// F は引数をそのまま返す。
func F[T any](v T) T { return v }

func UseInt() int { return F(1) }

// ViaGeneric は報告される。
func ViaGeneric() { Sink(F(Source())) }

// ViaGenericClean は報告されない。
func ViaGenericClean() { Sink(F("id = ?")) }

// Box はジェネリクスの型。
type Box[T any] struct{ v T }

func (b *Box[T]) Get() T { return b.v }

// ViaBox は報告される。
func ViaBox() {
	b := &Box[string]{v: Source()}
	Sink(b.Get())
}

// Wrap は関数の中で型を宣言する。
func Wrap[T any](v T) T {
	type cell struct{ v T }
	c := &cell{v: v}
	return c.v
}

func UseWrapInt() int { return Wrap(1) }

func UseWrapString() string { return Wrap("") }

// UseCmp は、対象でないパッケージのジェネリクスの関数を呼ぶ。
func UseCmp(a, b int) int { return cmp.Compare(a, b) }
