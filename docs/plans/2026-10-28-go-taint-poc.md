# Go コードを Infer の taint 解析にかける試作 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go のコードを Textual（`.sil`）に変換するツール `go2sil` を作り、Infer v1.3.0 の Pulse taint 解析で「RPC ハンドラの入力が bun の SQL 文字列に届く流れ」を検出できることを、自動テストで示す。

**Architecture:** `go/packages` で Go のコードを読み込み、`go/ssa` を `NaiveForm | InstantiateGenerics` で組み立てる。`NaiveForm` にするとローカル変数が `Alloc`/`Store`/`UnOp(*)` で表され、Phi が出ない。SSA を小さな中間表現（`internal/sil`）に変換して `.sil` として出力する。入力元と危険な関数はリポジトリ直下の `.inferconfig` に書く。

**Tech Stack:** Go 1.27、`golang.org/x/tools/go/{packages,ssa}`、Infer v1.3.0（公式バイナリ）。サンプルには `connectrpc.com/connect`、`google.golang.org/grpc`、`github.com/uptrace/bun` を使い、コード生成には buf を使う。

**Spec:** `docs/design/2026-10-28-go-taint-poc-design.md`

## Global Constraints

- リポジトリは公開する。特定の会社・プロダクトのコード、パス、構成、名前をサンプルやドキュメントに持ち込まない。
- Infer 本体のソースは変更しない。Infer バイナリはリポジトリにコミットしない（`.tools/` に置き、`.gitignore` に入れる）。
- 報告は、`go2sil` が Go のコードから生成した `.sil` を Infer が解析した結果でなければならない。サンプル用の `.sil` を手で書いて差し替えてはいけない（Task 1 の、Infer の挙動を確かめるための `.sil` は例外）。
- Infer を呼ぶテストは、Infer が見つからなければ `t.Skip` する。Infer の場所は環境変数 `INFER_BIN`、なければ `.tools/infer/bin/infer`、なければ `PATH` 上の `infer` の順に探す。
- 変換できない SSA 命令があったら、その関数は本体のない宣言として出力し、標準エラーに `go2sil: warning: <関数の Go 名>: unsupported <命令の型>` を 1 行出す。処理全体は止めない。
- 作業の経過は `docs/worklog/YYYY-MM-DD.md` に日ごとに残す。設計の変更は設計ドキュメントに追記する。

## Review Focus

1. **変換に失敗した関数があるのに、安全な側のテストが通ってしまう。** 警告が 1 件でも出たら e2e テストを失敗させる（Task 8）。
2. **生成コードの getter（`GetName()`）の中の nil チェックの分岐で taint が途切れる。** protobuf の getter には `if x != nil` がある。getter を経由する流れを e2e のサンプルに含める（Task 8 の `connectsvc`）。
3. **パッケージパスの `.` や `-` を `_` に置き換えたことによる名前の衝突。** `example.com/a-b` と `example.com/a_b` が同じ名前になる。衝突を見つけたら変換を失敗させ、テストする（Task 2）。
4. **ジェネリクスの型を展開した関数と、型引数ごとの名前。** `connect.Request[A]` と `connect.Request[B]` が別の型名になることをテストする（Task 2）。
5. **値レシーバのメソッドとポインタレシーバのメソッド。** `go/ssa` は値レシーバのメソッドについて、ポインタ用のラッパーを作る。ラッパーも本体つきで変換され、警告が出ないことをテストする（Task 5）。

---

## ファイル構成

```
go.mod                                  module github.com/nabetama/infer-go-proto
.gitignore                              .tools/ infer-out/ を無視
.inferconfig                            taint の入力元・危険な関数（Task 8）
scripts/install-infer.sh                Infer v1.3.0 を .tools/infer に展開（Task 1）
cmd/go2sil/main.go                      CLI（Task 7）
internal/infertest/infertest.go         テストから Infer を呼んで report.json を読む（Task 1）
internal/infertest/probe_test.go        Infer の挙動を確かめるテスト（Task 1）
internal/infertest/testdata/probe/*.sil 上のテストで使う手書きの .sil（Task 1）
internal/mangle/mangle.go               Go の名前 → Textual の名前（Task 2）
internal/sil/sil.go, print.go           Textual の中間表現と出力（Task 3）
internal/translate/load.go              packages + ssa の読み込み（Task 4）
internal/translate/types.go             型の変換と型宣言（Task 4）
internal/translate/func.go              関数本体の変換（Task 5, 6）
internal/translate/models.go            本体のない関数・モデルの出力（Task 6）
examples/proto/user/v1/user.proto       サンプルの RPC 定義（Task 8）
examples/buf.yaml, buf.gen.yaml         コード生成の設定（Task 8）
examples/gen/...                        生成コード（コミットする）（Task 8）
examples/connectsvc/handler.go          connect のハンドラ（Task 8）
examples/grpcsvc/server.go              gRPC のハンドラ（Task 8）
examples/userrepo/repo.go               repository 層（Task 8）
e2e/e2e_test.go                         go2sil → infer → 報告の確認（Task 8）
```

---

### Task 1: Infer を用意し、Infer の挙動をテストで確かめる

設計ドキュメントで「確かめていない」とした Infer の挙動を、手書きの `.sil` で確かめる。結果は後の Task の前提になるので、設計ドキュメントに追記する。

**Files:**
- Create: `go.mod`, `.gitignore`, `scripts/install-infer.sh`
- Create: `internal/infertest/infertest.go`
- Create: `internal/infertest/probe_test.go`, `internal/infertest/testdata/probe/*.sil`
- Modify: `docs/design/2026-10-28-go-taint-poc-design.md`（「Infer で確認済みのこと」に追記）

**Interfaces:**
- Produces:
  - `infertest.Bin(t testing.TB) string`: Infer の場所を Global Constraints の順で探す。見つからなければ `t.Skip`。
  - `infertest.Run(t testing.TB, silFiles []string, inferconfig string) []infertest.Issue`: 一時ディレクトリで `infer --capture-textual <files...>` と `infer analyze --pulse-only` を実行する（`inferconfig` は一時ディレクトリに `.inferconfig` として書く）。`infer-out/report.json` を読んで返す。
  - `type Issue struct { BugType string \`json:"bug_type"\`; Procedure string \`json:"procedure"\`; Qualifier string \`json:"qualifier"\`; File string \`json:"file"\` }`

- [ ] **Step 1: `scripts/install-infer.sh` を書き、実行する**

`https://github.com/facebook/infer/releases/download/v1.3.0/infer-<osx-arm64|linux-x86_64>-v1.3.0.tar.xz` を `uname` で選んでダウンロードし、`.tools/infer` に展開する。
Run: `scripts/install-infer.sh && .tools/infer/bin/infer --version`
Expected: `Infer version v1.3.0` を含む出力。

- [ ] **Step 2: `infertest.Run` と、最も単純な挙動のテストを書く**

`testdata/probe/direct.sil` は、`.source_language` を除いて Infer の `sil/pulse/basic.sil` と同じ形にする（`__taint_source()` の戻り値を `__taint_sink` に渡す）。

```go
// 言語ごとに、入力元から危険な関数への直接の流れが報告されるか
func TestProbeSourceLanguage(t *testing.T) {
	for _, lang := range []string{"c", "java", "rust", "hack", "python", "swift"} {
		t.Run(lang, func(t *testing.T) {
			issues := Run(t, []string{writeWithLang(t, "testdata/probe/direct.sil", lang)}, directConfig)
			t.Logf("%s: %v", lang, issues)
		})
	}
}
```

このテストは結果を記録するだけで、失敗させない。

- [ ] **Step 3: 実行して、`.source_language` を決める**

Run: `go test ./internal/infertest -run TestProbeSourceLanguage -v`
`TAINT_ERROR` が報告された言語の中から、ポインタと構造体の扱いが Go に近いもの（c → rust → swift の順に優先）を選ぶ。選んだ言語と理由を設計ドキュメントに追記する。以降の Task では、この言語を `sil.Lang` 定数として使う。
`TestProbeSourceLanguage` は、選んだ言語で `TAINT_ERROR` が 1 件出ることを確かめるテストに書き換える。

- [ ] **Step 4: 残りの挙動を確かめるテストを書く**

それぞれ、報告が出るべき `.sil` と、出るべきでない `.sil` を対にして用意する。期待どおりにならなかった場合は、テストの期待値を実際の挙動に合わせる。そのうえで、後の Task でどう扱うかを設計ドキュメントに書く（表を参照）。

| テスト名 | 確かめること | 期待どおりでなかった場合の扱い |
|---|---|---|
| `TestProbeFormalByType` | 入力元を `{"procedure_regex": ".*", "taint_target": ["ArgumentsMatchingTypes", ["Req"]]}` とすると、`*Req` 型の引数を持つ関数でその引数に taint が付き、危険な関数で報告される | `procedure_regex` でハンドラの名前に一致させ、`AllArguments` を使う方式に切り替える。設計ドキュメントの「引数の型で決める」の実現方法として記録する |
| `TestProbeFieldOfTaintedPointer` | taint の付いた `*Req` から `load n0.Req.Name` で読んだ値が危険な関数で報告される | `ArgumentsMatchingTypes` の代わりに `FieldsOfValue` を試す |
| `TestProbeUnknownCallPropagates` | 本体のない宣言 `declare concat(*S, *S): *S` に taint の付いた値を渡すと、戻り値に taint が伝わる | `models.go` で、引数を戻り値に返す本体つきのモデルを出力する（Task 6） |
| `TestProbeUnknownCallThroughField` | `store n0.Slice.elem <- tainted` した `*Slice` を本体のない宣言に渡すと、戻り値に taint が伝わる | `fmt.Sprintf` 用に、`args.elem` を読んで戻り値に流す本体つきのモデルを出力する（Task 6） |
| `TestProbeArgPositionWithReceiver` | `define Q.Where(this: *Q, query: *S, args: *Slice)` を危険な関数に指定するとき、`query` が `["ArgumentPositions", [1]]` で指定できる（0 が `this`） | 実際の番号を記録し、Task 8 の `.inferconfig` で使う |
| `TestProbeArgsNotSink` | 上の `Where` で、taint の付いた値を `args` 側だけに入れると報告されない | 報告される場合は、設計の「`args` は報告しない」が実現できない。作業を止めてユーザーに相談する |

- [ ] **Step 5: すべて通ることを確かめる**

Run: `go test ./internal/infertest -v`
Expected: PASS（Infer がない環境では SKIP）

- [ ] **Step 6: 記録してコミットする**

設計ドキュメントの「Infer で確認済みのこと」に、各テストの結果と、後の Task での扱いを書く。作業ログにも書く。

```bash
git add go.mod .gitignore scripts internal/infertest docs
git commit -m "test: Infer v1.3.0 の taint 解析の挙動を確かめるテストを追加"
```

---

### Task 2: Go の名前を Textual の名前に変換する

**Files:**
- Create: `internal/mangle/mangle.go`, `internal/mangle/mangle_test.go`

**Interfaces:**
- Produces:
  - `mangle.Pkg(path string) string`: パッケージパスを Textual の識別子にする。`/` → `::`、`.` と `-` → `_`。
  - `mangle.Type(t types.Type) string`: 名前付きの型を識別子にする。`Pkg(パッケージ) + "::" + 型名`。型引数があれば、`$` で囲んで `,` の代わりに `$` でつなぐ。
  - `mangle.Func(fn *ssa.Function) (class, name string)`: パッケージの関数は `class = Pkg(パッケージ)`、メソッドは `class = Type(レシーバの名前付きの型)`（ポインタは外す）、`name` は Go の名前。関数リテラルは `name = 親の name + "$" + 番号`（`go/ssa` の `func1` などをそのまま使う）。
  - `type Table struct{...}`、`(*Table).Add(goName, silName string) error`: 異なる Go 名が同じ Textual 名になったら error を返す。

- [ ] **Step 1: テストを書く**

```go
func TestPkg(t *testing.T) {
	assert(t, Pkg("github.com/uptrace/bun"), "github_com::uptrace::bun")
	assert(t, Pkg("connectrpc.com/connect"), "connectrpc_com::connect")
	assert(t, Pkg("fmt"), "fmt")
	assert(t, Pkg("example.com/a-b/v1"), "example_com::a_b::v1")
}
func TestTypeGeneric(t *testing.T) {
	// connect.Request[pb.GetUserRequest] と connect.Request[pb.ListUsersRequest]
	assert(t, Type(reqOf("GetUserRequest")),
		"connectrpc_com::connect::Request$example_com::x::pb::GetUserRequest$")
	assertNotEqual(t, Type(reqOf("GetUserRequest")), Type(reqOf("ListUsersRequest")))
}
func TestFuncMethod(t *testing.T) {
	// (*bun.SelectQuery).Where → class "github_com::uptrace::bun::SelectQuery", name "Where"
}
func TestTableCollision(t *testing.T) {
	var tb Table
	if err := tb.Add("example.com/a-b.F", "example_com::a_b.F"); err != nil { t.Fatal(err) }
	if err := tb.Add("example.com/a_b.F", "example_com::a_b.F"); err == nil { t.Fatal("want collision error") }
}
```

`reqOf` は、`go/types` で `connect.Request` に相当する型を組み立てるテスト用の関数。本物の connect を読み込まず、`types.NewPackage("connectrpc.com/connect", "connect")` などで作る。

- [ ] **Step 2: 失敗することを確かめる** — Run: `go test ./internal/mangle` → FAIL（未定義）
- [ ] **Step 3: 実装する**
- [ ] **Step 4: 通ることを確かめる** — Run: `go test ./internal/mangle` → PASS
- [ ] **Step 5: コミットする** — `git commit -m "feat: Go の名前を Textual の識別子に変換する"`

---

### Task 3: Textual の中間表現と出力

**Files:**
- Create: `internal/sil/sil.go`, `internal/sil/print.go`, `internal/sil/print_test.go`

**Interfaces:**
- Produces（`internal/sil`）:
  - `const Lang = "<Task 1 で決めた言語>"`
  - `type Module struct { Types []TypeDecl; Decls []ProcDecl; Procs []Proc }`
  - `type TypeDecl struct { Name string; Fields []Field }`、`type Field struct { Name string; Type Type }`
  - `type Type string`。`"int"`、`"float"`、`"void"`、`"*" + 名前` をそのまま入れる。
  - `type ProcDecl struct { Class, Name string; Params []Type; Ret Type }`（`declare`）
  - `type Proc struct { Class, Name string; Params []Param; Ret Type; Blocks []Block }`（`define`）、`type Param struct { Name string; Type Type }`
  - `type Block struct { Label string; Instrs []Instr; Term Term }`
  - 命令は `Instr` インタフェースの実装として `Load{Dst Ident; Type Type; Addr Expr}`、`Store{Addr Expr; Val Expr; Type Type}`、`Let{Dst Ident; Val Expr}`（`nX = 式`）、`Prune{Cond Expr; Neg bool}`
  - 式は `Expr` インタフェースの実装として `Ident`、`Var{Name}`（`&x`）、`FieldOf{Base Expr; Type, Field string}`（`n0.T.f`）、`Call{Class, Name string; Args []Expr}`、`IntConst`、`StrConst`、`Null`、`Alloc{Type string}`（`__sil_allocate(<T>)`）
  - 終端は `Term` インタフェースの実装として `Ret{Val Expr}`、`Jmp{Labels []string}`
  - `(*Module).WriteTo(w io.Writer) (int64, error)`: 先頭に `.source_language = "<Lang>"` を出す。型宣言、`declare`、`define` の順に出力する。同じ名前の型宣言と `declare` は 1 つにまとめる。

- [ ] **Step 1: テストを書く**

`print_test.go` で、Infer の `sil/pulse/npe.sil` の `basic_bad` 相当を `Module` で組み立てる。出力が `testdata/basic_bad.sil`（期待値として手で書く）と一致することを確かめる。Infer がある環境では、出力を `infertest.Run` に渡して `NULLPTR_DEREFERENCE` が 1 件出ることも確かめる（Textual として正しいことの確認）。

- [ ] **Step 2: 失敗を確かめる** — Run: `go test ./internal/sil` → FAIL
- [ ] **Step 3: 実装する**
- [ ] **Step 4: 通ることを確かめる** — Run: `go test ./internal/sil -v` → PASS
- [ ] **Step 5: コミットする** — `git commit -m "feat: Textual の中間表現と出力を追加"`

---

### Task 4: Go のコードの読み込みと型の変換

**Files:**
- Create: `internal/translate/load.go`, `internal/translate/types.go`, `internal/translate/types_test.go`
- Create: `internal/translate/testdata/src/structs/structs.go`（テスト用の小さな Go パッケージ。`testdata` の `go.mod` は作らず、リポジトリの module 配下として読み込む）

**Interfaces:**
- Consumes: `mangle.Type`、`sil.TypeDecl`
- Produces:
  - `translate.Load(patterns []string, dir string) (*ssa.Program, []*ssa.Package, error)`: `packages.Load` を `NeedName|NeedFiles|NeedTypes|NeedSyntax|NeedTypesInfo|NeedDeps|NeedImports` で呼ぶ。`ssautil.AllPackages` に `ssa.NaiveForm | ssa.InstantiateGenerics` を指定し、`prog.Build()` する。読み込みエラーがあれば error を返す。
  - `type typeConv struct{...}`、`(*typeConv).Type(t types.Type) sil.Type`、`(*typeConv).Decls() []sil.TypeDecl`

型の対応は次のとおり。
- 整数・bool → `int`、浮動小数 → `float`
- `string` → `*go::string`（中身は空の構造体として宣言する）。
- 名前付きの struct `T` → `*<mangle.Type(T)>`。参照された struct はパッケージの外のものも、`go/types` から取ったフィールドすべて（非公開のものを含む）で宣言する。
- その他の struct・ポインタ → 中身の型へのポインタ。
- interface → `*go::iface`、slice → `*go::slice`、map・chan・func → `*go::opaque`。
- `go::slice` は `{ elem: *go::opaque }` の 1 フィールドで宣言する。すべての要素をこの 1 つのフィールドにまとめて扱う。
- 複数の戻り値 → `*go::tuple<N>`（フィールド `r0`..`r<N-1>`）

- [ ] **Step 1: テストを書く**

`TestTypeDecls`: `testdata/src/structs` に `type User struct { Name string; next *User; Tags []string }` を置く。`User` を変換すると `*example...::structs::User` になること、`Decls()` に `Name: *go::string`、`next: *...::User`、`Tags: *go::slice` の 3 フィールドが入ることを確かめる。
`TestTypeExternalGeneric`: `connect.Request[T]` を参照するコードを読み込み、`Msg` フィールドを持つ型宣言が出ることを確かめる。

- [ ] **Step 2〜4: 失敗 → 実装 → 通過** — Run: `go test ./internal/translate -run TestType -v`
- [ ] **Step 5: コミットする** — `git commit -m "feat: Go の型を Textual の型に変換する"`

---

### Task 5: 関数本体の変換（制御フロー・メモリ・静的な呼び出し）

**Files:**
- Create: `internal/translate/func.go`, `internal/translate/func_test.go`
- Create: `internal/translate/translate.go`（パッケージ全体をまとめて `sil.Module` にする）
- Create: `internal/translate/testdata/src/flow/flow.go`

**Interfaces:**
- Consumes: Task 2〜4 の全部
- Produces:
  - `translate.Translate(pkgs []*ssa.Package, warn io.Writer) (*sil.Module, error)`: 対象パッケージの関数（メソッド、関数リテラル、`go/ssa` が作るラッパーを含む）を `define` にする。対象外のパッケージの関数は、呼ばれているものだけを `declare` にする。名前の衝突は error にする。
  - `type Warning struct { Func string; Instr string }` を `warn` に Global Constraints の形式で出力する。

この Task で変換する SSA 命令: `Alloc`、`Store`、`UnOp`（`*` のみ）、`FieldAddr`、`Field`、`Const`（数値、文字列、nil）、`BinOp`（比較と、数値の算術）、`If`、`Jump`、`Return`、`Call`（静的な呼び出し）、`Extract`。
- `Alloc` は `__sil_allocate(<T>)`。`NaiveForm` のローカル変数も同じにする。
- 文字列の定数は、`go::rt.string_const("...")` の呼び出しで `*go::string` を作る。Textual の文字列定数を直接使うかどうかは、Task 1 の結果を見て決める。
- 複数の戻り値の `Return` は `go::tuple<N>` を確保してフィールドに入れる。`Extract` はそのフィールドを `load` する。
- `If` は、Infer の `npe.sil` と同じく `jmp then, else` と、それぞれの先頭の `prune` / `prune !` で表す。

- [ ] **Step 1: テストを書く**

`testdata/src/flow/flow.go`:
```go
type T struct{ s string }
func Pick(t *T, b bool) (string, error) { if b { return t.s, nil }; return "", nil }
func Use(t *T) string { s, _ := Pick(t, true); return s }
type V struct{ n int }
func (v V) Get() int { return v.n } // 値レシーバ。ポインタ用のラッパーも作られる
func CallGet(p *V) int { return p.Get() }
```
- `TestTranslateFlowNoWarnings`: 変換して、警告が 0 件であること。`Pick`、`Use`、`V.Get`、`CallGet` と、`go/ssa` が作るラッパーが `define` として出ること。
- `TestTranslateFlowInfer`（Infer がある環境のみ）: `.sil` を出力して `infertest.Run` に渡し、Infer が構文エラーなしで capture できること（`infer capture` の終了コードが 0）。

- [ ] **Step 2〜4: 失敗 → 実装 → 通過** — Run: `go test ./internal/translate -run TestTranslateFlow -v`
- [ ] **Step 5: コミットする** — `git commit -m "feat: 関数本体の基本的な命令を変換する"`

---

### Task 6: 可変長引数・インタフェース・型への変換・モデル

**Files:**
- Modify: `internal/translate/func.go`
- Create: `internal/translate/models.go`, `internal/translate/models_test.go`
- Create: `internal/translate/testdata/src/taint/taint.go`

この Task で変換する SSA 命令: `IndexAddr`（slice 用。`go::slice.elem` のアドレスにする）、`Slice`（配列へのポインタから slice を作る。同じポインタをそのまま `*go::slice` として使う）、`MakeInterface`（値を `go::rt.make_iface(x)` に渡す）、`ChangeType` と `Convert`（変換先が名前付きの型なら `go::rt.convert$<mangle.Type(変換先)>(x)`、それ以外なら値をそのまま使う）、`Call` のうちインタフェースのメソッド呼び出し（`ssa.CallCommon.IsInvoke()`）、文字列の `BinOp ADD`（`go::rt.string_concat(a, b)`）。
- インタフェースの呼び出しは、メソッドの名前とインタフェースの型から `declare` を作って呼ぶ。呼び出し先を探すことはしない（設計の「静的に決められるものだけ」の範囲外なので）。
- 上の `go::rt` の関数は、Task 1 の `TestProbeUnknownCallPropagates` / `TestProbeUnknownCallThroughField` の結果に従って、`declare` だけにするか、本体つきのモデルにするかを決める。
- `fmt.Sprintf` は、`TestProbeUnknownCallThroughField` が期待どおりでなかった場合に限って、`args.elem` を読んで戻り値に流すモデルを `define` で出力する。

**Interfaces:**
- Produces: `.inferconfig` から参照する名前。`bun.Safe(s)` の変換の名前。`bun.Safe` は `schema.Safe` の別名なので、`go::rt.convert$github_com::uptrace::bun::schema::Safe` になる見込み。Task 8 で実際の出力を見て確かめてから使う。

- [ ] **Step 1: テストを書く**

`testdata/src/taint/taint.go` に `__source`/`__sink` の役の関数を置き、次の流れを作る。
```go
func Source() string          { return "" } // .inferconfig で戻り値を入力元にする
func Sink(q string)           {}             // .inferconfig で引数を危険な関数にする
type Q struct{}
func (q *Q) Where(query string, args ...any) *Q { return q }
func ViaSprintf()       { Sink(fmt.Sprintf("id = %s", Source())) }           // 報告する
func ViaConcat()        { Sink("id = " + Source()) }                         // 報告する
func ViaIface()         { var x any = Source(); Sink(x.(string)) }           // 型アサーションは対象外 → 警告を確かめる
func ArgsOnly(q *Q)     { q.Where("id = ?", Source()) }                     // Where の query を危険な関数にしたとき報告しない
func QueryTainted(q *Q) { q.Where("id = " + Source()) }                     // 報告する
type Safe string
func Conv()             { Sink(string(Safe(Source()))) }                    // Safe への変換を危険な関数にしたとき報告する
```
- `TestTranslateTaintWarnings`: `ViaIface` だけが `TypeAssert` の警告を出し、他の関数は警告なしで `define` されること。
- `TestTranslateTaintInfer`（Infer がある環境のみ）: `.inferconfig` で `Source` の戻り値を入力元、`Sink` と `Q.Where` の `query`（番号は Task 1 で確かめた値）と `go::rt.convert$...Safe` を危険な関数にする。報告される手続きが `ViaSprintf`、`ViaConcat`、`QueryTainted`、`Conv` のちょうど 4 つで、`ArgsOnly` が含まれないこと。

- [ ] **Step 2〜4: 失敗 → 実装 → 通過** — Run: `go test ./internal/translate -run TestTranslateTaint -v`
- [ ] **Step 5: コミットする** — `git commit -m "feat: 可変長引数・インタフェース・型変換を変換する"`

---

### Task 7: CLI `go2sil`

**Files:**
- Create: `cmd/go2sil/main.go`, `cmd/go2sil/main_test.go`

**Interfaces:**
- Consumes: `translate.Load`、`translate.Translate`、`(*sil.Module).WriteTo`
- Produces: `go2sil [-C dir] -o out.sil <package patterns...>`。`-o` を省略すると標準出力に書く。終了コードは、成功（警告あり・なしとも）なら 0、読み込みエラーや名前の衝突なら 1。

- [ ] **Step 1: テストを書く** — `TestMain` で `go build` したバイナリを、`internal/translate/testdata/src/flow` に対して実行する。終了コードが 0、出力ファイルが `.source_language` で始まり、標準エラーが空であること。存在しないパッケージを渡すと終了コードが 1 になること。
- [ ] **Step 2〜4: 失敗 → 実装 → 通過** — Run: `go test ./cmd/go2sil -v`
- [ ] **Step 5: コミットする** — `git commit -m "feat: go2sil コマンドを追加"`

---

### Task 8: 本物のライブラリを使うサンプルと e2e テスト

**Files:**
- Create: `examples/proto/user/v1/user.proto`、`examples/buf.yaml`、`examples/buf.gen.yaml`、`examples/gen/user/v1/*.go`（生成結果）
- Create: `examples/connectsvc/handler.go`、`examples/grpcsvc/server.go`、`examples/userrepo/repo.go`
- Create: `.inferconfig`、`e2e/e2e_test.go`
- Modify: `go.mod`（connect、grpc、bun、protobuf を追加）

**Interfaces:**
- Consumes: `go2sil`（Task 7）、`infertest`（Task 1）、Task 6 で確かめた `bun.Safe` の変換の名前

サンプルの RPC 定義は汎用的なもの（`UserService` の `GetUser(GetUserRequest{ string name })` と `SearchUsers(SearchUsersRequest{ string order_by })`）にする。生成には `protoc-gen-go`、`protoc-gen-go-grpc`、`protoc-gen-connect-go` を使い、生成コマンドを `examples/buf.gen.yaml` の隣の `examples/README.md` に書く。

ハンドラ（各関数に `// want: TAINT_ERROR` か `// want: none` のコメントを付ける）:

| ファイル | 関数 | 流れ | 期待 |
|---|---|---|---|
| `connectsvc/handler.go` | `GetUserBad` | `req.Msg.GetName()` を `"name = '" + x + "'"` にして `Where` の第1引数へ | 報告する |
| `connectsvc/handler.go` | `GetUserGood` | `Where("name = ?", req.Msg.GetName())` | 報告しない |
| `grpcsvc/server.go` | `SearchUsersBad` | `fmt.Sprintf("SELECT * FROM users ORDER BY %s", req.OrderBy)` を `NewRaw` の `query` へ | 報告する |
| `grpcsvc/server.go` | `SearchUsersGood` | `NewRaw("SELECT * FROM users WHERE name = ?", req.OrderBy)` | 報告しない |
| `grpcsvc/server.go` | `SearchUsersSafeBad` | `OrderExpr(string(bun.Safe(req.OrderBy)))` | 報告する |
| `connectsvc/handler.go` + `userrepo/repo.go` | `GetUserViaRepoBad` | ハンドラが `repo.FindByName(ctx, req.Msg.Name)` を呼び、repo が `Where("name = " + name)` | 報告する |

`.inferconfig`:
- `pulse-taint-sources`: Task 1 の `TestProbeFormalByType` で確かめた方式で、`connectrpc_com::connect::Request` で始まる型と、`github_com::nabetama::infer_go_proto::examples::gen::user::v1::` 配下で名前が `Request` で終わる型の引数を入力元にする。
- `pulse-taint-sinks`:
  - `github_com::uptrace::bun::SelectQuery` の `Where`、`WhereOr`、`ColumnExpr`、`OrderExpr` の query 引数
  - `github_com::uptrace::bun::DB` の `NewRaw` の query 引数
  - `database_sql::DB` の `QueryContext`、`ExecContext` の query 引数（`*sql.DB` を直接使うコード用。サンプルはないが設定には入れる）
  - Task 6 で確かめた `bun.Safe` の変換の名前の、すべての引数
  - 引数の番号は Task 1 の `TestProbeArgPositionWithReceiver` で確かめた値を使う。

- [ ] **Step 1: proto とコード生成** — Run: `cd examples && buf generate` → `examples/gen/user/v1/` に `user.pb.go`、`user_grpc.pb.go`、`userv1connect/user.connect.go` ができ、`go build ./...` が通る。
- [ ] **Step 2: ハンドラと repo を書く** — Run: `go vet ./examples/...` → 問題なし
- [ ] **Step 3: e2e テストを書く**

```go
func TestE2E(t *testing.T) {
	sil, stderr := runGo2sil(t, "./examples/...") // go2sil -o <tmp>/out.sil
	if stderr != "" {                             // Review Focus 1
		t.Fatalf("go2sil warnings:\n%s", stderr)
	}
	issues := infertest.Run(t, []string{sil}, readFile(t, "../.inferconfig"))
	got := taintProcedures(issues) // bug_type == "TAINT_ERROR" の Procedure を、Go の関数名（最後の "." の後）に直した集合
	want := wantFromComments(t, "../examples") // "// want: TAINT_ERROR" の付いた関数名の集合
	if !equal(got, want) { t.Fatalf("got %v, want %v", got, want) }
}
```

`want` は `{GetUserBad, SearchUsersBad, SearchUsersSafeBad, GetUserViaRepoBad}` になる。repo の `FindByName` 側でも報告されることがある（設計で重複を許容している）。その場合、`FindByName` は `want` に入れず、`got` から除外するリストに理由を書いて入れる。

- [ ] **Step 4: 実行する** — Run: `go test ./e2e -v` → PASS
  変換できない命令が出て失敗した場合は、Task 6 の範囲に命令を足す（`TypeAssert` などをサンプルが使うなら変換を足す）。報告されるべきものが報告されない場合は、`infer-out` の `infer debug --procedures --procedures-summary` で途切れた場所を調べ、原因と対処を作業ログに書く。
- [ ] **Step 5: コミットする** — `git commit -m "test: connect / gRPC / bun を使うサンプルで e2e テストを追加"`

---

### Task 9: 使い方と結果の記録

**Files:**
- Modify: `README.md`、`docs/design/2026-10-28-go-taint-poc-design.md`、`docs/worklog/<日付>.md`

- [ ] **Step 1: `README.md` を書く** — 何をするツールか、`scripts/install-infer.sh`、`go2sil` の使い方、`.inferconfig` の書き換え方（別のコードに当てはめるとき、生成コードのパッケージパスを変える場所）、`go test ./...` の実行方法、変換の対象外の Go の機能の一覧。
- [ ] **Step 2: 設計ドキュメントに結果を書く** — 関数名の変換規則（Task 2 の内容）、`.source_language` の選択、Task 1 で分かった Infer の挙動、成功の条件を満たしたかどうか。
- [ ] **Step 3: 確認する** — Run: `go test ./... && go vet ./...` → PASS
- [ ] **Step 4: コミットする** — `git commit -m "docs: 使い方と試作の結果を記録"`
