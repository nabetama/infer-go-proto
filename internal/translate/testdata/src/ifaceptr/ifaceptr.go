// Package ifaceptr converts pointers to non-struct values (*string, *[]string)
// to interfaces, like the destination of bun's Scan(ctx, &name).
package ifaceptr

// Source は .inferconfig で戻り値を入力元にする。
func Source() string { return "" }

// SinkAny は .inferconfig で引数を危険な関数の引数にする。
func SinkAny(x any) {}

// StringPtr は *string をインタフェースにする。
func StringPtr() {
	s := Source()
	SinkAny(&s)
}

// SlicePtr は *[]string をインタフェースにする。
func SlicePtr() {
	var names []string
	SinkAny(&names)
}

// Value は string をインタフェースにする。報告される。
func Value() { SinkAny(Source()) }
