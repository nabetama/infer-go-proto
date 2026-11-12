// Package ab has the same mangled path as ../a.b.
package ab

type T struct{ Name string }

func UseUnderscore(t *T) string { return t.Name }
