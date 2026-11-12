package translate

import (
	"fmt"

	"github.com/nabetama/infer-go-proto/internal/mangle"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

// A model is a Textual body for a function of another package. It is
// emitted as a define in place of the declare that the function would
// otherwise get, for functions where a declare does not carry taint the way
// the Go function does.
//
// A model returns its define and the declares of the runtime helpers it
// calls.
type model func(tc *typeConv) (sil.Proc, []sil.ProcDecl)

// models is keyed by mangle.Func of the modelled function.
var models = map[string]model{
	"fmt::Sprintf": sprintfModel,
}

// sprintfModel models fmt.Sprintf(format string, a ...any) string.
//
// Infer passes taint from the arguments of a declare to its return value,
// but not from a field of a pointer argument, so a declare of fmt.Sprintf
// would lose the taint of the variadic arguments (stored in
// args.go::slice.elem). The model reads that field and joins it to format
// with go::rt::string_concat. When args is nil (fmt.Sprintf with no
// variadic arguments), it returns format and does not read the field.
//
//	define fmt::Sprintf(format: *go::string, args: *go::slice) : *go::string {
//	  #b0:
//	    n0: *go::string = load &format
//	    n1: *go::slice = load &args
//	    n2 = __sil_ne(n1, null)
//	    jmp b1, b2
//	  #b1:
//	    prune n2
//	    n3: *go::opaque = load n1.go::slice.elem
//	    n4 = go::rt::string_concat(n0, n3)
//	    ret n4
//	  #b2:
//	    prune ! n2
//	    ret n0
//	}
func sprintfModel(tc *typeConv) (sil.Proc, []sil.ProcDecl) {
	ts := tc.runtime(tyString)
	tsl := tc.runtime(tySlice)
	topq := tc.runtime(tyOpaque)
	concat := sil.ProcDecl{Name: mangle.RT("string_concat"), Params: []sil.Type{ts, ts}, Ret: ts}
	n := func(i int) sil.Ident { return sil.Ident(fmt.Sprintf("n%d", i)) }
	proc := sil.Proc{
		Name:   "fmt::Sprintf",
		Params: []sil.Param{{Name: "format", Type: ts}, {Name: "args", Type: tsl}},
		Ret:    ts,
		Blocks: []sil.Block{
			{
				Label: "b0",
				Instrs: []sil.Instr{
					sil.Load{Dst: n(0), Type: ts, Addr: sil.Var{Name: "format"}},
					sil.Load{Dst: n(1), Type: tsl, Addr: sil.Var{Name: "args"}},
					sil.Let{Dst: n(2), Val: sil.Call{Name: "__sil_ne", Args: []sil.Expr{n(1), sil.Null{}}}},
				},
				Term: sil.Jmp{Labels: []string{"b1", "b2"}},
			},
			{
				Label: "b1",
				Instrs: []sil.Instr{
					sil.Prune{Cond: n(2)},
					sil.Load{Dst: n(3), Type: topq, Addr: sil.FieldOf{Base: n(1), Type: tySlice, Field: "elem"}},
					sil.Let{Dst: n(4), Val: sil.Call{Name: concat.Name, Args: []sil.Expr{n(0), n(3)}}},
				},
				Term: sil.Ret{Val: n(4)},
			},
			{
				Label:  "b2",
				Instrs: []sil.Instr{sil.Prune{Cond: n(2), Neg: true}},
				Term:   sil.Ret{Val: n(0)},
			},
		},
	}
	return proc, []sil.ProcDecl{concat}
}
