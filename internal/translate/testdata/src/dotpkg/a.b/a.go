// Package ab has the same mangled path as ../a_b.
package ab

type T struct{ ID int }

func UseDot(t *T) int { return t.ID }
