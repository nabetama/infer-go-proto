package translate

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nabetama/infer-go-proto/internal/infertest"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

const (
	taintPkg  = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::taint"
	branchPkg = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::branch"
	elemsPkg  = "github_com::nabetama::infer_go_proto::internal::translate::testdata::src::elems"
)

// sourceSinkConfig は、pkg の Source の戻り値を入力元にし、Sink の引数と extraSinks を
// 危険な関数にする .inferconfig を返す。
func sourceSinkConfig(pkg string, extraSinks ...string) string {
	sinks := append([]string{`{ "procedure_regex": "^` + pkg + `::Sink$" }`}, extraSinks...)
	return `{
  "pulse-taint-sources": [{ "procedure_regex": "^` + pkg + `::Source$" }],
  "pulse-taint-sinks": [
    ` + strings.Join(sinks, ",\n    ") + `
  ]
}`
}

// reported は TAINT_ERROR が報告された手続きの名前（pkg:: を除く）を、重複なしで名前順に返す。
// TAINT_ERROR 以外の報告があればテストを失敗にする。
func reported(t *testing.T, issues []infertest.Issue, pkg string) []string {
	t.Helper()
	set := map[string]bool{}
	for _, is := range issues {
		if is.BugType != "TAINT_ERROR" {
			t.Errorf("unexpected issue: %+v", is)
			continue
		}
		set[strings.TrimPrefix(is.Procedure, pkg+"::")] = true
	}
	out := []string{}
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// runInfer は m を .sil に書き、取り込みエラーがないことを確かめてから Pulse で解析する。
func runInfer(t *testing.T, m *sil.Module, inferconfig string) []infertest.Issue {
	t.Helper()
	infertest.Bin(t)
	path, text := writeSil(t, m)
	if err := infertest.Capture(t, []string{path}); err != nil {
		t.Fatalf("%v\n--- sil ---\n%s", err, text)
	}
	return infertest.Run(t, []string{path}, inferconfig)
}

func TestTranslateTaintWarnings(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/taint")
	want := "go2sil: warning: github.com/nabetama/infer-go-proto/internal/translate/testdata/src/taint.ViaIface: unsupported *ssa.TypeAssert\n"
	if warn != want {
		t.Errorf("warnings =\n%s\nwant\n%s", warn, want)
	}
	if _, ok := declNamed(m, taintPkg+"::ViaIface"); !ok {
		t.Error("ViaIface is not declared")
	}
	for _, name := range []string{"Source", "Sink", "Q$Where", "ViaSprintf", "ViaConcat", "ArgsOnly", "QueryTainted", "Conv"} {
		p, ok := procByName(m, taintPkg+"::"+name)
		if !ok || len(p.Blocks) == 0 {
			t.Errorf("%s is not defined", name)
		}
	}
	// fmt.Sprintf is defined by its model, and not declared.
	if _, ok := procByName(m, "fmt::Sprintf"); !ok {
		t.Error("fmt::Sprintf model is not defined")
	}
	for _, d := range m.Decls {
		if _, ok := procByName(m, d.Name); ok {
			t.Errorf("%s is both defined and declared", d.Name)
		}
	}
	// The conversion to Safe calls go::rt::convert$<pkg>::Safe.
	conv := "go::rt::convert$" + taintPkg + "::Safe"
	if d, ok := declNamed(m, conv); !ok || !reflect.DeepEqual(d.Params, []sil.Type{"*go::string"}) || d.Ret != "*go::string" {
		t.Errorf("%s decl = %+v, %v", conv, d, ok)
	}
	for _, name := range []string{"go::rt::string_concat", "go::rt::make_iface"} {
		if _, ok := declNamed(m, name); !ok {
			t.Errorf("%s is not declared", name)
		}
	}
}

func TestTranslateTaintInfer(t *testing.T) {
	infertest.Bin(t)
	m, _ := translatePkgs(t, "./testdata/src/taint")
	where := `{ "procedure_regex": "^` + taintPkg + `::Q\\$Where$", "taint_target": ["ArgumentPositions", [1]] }`
	conv := `{ "procedure_regex": "^go::rt::convert\\$` + taintPkg + `::Safe$" }`

	got := reported(t, runInfer(t, m, sourceSinkConfig(taintPkg, where, conv)), taintPkg)
	want := []string{"Conv", "QueryTainted", "ViaConcat", "ViaSprintf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}

	// The conversion to Safe alone is reported as a sink: drop Sink and Where.
	onlyConv := `{
  "pulse-taint-sources": [{ "procedure_regex": "^` + taintPkg + `::Source$" }],
  "pulse-taint-sinks": [` + conv + `]
}`
	issues := runInfer(t, m, onlyConv)
	got = reported(t, issues, taintPkg)
	if !reflect.DeepEqual(got, []string{"Conv"}) {
		t.Errorf("with only the Safe conversion as sink: reported %v, want [Conv]", got)
	}
	for _, is := range issues {
		if !strings.Contains(is.Qualifier, "convert$") {
			t.Errorf("qualifier does not name the conversion: %s", is.Qualifier)
		}
	}
}

// If の向き（then が先の後続）と、複数の戻り値の要素の区別を Pulse で確かめる。
func TestTranslateBranchInfer(t *testing.T) {
	infertest.Bin(t)
	m, warn := translatePkgs(t, "./testdata/src/branch")
	if warn != "" {
		t.Errorf("warnings:\n%s", warn)
	}
	got := reported(t, runInfer(t, m, sourceSinkConfig(branchPkg)), branchPkg)
	want := []string{"PickTrue", "SecondTainted"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
}

func TestTranslateElems(t *testing.T) {
	m, warn := translatePkgs(t, "./testdata/src/elems")
	if warn != "" {
		t.Errorf("warnings:\n%s", warn)
	}
	for _, d := range m.Decls {
		if _, ok := procByName(m, d.Name); ok {
			t.Errorf("%s is both defined and declared", d.Name)
		}
	}
	// An interface method call declares <interface type>$<method>, receiver first.
	get, ok := declNamed(m, elemsPkg+"::Getter$Get")
	if !ok || !reflect.DeepEqual(get.Params, []sil.Type{"*go::iface", "*go::string"}) || get.Ret != "*go::string" {
		t.Errorf("Getter$Get decl = %+v, %v", get, ok)
	}
	// A conversion to a named type calls go::rt::convert$<type>; to an
	// unnamed type it uses the value as it is.
	if d, ok := declNamed(m, "go::rt::convert$"+elemsPkg+"::ID"); !ok || !reflect.DeepEqual(d.Params, []sil.Type{"int"}) || d.Ret != "int" {
		t.Errorf("convert$ID decl = %+v, %v", d, ok)
	}
	for _, d := range m.Decls {
		if strings.HasPrefix(d.Name, "go::rt::convert$") && d.Name != "go::rt::convert$"+elemsPkg+"::ID" {
			t.Errorf("unexpected conversion helper %s", d.Name)
		}
	}
	// A local array is a pointer to a *go::slice; a[:] and &a[i] load it,
	// and the element address is the single field go::slice.elem.
	arr, _ := procByName(m, elemsPkg+"::ArrayToSlice")
	_, text := writeSil(t, m)
	if !strings.Contains(text, "__sil_allocate(<go::slice>)") {
		t.Errorf("array storage is not a go::slice:\n%s", text)
	}
	elemAddrs := 0
	for _, b := range arr.Blocks {
		for _, in := range b.Instrs {
			if l, ok := in.(sil.Let); ok {
				if f, ok := l.Val.(sil.FieldOf); ok && f.Type == "go::slice" && f.Field == "elem" {
					elemAddrs++
				}
			}
		}
	}
	if elemAddrs != 2 {
		t.Errorf("ArrayToSlice has %d element addresses, want 2", elemAddrs)
	}

	if testing.Short() {
		return
	}
	got := reported(t, runInfer(t, m, sourceSinkConfig(elemsPkg)), elemsPkg)
	want := []string{"ArrayToSlice", "SliceParam", "ViaInvoke"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
}
