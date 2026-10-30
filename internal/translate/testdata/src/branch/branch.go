// Package branch は、If の向きと複数の戻り値の要素を Pulse で確かめるためのサンプル。
package branch

func Source() string { return "" }
func Sink(s string)  {}

func Pick(b bool, s string) (string, error) {
	if b {
		return s, nil
	}
	return "", nil
}

func Second(s string) (string, string) { return "", s }

// PickTrue は報告される。
func PickTrue() {
	s, _ := Pick(true, Source())
	Sink(s)
}

// PickFalse は、入力元の値が b が真の経路でしか返らないので報告されない。
func PickFalse() {
	s, _ := Pick(false, Source())
	Sink(s)
}

// SecondTainted は報告される。
func SecondTainted() {
	_, b := Second(Source())
	Sink(b)
}

// FirstClean は、入力元の値が 2 番目の戻り値にしか入らないので報告されない。
func FirstClean() {
	a, _ := Second(Source())
	Sink(a)
}
