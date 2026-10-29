package mangle

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func assertEq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPkg(t *testing.T) {
	assertEq(t, Pkg("github.com/uptrace/bun"), "github_com::uptrace::bun")
	assertEq(t, Pkg("connectrpc.com/connect"), "connectrpc_com::connect")
	assertEq(t, Pkg("fmt"), "fmt")
	assertEq(t, Pkg("example.com/a-b/v1"), "example_com::a_b::v1")
}

func TestRT(t *testing.T) {
	assertEq(t, RT("sink"), "go::rt::sink")
}

// reqOf returns connect.Request[pb.<msg>] built by hand.
func reqOf(msg string) types.Type {
	pb := types.NewPackage("example.com/x/pb", "pb")
	msgObj := types.NewTypeName(token.NoPos, pb, msg, nil)
	msgT := types.NewNamed(msgObj, types.NewStruct(nil, nil), nil)

	conn := types.NewPackage("connectrpc.com/connect", "connect")
	tp := types.NewTypeParam(types.NewTypeName(token.NoPos, conn, "T", nil), types.Universe.Lookup("any").Type())
	reqObj := types.NewTypeName(token.NoPos, conn, "Request", nil)
	generic := types.NewNamed(reqObj, types.NewStruct(nil, nil), nil)
	generic.SetTypeParams([]*types.TypeParam{tp})
	inst, err := types.Instantiate(nil, generic, []types.Type{msgT}, false)
	if err != nil {
		panic(err)
	}
	return inst
}

func TestTypeGeneric(t *testing.T) {
	got := Type(reqOf("GetUserRequest"))
	assertEq(t, got, "connectrpc_com::connect::Request$example_com::x::pb::GetUserRequest$")
	if got == Type(reqOf("ListUsersRequest")) {
		t.Error("different type arguments must give different names")
	}
	assertEq(t, Type(types.NewPointer(reqOf("GetUserRequest"))), got)
}

func TestTypeBasicArgAndMultipleArgs(t *testing.T) {
	p := types.NewPackage("example.com/g", "g")
	tp1 := types.NewTypeParam(types.NewTypeName(token.NoPos, p, "K", nil), types.Universe.Lookup("any").Type())
	tp2 := types.NewTypeParam(types.NewTypeName(token.NoPos, p, "V", nil), types.Universe.Lookup("any").Type())
	g := types.NewNamed(types.NewTypeName(token.NoPos, p, "Pair", nil), types.NewStruct(nil, nil), nil)
	g.SetTypeParams([]*types.TypeParam{tp1, tp2})
	inst, err := types.Instantiate(nil, g, []types.Type{types.Typ[types.String], types.Typ[types.Int]}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertEq(t, Type(inst), "example_com::g::Pair$string$int$")
}

func TestTypeUniverseError(t *testing.T) {
	assertEq(t, Type(types.Universe.Lookup("error").Type()), "error")
}

const src = `package p

type S struct{}

func (s S) Val() {}
func (s *S) Ptr() {}

type A struct{}
type B struct{}

func (A) M()  {}
func (*B) M() {}

func UseBoundThunk() {
	var a A
	var b B
	_ = a.M
	_ = b.M
	_ = A.M
	_ = (*B).M
	_ = C.M
	sinkI = I.M
}

var sinkI func(I)

type C struct{ A }

type I interface{ M() }

func Top() {
	f := func() {}
	f()
	var s S
	s.Val()
	(&s).Ptr()
}

type Box[T any] struct{ v T }

func (b *Box[T]) Get() T { return b.v }

func UseBox() int { return (&Box[int]{}).Get() }

func Id[T any](x T) T { return x }

func UseId() int { return Id[int](1) }
`

func buildPkg(t *testing.T) (*ssa.Package, *ssa.Program) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset,
		types.NewPackage("example.com/p", "p"), []*ast.File{f}, ssa.InstantiateGenerics)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, pkg.Prog
}

func TestFuncSSA(t *testing.T) {
	pkg, prog := buildPkg(t)

	top := pkg.Func("Top")
	assertEq(t, Func(top), "example_com::p::Top")
	assertEq(t, Func(top.AnonFuncs[0]), "example_com::p::Top$1")

	sT := pkg.Type("S").Type()
	assertEq(t, Func(prog.LookupMethod(sT, pkg.Pkg, "Val")), "example_com::p::S$Val")
	ptrS := types.NewPointer(sT)
	assertEq(t, Func(prog.LookupMethod(ptrS, pkg.Pkg, "Ptr")), "example_com::p::S$Ptr")

	// (*S).Val is a go/ssa synthetic wrapper; it must not collide with S.Val.
	w := prog.LookupMethod(ptrS, pkg.Pkg, "Val")
	if w.Synthetic == "" {
		t.Fatal("expected synthetic wrapper")
	}
	assertEq(t, Func(w), "example_com::p::S$Val$wrapper")
}

func TestFuncGenericMethodAndInstance(t *testing.T) {
	pkg, _ := buildPkg(t)
	seen := map[string]bool{}
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		seen[Func(fn)] = true
	}
	for _, want := range []string{
		"example_com::p::Box$int$$Get",
		"example_com::p::Id$int$",
	} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, seen)
		}
	}
}

func TestTableCollision(t *testing.T) {
	var tb Table
	if err := tb.Add("example.com/a-b.F", "example_com::a_b::F"); err != nil {
		t.Fatal(err)
	}
	if err := tb.Add("example.com/a-b.F", "example_com::a_b::F"); err != nil {
		t.Fatalf("re-adding the same pair must be ok: %v", err)
	}
	if err := tb.Add("example.com/a_b.F", "example_com::a_b::F"); err == nil {
		t.Fatal("want collision error")
	}
}

func TestTableRejectsUnqualified(t *testing.T) {
	var tb Table
	if err := tb.Add("main.sprintf", "sprintf"); err == nil {
		t.Fatal("want error for name without ::")
	}
	if err := tb.Add("builtin", "__sil_allocate"); err != nil {
		t.Fatalf("__sil_ names are allowed: %v", err)
	}
}

func TestFuncBoundAndThunk(t *testing.T) {
	pkg, _ := buildPkg(t)
	got := map[string]bool{}
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn.Pkg == pkg || fn.Synthetic != "" {
			got[Func(fn)] = true
		}
	}
	// Thunks are only reachable as operands.
	for _, b := range pkg.Func("UseBoundThunk").Blocks {
		for _, in := range b.Instrs {
			for _, o := range in.Operands(nil) {
				if f, ok := (*o).(*ssa.Function); ok {
					got[Func(f)] = true
				}
			}
		}
	}
	for _, want := range []string{
		"example_com::p::A$M$bound",
		"example_com::p::B$M$bound", "example_com::p::C$M$wrapper",
		"example_com::p::I$M$thunk",
	} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}
