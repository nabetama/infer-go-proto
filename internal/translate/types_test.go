package translate

import (
	"go/types"
	"reflect"
	"strings"
	"testing"

	_ "connectrpc.com/connect" // keeps the dependency in go.mod for testdata/src/extgeneric

	"golang.org/x/tools/go/ssa"

	"github.com/nabetama/infer-go-proto/internal/infertest"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

const modPath = "github.com/nabetama/infer-go-proto/internal/translate/testdata/src/"

func loadPkg(t *testing.T, name string) *ssa.Package {
	t.Helper()
	_, pkgs, err := Load([]string{"./testdata/src/" + name}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0] == nil {
		t.Fatalf("got %d packages", len(pkgs))
	}
	return pkgs[0]
}

func declByName(decls []sil.TypeDecl, name string) (sil.TypeDecl, bool) {
	for _, d := range decls {
		if d.Name == name {
			return d, true
		}
	}
	return sil.TypeDecl{}, false
}

func TestTypeDecls(t *testing.T) {
	pkg := loadPkg(t, "structs")
	user := pkg.Pkg.Scope().Lookup("User").Type()
	tc := newTypeConv()
	userName := "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::structs::User"
	if got := tc.Type(user); got != sil.Type("*"+userName) {
		t.Fatalf("Type(User) = %q", got)
	}
	decls := tc.Decls()
	d, ok := declByName(decls, userName)
	if !ok {
		t.Fatalf("no decl for User in %v", decls)
	}
	want := []sil.Field{
		{Name: "Name", Type: "*go::string"},
		{Name: "next", Type: sil.Type("*" + userName)},
		{Name: "Tags", Type: "*go::slice"},
	}
	if !reflect.DeepEqual(d.Fields, want) {
		t.Errorf("fields = %v, want %v", d.Fields, want)
	}
	s, ok := declByName(decls, "go::string")
	if !ok || len(s.Fields) != 0 {
		t.Errorf("go::string decl = %v, %v", s, ok)
	}
	sl, ok := declByName(decls, "go::slice")
	if !ok || !reflect.DeepEqual(sl.Fields, []sil.Field{{Name: "elem", Type: "*go::opaque"}}) {
		t.Errorf("go::slice decl = %v, %v", sl, ok)
	}
	if _, ok := declByName(decls, "go::opaque"); !ok {
		t.Errorf("go::opaque not declared")
	}
	for i := 1; i < len(decls); i++ {
		if decls[i-1].Name >= decls[i].Name {
			t.Errorf("decls not sorted/unique at %d: %s, %s", i, decls[i-1].Name, decls[i].Name)
		}
	}
}

func TestTypeBasics(t *testing.T) {
	tc := newTypeConv()
	cases := []struct {
		t    types.Type
		want sil.Type
	}{
		{types.Typ[types.Int], "int"},
		{types.Typ[types.Uint8], "int"},
		{types.Typ[types.Bool], "int"},
		{types.Typ[types.Float64], "float"},
		{types.Typ[types.String], "*go::string"},
		{types.NewSlice(types.Typ[types.Int]), "*go::slice"},
		{types.NewMap(types.Typ[types.Int], types.Typ[types.Int]), "*go::opaque"},
		{types.NewChan(types.SendRecv, types.Typ[types.Int]), "*go::opaque"},
		{types.NewSignatureType(nil, nil, nil, nil, nil, false), "*go::opaque"},
		{types.NewInterfaceType(nil, nil), "*go::iface"},
		{types.NewTuple(types.NewVar(0, nil, "a", types.Typ[types.Int]), types.NewVar(0, nil, "b", types.Typ[types.String])), "*go::tuple2$int$ptr_go::string$"},
	}
	for _, c := range cases {
		if got := tc.Type(c.t); got != c.want {
			t.Errorf("Type(%s) = %q, want %q", c.t, got, c.want)
		}
	}
	d, ok := declByName(tc.Decls(), "go::tuple2$int$ptr_go::string$")
	if !ok || !reflect.DeepEqual(d.Fields, []sil.Field{{Name: "r0", Type: "int"}, {Name: "r1", Type: "*go::string"}}) {
		t.Errorf("tuple decl = %v, %v", d, ok)
	}
}

func TestTypeExternalGeneric(t *testing.T) {
	pkg := loadPkg(t, "extgeneric")
	fn := pkg.Func("Handle")
	tc := newTypeConv()
	got := tc.Type(fn.Params[0].Type())
	if !strings.HasPrefix(string(got), "*connectrpc_com::connect::Request$") {
		t.Fatalf("Type = %q", got)
	}
	var found bool
	for _, d := range tc.Decls() {
		if strings.HasPrefix(d.Name, "connectrpc_com::connect::Request$") {
			found = true
			var msg *sil.Field
			for i := range d.Fields {
				if d.Fields[i].Name == "Msg" {
					msg = &d.Fields[i]
				}
			}
			if msg == nil || !strings.HasSuffix(string(msg.Type), "::structs::User") {
				t.Errorf("Msg field = %v in %v", msg, d)
			}
		}
	}
	if !found {
		t.Error("no Request decl")
	}
	// Types reachable through fields are declared as well.
	if _, ok := declByName(tc.Decls(), "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::structs::User"); !ok {
		t.Error("User not declared transitively")
	}
}

func TestTypeBlankFields(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/blank")
	if warn != "" {
		t.Fatalf("warnings:\n%s", warn)
	}
	d, ok := declByName(m.Types, "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::blank::Padded")
	if !ok {
		t.Fatal("Padded not declared")
	}
	var names []string
	for _, f := range d.Fields {
		names = append(names, f.Name)
	}
	if want := []string{"blank$0", "A", "blank$2"}; !reflect.DeepEqual(names, want) {
		t.Errorf("fields = %v, want %v", names, want)
	}
	// Textual rejects "_" as a field name (SIL syntax error: unexpected token _).
	path, _ := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatal(err)
	}
}

const localPkg = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::localtypes"

func TestTypeLocalTypesGetDistinctNames(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/localtypes")
	if warn != "" {
		t.Fatalf("warnings:\n%s", warn)
	}
	for name, field := range map[string]string{localPkg + "::row$F": "ID", localPkg + "::row$G": "Name"} {
		d, ok := declByName(m.Types, name)
		if !ok || len(d.Fields) != 1 || d.Fields[0].Name != field {
			t.Errorf("decl %s = %v, %v", name, d, ok)
		}
	}
	path, _ := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatal(err)
	}
}

func TestTranslateTypeNameCollision(t *testing.T) {
	_, pkgs, err := Load([]string{"./testdata/src/dotpkg/a.b", "./testdata/src/dotpkg/a_b"}, ".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Translate(pkgs, nil)
	want := "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::dotpkg::a_b::T"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want a collision error naming %s", err, want)
	}
}

func TestTypeAnonStructCollision(t *testing.T) {
	// struct{X_ *int} and struct{X []int} are both spelled
	// go::struct$struct_X___int_$.
	field := func(name string, typ types.Type) *types.Var { return types.NewField(0, nil, name, typ, false) }
	s1 := types.NewStruct([]*types.Var{field("X_", types.NewPointer(types.Typ[types.Int]))}, nil)
	s2 := types.NewStruct([]*types.Var{field("X", types.NewSlice(types.Typ[types.Int]))}, nil)
	tc := newTypeConv()
	if a, b := tc.Type(s1), tc.Type(s2); a != b {
		t.Fatalf("test premise: names differ: %s, %s", a, b)
	}
	if tc.Err() == nil {
		t.Fatal("want a collision error")
	}
	// The same type twice is not a collision.
	tc = newTypeConv()
	tc.Type(s1)
	tc.Type(types.NewStruct([]*types.Var{field("X_", types.NewPointer(types.Typ[types.Int]))}, nil))
	if err := tc.Err(); err != nil {
		t.Fatal(err)
	}
}
