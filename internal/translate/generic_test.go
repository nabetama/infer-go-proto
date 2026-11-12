package translate

import (
	"reflect"
	"testing"

	"github.com/nabetama/infer-go-proto/internal/infertest"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

const genericPkg = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::generic"

// Instances of generic functions and methods of the target package that a
// defined function calls are defined; the generic originals are not.
func TestTranslateGenericInstances(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/generic")
	if warn != "" {
		t.Errorf("warnings:\n%s", warn)
	}
	for _, name := range []string{
		"F$int$",
		"F$string$",
		"Box$string$$Get",
		"Wrap$int$",
		"Wrap$string$",
		"UseCmp",
	} {
		p, ok := procByName(m, genericPkg+"::"+name)
		if !ok || len(p.Blocks) == 0 {
			t.Errorf("%s is not defined", name)
		}
	}
	// go/ssa creates a distinct type per instance for the type declared
	// inside Wrap; the name has the type arguments of the instance.
	for name, ft := range map[string]sil.Type{"cell$Wrap$int$": "int", "cell$Wrap$string$": "*go::string"} {
		d, ok := declByName(m.Types, genericPkg+"::"+name)
		if !ok || !reflect.DeepEqual(d.Fields, []sil.Field{{Name: "v", Type: ft}}) {
			t.Errorf("decl %s = %v, %v", name, d, ok)
		}
	}
	for _, p := range m.Procs {
		if p.Name == genericPkg+"::F" || p.Name == genericPkg+"::Wrap" {
			t.Errorf("generic original %s is defined", p.Name)
		}
	}
	// An instance of a generic function of another package is declared.
	if d, ok := declNamed(m, "cmp::Compare$int$"); !ok || !reflect.DeepEqual(d.Params, []sil.Type{"int", "int"}) || d.Ret != "int" {
		t.Errorf("cmp::Compare$int$ decl = %+v, %v", d, ok)
	}
	for _, d := range m.Decls {
		if _, ok := procByName(m, d.Name); ok {
			t.Errorf("%s is both defined and declared", d.Name)
		}
	}
	path, text := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatalf("%v\n--- sil ---\n%s", err, text)
	}
}

func TestTranslateGenericInfer(t *testing.T) {
	infertest.Bin(t)
	m, _ := translatePkgs(t, "./testdata/src/generic")
	got := reported(t, runInfer(t, m, sourceSinkConfig(genericPkg)), genericPkg)
	want := []string{"ViaBox", "ViaGeneric"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
}
