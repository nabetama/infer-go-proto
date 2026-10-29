// Package mangle converts Go names into Textual identifiers.
//
// Every procedure is a single top-level identifier containing "::" so that it
// cannot collide with a C library function name. Allowed characters are
// letters, digits, '_', '$' and "::".
package mangle

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

var pkgReplacer = strings.NewReplacer("/", "::", ".", "_", "-", "_")

// Pkg converts a package path: "/" becomes "::", "." and "-" become "_".
func Pkg(path string) string { return pkgReplacer.Replace(path) }

// RT returns the name of a runtime helper, go::rt::<name>.
func RT(name string) string { return "go::rt::" + name }

// Type returns the identifier of a named type (a pointer is stripped first).
// A generic instance becomes Pkg::Name$<arg1>$<arg2>$ : each type argument is
// followed by '$'. Basic type arguments use the Go name. Type panics for
// types that are not named, pointer-to-named or basic.
func Type(t types.Type) string {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	switch t := t.(type) {
	case *types.Named:
		return named(t.Obj(), t.TypeArgs())
	case *types.Basic:
		return t.Name()
	}
	panic(fmt.Sprintf("mangle.Type: unsupported type %s (%T)", t, t))
}

func named(obj *types.TypeName, args *types.TypeList) string {
	name := obj.Name()
	if obj.Pkg() != nil {
		name = Pkg(obj.Pkg().Path()) + "::" + name
	}
	return name + typeArgs(args)
}

func typeArgs(args *types.TypeList) string {
	if args == nil || args.Len() == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < args.Len(); i++ {
		b.WriteString("$")
		b.WriteString(typeArg(args.At(i)))
	}
	b.WriteString("$")
	return b.String()
}

// typeArg mangles one type argument. Types other than named, pointer-to-named
// and basic fall back to their Go spelling with non-identifier characters
// replaced by '_'.
func typeArg(t types.Type) string {
	u := types.Unalias(t)
	if p, ok := u.(*types.Pointer); ok {
		u = types.Unalias(p.Elem())
	}
	switch u.(type) {
	case *types.Named, *types.Basic:
		return Type(u)
	}
	var b strings.Builder
	for _, r := range types.TypeString(t, nil) {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Func returns the single top-level identifier of fn.
//
//	package function:   Pkg::Name            (generic instance: Pkg::Name$arg$)
//	method:             Type(receiver)$Name  (pointer receiver is stripped)
//	function literal:   <parent name>$N      (go/ssa names it <parent>$N)
//
// go/ssa synthetic functions would collide with the original name (for
// example the (*T).M wrapper of a value-receiver method T.M), so a suffix is
// appended: "$wrapper" for wrappers, "$bound" for bound method values and
// "$thunk" for method expression thunks.
func Func(fn *ssa.Function) string {
	if fn.Parent() != nil {
		// go/ssa names literals "<parent>$1", "<parent>$1$1", ...
		return Func(fn.Parent()) + strings.TrimPrefix(fn.Name(), fn.Parent().Name())
	}
	base := funcBase(fn)
	switch {
	case strings.HasPrefix(fn.Synthetic, "wrapper for"):
		return base + "$wrapper"
	case strings.HasPrefix(fn.Synthetic, "bound method wrapper"):
		return base + "$bound"
	case strings.HasPrefix(fn.Synthetic, "thunk for"):
		return base + "$thunk"
	}
	return base
}

func funcBase(fn *ssa.Function) string {
	if recv := fn.Signature.Recv(); recv != nil {
		return Type(recv.Type()) + "$" + fn.Name()
	}
	name := fn.Name()
	if strings.HasPrefix(fn.Synthetic, "instance of") {
		// go/ssa names an instance "Id[int]"; use the origin's name and the
		// type arguments instead.
		if o := fn.Origin(); o != nil {
			name = o.Name() + typeArgList(fn.TypeArgs())
		}
	}
	pkg := fn.Pkg
	if pkg == nil && fn.Object() != nil && fn.Object().Pkg() != nil {
		return Pkg(fn.Object().Pkg().Path()) + "::" + name
	}
	if pkg == nil {
		panic(fmt.Sprintf("mangle.Func: %s has no package", fn))
	}
	return Pkg(pkg.Pkg.Path()) + "::" + name
}

// Table remembers which Go name produced each Textual name.
type Table struct {
	bySil map[string]string
}

// Add records goName -> silName. It returns an error if a different Go name
// already maps to silName, or if silName has no "::" (names starting with
// "__sil_" are Infer builtins and are exempt).
func (t *Table) Add(goName, silName string) error {
	if !strings.Contains(silName, "::") && !strings.HasPrefix(silName, "__sil_") {
		return fmt.Errorf("mangle: %q (from %q) has no \"::\"; it could collide with a C library function", silName, goName)
	}
	if prev, ok := t.bySil[silName]; ok && prev != goName {
		return fmt.Errorf("mangle: %q and %q both map to %q", prev, goName, silName)
	}
	if t.bySil == nil {
		t.bySil = map[string]string{}
	}
	t.bySil[silName] = goName
	return nil
}

func typeArgList(args []types.Type) string {
	if len(args) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString("$" + typeArg(a))
	}
	return b.String() + "$"
}
