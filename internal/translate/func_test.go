package translate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabetama/infer-go-proto/internal/infertest"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

func translatePkgs(t *testing.T, patterns ...string) (*sil.Module, string) {
	t.Helper()
	_, pkgs, err := Load(patterns, ".")
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	m, err := Translate(pkgs, &warn)
	if err != nil {
		t.Fatal(err)
	}
	return m, warn.String()
}

func procByName(m *sil.Module, name string) (sil.Proc, bool) {
	for _, p := range m.Procs {
		if p.Name == name {
			return p, true
		}
	}
	return sil.Proc{}, false
}

func declNamed(m *sil.Module, name string) (sil.ProcDecl, bool) {
	for _, d := range m.Decls {
		if d.Name == name {
			return d, true
		}
	}
	return sil.ProcDecl{}, false
}

// writeSil writes m to a temporary .sil file and returns its path and text.
func writeSil(t *testing.T, m *sil.Module) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "out.sil")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, buf.String()
}

const flowPkg = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::flow"

func TestTranslateFlowNoWarnings(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/flow")
	if warn != "" {
		t.Errorf("warnings:\n%s", warn)
	}
	for _, name := range []string{
		flowPkg + "::Pick",
		flowPkg + "::Use",
		flowPkg + "::V$Get",
		flowPkg + "::CallGet",
		flowPkg + "::V$Get$wrapper", // go/ssa のラッパー (*V).Get
	} {
		p, ok := procByName(m, name)
		if !ok {
			t.Errorf("%s is not defined", name)
			continue
		}
		if len(p.Blocks) == 0 {
			t.Errorf("%s has no blocks", name)
		}
		if _, ok := declNamed(m, name); ok {
			t.Errorf("%s is both defined and declared", name)
		}
	}
	// The package initializer is not translated.
	if _, ok := procByName(m, flowPkg+"::init"); ok {
		t.Errorf("package initializer is defined")
	}
	// Multiple results are returned in a tuple.
	pick, _ := procByName(m, flowPkg+"::Pick")
	if pick.Ret != "*go::tuple2$ptr_go::string$ptr_go::iface$" {
		t.Errorf("Pick returns %s", pick.Ret)
	}
	if _, text := writeSil(t, m); !strings.Contains(text, `go::rt::string_const("")`) {
		t.Errorf("string constant not converted:\n%s", text)
	}
}

func TestTranslateFlowInfer(t *testing.T) {
	infertest.Bin(t)
	m, _ := translatePkgs(t, "./testdata/src/flow")
	path, text := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatalf("%v\n--- sil ---\n%s", err, text)
	}
	infertest.Run(t, []string{path}, "{}")
}

const callsPkg = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::calls"

func TestTranslateCalls(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/calls")
	if warn != "" {
		t.Errorf("warnings:\n%s", warn)
	}
	for _, name := range []string{"Upper", "Len", "Max", "Sum", "Apply", "Apply$1", "Ratio", "Copy"} {
		if _, ok := procByName(m, callsPkg+"::"+name); !ok {
			t.Errorf("%s is not defined", name)
		}
	}
	// Only the external functions that are called are declared, with the
	// parameter types of the callee (receiver first).
	up, ok := declNamed(m, "strings::ToUpper")
	if !ok || len(up.Params) != 1 || up.Params[0] != "*go::string" || up.Ret != "*go::string" {
		t.Errorf("strings::ToUpper decl = %+v, %v", up, ok)
	}
	l, ok := declNamed(m, "strings::Builder$Len")
	if !ok || len(l.Params) != 1 || l.Params[0] != "*strings::Builder" || l.Ret != "int" {
		t.Errorf("strings::Builder$Len decl = %+v, %v", l, ok)
	}
	if _, ok := declNamed(m, "strings::ToLower"); ok {
		t.Error("strings::ToLower is declared but not called")
	}
	for _, d := range m.Decls {
		if _, ok := procByName(m, d.Name); ok {
			t.Errorf("%s is both defined and declared", d.Name)
		}
	}

	// A successor of If with two predecessors gets its prune in a separate block.
	max, _ := procByName(m, callsPkg+"::Max")
	prunes := 0
	for _, b := range max.Blocks {
		for i, in := range b.Instrs {
			if _, ok := in.(sil.Prune); ok {
				prunes++
				if i != 0 {
					t.Errorf("Max: prune is not first in #%s", b.Label)
				}
			}
		}
	}
	if prunes != 2 {
		t.Errorf("Max has %d prunes, want 2", prunes)
	}
	preds := map[string]int{}
	for _, b := range max.Blocks {
		if j, ok := b.Term.(sil.Jmp); ok {
			for _, l := range j.Labels {
				preds[l]++
			}
		}
	}
	for _, b := range max.Blocks {
		if len(b.Instrs) > 0 {
			if _, ok := b.Instrs[0].(sil.Prune); ok && preds[b.Label] != 1 {
				t.Errorf("Max: #%s has a prune and %d predecessors", b.Label, preds[b.Label])
			}
		}
	}

	if testing.Short() {
		return
	}
	infertest.Bin(t)
	path, text := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatalf("%v\n--- sil ---\n%s", err, text)
	}
}

func TestTranslateUnsupported(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/unsupported")
	want := "go2sil: warning: github.com/nabetama/infer-go-proto/internal/translate/testdata/src/unsupported.Assert: unsupported *ssa.TypeAssert\n"
	if warn != want {
		t.Errorf("warnings = %q, want %q", warn, want)
	}
	pkg := "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::unsupported"
	if _, ok := procByName(m, pkg+"::Assert"); ok {
		t.Error("Assert is defined")
	}
	d, ok := declNamed(m, pkg+"::Assert")
	if !ok || len(d.Params) != 1 || d.Params[0] != "*go::iface" || d.Ret != "*go::string" {
		t.Errorf("Assert decl = %+v, %v", d, ok)
	}
	if _, ok := procByName(m, pkg+"::CallAssert"); !ok {
		t.Error("CallAssert is not defined")
	}
	if testing.Short() {
		return
	}
	infertest.Bin(t)
	path, text := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatalf("%v\n--- sil ---\n%s", err, text)
	}
}

func TestTranslateNameCollision(t *testing.T) {
	_, pkgs, err := Load([]string{"./testdata/src/coll/a_b", "./testdata/src/coll/a.b"}, ".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Translate(pkgs, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "coll::a_b::F") {
		t.Fatalf("err = %v, want a collision on coll::a_b::F", err)
	}
}
