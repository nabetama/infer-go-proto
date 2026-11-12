package translate

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"github.com/nabetama/infer-go-proto/internal/mangle"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

// Fixed runtime type names.
const (
	tyString = "go::string"
	tyIface  = "go::iface"
	tySlice  = "go::slice"
	tyOpaque = "go::opaque"
)

// typeConv converts Go types to Textual types and collects the type
// declarations that the converted types refer to.
type typeConv struct {
	decls map[string]sil.TypeDecl
	// seen holds the names whose declaration is built or being built, so
	// recursive types terminate.
	seen map[string]bool
	// goTypes holds, for each struct declaration, the Go types converted to
	// it that are not types.Identical to each other. The first one is the
	// type the declaration was built from.
	goTypes map[string][]types.Type
	// others holds the fields built from goTypes[name][1:], to be compared
	// with the declaration in Err.
	others map[string][][]sil.Field
}

func newTypeConv() *typeConv {
	return &typeConv{
		decls:   map[string]sil.TypeDecl{},
		seen:    map[string]bool{},
		goTypes: map[string][]types.Type{},
		others:  map[string][][]sil.Field{},
	}
}

// Err returns an error if two Go struct types were converted to the same
// Textual name but their Textual declarations differ: the field names or the
// Textual field types, in order (for example the packages "example.com/a.b"
// and "example.com/a_b" both declaring T with different fields). Only one
// declaration per name is emitted, so Infer would reject the fields of the
// other type. Types that are not types.Identical but have the same
// declaration (struct{ x int } written in two packages: the unexported field
// belongs to its package) share the declaration.
func (c *typeConv) Err() error {
	names := make([]string, 0, len(c.others))
	for n := range c.others {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for i, fields := range c.others[name] {
			if !reflect.DeepEqual(fields, c.decls[name].Fields) {
				return fmt.Errorf("types %s and %s both map to the Textual type %q", c.goTypes[name][0], c.goTypes[name][i+1], name)
			}
		}
	}
	return nil
}

// Type returns the Textual type of t and records the declarations it needs.
//
//	integer, bool          int
//	float                  float
//	string                 *go::string
//	named struct T         *<mangle.Type(T)>
//	other struct, pointer  pointer to the pointee type
//	interface              *go::iface
//	slice, array           *go::slice
//	map, chan, func        *go::opaque
//	tuple of N >= 2        *go::tuple<N>$<field types>$   (a 1-tuple is its element, a 0-tuple is void)
//
// The tuple name includes the field types ('*' spelled "ptr_", each followed
// by '$'), e.g. go::tuple2$int$ptr_go::string$, because Infer type-checks
// field stores and tuples of different element types need different
// declarations.
//
// Named types that are not structs are converted from their underlying type.
func (c *typeConv) Type(t types.Type) sil.Type {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		switch info := t.Info(); {
		case info&(types.IsInteger|types.IsBoolean) != 0:
			return "int"
		case info&types.IsFloat != 0:
			return "float"
		case info&types.IsString != 0:
			return c.runtime(tyString)
		}
		return c.runtime(tyOpaque)
	case *types.Named:
		if st, ok := t.Underlying().(*types.Struct); ok {
			return c.structType(mangle.Type(t), t, st)
		}
		return c.Type(t.Underlying())
	case *types.Struct:
		return c.structType(anonStructName(t), t, t)
	case *types.Pointer:
		elem := types.Unalias(t.Elem())
		if isStruct(elem) {
			return c.Type(elem)
		}
		return "*" + c.Type(elem)
	case *types.Interface:
		return c.runtime(tyIface)
	case *types.Slice, *types.Array:
		return c.runtime(tySlice)
	case *types.Tuple:
		switch t.Len() {
		case 0:
			return "void"
		case 1:
			return c.Type(t.At(0).Type())
		}
		fields := make([]sil.Field, t.Len())
		var name strings.Builder
		fmt.Fprintf(&name, "go::tuple%d$", t.Len())
		for i := range fields {
			ft := c.Type(t.At(i).Type())
			fields[i] = sil.Field{Name: fmt.Sprintf("r%d", i), Type: ft}
			s := string(ft)
			for strings.HasPrefix(s, "*") {
				name.WriteString("ptr_")
				s = s[1:]
			}
			name.WriteString(s + "$")
		}
		if !c.seen[name.String()] {
			c.seen[name.String()] = true
			c.decls[name.String()] = sil.TypeDecl{Name: name.String(), Fields: fields}
		}
		return sil.Type("*" + name.String())
	}
	// map, chan, func, type parameters and anything else
	return c.runtime(tyOpaque)
}

func isStruct(t types.Type) bool {
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

// runtime declares one of the fixed runtime types and returns a pointer to it.
func (c *typeConv) runtime(name string) sil.Type {
	if !c.seen[name] {
		c.seen[name] = true
		d := sil.TypeDecl{Name: name}
		if name == tySlice {
			d.Fields = []sil.Field{{Name: "elem", Type: c.runtime(tyOpaque)}}
		}
		c.decls[name] = d
	}
	return sil.Type("*" + name)
}

func (c *typeConv) structType(name string, t types.Type, st *types.Struct) sil.Type {
	for _, prev := range c.goTypes[name] {
		if types.Identical(prev, t) {
			return sil.Type("*" + name)
		}
	}
	// Record t before converting the fields, so that recursive types terminate.
	first := len(c.goTypes[name]) == 0
	c.goTypes[name] = append(c.goTypes[name], t)
	if first {
		c.seen[name] = true
	}
	fields := make([]sil.Field, st.NumFields())
	for i := range fields {
		fields[i] = sil.Field{Name: fieldName(st, i), Type: c.Type(st.Field(i).Type())}
	}
	if first {
		c.decls[name] = sil.TypeDecl{Name: name, Fields: fields}
	} else {
		// The declaration may still be being built (recursive types), so
		// the comparison is left to Err.
		c.others[name] = append(c.others[name], fields)
	}
	return sil.Type("*" + name)
}

// fieldName returns the Textual name of field i of st. Textual does not
// accept "_" as a field name, and a struct may have several blank fields, so
// a blank field is named blank$<i>. Go identifiers cannot contain '$', so
// the name does not collide with another field.
func fieldName(st *types.Struct, i int) string {
	if n := st.Field(i).Name(); n != "_" {
		return n
	}
	return fmt.Sprintf("blank$%d", i)
}

// anonStructName names an unnamed struct type after its Go spelling.
func anonStructName(t *types.Struct) string {
	var b strings.Builder
	for _, r := range types.TypeString(t, nil) {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "go::struct$" + b.String() + "$"
}

// Decls returns the declarations of all types referenced so far, sorted by name.
func (c *typeConv) Decls() []sil.TypeDecl {
	out := make([]sil.TypeDecl, 0, len(c.decls))
	for _, d := range c.decls {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
