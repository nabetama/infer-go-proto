// Package unsupported は、変換できない命令を含む関数のサンプル。
package unsupported

// Assert は型アサーション（この Task では変換しない命令）を含む。
func Assert(x any) string { return x.(string) }

func CallAssert(x any) string { return Assert(x) }

var g string

// ReadGlobal は package の変数を読む。命令は *ssa.UnOp、オペランドは *ssa.Global。
func ReadGlobal() string {
	x := g
	return x
}

// CallFunc は関数の値を呼ぶ（呼び出し先が静的に決まらない *ssa.Call）。
func CallFunc(f func() int) int { return f() }
