// Package blank has struct types with blank (_) fields, like sync.Mutex and
// the protobuf generated messages.
package blank

type Padded struct {
	_ int
	A string
	_ [0]func()
}

// Copy copies a Padded value, which copies every field, including the
// blank ones.
func Copy(s string) *Padded {
	var p Padded
	p.A = s
	q := p
	return &q
}
