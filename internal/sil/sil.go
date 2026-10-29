// Package sil は、Infer の Textual (.sil) を組み立てて出力するための小さな中間表現を提供する。
package sil

// Lang は出力の先頭に書く .source_language の値。
const Lang = "c"

// Module は 1 つの .sil ファイルに対応する。
type Module struct {
	Types []TypeDecl
	Decls []ProcDecl
	Procs []Proc
}

// Type は Textual の型。"int"、"float"、"void"、"*" + 型名 をそのまま入れる。
type Type string

type TypeDecl struct {
	Name   string
	Fields []Field
}

type Field struct {
	Name string
	Type Type
}

// ProcDecl は declare 文。
type ProcDecl struct {
	Name   string
	Params []Type
	Ret    Type
}

// Proc は define 文。
type Proc struct {
	Name   string
	Params []Param
	Ret    Type
	Blocks []Block
}

type Param struct {
	Name string
	Type Type
}

// Block の Label は "#" と ":" を含まない名前。
type Block struct {
	Label  string
	Instrs []Instr
	Term   Term
}

type Instr interface{ instr() }
type Expr interface{ expr() }
type Term interface{ term() }

// Load は `Dst: Type = load Addr`。
type Load struct {
	Dst  Ident
	Type Type
	Addr Expr
}

// Store は `store Addr <- Val: Type`。
type Store struct {
	Addr Expr
	Val  Expr
	Type Type
}

// Let は `Dst = Val`。
type Let struct {
	Dst Ident
	Val Expr
}

// Prune は `prune Cond`、Neg が真なら `prune ! Cond`。
type Prune struct {
	Cond Expr
	Neg  bool
}

func (Load) instr()  {}
func (Store) instr() {}
func (Let) instr()   {}
func (Prune) instr() {}

// Ident は nX のような一時変数。
type Ident string

// Var は `&x`。
type Var struct{ Name string }

// FieldOf は `Base.Type.Field`。
type FieldOf struct {
	Base        Expr
	Type, Field string
}

// Call は `Name(Args...)`。
type Call struct {
	Name string
	Args []Expr
}

type IntConst int64

// StrConst は文字列リテラル。出力時に引用符で囲み、エスケープする。
type StrConst string

type Null struct{}

// Alloc は `__sil_allocate(<Type>)`。
type Alloc struct{ Type string }

func (Ident) expr()    {}
func (Var) expr()      {}
func (FieldOf) expr()  {}
func (Call) expr()     {}
func (IntConst) expr() {}
func (StrConst) expr() {}
func (Null) expr()     {}
func (Alloc) expr()    {}

// Ret は `ret Val`。
type Ret struct{ Val Expr }

// Jmp は `jmp L1, L2`。
type Jmp struct{ Labels []string }

func (Ret) term() {}
func (Jmp) term() {}
