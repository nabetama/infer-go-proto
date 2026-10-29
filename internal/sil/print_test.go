package sil

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabetama/infer-go-proto/internal/infertest"
)

func basicBad() *Module {
	return &Module{
		Types: []TypeDecl{
			{Name: "cell", Fields: []Field{{"value", "int"}, {"next", "*cell"}}},
			{Name: "list", Fields: []Field{{"header", "*cell"}}},
		},
		Decls: []ProcDecl{{Name: "check", Params: []Type{"*list"}, Ret: "void"}},
		Procs: []Proc{{
			Name:   "basic_bad",
			Params: []Param{{"l", "*list"}},
			Ret:    "void",
			Blocks: []Block{
				{
					Label: "entry",
					Instrs: []Instr{
						Store{Addr: Var{"l"}, Val: Null{}, Type: "*list"},
						Load{Dst: "n0", Type: "*list", Addr: Var{"l"}},
						Load{Dst: "n1", Type: "*cell", Addr: FieldOf{Base: Ident("n0"), Type: "list", Field: "header"}},
						Let{Dst: "n2", Val: Call{Name: "check", Args: []Expr{Ident("n0")}}},
					},
					Term: Jmp{Labels: []string{"ok", "done"}},
				},
				{
					Label: "ok",
					Instrs: []Instr{
						Prune{Cond: Ident("n2"), Neg: true},
						Let{Dst: "n3", Val: Alloc{Type: "cell"}},
						Let{Dst: "n4", Val: StrConst(`s"x`)},
					},
					Term: Ret{Val: Null{}},
				},
				{Label: "done", Term: Ret{Val: Null{}}},
			},
		}},
	}
}

func TestWriteToGolden(t *testing.T) {
	var buf bytes.Buffer
	if _, err := basicBad().WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/basic_bad.sil")
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

func TestDedup(t *testing.T) {
	m := &Module{
		Types: []TypeDecl{{Name: "T", Fields: []Field{{"f", "int"}}}, {Name: "T", Fields: []Field{{"f", "int"}}}},
		Decls: []ProcDecl{{Name: "g", Ret: "void"}, {Name: "g", Ret: "void"}},
	}
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "type T ="); n != 1 {
		t.Errorf("type T printed %d times", n)
	}
	if n := strings.Count(buf.String(), "declare g("); n != 1 {
		t.Errorf("declare g printed %d times", n)
	}
}

func TestConflict(t *testing.T) {
	cases := map[string]*Module{
		"type": {Types: []TypeDecl{{Name: "T", Fields: []Field{{"f", "int"}}}, {Name: "T"}}},
		"decl": {Decls: []ProcDecl{{Name: "g", Ret: "void"}, {Name: "g", Ret: "int"}}},
	}
	for name, m := range cases {
		if _, err := m.WriteTo(&bytes.Buffer{}); err == nil {
			t.Errorf("%s: want error for conflicting declarations", name)
		}
	}
}

func TestInferAccepts(t *testing.T) {
	infertest.Bin(t)
	var buf bytes.Buffer
	if _, err := basicBad().WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "basic_bad.sil")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := infertest.Run(t, []string{path}, "{}")
	n := 0
	for _, is := range issues {
		if is.BugType == "NULLPTR_DEREFERENCE" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("NULLPTR_DEREFERENCE = %d, want 1; issues=%+v", n, issues)
	}
}

func TestWriteToRejectsMalformedProcs(t *testing.T) {
	ret := Ret{Val: Null{}}
	cases := map[string]*Module{
		"nil term":  {Procs: []Proc{{Name: "f", Ret: "void", Blocks: []Block{{Label: "b0"}}}}},
		"empty jmp": {Procs: []Proc{{Name: "f", Ret: "void", Blocks: []Block{{Label: "b0", Term: Jmp{}}}}}},
		"duplicate proc": {Procs: []Proc{
			{Name: "f", Ret: "void", Blocks: []Block{{Label: "b0", Term: ret}}},
			{Name: "f", Ret: "void", Blocks: []Block{{Label: "b0", Term: ret}}},
		}},
	}
	for name, m := range cases {
		if _, err := m.WriteTo(&bytes.Buffer{}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRetNilIsNull(t *testing.T) {
	m := &Module{Procs: []Proc{{Name: "f", Ret: "void", Blocks: []Block{{Label: "b0", Term: Ret{}}}}}}
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "    ret null\n") {
		t.Fatalf("output:\n%s", buf.String())
	}
}

func TestFloatConst(t *testing.T) {
	for _, c := range []struct {
		v    FloatConst
		want string
	}{{1.5, "1.5"}, {2, "2.0"}, {-0.25, "-0.25"}, {1e21, "1000000000000000000000.0"}} {
		if got := exprString(c.v); got != c.want {
			t.Errorf("exprString(%v) = %q, want %q", float64(c.v), got, c.want)
		}
	}
}
