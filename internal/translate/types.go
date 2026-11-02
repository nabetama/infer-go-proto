package translate

import (
	"fmt"
	"go/types"
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
}

func newTypeConv() *typeConv {
	return &typeConv{decls: map[string]sil.TypeDecl{}, seen: map[string]bool{}}
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
			return c.structType(mangle.Type(t), st)
		}
		return c.Type(t.Underlying())
	case *types.Struct:
		return c.structType(anonStructName(t), t)
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

func (c *typeConv) structType(name string, st *types.Struct) sil.Type {
	if !c.seen[name] {
		c.seen[name] = true // before the fields, so that recursive types terminate
		fields := make([]sil.Field, st.NumFields())
		for i := range fields {
			f := st.Field(i)
			fields[i] = sil.Field{Name: fieldName(st, i), Type: c.Type(f.Type())}
		}
		c.decls[name] = sil.TypeDecl{Name: name, Fields: fields}
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
