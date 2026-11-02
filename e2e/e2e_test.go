// Package e2e converts the samples in examples/ with go2sil, analyzes them
// with Infer, and compares the reported functions with the "// want:"
// comments in the samples.
package e2e

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nabetama/infer-go-proto/internal/infertest"
)

const repoRoot = ".."

// samplePackages are the go2sil patterns. examples/gen is not passed: the
// generated protobuf / gRPC / connect code uses globals and type assertions
// that go2sil does not translate, and the samples only call its getters,
// which are declared (without a body) when called from the packages below.
var samplePackages = []string{
	"./examples/connectsvc",
	"./examples/grpcsvc",
	"./examples/userrepo",
}

// notWanted lists reported Go functions that are not compared with want.
// It is empty: Repo.FindByName receives a plain string, so Pulse reports the
// flow only in its caller GetUserViaRepoBad. If FindByName is ever reported
// as well, it is the same flow reported in the callee (the design allows
// that duplicate) and it goes here, not into want.
var notWanted = map[string]bool{}

func TestE2E(t *testing.T) {
	infertest.Bin(t)
	sil, stderr := runGo2sil(t, samplePackages...)
	if stderr != "" {
		t.Fatalf("go2sil warnings:\n%s", stderr)
	}
	cfg, err := os.ReadFile(filepath.Join(repoRoot, ".inferconfig"))
	if err != nil {
		t.Fatal(err)
	}
	issues := infertest.Run(t, []string{sil}, string(cfg))
	for _, is := range issues {
		t.Logf("%s %s: %s", is.BugType, is.Procedure, is.Qualifier)
	}
	got := taintFuncs(issues)
	want := wantFromComments(t, filepath.Join(repoRoot, "examples"))
	t.Logf("got  %v", got)
	t.Logf("want %v", want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// runGo2sil builds go2sil, runs it from the repository root on patterns and
// returns the output file and the standard error.
func runGo2sil(t *testing.T, patterns ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "go2sil")
	build := exec.Command("go", "build", "-o", bin, "./cmd/go2sil")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out := filepath.Join(dir, "out.sil")
	cmd := exec.Command(bin, append([]string{"-o", out}, patterns...)...)
	cmd.Dir = repoRoot
	var se bytes.Buffer
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("go2sil: %v\n%s", err, se.String())
	}
	return out, se.String()
}

// taintFuncs returns the sorted Go function names of the TAINT_ERROR issues,
// without the names in notWanted.
func taintFuncs(issues []infertest.Issue) []string {
	set := map[string]bool{}
	for _, is := range issues {
		if is.BugType != "TAINT_ERROR" {
			continue
		}
		if name := goFuncName(is.Procedure); !notWanted[name] {
			set[name] = true
		}
	}
	return sortedKeys(set)
}

// goFuncName converts a procedure name of go2sil back to the Go function
// name: the part after the last "$" for a method
// ("pkg::Handler$GetUserBad" -> "GetUserBad"), otherwise the part after the
// last "::" ("pkg::New" -> "New").
func goFuncName(proc string) string {
	if i := strings.LastIndex(proc, "$"); i >= 0 {
		return proc[i+1:]
	}
	if i := strings.LastIndex(proc, "::"); i >= 0 {
		return proc[i+2:]
	}
	return proc
}

// wantFromComments returns the sorted names of the functions under dir whose
// doc comment has the line "want: TAINT_ERROR".
func wantFromComments(t *testing.T, dir string) []string {
	t.Helper()
	set := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Doc == nil {
				continue
			}
			for _, c := range fd.Doc.List {
				if strings.TrimSpace(strings.TrimPrefix(c.Text, "//")) == "want: TAINT_ERROR" {
					set[fd.Name.Name] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestGoFuncName(t *testing.T) {
	for proc, want := range map[string]string{
		"github_com::nabetama::infer_go_proto::examples::connectsvc::Handler$GetUserBad": "GetUserBad",
		"github_com::nabetama::infer_go_proto::examples::userrepo::Repo$FindByName":      "FindByName",
		"github_com::nabetama::infer_go_proto::examples::grpcsvc::New":                   "New",
		"main": "main",
	} {
		if got := goFuncName(proc); got != want {
			t.Errorf("goFuncName(%q) = %q, want %q", proc, got, want)
		}
	}
}
