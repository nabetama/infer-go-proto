// Package localtypes declares types of the same name inside different
// functions.
package localtypes

func F(id int) int {
	type row struct{ ID int }
	r := &row{ID: id}
	return r.ID
}

func G(name string) string {
	type row struct{ Name string }
	r := &row{Name: name}
	return r.Name
}
