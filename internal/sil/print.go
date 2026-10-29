package sil

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// WriteTo は m を Textual として w に書く。型宣言、declare、define の順に出力する。
// 同じ名前の型宣言と declare は、内容が同じなら最初の 1 つだけを出力し、
// 内容が異なればエラーを返す。
func (m *Module) WriteTo(w io.Writer) (int64, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, ".source_language = %q\n", Lang)

	seenType := map[string]TypeDecl{}
	var types []string
	for _, t := range m.Types {
		if prev, ok := seenType[t.Name]; ok {
			if !reflect.DeepEqual(prev, t) && !(len(prev.Fields) == 0 && len(t.Fields) == 0) {
				return 0, fmt.Errorf("sil: type %s declared twice with different fields", t.Name)
			}
			continue
		}
		seenType[t.Name] = t
		fs := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			fs[i] = f.Name + ": " + string(f.Type)
		}
		types = append(types, fmt.Sprintf("type %s = {%s}", t.Name, joinFields(fs)))
	}
	writeSection(&sb, types)

	seenDecl := map[string]ProcDecl{}
	var decls []string
	for _, d := range m.Decls {
		if prev, ok := seenDecl[d.Name]; ok {
			if !(len(prev.Params) == 0 && len(d.Params) == 0 && prev.Ret == d.Ret) && !reflect.DeepEqual(prev, d) {
				return 0, fmt.Errorf("sil: declare %s declared twice with different signatures", d.Name)
			}
			continue
		}
		seenDecl[d.Name] = d
		ps := make([]string, len(d.Params))
		for i, p := range d.Params {
			ps[i] = string(p)
		}
		decls = append(decls, fmt.Sprintf("declare %s(%s) : %s", d.Name, strings.Join(ps, ", "), d.Ret))
	}
	writeSection(&sb, decls)

	for _, p := range m.Procs {
		sb.WriteString("\n")
		ps := make([]string, len(p.Params))
		for i, a := range p.Params {
			ps[i] = a.Name + ": " + string(a.Type)
		}
		fmt.Fprintf(&sb, "define %s(%s) : %s {\n", p.Name, strings.Join(ps, ", "), p.Ret)
		for _, b := range p.Blocks {
			fmt.Fprintf(&sb, "  #%s:\n", b.Label)
			for _, in := range b.Instrs {
				fmt.Fprintf(&sb, "    %s\n", instrString(in))
			}
			if b.Term != nil {
				fmt.Fprintf(&sb, "    %s\n", termString(b.Term))
			}
		}
		sb.WriteString("}\n")
	}

	bw := bufio.NewWriter(w)
	n, err := bw.WriteString(sb.String())
	if err == nil {
		err = bw.Flush()
	}
	return int64(n), err
}

func joinFields(fs []string) string {
	if len(fs) == 0 {
		return ""
	}
	return " " + strings.Join(fs, "; ") + " "
}

func writeSection(sb *strings.Builder, lines []string) {
	if len(lines) == 0 {
		return
	}
	sb.WriteString("\n")
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
}

func instrString(in Instr) string {
	switch v := in.(type) {
	case Load:
		return fmt.Sprintf("%s: %s = load %s", v.Dst, v.Type, exprString(v.Addr))
	case Store:
		return fmt.Sprintf("store %s <- %s: %s", exprString(v.Addr), exprString(v.Val), v.Type)
	case Let:
		return fmt.Sprintf("%s = %s", v.Dst, exprString(v.Val))
	case Prune:
		if v.Neg {
			return "prune ! " + exprString(v.Cond)
		}
		return "prune " + exprString(v.Cond)
	}
	panic(fmt.Sprintf("sil: unknown instruction %T", in))
}

func termString(t Term) string {
	switch v := t.(type) {
	case Ret:
		return "ret " + exprString(v.Val)
	case Jmp:
		return "jmp " + strings.Join(v.Labels, ", ")
	}
	panic(fmt.Sprintf("sil: unknown terminator %T", t))
}

func exprString(e Expr) string {
	switch v := e.(type) {
	case Ident:
		return string(v)
	case Var:
		return "&" + v.Name
	case FieldOf:
		return fmt.Sprintf("%s.%s.%s", exprString(v.Base), v.Type, v.Field)
	case Call:
		as := make([]string, len(v.Args))
		for i, a := range v.Args {
			as[i] = exprString(a)
		}
		return fmt.Sprintf("%s(%s)", v.Name, strings.Join(as, ", "))
	case IntConst:
		return strconv.FormatInt(int64(v), 10)
	case StrConst:
		return strconv.Quote(string(v))
	case Null:
		return "null"
	case Alloc:
		return fmt.Sprintf("__sil_allocate(<%s>)", v.Type)
	}
	panic(fmt.Sprintf("sil: unknown expression %T", e))
}
