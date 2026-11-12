package translate

import (
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/nabetama/infer-go-proto/internal/mangle"
)

// nameInstanceLocals gives names to the types that go/ssa created for the
// types declared inside the generic function of the instance fn.
//
// For each instance, go/ssa replaces a type declared inside the generic
// function by a new named type whose object has the same name and position
// but is in no scope. mangle would name all of them Pkg::<name>, and the
// instances' declarations would collide when their fields differ (for
// "type cell struct{ v T }", int in Wrap[int] and *go::string in
// Wrap[string]). The types are found in the values of fn and of its
// function literals, and named after the declared type and the type
// arguments of fn (mangle.SetInstanceLocal).
func nameInstanceLocals(fn *ssa.Function) {
	origin := fn.Origin()
	if origin == nil || fn.Parent() != nil || len(fn.TypeArgs()) == 0 {
		return
	}
	obj, ok := origin.Object().(*types.Func)
	if !ok || obj.Scope() == nil {
		return
	}
	scope := obj.Scope()
	w := &localWalker{scope: scope, pkg: obj.Pkg(), seen: map[types.Type]bool{}}
	w.function(fn)
	for _, fresh := range w.found {
		if orig := lookupLocal(scope, fresh); orig != nil {
			mangle.SetInstanceLocal(fresh, orig, fn.TypeArgs())
		}
	}
}

// lookupLocal returns the type declared in scope or its children with the
// name and position of fresh.
func lookupLocal(scope *types.Scope, fresh *types.TypeName) *types.TypeName {
	if tn, ok := scope.Lookup(fresh.Name()).(*types.TypeName); ok && tn.Pos() == fresh.Pos() {
		return tn
	}
	for i := 0; i < scope.NumChildren(); i++ {
		if tn := lookupLocal(scope.Child(i), fresh); tn != nil {
			return tn
		}
	}
	return nil
}

// localWalker collects the named types, reachable from the types of a
// function's values, whose object is in no scope and lies inside scope.
type localWalker struct {
	scope *types.Scope
	pkg   *types.Package
	seen  map[types.Type]bool
	found []*types.TypeName
}

func (w *localWalker) function(fn *ssa.Function) {
	w.typ(fn.Signature)
	for _, fv := range fn.FreeVars {
		w.typ(fv.Type())
	}
	var ops []*ssa.Value
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if v, ok := instr.(ssa.Value); ok {
				w.typ(v.Type())
			}
			if ta, ok := instr.(*ssa.TypeAssert); ok {
				w.typ(ta.AssertedType)
			}
			for _, op := range instr.Operands(ops[:0]) {
				if *op != nil {
					w.typ((*op).Type())
				}
			}
		}
	}
	for _, anon := range fn.AnonFuncs {
		w.function(anon)
	}
}

func (w *localWalker) typ(t types.Type) {
	if t == nil || w.seen[t] {
		return
	}
	w.seen[t] = true
	switch t := t.(type) {
	case *types.Alias:
		w.typ(types.Unalias(t))
	case *types.Named:
		if obj := t.Obj(); obj.Parent() == nil && obj.Pkg() == w.pkg && w.scope.Contains(obj.Pos()) {
			w.found = append(w.found, obj)
			w.typ(t.Underlying())
		}
		for i := 0; i < t.TypeArgs().Len(); i++ {
			w.typ(t.TypeArgs().At(i))
		}
	case *types.Pointer:
		w.typ(t.Elem())
	case *types.Slice:
		w.typ(t.Elem())
	case *types.Array:
		w.typ(t.Elem())
	case *types.Chan:
		w.typ(t.Elem())
	case *types.Map:
		w.typ(t.Key())
		w.typ(t.Elem())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			w.typ(t.At(i).Type())
		}
	case *types.Signature:
		if t.Recv() != nil {
			w.typ(t.Recv().Type())
		}
		w.typ(t.Params())
		w.typ(t.Results())
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			w.typ(t.Field(i).Type())
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			w.typ(t.Method(i).Type())
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			w.typ(t.EmbeddedType(i))
		}
	}
}
