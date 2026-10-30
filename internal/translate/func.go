package translate

import (
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/nabetama/infer-go-proto/internal/mangle"
	"github.com/nabetama/infer-go-proto/internal/sil"
)

// unsupportedError makes the whole function a declare. what is the Go type
// of the unsupported instruction or operand, e.g. "*ssa.TypeAssert" or
// "*ssa.Global". build sets instr to the Go type of the instruction being
// translated, so that the warning always names the instruction.
type unsupportedError struct{ what, instr string }

func (e *unsupportedError) Error() string { return "unsupported " + e.what }

func unsupported(x any) error { return &unsupportedError{what: fmt.Sprintf("%T", x)} }

// funcBuilder translates the body of one function.
//
// Every SSA value is bound to a Textual identifier nK (vals). Identifiers are
// used across blocks as they are: go/ssa guarantees that a definition
// dominates its uses, and Infer accepts identifiers defined in a dominating
// block. Parameters are Textual variables and are read with `load &name` at
// each use.
type funcBuilder struct {
	tr   *translator
	fn   *ssa.Function
	name string

	params  map[ssa.Value]string // *ssa.Parameter / *ssa.FreeVar -> variable name
	vals    map[ssa.Value]sil.Expr
	skipped map[ssa.Value]bool // values that are not translated (defer stack)
	ifConds map[*ssa.If]sil.Expr
	nextID  int

	blocks []sil.Block
	cur    *sil.Block

	callees []*ssa.Function         // statically called functions
	rtDecls map[string]sil.ProcDecl // runtime helpers used by the body
	// ifaceDecls are the declares of the interface methods the body calls,
	// by Textual name.
	ifaceDecls map[string]ifaceDecl
}

type ifaceDecl struct {
	goName string // e.g. "(pkg.Getter).Get"
	decl   sil.ProcDecl
}

func newFuncBuilder(tr *translator, fn *ssa.Function, name string) *funcBuilder {
	return &funcBuilder{
		tr:      tr,
		fn:      fn,
		name:    name,
		params:  map[ssa.Value]string{},
		vals:    map[ssa.Value]sil.Expr{},
		skipped: map[ssa.Value]bool{},
		ifConds: map[*ssa.If]sil.Expr{},
		rtDecls: map[string]sil.ProcDecl{},

		ifaceDecls: map[string]ifaceDecl{},
	}
}

func blockLabel(b *ssa.BasicBlock) string { return fmt.Sprintf("b%d", b.Index) }

func (fb *funcBuilder) build() (sil.Proc, error) {
	proc := sil.Proc{Name: fb.name, Ret: fb.tr.tc.Type(fb.fn.Signature.Results())}
	used := map[string]bool{}
	var ins []ssa.Value
	for _, fv := range fb.fn.FreeVars {
		ins = append(ins, fv)
	}
	for _, p := range fb.fn.Params {
		ins = append(ins, p)
	}
	for i, v := range ins {
		name := v.Name()
		if !isPlainVarName(name) || used[name] {
			name = fmt.Sprintf("arg$%d", i)
		}
		used[name] = true
		fb.params[v] = name
		proc.Params = append(proc.Params, sil.Param{Name: name, Type: fb.tr.tc.Type(v.Type())})
	}

	for _, b := range fb.fn.Blocks {
		fb.blocks = append(fb.blocks, sil.Block{Label: blockLabel(b)})
		fb.cur = &fb.blocks[len(fb.blocks)-1]
		for _, instr := range b.Instrs {
			if err := fb.instr(instr); err != nil {
				var unsup *unsupportedError
				if errors.As(err, &unsup) {
					unsup.instr = fmt.Sprintf("%T", instr)
				}
				return sil.Proc{}, err
			}
		}
		if fb.cur.Term == nil {
			return sil.Proc{}, fmt.Errorf("%s: block %d has no terminator", fb.fn, b.Index)
		}
	}

	// `If` becomes `jmp then, else`; each edge starts with `prune cond` or
	// `prune ! cond`. A successor with other predecessors gets a block of
	// its own for the prune, so that the prune applies only to this edge.
	// This runs after all blocks are translated because a successor may
	// come before its predecessor in fn.Blocks.
	for _, b := range fb.fn.Blocks {
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		jmp := fb.blocks[b.Index].Term.(sil.Jmp)
		for i, succ := range b.Succs {
			prune := sil.Prune{Cond: fb.ifConds[ifInstr], Neg: i == 1}
			if len(succ.Preds) == 1 {
				sb := &fb.blocks[succ.Index]
				sb.Instrs = append([]sil.Instr{prune}, sb.Instrs...)
				continue
			}
			label := fmt.Sprintf("b%d_%s", b.Index, []string{"then", "else"}[i])
			fb.blocks = append(fb.blocks, sil.Block{
				Label:  label,
				Instrs: []sil.Instr{prune},
				Term:   sil.Jmp{Labels: []string{blockLabel(succ)}},
			})
			jmp.Labels[i] = label // shares the backing array with the block's Term
		}
	}
	proc.Blocks = fb.blocks
	return proc, nil
}

var (
	plainVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	identLike    = regexp.MustCompile(`^n[0-9]+$`)
)

// textualWords are words of the Textual syntax that cannot be variable names.
var textualWords = map[string]bool{
	"declare": true, "define": true, "extends": true, "false": true, "float": true,
	"global": true, "handlers": true, "int": true, "jmp": true, "load": true,
	"local": true, "null": true, "prune": true, "ret": true, "store": true,
	"throw": true, "true": true, "type": true, "unreachable": true, "void": true,
}

// isPlainVarName reports whether a Go parameter name can be used as the
// Textual variable name as it is. Names like n0 look like Textual
// identifiers and are renamed.
func isPlainVarName(s string) bool {
	return s != "_" && plainVarName.MatchString(s) && !textualWords[s] &&
		!identLike.MatchString(s) && !strings.HasPrefix(s, "__sil_")
}

func (fb *funcBuilder) emit(in sil.Instr) { fb.cur.Instrs = append(fb.cur.Instrs, in) }

func (fb *funcBuilder) fresh() sil.Ident {
	id := sil.Ident(fmt.Sprintf("n%d", fb.nextID))
	fb.nextID++
	return id
}

// let emits `nK = e` and returns nK.
func (fb *funcBuilder) let(e sil.Expr) sil.Ident {
	id := fb.fresh()
	fb.emit(sil.Let{Dst: id, Val: e})
	return id
}

// load emits `nK: t = load addr` and returns nK.
func (fb *funcBuilder) load(t sil.Type, addr sil.Expr) sil.Ident {
	id := fb.fresh()
	fb.emit(sil.Load{Dst: id, Type: t, Addr: addr})
	return id
}

// callRT emits a call to the runtime helper go::rt::<name> and records its
// declare.
func (fb *funcBuilder) callRT(name string, params []sil.Type, ret sil.Type, args ...sil.Expr) sil.Ident {
	full := mangle.RT(name)
	fb.rtDecls[full] = sil.ProcDecl{Name: full, Params: params, Ret: ret}
	return fb.let(sil.Call{Name: full, Args: args})
}

// value returns the Textual expression for an operand.
func (fb *funcBuilder) value(v ssa.Value) (sil.Expr, error) {
	if e, ok := fb.vals[v]; ok {
		return e, nil
	}
	if fb.skipped[v] {
		return nil, unsupported(v)
	}
	switch v := v.(type) {
	case *ssa.Parameter, *ssa.FreeVar:
		name, ok := fb.params[v]
		if !ok {
			return nil, unsupported(v)
		}
		return fb.load(fb.tr.tc.Type(v.Type()), sil.Var{Name: name}), nil
	case *ssa.Const:
		return fb.constant(v)
	}
	// Globals, functions used as values, builtins, ...
	return nil, unsupported(v)
}

func (fb *funcBuilder) values(vs []ssa.Value) ([]sil.Expr, error) {
	out := make([]sil.Expr, len(vs))
	for i, v := range vs {
		e, err := fb.value(v)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

func (fb *funcBuilder) constant(c *ssa.Const) (sil.Expr, error) {
	t := c.Type()
	if st, ok := t.Underlying().(*types.Struct); ok {
		return fb.allocStruct(t, st), nil // zero value of a struct
	}
	if _, ok := t.Underlying().(*types.Array); ok {
		return fb.let(sil.Alloc{Type: tySlice}), nil // zero value of an array
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		if c.Value != nil {
			return nil, unsupported(c)
		}
		return sil.Null{}, nil // nil pointer, interface, slice, map, ...
	}
	info := b.Info()
	switch {
	case info&types.IsString != 0:
		s := ""
		if c.Value != nil {
			s = constant.StringVal(c.Value)
		}
		ts := fb.tr.tc.Type(types.Typ[types.String])
		return fb.callRT("string_const", []sil.Type{ts}, ts, sil.StrConst(s)), nil
	case info&types.IsBoolean != 0:
		if c.Value != nil && constant.BoolVal(c.Value) {
			return sil.IntConst(1), nil
		}
		return sil.IntConst(0), nil
	case info&types.IsInteger != 0:
		if c.Value == nil {
			return sil.IntConst(0), nil
		}
		if i, exact := constant.Int64Val(c.Value); exact {
			return sil.IntConst(i), nil
		}
		u, _ := constant.Uint64Val(c.Value)
		return sil.IntConst(int64(u)), nil
	case info&types.IsFloat != 0:
		if c.Value == nil {
			return sil.FloatConst(0), nil
		}
		f, _ := constant.Float64Val(constant.ToFloat(c.Value))
		return sil.FloatConst(f), nil
	case b.Kind() == types.UntypedNil || b.Kind() == types.UnsafePointer:
		return sil.Null{}, nil
	}
	return nil, unsupported(c) // complex numbers
}

// zero returns the zero value of a non-struct Textual type.
func zero(t sil.Type) sil.Expr {
	switch t {
	case "int":
		return sil.IntConst(0)
	case "float":
		return sil.FloatConst(0)
	}
	return sil.Null{}
}

// structName returns the Textual type name (without '*') of a struct type.
func (fb *funcBuilder) structName(t types.Type) string {
	return strings.TrimPrefix(string(fb.tr.tc.Type(t)), "*")
}

func structOf(t types.Type) (*types.Struct, bool) {
	st, ok := t.Underlying().(*types.Struct)
	return st, ok
}

// allocStruct allocates a zero struct of type t. Struct values are
// represented by a pointer to their storage; fields of struct type point to
// their own allocation.
func (fb *funcBuilder) allocStruct(t types.Type, st *types.Struct) sil.Ident {
	name := fb.structName(t)
	id := fb.let(sil.Alloc{Type: name})
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		addr := sil.FieldOf{Base: id, Type: name, Field: f.Name()}
		ft := fb.tr.tc.Type(f.Type())
		var v sil.Expr
		if inner, ok := structOf(f.Type()); ok {
			v = fb.allocStruct(f.Type(), inner)
		} else {
			v = zero(ft)
		}
		fb.emit(sil.Store{Addr: addr, Val: v, Type: ft})
	}
	return id
}

// copyStruct copies the fields of the struct src into dst (both pointers to
// struct storage of type t).
func (fb *funcBuilder) copyStruct(t types.Type, st *types.Struct, dst, src sil.Expr) {
	name := fb.structName(t)
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		ft := fb.tr.tc.Type(f.Type())
		d := sil.FieldOf{Base: dst, Type: name, Field: f.Name()}
		s := sil.FieldOf{Base: src, Type: name, Field: f.Name()}
		if inner, ok := structOf(f.Type()); ok {
			fb.copyStruct(f.Type(), inner, fb.load(ft, d), fb.load(ft, s))
			continue
		}
		fb.emit(sil.Store{Addr: d, Val: fb.load(ft, s), Type: ft})
	}
}

func deref(t types.Type) types.Type {
	return t.Underlying().(*types.Pointer).Elem()
}

// isDeferStack reports whether t is go/ssa's internal defer stack type.
func isDeferStack(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "deferStack" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "$ssa"
}

func (fb *funcBuilder) instr(instr ssa.Instruction) error {
	switch in := instr.(type) {
	case *ssa.DebugRef, *ssa.RunDefers:
		// RunDefers has nothing to run: Defer is not supported.
		return nil

	case *ssa.Alloc:
		elem := deref(in.Type())
		if isDeferStack(elem) {
			fb.skipped[in] = true
			return nil
		}
		if st, ok := structOf(elem); ok {
			fb.vals[in] = fb.allocStruct(elem, st)
			return nil
		}
		if _, ok := elem.Underlying().(*types.Array); ok {
			// An array is a *go::slice, so the Alloc is a **go::slice
			// pointing to new storage.
			et := fb.tr.tc.Type(elem)
			storage := fb.let(sil.Alloc{Type: tySlice})
			id := fb.let(sil.Alloc{Type: string(et)})
			fb.emit(sil.Store{Addr: id, Val: storage, Type: et})
			fb.vals[in] = id
			return nil
		}
		et := fb.tr.tc.Type(elem)
		id := fb.let(sil.Alloc{Type: string(et)})
		fb.emit(sil.Store{Addr: id, Val: zero(et), Type: et})
		fb.vals[in] = id
		return nil

	case *ssa.Store:
		if fb.skipped[in.Addr] {
			return nil
		}
		addr, err := fb.value(in.Addr)
		if err != nil {
			return err
		}
		val, err := fb.value(in.Val)
		if err != nil {
			return err
		}
		if st, ok := structOf(in.Val.Type()); ok {
			fb.copyStruct(in.Val.Type(), st, addr, val)
			return nil
		}
		fb.emit(sil.Store{Addr: addr, Val: val, Type: fb.tr.tc.Type(in.Val.Type())})
		return nil

	case *ssa.UnOp:
		x, err := fb.value(in.X)
		if err != nil {
			return err
		}
		switch in.Op {
		case token.MUL:
			if st, ok := structOf(in.Type()); ok {
				id := fb.allocStruct(in.Type(), st)
				fb.copyStruct(in.Type(), st, id, x)
				fb.vals[in] = id
				return nil
			}
			fb.vals[in] = fb.load(fb.tr.tc.Type(in.Type()), x)
		case token.NOT:
			fb.vals[in] = fb.let(sil.Call{Name: "__sil_lnot", Args: []sil.Expr{x}})
		case token.SUB:
			fb.vals[in] = fb.let(sil.Call{Name: "__sil_neg", Args: []sil.Expr{x}})
		case token.XOR:
			fb.vals[in] = fb.let(sil.Call{Name: "__sil_bnot", Args: []sil.Expr{x}})
		default:
			return unsupported(in) // channel receive
		}
		return nil

	case *ssa.FieldAddr:
		x, err := fb.value(in.X)
		if err != nil {
			return err
		}
		st := deref(in.X.Type())
		f := st.Underlying().(*types.Struct).Field(in.Field)
		addr := sil.FieldOf{Base: x, Type: fb.structName(st), Field: f.Name()}
		if _, ok := structOf(f.Type()); ok {
			// The field holds a pointer to the inner struct's storage,
			// which is the address of the inner struct.
			fb.vals[in] = fb.load(fb.tr.tc.Type(f.Type()), addr)
			return nil
		}
		fb.vals[in] = fb.let(addr)
		return nil

	case *ssa.Field:
		x, err := fb.value(in.X)
		if err != nil {
			return err
		}
		st := in.X.Type()
		f := st.Underlying().(*types.Struct).Field(in.Field)
		addr := sil.FieldOf{Base: x, Type: fb.structName(st), Field: f.Name()}
		fb.vals[in] = fb.load(fb.tr.tc.Type(f.Type()), addr)
		return nil

	case *ssa.IndexAddr:
		// All elements of a slice or array share the field go::slice.elem.
		// The index is not used.
		s, err := fb.sliceOf(in.X)
		if err != nil {
			return err
		}
		addr := sil.FieldOf{Base: s, Type: tySlice, Field: "elem"}
		elem := deref(in.Type())
		if _, ok := structOf(elem); ok {
			// Like a struct field, the element holds a pointer to the
			// struct's storage.
			fb.vals[in] = fb.load(fb.tr.tc.Type(elem), addr)
			return nil
		}
		fb.vals[in] = fb.let(addr)
		return nil

	case *ssa.Slice:
		if b, ok := in.X.Type().Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
			// A substring keeps the string as it is.
			x, err := fb.value(in.X)
			if err != nil {
				return err
			}
			fb.vals[in] = x
			return nil
		}
		// The slice shares the storage of the slice or array it is made from.
		s, err := fb.sliceOf(in.X)
		if err != nil {
			return err
		}
		fb.vals[in] = s
		return nil

	case *ssa.MakeInterface:
		x, err := fb.value(in.X)
		if err != nil {
			return err
		}
		fb.vals[in] = fb.callRT("make_iface", []sil.Type{fb.tr.tc.runtime(tyOpaque)}, fb.tr.tc.runtime(tyIface), x)
		return nil

	case *ssa.ChangeType:
		return fb.convert(in, in.X)

	case *ssa.Convert:
		return fb.convert(in, in.X)

	case *ssa.BinOp:
		return fb.binOp(in)

	case *ssa.Call:
		return fb.call(in)

	case *ssa.Extract:
		tup, err := fb.value(in.Tuple)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(string(fb.tr.tc.Type(in.Tuple.Type())), "*")
		addr := sil.FieldOf{Base: tup, Type: name, Field: fmt.Sprintf("r%d", in.Index)}
		fb.vals[in] = fb.load(fb.tr.tc.Type(in.Type()), addr)
		return nil

	case *ssa.If:
		cond, err := fb.value(in.Cond)
		if err != nil {
			return err
		}
		fb.ifConds[in] = cond
		b := in.Block()
		fb.cur.Term = sil.Jmp{Labels: []string{blockLabel(b.Succs[0]), blockLabel(b.Succs[1])}}
		return nil

	case *ssa.Jump:
		fb.cur.Term = sil.Jmp{Labels: []string{blockLabel(in.Block().Succs[0])}}
		return nil

	case *ssa.Return:
		return fb.ret(in)
	}
	return unsupported(instr)
}

// sliceOf returns the *go::slice of x, which is a slice or a pointer to an
// array. A pointer to an array is a **go::slice and is loaded once.
func (fb *funcBuilder) sliceOf(x ssa.Value) (sil.Expr, error) {
	v, err := fb.value(x)
	if err != nil {
		return nil, err
	}
	switch t := x.Type().Underlying().(type) {
	case *types.Slice:
		return v, nil
	case *types.Pointer:
		if _, ok := t.Elem().Underlying().(*types.Array); ok {
			return fb.load(fb.tr.tc.Type(t.Elem()), v), nil
		}
	}
	return nil, unsupported(x)
}

// convert translates ChangeType and Convert. A conversion to a named type T
// calls go::rt::convert$<mangle.Type(T)>, declared with T's Textual type as
// both parameter and result type, so that .inferconfig can name the
// conversion. A conversion to an unnamed type uses the value as it is,
// except between int and float, which is a __sil_cast.
func (fb *funcBuilder) convert(in ssa.Value, x ssa.Value) error {
	v, err := fb.value(x)
	if err != nil {
		return err
	}
	t := fb.tr.tc.Type(in.Type())
	target, ok := types.Unalias(in.Type()).(*types.Named)
	if !ok {
		// Infer rejects an int where a float is expected (and the other way
		// round), so numbers are cast. Other conversions between a number
		// and a pointer type (string(r)) are not translated.
		switch xt := fb.tr.tc.Type(x.Type()); {
		case xt == t:
			fb.vals[in] = v
		case isNumber(xt) && isNumber(t):
			fb.vals[in] = fb.let(sil.Cast{Type: t, Val: v})
		case isNumber(xt) || isNumber(t):
			return unsupported(in)
		default:
			fb.vals[in] = v
		}
		return nil
	}
	fb.vals[in] = fb.callRT("convert$"+mangle.Type(target), []sil.Type{t}, t, v)
	return nil
}

func isNumber(t sil.Type) bool { return t == "int" || t == "float" }

// Textual builtins for Go binary operators on int (and bool) operands.
var intBinOps = map[token.Token]string{
	token.ADD: "__sil_plusa_int", token.SUB: "__sil_minusa_int", token.MUL: "__sil_mult_int",
	token.QUO: "__sil_divi", token.REM: "__sil_mod",
	token.AND: "__sil_band", token.OR: "__sil_bor", token.XOR: "__sil_bxor",
	token.SHL: "__sil_shiftlt", token.SHR: "__sil_shiftrt",
}

var floatBinOps = map[token.Token]string{
	token.ADD: "__sil_plusa", token.SUB: "__sil_minusa", token.MUL: "__sil_mult",
	token.QUO: "__sil_divf",
}

// Comparisons. Pointers, interfaces and strings are compared by identity.
var cmpOps = map[token.Token]string{
	token.EQL: "__sil_eq", token.NEQ: "__sil_ne",
	token.LSS: "__sil_lt", token.LEQ: "__sil_le", token.GTR: "__sil_gt", token.GEQ: "__sil_ge",
}

func (fb *funcBuilder) binOp(in *ssa.BinOp) error {
	xt := in.X.Type().Underlying()
	var name string
	switch {
	case cmpOps[in.Op] != "":
		if _, ok := xt.(*types.Struct); ok {
			return unsupported(in) // struct comparison
		}
		if _, ok := xt.(*types.Array); ok {
			return unsupported(in)
		}
		name = cmpOps[in.Op]
	default:
		b, ok := xt.(*types.Basic)
		if !ok {
			return unsupported(in)
		}
		if b.Info()&types.IsString != 0 && in.Op == token.ADD {
			return fb.stringConcat(in)
		}
		switch info := b.Info(); {
		case info&types.IsInteger != 0:
			name = intBinOps[in.Op]
		case info&types.IsFloat != 0:
			name = floatBinOps[in.Op]
		}
		if name == "" && in.Op != token.AND_NOT {
			return unsupported(in) // complex numbers
		}
	}
	x, err := fb.value(in.X)
	if err != nil {
		return err
	}
	y, err := fb.value(in.Y)
	if err != nil {
		return err
	}
	if in.Op == token.AND_NOT {
		if b, ok := xt.(*types.Basic); !ok || b.Info()&types.IsInteger == 0 {
			return unsupported(in)
		}
		y = fb.let(sil.Call{Name: "__sil_bnot", Args: []sil.Expr{y}})
		name = "__sil_band"
	}
	fb.vals[in] = fb.let(sil.Call{Name: name, Args: []sil.Expr{x, y}})
	return nil
}

func (fb *funcBuilder) stringConcat(in *ssa.BinOp) error {
	x, err := fb.value(in.X)
	if err != nil {
		return err
	}
	y, err := fb.value(in.Y)
	if err != nil {
		return err
	}
	ts := fb.tr.tc.Type(in.Type())
	fb.vals[in] = fb.callRT("string_concat", []sil.Type{ts, ts}, ts, x, y)
	return nil
}

// invoke translates a call of an interface method. The callee is not
// resolved: it is a declare named <mangle.Type(interface)>$<method> whose
// first parameter is the interface value.
func (fb *funcBuilder) invoke(in *ssa.Call) error {
	cc := in.Common()
	iface, ok := types.Unalias(cc.Value.Type()).(*types.Named)
	if !ok {
		return unsupported(in) // method of an unnamed interface type
	}
	recv, err := fb.value(cc.Value)
	if err != nil {
		return err
	}
	args, err := fb.values(cc.Args)
	if err != nil {
		return err
	}
	tc := fb.tr.tc
	sig := cc.Signature()
	params := []sil.Type{tc.Type(iface)}
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, tc.Type(sig.Params().At(i).Type()))
	}
	name := mangle.Type(iface) + "$" + cc.Method.Name()
	fb.ifaceDecls[name] = ifaceDecl{
		goName: "(" + iface.String() + ")." + cc.Method.Name(),
		decl:   sil.ProcDecl{Name: name, Params: params, Ret: tc.Type(sig.Results())},
	}
	fb.vals[in] = fb.let(sil.Call{Name: name, Args: append([]sil.Expr{recv}, args...)})
	return nil
}

func (fb *funcBuilder) call(in *ssa.Call) error {
	cc := in.Common()
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		switch b.Name() {
		case "ssa:deferstack":
			fb.skipped[in] = true
			return nil
		case "ssa:wrapnilchk":
			// Returns its first argument (or panics on nil).
			x, err := fb.value(cc.Args[0])
			if err != nil {
				return err
			}
			fb.vals[in] = x
			return nil
		}
		return unsupported(in) // len, append, ...
	}
	if cc.IsInvoke() {
		return fb.invoke(in)
	}
	callee := cc.StaticCallee()
	if callee == nil {
		return unsupported(in)
	}
	if _, ok := cc.Value.(*ssa.Function); !ok {
		return unsupported(cc.Value) // closure call
	}
	args, err := fb.values(cc.Args)
	if err != nil {
		return err
	}
	fb.callees = append(fb.callees, callee)
	fb.vals[in] = fb.let(sil.Call{Name: mangle.Func(callee), Args: args})
	return nil
}

func (fb *funcBuilder) ret(in *ssa.Return) error {
	res, err := fb.values(in.Results)
	if err != nil {
		return err
	}
	switch len(res) {
	case 0:
		fb.cur.Term = sil.Ret{Val: sil.Null{}}
	case 1:
		fb.cur.Term = sil.Ret{Val: res[0]}
	default:
		tt := fb.tr.tc.Type(fb.fn.Signature.Results())
		name := strings.TrimPrefix(string(tt), "*")
		tup := fb.let(sil.Alloc{Type: name})
		results := fb.fn.Signature.Results()
		for i, r := range res {
			fb.emit(sil.Store{
				Addr: sil.FieldOf{Base: tup, Type: name, Field: fmt.Sprintf("r%d", i)},
				Val:  r,
				Type: fb.tr.tc.Type(results.At(i).Type()),
			})
		}
		fb.cur.Term = sil.Ret{Val: tup}
	}
	return nil
}
