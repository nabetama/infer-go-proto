// Package an1 は、../an2 と同じ書き方の無名の struct（小文字のフィールドを持つもの）を使う。
package an1

func A() int {
	p := &struct{ x int }{x: 1}
	return p.x
}
