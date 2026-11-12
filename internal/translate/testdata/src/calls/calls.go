// Package calls は、対象外のパッケージの関数の呼び出しと、制御フローのサンプル。
package calls

import "strings"

func Upper(s string) string { return strings.ToUpper(s) }

func Len(b *strings.Builder) int { return b.Len() }

// Max の if の後のブロックには、2 つの経路から来る。
func Max(a, b int) int {
	m := a
	if b > a {
		m = b
	}
	return m
}

func Sum(n int) int {
	s := 0
	for i := 0; i < n; i++ {
		s = s + i*2 - 1
	}
	return s
}

// Apply は関数リテラルをその場で呼ぶ。
func Apply() int { return func(x int) int { return x + 1 }(1) }

func Ratio(x float64) float64 { return x/2 + x*x - 1 }

type Inner struct{ S string }

type Outer struct {
	In Inner
	K  int
}

// Copy は入れ子の struct の値をコピーし、フィールドのアドレスを取る。
func Copy(o Outer) string {
	o2 := o
	p := &o2.In
	return p.S
}
