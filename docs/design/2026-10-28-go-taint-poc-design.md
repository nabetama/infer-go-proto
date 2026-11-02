# Go コードを Infer の taint 解析にかける試作 設計

- 作成日: 2026-10-28
- 状態: 承認済み（2026-10-28）

## 目的と背景

Go で書かれたサービスについて、外部入力を起点とするセキュリティ上の重大な不具合を、本番に出る前に静的解析で見つけたい。現時点で実際に見つかった不具合があるわけではなく、予防が目的である。

[facebook/infer](https://github.com/facebook/infer) は Go に対応していない。ただし Infer には、新しい言語を足すための中間言語 Textual（SIL をテキストで書いた形式、拡張子 `.sil`）がある。`.sil` を `infer --capture-textual` に渡せば、Pulse など既存の解析エンジンがそのまま動く。Python、Rust、Swift のフロントエンドもこの方式で作られている。

この試作では、Go のコードを `.sil` に変換するツールを Go で書き、Pulse の taint 解析で「外部入力が SQL の文字列に届く」流れを検出できるかを確かめる。

## 検出したいもの

RPC ハンドラが受け取った外部入力が、文字列連結などを経て、SQL の文字列そのものとして使われる流れを検出する。

想定する構成は、Go の RPC サービスでよく使われる次の組み合わせとする。

- 入力元: connect（`connectrpc.com/connect`）のハンドラが受け取る `*connect.Request[T]` と、gRPC（`google.golang.org/grpc`）のハンドラが受け取るリクエストメッセージ `req *pb.XxxRequest`。
- 危険な関数: bun（`github.com/uptrace/bun`）の、SQL 文字列そのものを受け取る引数。
  - `(*bun.DB).NewRaw(query string, args ...any)` の `query`
  - `(*bun.SelectQuery).Where` / `WhereOr` / `ColumnExpr` / `OrderExpr` の第1引数（クエリ文字列）
  - `bun.Safe(s string)` の `s`
  - `database/sql` の `(*sql.DB).QueryContext` / `ExecContext` のクエリ文字列引数
- 報告しないもの: プレースホルダ `?` に対応する `args` に外部入力を渡す場合。これは安全な使い方なので報告しない。

protovalidate のような proto の制約による入力検証は、文字列の長さや形式を確認するだけで、SQL インジェクションを防ぐものではない。そのため、検証を通った値も入力元として扱い続ける（Pulse の sanitizer には指定しない）。

## 成功の条件

リポジトリ内のサンプルについて、次の両方が自動テストで確認できること。

1. 外部入力が SQL 文字列に届くサンプルを、Infer が taint の問題として報告する。
2. プレースホルダを使うなど安全に書いた対のサンプルは、報告しない。

サンプルは少なくとも次の流れを含む。

- connect 風のハンドラ → `req.Msg` のフィールド → 文字列連結 → `Where` の第1引数（報告する）
- gRPC 風のハンドラ → `req` のフィールド → `fmt.Sprintf` → `NewRaw` の `query`（報告する）
- 上の 2 つで、外部入力を `args` 側に渡すように書き直したもの（報告しない）
- ハンドラから別の関数（repository 層を想定）を経由して届く流れ（報告する）。関数をまたぐ解析ができることを確かめるため。

## 作るもの

### 構成

```
Go のソース
  └─(go/packages で読み込み、go/ssa で SSA に変換)
       └─ 変換ツール（このリポジトリで作る、Go 製）
            └─ .sil ファイル
                 └─ infer --capture-textual（Infer v1.3.0 公式バイナリ）
                      └─ infer analyze（Pulse の taint 解析、.inferconfig で入力元・危険な関数を指定）
                           └─ 報告（infer-out/report.json）
```

### 決めたこと

- 変換ツールは Go で書く。Go の読み込みには `golang.org/x/tools/go/packages`、SSA への変換には `golang.org/x/tools/go/ssa` を使う。
- `connect.Request[T]` などのジェネリクスに対応するため、`go/ssa` の `InstantiateGenerics` モードを使い、具体的な型ごとに展開した関数を変換する。
- Infer は v1.3.0 の公式バイナリを使う（macOS arm64 用の `infer-osx-arm64-v1.3.0.tar.xz` と Linux x86_64 用がある）。Infer 本体のソースは変更しない。
- 入力元と危険な関数は Infer の設定ファイル（`.inferconfig` の `pulse-taint-sources` / `pulse-taint-sinks`）で指定する。
- サンプルは本物の `connectrpc.com/connect`、`google.golang.org/grpc`、`github.com/uptrace/bun` を go.mod に入れて使う（ユーザーの決定）。こうすると、`.inferconfig` に書く設定を実際のコードにもそのまま使える。`.proto` から生成するコードは buf で作り、生成結果をコミットする。これにより、テストの実行に buf は要らない（設計時の判断）。`.sil` に本体つきで変換するのはサンプルのパッケージの関数だけで、依存パッケージの関数は呼ばれるものだけ本体のない宣言にする。
- どのハンドラを入力元にするかは、引数の型で決める（ユーザーの決定）。`*connect.Request[T]` 型の引数と、gRPC 用に生成されたリクエストメッセージ型の引数を、それを受け取る関数がどれであっても入力元とする。Pulse の `taint_target` の `ArgumentsMatchingTypes` を使う想定。生成されたメッセージ型をどう見分けるか（生成コードのパッケージパスで決めるか、型名の規則で決めるか）は、作る人に任せる。ただし、`.inferconfig` の書き換えだけで別のコードにも当てはめられる形にする。ハンドラから repository 層の関数にリクエストの値がそのまま渡された場合、その関数の引数も入力元になる。重複して報告されることは許容する。
- bun、connect、gRPC、`database/sql`、`fmt` など外部パッケージの中身は解析しない。サンプルから呼ばれる関数だけを、本体を持たない宣言（Textual の `declare`）として `.sil` に出力する。
- 文字列連結や `fmt.Sprintf` は、入力の taint が戻り値に伝わる必要がある。本体を持たない宣言の taint を Pulse がどう扱うかは確認していない。伝わらない場合は、変換ツールが `.sil` に簡単なモデル（引数を戻り値に流すだけの本体）を出力する。

### Infer で確認済みのこと（2026-10-28 時点、facebook/infer main ブランチ）

- `infer/tests/codetoanalyze/sil/pulse/.inferconfig` で、`.sil` のコードに対して `pulse-taint-sources` / `pulse-taint-sinks` が使われている。
- 危険な関数の「何番目の引数が危険か」は、matcher の `taint_target` に `["ArgumentPositions", [0]]` のように書いて指定できる（`infer/src/atd/pulse_config.atd`）。
- 入力元に指定した関数を解析するとき、Pulse はその関数の引数に taint を付ける（`infer/src/pulse/PulseTaintOperations.ml` の `taint_initial`）。これを使い、ハンドラ関数を入力元に指定することで、`req` 引数を入力元として扱う。
- Textual の `.source_language` に指定できるのは C、Hack、Java、Python、Rust、Swift、ObjectiveC で、Go はない（`infer/src/textual/Textual.ml` の `Lang`）。
- 上の機能（`ArgumentPositions`、`ArgumentsMatchingTypes`、`taint_initial`、`--capture-textual`）は v1.3.0 タグのソースにもある。ただし、v1.3.0 のバイナリで実際に動かして確かめたわけではない。動かなかった場合は、main の特定のコミットをソースからビルドして使う（設計時の判断）。

#### v1.3.0 のバイナリで確かめたこと（2026-10-28、計画の Task 1）

手書きの `.sil`（`internal/infertest/testdata/probe/`）を Infer v1.3.0 の公式バイナリ（macOS arm64）で `infer capture --capture-textual` と `infer analyze --pulse-only` にかけ、`internal/infertest/probe_test.go` のテストで結果を固定した。上で必要とした機能はすべて v1.3.0 のバイナリで動いたので、main をソースからビルドする必要はない。

**`.source_language` は `c` にする。** `__taint_source()` の戻り値を `__taint_sink` に渡すだけの `.sil`（`direct.sil`）で言語ごとに試した結果は次のとおり。

- `c`、`rust`、`hack`、`swift`: `TAINT_ERROR` が 1 件報告された。
- `java`: 報告なし。
- `python`: 取り込みで失敗した（`Python frontend does not allow toplevel definitions`）。

報告された言語のうち、計画で決めた優先順（c → rust → swift）で先頭の `c` を選んだ。C はポインタと構造体を値として扱い、Go の構造体へのポインタとフィールドの読み書きをそのまま書ける。なお `swift` は `direct.sil` では報告されたが、下の他のテストの `.sil` では報告されるべきものも含めて 1 件も報告されなかった。`rust` は下のテストで `c` と同じ結果だった。

**各テストの結果と、後の Task での扱い**（`.source_language = "c"`）

- `TestProbeFormalByType`: 期待どおり。入力元を次のように書くと、`*Req` 型の引数を持つ関数を解析するときにその引数に taint が付き、その値を危険な関数に渡すと報告される。`*Plain` 型の引数は報告されない。
  ```json
  { "procedure_regex": ".*", "taint_target": ["ArgumentsMatchingTypes", ["Req"]] }
  ```
  `ArgumentsMatchingTypes` は、引数の型が「構造体へのポインタ」で、その構造体の名前（または親の型の名前）が指定した文字列を部分文字列として含むときに一致する（v1.3.0 の `PulseTaintItemMatcher.ml` の `type_matches`。`String.is_substring` で比べる）。`connectrpc_com::connect::Request$example_com::x::pb::GetUserRequest$` のような `::` と `$` を含む型名でも、`"connectrpc_com::connect::Request"` で一致した。部分文字列で比べるので、短い文字列を指定すると関係のない型にも一致する。後の Task では、パッケージを含めた長い名前で指定する。
- `TestProbeFieldOfTaintedPointer`: 期待どおり。taint の付いた `*Req` から `load n0.Req.Name` で読んだ値を危険な関数に渡すと報告される。`FieldsOfValue` は不要。
- `TestProbeUnknownCallPropagates`: 期待どおり。本体のない宣言 `declare concat(*S, *S): *S` に taint の付いた値を引数として直接渡すと、戻り値に taint が伝わる。文字列連結（`go::rt` の連結の関数など）は本体のない宣言のままでよい。
- `TestProbeUnknownCallThroughField`: 期待と異なる。`store n1.Slice.elem <- tainted` した `*Slice` を本体のない宣言 `fmt_sprintf(*S, *Slice): *S` に渡しても、戻り値に taint は伝わらない。後の Task（計画の Task 6）では、`fmt.Sprintf` 用に、`args.elem` を読んで戻り値に流す本体つきのモデルを出力する。動くことを確かめた形は次のもの（`TestProbeSprintfModel`）。本体の中で、本体のない宣言 `concat` を使って `format` と要素をつないでいる。
  ```
  define fmt_sprintf(format: *S, args: *Slice) : *S {
    #b0:
      n0: *S = load &format
      n1: *Slice = load &args
      n2: *S = load n1.Slice.elem
      n3 = concat(n0, n2)
      ret n3
  }
  ```
- `TestProbeSprintfModel` で分かった注意点: 同じ本体を `sprintf` という名前で定義すると、taint は伝わらない。`malloc`、`free`、`sprintf` のような C の標準ライブラリと同じ名前の関数は、`.sil` に定義した本体ではなく Pulse が C 用に持つモデルが使われるとみられる。後の Task では、出力する関数名を C の標準ライブラリの関数名と一致させない（パッケージの名前を前に付けた名前にすれば一致しない）。
- `TestProbeArgPositionWithReceiver`: 期待どおり。`Q.Where(this: *Q, query: *S, args: *Slice)` の `query` は `["ArgumentPositions", [1]]` で指定できる。番号は `.sil` 上の引数の並びのままで、0 番がレシーバ（`this`）になる。`Q.Where` が本体のない宣言でも本体つきの定義でも同じだった。`[0]` を指定すると `query` の taint は報告されない。
- `TestProbeArgsNotSink`: 期待どおり。taint の付いた値を `args` の要素にだけ入れた場合、`["ArgumentPositions", [1]]` では報告されない。参考として、`["ArgumentPositions", [2]]`（`args` 自身）を指定した場合は、`Q.Where` が本体つきの定義なら要素の taint が報告され、本体のない宣言なら報告されなかった。危険な引数の番号は `query` の番号だけを書く。

**関数名について分かったこと**（`TestProbeProcedureNames`）

- `.source_language = "c"`（`rust` でも同じ）では、`define Cls.m(...)` のクラスの部分が Infer の中の関数名から落ちて、`m` だけになる。
  - report.json の `procedure` は `"m"` になる（例: `define Svc.Handle(...)` は `"Handle"`）。
  - 設定の `procedure` / `procedure_regex` が照合する名前も `m` になる。`"procedure_regex": "Q\\.Where"`、`"Q::Where"`、`class_names: ["Q"]` と `method_names: ["Where"]` の組み合わせは一致せず、`"^Where$"` は一致した。
  - クラスだけが違う同じ名前のメソッド（`define a::A.get` と `define b::B.get`）を 1 回の取り込みに入れると、`infer capture` が `Creating two procdescs for the same procname.` で失敗する。
- トップレベルの名前に `::` と `$` を使った `define example_com::app::Svc$Handle(...)` は、名前がそのまま使われる。
  - report.json の `procedure` は `"example_com::app::Svc$Handle"`。トップレベルの `define f(...)` は `"f"`。
  - qualifier には、呼び出した危険な関数の名前が `` `example_com::orm::Q$Where` `` のように backquote で囲まれて入る。
  - `"procedure_regex": "^example_com::orm::Q\\$Where$"` は `example_com::orm::Q$Where` にだけ一致し、パッケージだけが違う `example_com::other::Q$Where` には一致しなかった。
- `procedure` は名前の部分文字列で比べる（`PulseTaintItemMatcher.ml` の `procedure_matches` の `String.is_substring`）。`procedure_regex` は名前の一部に一致すればよいので、名前全体に一致させるときは `^` と `$` を付ける。

このため、計画の `mangle.Func` のように Go のメソッドを `class` と `name` に分けて `define <class>.<name>` と出力すると、パッケージやレシーバの型が名前から落ち、同じ名前のメソッドが 2 つあると取り込みが失敗する。後の Task では、関数もメソッドも、パッケージとレシーバの型を含めた 1 つのトップレベルの名前（例: `example_com::orm::Q$Where`）で出力する。具体的な規則は「関数名の変換規則」に追記する。

#### 関数本体の変換で確かめたこと（2026-10-29、計画の Task 5）

- `infer capture --capture-textual` は、`.sil` に構文エラー、型の不一致、宣言のない関数の呼び出しがあっても終了コード 0 を返し、エラーを出力に書くだけである。テストでは終了コードに加えて出力に `error` / `Error` が含まれないことを確かめる（`infertest.Capture`、`infertest.Run`）。
- Textual はフィールドへの `store` の値の型を、型宣言のフィールドの型と比べる（`expression n9.go::tuple2.r0 has type *int, while a supertype of *float was expected`）。そのため、複数の戻り値を入れる型の名前に要素の型を含め、`go::tuple2$ptr_go::string$ptr_go::iface$` のようにする（`*` は `ptr_` と書き、各要素の後に `$` を付ける）。要素の型が違う 2 つの戻り値の型が、同じ名前の宣言を共有しないようにするため。
- 前のブロックで定義した `nK` を後のブロックで使っても、取り込みは成功し、taint も伝わった（`n0 = src()` を分岐の先で `snk(n0)` に渡すと報告された）。go/ssa の値は定義が使う場所を支配しているので、SSA の値はそのまま `nK` に対応させる。
- `__sil_allocate(<int>)`、`__sil_allocate(<*go::string>)` のように、struct 以外の型も確保できる。`n = x.T.f`（フィールドのアドレスを `nK` に入れる）、`__sil_divf` などの組み込み、`1.5` のような浮動小数点数の定数も取り込める。
- 文字列の定数は `go::rt::string_const("...")` の呼び出しにする。引数は Textual の文字列リテラル、戻り値は `*go::string`、本体のない宣言のまま（Task 1 で、本体のない宣言は引数の taint を戻り値に伝えることを確かめた。定数には taint はない）。
- struct の値は、確保した領域へのポインタで表す。値のコピー（`Store`、`*p`）ではフィールドを 1 つずつコピーし、struct 型のフィールドは確保した領域へのポインタを持たせて中身をコピーする。変換したコードを Pulse にかけ、コピーした後に元の値へ入れた taint がコピー先から報告されないことを確かめた。

#### 可変長引数・インタフェース・型の変換で確かめたこと（2026-10-30、計画の Task 6）

- slice と配列の要素は、添字を区別せず `go::slice` の 1 つのフィールド `elem` で表す。`IndexAddr` は `nK = s.go::slice.elem`。配列は `*go::slice` なので、配列の `Alloc` は `**go::slice` になる。`Alloc` は `__sil_allocate(<go::slice>)` で領域を確保し、それを `*go::slice` の領域に入れる。配列へのポインタからの `IndexAddr` と `Slice` は、一度 `load` して `*go::slice` を取り出す。これで `a[:]` は配列と同じ `go::slice` を指す。配列から作った slice の要素に入れた taint が報告されることを Pulse で確かめた（`testdata/src/elems` の `ArrayToSlice`）。
- 要素が数値（`int` / `float`）のときは、`elem` のアドレス（`**go::opaque`）を `__sil_cast(<*int>, ...)` / `__sil_cast(<*float>, ...)` で変換してから `store` / `load` する。変換しないと `expression n4 has type **go::opaque, while a supertype of *float was expected` で取り込みが失敗した。アドレスを `__sil_cast` で変換しても、同じ要素への `store` と `load` の間で taint が伝わることを、手で書いた `.sil` で Pulse にかけて確かめた（報告 1 件）。
- 名前付きの型への変換で数値の型が変わるとき（`type F float64; F(n)`、n は int）は、引数を `__sil_cast(<float>, n)` にしてから `go::rt::convert$...::F(float) : float` に渡す。int をそのまま渡すと `expression n3 has type int, while a subtype of float was expected` で取り込みが失敗した。呼び出しの引数の型も、int と float の違いは確かめられている。
- `MakeInterface` は `go::rt::make_iface(x)`、文字列の `+` は `go::rt::string_concat(a, b)`。どちらも本体のない宣言で、引数の taint が戻り値に伝わる。
- 名前付きの型 T への `ChangeType` / `Convert` は `go::rt::convert$<mangle.Type(T)>(x)` の呼び出しにする。宣言の引数と戻り値の型は、どちらも T の Textual の型。名前のない型への変換は値をそのまま使う。ただし int と float の間の変換は `__sil_cast(<float>, n0)` にする（そのまま使うと取り込みが `expression n4 has type int, while a subtype of float was expected` で失敗し、ファイル全体が取り込まれない）。数値と文字列の間の変換（`string(r)`）は変換しない（警告）。`type Safe string` への変換は `go::rt::convert$<パッケージ>::Safe(*go::string) : *go::string` になり、`"procedure_regex": "^go::rt::convert\\$<パッケージ>::Safe$"` を危険な関数にすると報告された。
- インタフェースのメソッド呼び出し（`IsInvoke`）は、呼び出し先を探さず、`<mangle.Type(インタフェース)>$<メソッド名>` という本体のない宣言を呼ぶ。最初の引数はインタフェースの値（`*go::iface`）。名前のないインタフェース型のメソッド呼び出しは変換しない（警告）。
- `fmt.Sprintf` は `fmt::Sprintf` という本体つきのモデルにする（`internal/translate/models.go`）。format と `args.go::slice.elem` を `go::rt::string_concat` でつないで返す。args が nil（可変長引数がない呼び出し）のときは format を返す。モデルのある関数は `declare` を出さない。`sil.WriteTo` は、同じ名前を `declare` と `define` の両方で出すとエラーを返す。
- 取り込みは、呼び出しの引数の型が宣言の引数の型と違っても（`int` を `*go::string` の引数に渡す）、`*go::iface` の値を `*go::opaque` のフィールドに `store` しても、エラーを出さなかった。一方、`int` と `float` の不一致（`store`、`ret`、呼び出しの引数）と、`**go::opaque` と `*float` の不一致はエラーになる。引数の型が呼び出しごとに違っても宣言が 1 つで済むよう、`go::rt::make_iface` の宣言は引数の型を `*go::opaque` に固定している。

#### 本物のライブラリを使うサンプルで確かめたこと（2026-11-02、計画の Task 8）

- go2sil に渡すパッケージは `./examples/connectsvc`、`./examples/grpcsvc`、`./examples/userrepo` の 3 つにし、`./examples/...` にはしない。`./examples/...` にすると生成コード（`examples/gen/...`）も変換の対象になり、`ProtoReflect`、`Reset`、`String`、`file_user_v1_user_proto_init` などがパッケージの変数（`*ssa.Global`）や型アサーションのために警告になる。サンプルが呼ぶのは `GetName()` などの getter だけで、対象外のパッケージの関数は本体のない宣言として出力され、引数の taint が戻り値に伝わる（Task 1 の `TestProbeUnknownCallPropagates`）。
- 名前が `_` のフィールドは、Textual では `SIL syntax error: unexpected token _` で取り込みが失敗する。`sync.Mutex` や生成された message の型が `_` のフィールドを持つ。`_` のフィールドは `blank$<フィールドの番号>` という名前で出力する（`internal/translate/types.go` の `fieldName`）。Go の識別子は `$` を含まないので、他のフィールドの名前と一致しない。
- `Scan(ctx, &name)` の `&name` のように、struct 以外の値へのポインタ（Textual では `**go::string`、`**go::slice`）を `go::rt::make_iface` に渡すと、`expression n8 has type **go::string, while a subtype of *go::opaque was expected` で取り込みが失敗する。Textual の型が `**` で始まる値は、`__sil_cast(<*go::opaque>, x)` にしてから渡す。
- 生成された message の型の入力元は、型の名前を 1 つずつ書くのではなく、生成コードのパッケージのパス `github_com::nabetama::infer_go_proto::examples::gen::user::v1::` で指定する（設計時の判断）。`ArgumentsMatchingTypes` は部分文字列で比べるので、「名前が `Request` で終わる型」は書けない。パッケージのパスで書けば、message を足しても `.inferconfig` を変えなくてよい。このパスは、そのパッケージのすべての型（`GetUserResponse` などの応答の型も）と、型引数にそれらを含む `connectrpc_com::connect::Response$...GetUserResponse$` のような型にも一致する。応答の型を引数に取る関数があれば、その引数も入力元になる。他のコードに使うときは、この文字列を、そのコードの生成コードのパッケージのパスを `mangle.Pkg` と同じ規則で変換したもの（`/` を `::`、`.` と `-` を `_`）に、末尾に `::` を付けて書き換える。`connectrpc_com::connect::Request` はそのまま使える。
- 入力元を `connectrpc_com::connect::Request` だけにして同じ `.sil` を解析すると、`GetUserBad` と `GetUserViaRepoBad` が報告された（gRPC の 3 件は入力元がないので報告されない）。`GetUserBad` の qualifier は `` `go::rt::string_concat()` is tainted by value passed as argument `#2` to `...::connectsvc::Handler$GetUserBad` ... flows to value passed as argument `#1` to `github_com::uptrace::bun::SelectQuery$Where` `` で、ハンドラの `req` から本体のない `GetName` を通って taint が伝わっている。connect のハンドラの報告は、生成された message の型を入力元に入れなくても出る。生成された message の型を入力元に入れるのは、gRPC のハンドラが `*userv1.SearchUsersRequest` を直接受け取るためである。
- 入力元の `"procedure_regex": ".*"` は、本体のない宣言の呼び出しにも当てはまる。生成された message の型も入力元に入れた `.inferconfig` では、`GetUserBad` の報告の qualifier は `` `go::rt::string_concat()` is tainted by value passed as argument `#0` to `...::v1::GetUserRequest$GetName` `` になり、`*GetUserRequest` を引数に取る `GetName` の呼び出しが入力元として示される。
- `database/sql` の関数名は `mangle.Pkg` で `/` が `::` になるので `database::sql::DB$QueryContext` である（計画の `database_sql::DB` は誤り）。go2sil が出力した宣言 `declare database::sql::DB$QueryContext(*database::sql::DB, *go::iface, *go::string, *go::slice)` で、`query` が 2 番（0 番がレシーバ、1 番が `ctx`）であることを確かめた。`grpcsvc` の `SearchUsersSQLBad` が `QueryContext` の引数 `#2` への流れとして報告され、`SearchUsersSQLGood` は報告されない。
- 組み込みの `append` の呼び出しは変換されず、`unsupported *ssa.Call` の警告になる。サンプルでは `append` を使っていない。
- `SearchUsersSafeBad` は、`bun.Safe` への変換（`go::rt::convert$github_com::uptrace::bun::schema::Safe`）と `OrderExpr` の両方で報告され、report.json に 2 件出る。e2e テストは関数名の集合で比べるので 1 つになる。
- `Repo.FindByName` は `string` を受け取るだけで入力元の型の引数を持たないので、報告は呼び出し元の `GetUserViaRepoBad` にだけ出る。

### 関数名の変換規則

Infer の設定は関数名で入力元と危険な関数を指定する。そのため、Go の関数・メソッドを `.sil` 上でどんな名前にするかが設計の中心になる。次の条件を満たす規則にする。

- Go のパッケージパス・レシーバの型・メソッド名から一意に決まり、`.inferconfig` に書いたとき人が読める。
- `procedure_regex` で「あるパッケージのあるメソッド」を指定できる。

具体的な文字の置き換え方は実装時に決める（作る人に任せる）。決めた規則はこのドキュメントに追記する。

2026-10-28 追記: `.source_language = "c"` では Textual の `Cls.m` という名前のクラスの部分が落ちる（「v1.3.0 のバイナリで確かめたこと」を参照）。そのため、関数もメソッドも、パッケージとレシーバの型を含めた 1 つのトップレベルの名前で出力する。トップレベルの名前に `::` と `$` を使えること、report.json の `procedure` にその名前がそのまま出ることは確かめてある。C の標準ライブラリの関数と同じ名前にならないようにする。

### 変換する Go の機能の範囲

成功の条件のサンプルを `.sil` に変換するのに必要な範囲に限る。

- 対象にする: 関数とメソッドの定義と呼び出し、引数と戻り値（複数の戻り値を含む）、ローカル変数、`if` と `for`（SSA の基本ブロックと分岐）、構造体のポインタとフィールドの読み書き、文字列の定数と連結、インタフェースを通したメソッド呼び出し（呼び出し先を静的に決められるものだけ）。
- 可変長引数の受け渡しも対象にする。`fmt.Sprintf(format, x)` や `Where("id = ?", x)` は、SSA では `[]any` の確保（`Alloc`）、要素のアドレス（`IndexAddr`）と `Store`、`Slice`、インタフェースへの変換（`MakeInterface`）になる。成功の条件のサンプルで必ず出てくるため、これらの命令は変換する。外部入力の taint が `fmt.Sprintf` の戻り値に伝わるよう、slice の要素から戻り値へ taint を流すモデルを用意する（設計時の判断）。
- 名前付きの型への変換（`bun.Safe(s)` のような、SSA の `ChangeType` / `Convert`）は、変換先の型ごとに決まった名前の、本体のない関数の呼び出しとして `.sil` に出力する。これによって、型への変換も関数名で危険な関数に指定できる。この出力は Go のコードにある変換命令ひとつずつに対応しているので、「Go のコードを見ずに報告を出す仕組み」には当たらない（設計時の判断）。
- 対象にしない: goroutine、channel、`select`、`defer`、`panic`/`recover`、map、可変長引数以外の slice の操作（必要になったら本体のない宣言の呼び出しに置き換える）、reflect。
- 変換できない SSA 命令が出てきたら、変換ツールはその関数を本体のない宣言として出力し、警告を標準エラーに出す。その関数のために処理全体を止めることはしない。
- テストでは、サンプルの関数が本体つきで変換されたこと（変換できなかった警告が出ていないこと）も確かめる。これを確かめないと、安全な側のサンプルが変換に失敗しただけで「報告しない」として通ってしまう（設計時の判断）。

## 目的に含めないもの

- Go の全言語機能への対応。
- リソースの閉じ忘れ、nil 参照外し、goroutine リークの検出（taint の後で検討する）。
- 実在する大規模なコードベースへの適用と、その実用性の評価。
- Infer 本体への変更、upstream への貢献。
- IDE や CI との連携。

## やってはいけないこと

- このリポジトリは公開する。特定の会社・プロダクトのコード、ファイルパス、構成、プロダクト名をサンプルやドキュメントに持ち込まない。サンプルは汎用的な例だけで書く。
- テストを通すために、サンプルごとに変換結果を手で書いた `.sil` で置き換えたり、Go のコードを見ずに報告を出す仕組みにしたりしない。報告は、変換ツールが Go のコードから生成した `.sil` を Infer が解析した結果でなければならない。

## 作る人に任せること

- `.source_language` に何を指定するか。最初に小さな `.sil`（入力元から危険な関数へ直接渡すだけのもの）で各言語を試し、taint が報告されるもののうち、Go の値の扱い（ポインタ、構造体）に近いものを選ぶ。選んだ理由をこのドキュメントに追記する。
- Go の型と Textual の型の対応づけ。
- ディレクトリ構成、CLI の引数、テストの書き方（Infer を呼ぶテストは、Infer が見つからなければ skip する）。
- Infer バイナリの置き場所（リポジトリにはコミットしない）。

## 記録

- 設計の判断と変更はこのドキュメントに追記する。
- 作業の経過は `docs/worklog/YYYY-MM-DD.md` に日ごとに残す。
