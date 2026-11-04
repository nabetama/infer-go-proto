// Package mangle converts Go names into Textual identifiers.
//
// Every procedure is a single top-level identifier containing "::" so that it
// cannot collide with a C library function name. Allowed characters are
// letters, digits, '_', '$' and "::".
package mangle

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/ssa"
)

var pkgReplacer = strings.NewReplacer("/", "::", ".", "_", "-", "_")

// Pkg converts a package path: "/" becomes "::", "." and "-" become "_".
func Pkg(path string) string { return pkgReplacer.Replace(path) }

// RT returns the name of a runtime helper, go::rt::<name>.
func RT(name string) string { return "go::rt::" + name }

// Type returns the identifier of a named type (a pointer is stripped first).
// A generic instance becomes Pkg::Name$<arg1>$<arg2>$ : each type argument is
// followed by '$'. Basic type arguments use the Go name, and a pointer type
// argument is "ptr_" followed by its element. A type declared inside a
// function gets the suffix described at localSuffix. Type panics for
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
		name = Pkg(obj.Pkg().Path()) + "::" + name + localSuffix(obj)
	}
	return name + typeArgs(args)
}

var localSuffixes sync.Map // *types.TypeName -> string

// localSuffix returns "" for a package-level type, and for a type that is
// not in any scope (types built by hand). For a type declared inside a
// function it returns "$<function>", where <function> is the name
// of the enclosing top-level function, or "<receiver type>$<method>" for a
// method. When the function declares several types of the same name (in
// different blocks or function literals), "$<n>" is appended, n being the
// 1-based position of the declaration in source order. If the enclosing
// function cannot be found, the suffix is "$local". Translate reports an error when two different types
// end up with the same name.
func localSuffix(obj *types.TypeName) string {
	pkg := obj.Pkg()
	if obj.Parent() == nil || obj.Parent() == pkg.Scope() {
		return ""
	}
	if s, ok := localSuffixes.Load(obj); ok {
		return s.(string)
	}
	s := computeLocalSuffix(obj)
	localSuffixes.Store(obj, s)
	return s
}

func computeLocalSuffix(obj *types.TypeName) string {
	pkg := obj.Pkg()
	// The function scope is the child of a file scope, whose parent is the
	// package scope.
	fs := obj.Parent()
	for fs != nil && (fs.Parent() == nil || fs.Parent().Parent() != pkg.Scope()) {
		fs = fs.Parent()
	}
	fnName := ""
	if fs != nil {
		fnName = enclosingFunc(pkg, fs)
	}
	if fnName == "" {
		return "$local"
	}
	var same []*types.TypeName
	var walk func(s *types.Scope)
	walk = func(s *types.Scope) {
		if tn, ok := s.Lookup(obj.Name()).(*types.TypeName); ok {
			same = append(same, tn)
		}
		for i := 0; i < s.NumChildren(); i++ {
			walk(s.Child(i))
		}
	}
	walk(fs)
	if len(same) < 2 {
		return "$" + fnName
	}
	sort.Slice(same, func(i, j int) bool { return same[i].Pos() < same[j].Pos() })
	for i, tn := range same {
		if tn == obj {
			return fmt.Sprintf("$%s$%d", fnName, i+1)
		}
	}
	return "$" + fnName
}

// enclosingFunc returns the name of the package-level function or method
// whose scope is fs: "<name>" or "<receiver type>$<method>".
func enclosingFunc(pkg *types.Package, fs *types.Scope) string {
	sc := pkg.Scope()
	for _, n := range sc.Names() {
		switch o := sc.Lookup(n).(type) {
		case *types.Func:
			if o.Scope() == fs {
				return o.Name()
			}
		case *types.TypeName:
			nt, ok := o.Type().(*types.Named)
			if !ok {
				continue
			}
			for i := 0; i < nt.NumMethods(); i++ {
				if m := nt.Method(i); m.Scope() == fs {
					return o.Name() + "$" + m.Name()
				}
			}
		}
	}
	return ""
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

// typeArg mangles one type argument. A pointer is spelled "ptr_" followed by
// its element, so that Request[*T] and Request[T] get different names.
// Types other than named, pointer-to-named and basic fall back to their Go spelling with non-identifier characters
// replaced by '_'.
func typeArg(t types.Type) string {
	u := types.Unalias(t)
	if p, ok := u.(*types.Pointer); ok {
		return "ptr_" + typeArg(p.Elem())
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
	suffix := ""
	switch {
	case strings.HasPrefix(fn.Synthetic, "wrapper for"):
		suffix = "$wrapper"
	case strings.HasPrefix(fn.Synthetic, "bound method wrapper"):
		suffix = "$bound"
	case strings.HasPrefix(fn.Synthetic, "thunk for"):
		suffix = "$thunk"
	}
	// go/ssa already names bound methods and thunks "M$bound" / "M$thunk".
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func funcBase(fn *ssa.Function) string {
	name := fn.Name()
	if strings.HasPrefix(fn.Synthetic, "instance of") && fn.Signature.Recv() == nil {
		// go/ssa names an instance "Id[int]"; use the origin's name and the
		// type arguments instead.
		if o := fn.Origin(); o != nil {
			name = o.Name() + typeArgList(fn.TypeArgs())
		}
	}
	// Bound methods and thunks have no receiver in their own signature; take
	// it from the method they wrap.
	recv := fn.Signature.Recv()
	if obj, ok := fn.Object().(*types.Func); ok && recv == nil {
		recv = obj.Signature().Recv()
	}
	if recv != nil {
		return Type(recv.Type()) + "$" + name
	}
	var pkg *types.Package
	if fn.Pkg != nil {
		pkg = fn.Pkg.Pkg
	} else if fn.Object() != nil {
		pkg = fn.Object().Pkg()
	}
	if pkg == nil {
		panic(fmt.Sprintf("mangle.Func: %s has no package", fn))
	}
	return Pkg(pkg.Path()) + "::" + name
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
