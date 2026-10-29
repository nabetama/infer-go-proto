// Package unsupported は、変換できない命令を含む関数のサンプル。
package unsupported

// Assert は型アサーション（この Task では変換しない命令）を含む。
func Assert(x any) string { return x.(string) }

func CallAssert(x any) string { return Assert(x) }
