package structs

type User struct {
	Name string
	next *User
	Tags []string
}
