// Package taint は、可変長引数・インタフェース・型の変換を通る taint の流れを確かめるためのサンプル。
package taint

import "fmt"

// Source は .inferconfig で戻り値を入力元にする。
func Source() string { return "" }

// Sink は .inferconfig で引数を危険な関数の引数にする。
func Sink(q string) {}

type Q struct{}

func (q *Q) Where(query string, args ...any) *Q { return q }

// ViaSprintf は報告される。
func ViaSprintf() { Sink(fmt.Sprintf("id = %s", Source())) }

// ViaConcat は報告される。
func ViaConcat() { Sink("id = " + Source()) }

// ViaIface は型アサーションを含むので変換されず、警告が出る。
func ViaIface() {
	var x any = Source()
	Sink(x.(string))
}

// ArgsOnly は、Where の query だけを危険な引数にしたときは報告されない。
func ArgsOnly(q *Q) { q.Where("id = ?", Source()) }

// QueryTainted は報告される。
func QueryTainted(q *Q) { q.Where("id = " + Source()) }

type Safe string

// Conv は、Safe への変換を危険な関数にしたときに報告される。
func Conv() { Sink(string(Safe(Source()))) }
