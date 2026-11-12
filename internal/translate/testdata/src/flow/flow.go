// Package flow は、関数本体の基本的な命令の変換を確かめるためのサンプル。
package flow

type T struct{ s string }

func Pick(t *T, b bool) (string, error) {
	if b {
		return t.s, nil
	}
	return "", nil
}

func Use(t *T) string {
	s, _ := Pick(t, true)
	return s
}

type V struct{ n int }

// Get は値レシーバのメソッド。go/ssa は (*V).Get のラッパーも作る。
func (v V) Get() int { return v.n }

func CallGet(p *V) int { return p.Get() }
