package translate

import (
	"errors"
	"fmt"
	"go/types"
	"io"
	"sort"

	"golang.org/x/tools/go/ssa"

	"github.com/nabetama/infer-go-proto/internal/mangle"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

// Warning is a function that could not be translated because of an
// unsupported SSA instruction (or operand). The function is emitted as a
// declare instead.
type Warning struct {
	Func  string // fn.String()
	Instr string // Go type of the instruction, e.g. "*ssa.UnOp"
	// Operand is the Go type of the unsupported operand of Instr (e.g.
	// "*ssa.Global"), or "" when the instruction itself is unsupported.
	Operand string
}

// String returns `go2sil: warning: <Func>: unsupported <Instr>`, followed by
// ` (operand <Operand>)` when Operand is set.
func (w Warning) String() string {
	s := fmt.Sprintf("go2sil: warning: %s: unsupported %s", w.Func, w.Instr)
	if w.Operand != "" {
		s += " (operand " + w.Operand + ")"
	}
	return s
}

// Translate converts the functions of pkgs into a Textual module.
//
// Defined (with a body): package-level functions, methods of the package's
// named types (including the wrappers go/ssa creates for their method sets),
// function literals, and any generic instance or go/ssa wrapper of the target
// packages that a defined function calls. Functions of other packages are
// declared, and only when a defined function calls them. The synthetic
// package initializer is not translated.
//
// A function containing an unsupported instruction is declared instead and
// a Warning line is written to warn (nil discards warnings). Two Go functions mapping to the same
// Textual name is an error.
func Translate(pkgs []*ssa.Package, warn io.Writer) (*sil.Module, error) {
	if warn == nil {
		warn = io.Discard
	}
	tr := &translator{
		targets: map[*types.Package]bool{},
		tc:      newTypeConv(),
		queued:  map[*ssa.Function]bool{},
		decls:   map[string]sil.ProcDecl{},
		warn:    warn,
	}
	var prog *ssa.Program
	for _, p := range pkgs {
		if p != nil {
			tr.targets[p.Pkg] = true
			prog = p.Prog
		}
	}
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		for _, name := range sortedMembers(p) {
			switch m := p.Members[name].(type) {
			case *ssa.Function:
				if m.Synthetic == "package initializer" {
					continue
				}
				tr.enqueue(m)
			case *ssa.Type:
				named, ok := m.Type().(*types.Named)
				if !ok || named.TypeParams().Len() > 0 || types.IsInterface(named) {
					continue
				}
				for _, t := range []types.Type{named, types.NewPointer(named)} {
					ms := prog.MethodSets.MethodSet(t)
					for i := 0; i < ms.Len(); i++ {
						if fn := prog.MethodValue(ms.At(i)); fn != nil {
							tr.enqueue(fn)
						}
					}
				}
			}
		}
	}
	for len(tr.queue) > 0 {
		fn := tr.queue[0]
		tr.queue = tr.queue[1:]
		if err := tr.function(fn); err != nil {
			return nil, err
		}
	}
	return tr.module(), nil
}

func sortedMembers(p *ssa.Package) []string {
	names := make([]string, 0, len(p.Members))
	for n := range p.Members {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type translator struct {
	targets map[*types.Package]bool
	tc      *typeConv
	names   mangle.Table
	queue   []*ssa.Function
	queued  map[*ssa.Function]bool
	decls   map[string]sil.ProcDecl
	procs   []sil.Proc
	warn    io.Writer
}

// enqueue schedules fn to be defined (or declared if it has no body or
// cannot be translated). Generic functions are skipped; their instances are
// reached through calls.
func (tr *translator) enqueue(fn *ssa.Function) {
	if tr.queued[fn] || fn.TypeParams().Len() > 0 {
		return
	}
	tr.queued[fn] = true
	tr.queue = append(tr.queue, fn)
}

// isTarget reports whether fn belongs to a target package and should be
// defined rather than declared.
func (tr *translator) isTarget(fn *ssa.Function) bool {
	p := homePkg(fn)
	return p != nil && tr.targets[p]
}

// homePkg returns the package fn belongs to. go/ssa leaves Pkg nil for
// generic instances and wrappers; for those the package of the origin, or
// of the receiver's named type, is used.
func homePkg(fn *ssa.Function) *types.Package {
	if fn.Pkg != nil {
		return fn.Pkg.Pkg
	}
	if o := fn.Origin(); o != nil && o != fn {
		return homePkg(o)
	}
	recv := fn.Signature.Recv()
	if obj, ok := fn.Object().(*types.Func); ok && recv == nil {
		recv = obj.Signature().Recv()
	}
	if recv != nil {
		t := types.Unalias(recv.Type())
		if p, ok := t.(*types.Pointer); ok {
			t = types.Unalias(p.Elem())
		}
		if n, ok := t.(*types.Named); ok {
			return n.Obj().Pkg()
		}
	}
	if fn.Object() != nil {
		return fn.Object().Pkg()
	}
	return nil
}

// function defines fn, or declares it if it has no body or contains an
// unsupported instruction.
func (tr *translator) function(fn *ssa.Function) error {
	name := mangle.Func(fn)
	if err := tr.names.Add(fn.String(), name); err != nil {
		return err
	}
	for _, anon := range fn.AnonFuncs {
		tr.enqueue(anon)
	}
	if len(fn.Blocks) == 0 {
		return tr.declare(fn)
	}
	fb := newFuncBuilder(tr, fn, name)
	proc, err := fb.build()
	var unsup *unsupportedError
	if errors.As(err, &unsup) {
		w := Warning{Func: fn.String(), Instr: unsup.instr}
		if unsup.what != unsup.instr {
			w.Operand = unsup.what
		}
		fmt.Fprintln(tr.warn, w.String())
		return tr.declare(fn)
	}
	if err != nil {
		return err
	}
	// Commit what the body refers to only once the body is known to be good.
	for _, callee := range fb.callees {
		if tr.isTarget(callee) {
			tr.enqueue(callee)
		} else if err := tr.declare(callee); err != nil {
			return err
		}
	}
	for _, d := range fb.rtDecls {
		if err := tr.declareRT(d); err != nil {
			return err
		}
	}
	tr.procs = append(tr.procs, proc)
	return nil
}

// declare adds a declare for fn with the parameter types of its signature
// (free variables first for closures, then the receiver, then the
// parameters).
func (tr *translator) declare(fn *ssa.Function) error {
	name := mangle.Func(fn)
	if err := tr.names.Add(fn.String(), name); err != nil {
		return err
	}
	var params []sil.Type
	for _, fv := range fn.FreeVars {
		params = append(params, tr.tc.Type(fv.Type()))
	}
	if r := fn.Signature.Recv(); r != nil {
		params = append(params, tr.tc.Type(r.Type()))
	}
	ps := fn.Signature.Params()
	for i := 0; i < ps.Len(); i++ {
		params = append(params, tr.tc.Type(ps.At(i).Type()))
	}
	tr.decls[name] = sil.ProcDecl{Name: name, Params: params, Ret: tr.tc.Type(fn.Signature.Results())}
	return nil
}

// declareRT adds the declare of a runtime helper (go::rt::...).
func (tr *translator) declareRT(d sil.ProcDecl) error {
	if err := tr.names.Add(d.Name, d.Name); err != nil {
		return err
	}
	tr.decls[d.Name] = d
	return nil
}

func (tr *translator) module() *sil.Module {
	m := &sil.Module{Types: tr.tc.Decls()}
	for _, d := range tr.decls {
		m.Decls = append(m.Decls, d)
	}
	sort.Slice(m.Decls, func(i, j int) bool { return m.Decls[i].Name < m.Decls[j].Name })
	m.Procs = tr.procs
	sort.Slice(m.Procs, func(i, j int) bool { return m.Procs[i].Name < m.Procs[j].Name })
	return m
}
