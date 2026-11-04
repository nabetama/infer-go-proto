# infer-go-proto

Go のコードを [Infer](https://github.com/facebook/infer) の中間言語 Textual（`.sil`）に変換し、Infer v1.3.0 の Pulse の taint 解析にかける試作。RPC ハンドラが受け取った外部入力が、SQL の文字列そのものとして使われる流れを検出できるかを確かめる。

Infer は Go に対応していないが、`.sil` を `infer capture --capture-textual` に渡せば既存の解析エンジンが動く。このリポジトリは、`go/ssa` の SSA を `.sil` に変換するコマンド `go2sil` と、そのテストとサンプルからなる。設計と確かめた内容は [docs/design/2026-10-28-go-taint-poc-design.md](docs/design/2026-10-28-go-taint-poc-design.md)、作業の経過は `docs/worklog/` にある。

## 検出するもの

- 入力元: connect（`connectrpc.com/connect`）の `*connect.Request[T]` を引数に取る関数と、gRPC のハンドラが受け取る生成されたリクエストメッセージを引数に取る関数。
- 危険な関数（SQL の文字列を受け取る引数）: bun の `(*bun.DB).NewRaw`、`(*bun.SelectQuery).Where` / `WhereOr` / `ColumnExpr` / `OrderExpr` の第 1 引数、`bun.Safe` への変換、`database/sql` の `(*sql.DB).QueryContext` / `ExecContext` のクエリ文字列。
- 報告しない: 外部入力をプレースホルダ `?` に対応する `args` に渡す書き方。

`examples/` のサンプルでは、`GetUserBad`、`GetUserViaRepoBad`、`SearchUsersBad`、`SearchUsersSQLBad`、`SearchUsersSafeBad` が報告され、`args` に渡す書き方の対は報告されない。

## Infer のインストール

```sh
scripts/install-infer.sh
```

Infer v1.3.0 の公式バイナリを `.tools/infer` に展開する（macOS arm64 と Linux x86_64 に対応。`.tools/` は `.gitignore` に入っている）。テストは次の順で Infer を探し、見つからなければ skip する。

1. 環境変数 `INFER_BIN`
2. `.tools/infer/bin/infer`
3. `PATH` 上の `infer`

## go2sil の使い方

```sh
go run ./cmd/go2sil [-C dir] [-o out.sil] <パッケージのパターン...>
```

- `-C dir`: パッケージを読み込むディレクトリ（既定は `.`）。
- `-o out.sil`: 出力ファイル（既定は標準出力）。
- 本体を変換するのは、引数に指定したパッケージの関数だけ。依存パッケージの関数は、呼ばれるものだけ本体のない宣言（`declare`）として出力する。
- 変換できない SSA 命令がある関数は本体のない宣言として出力し、標準エラーに `go2sil: warning: <関数の Go 名>: unsupported <命令の型>` を 1 行出す。処理全体は止めない。

Infer にかける例（`.inferconfig` のあるディレクトリで実行する）:

```sh
go run ./cmd/go2sil -o /tmp/out.sil ./examples/connectsvc ./examples/grpcsvc ./examples/userrepo
.tools/infer/bin/infer capture --no-progress-bar --capture-textual /tmp/out.sil
.tools/infer/bin/infer analyze --pulse-only --no-progress-bar
```

結果は `infer-out/report.json` に出る。`examples/gen`（生成コード）は渡さない。生成コードにはパッケージの変数や型アサーションがあり、警告になる。サンプルが呼ぶのは getter だけで、getter は本体のない宣言のまま引数の taint を戻り値に伝える。

## `.inferconfig` を別のコードに当てはめる

入力元と危険な関数は、リポジトリのルートの `.inferconfig` の `pulse-taint-sources` と `pulse-taint-sinks` で指定する。Infer は関数名の文字列で照合するので、go2sil が出力する名前の規則を知っておく必要がある。

### 関数名の規則

関数もメソッドも、1 つのトップレベルの名前にして出力する（`.source_language = "c"` では `Cls.m` のクラスの部分が落ちるため）。

| Go | 名前 |
|---|---|
| パッケージのパス | `/` を `::`、`.` と `-` を `_` に置き換える。`github.com/uptrace/bun` は `github_com::uptrace::bun`、`database/sql` は `database::sql` |
| パッケージの関数 | `<パッケージ>::<関数名>` |
| メソッド | `<パッケージ>::<型名>$<メソッド名>`（ポインタレシーバの `*` は付かない）。例: `github_com::uptrace::bun::SelectQuery$Where` |
| 名前付きの型への変換 | `go::rt::convert$<型の名前>`。例: `go::rt::convert$github_com::uptrace::bun::schema::Safe` |

実装は `internal/mangle/mangle.go`。

### 書き換える箇所

- 生成コードのパッケージのパス接頭辞（`pulse-taint-sources` の 2 つ目の文字列）: 現在は `github_com::nabetama::infer_go_proto::examples::gen::user::v1::`。自分のコードの生成コードのパッケージのパスを上の規則で変換し、末尾に `::` を付けたものに書き換える。`ArgumentsMatchingTypes` は、引数の型（構造体へのポインタ）の名前が指定した文字列を部分文字列として含むかで比べる。そのため、このパッケージのすべての型（応答の型も）が入力元になる。
- connect の `Request`（同じ配列の 1 つ目の文字列）: `connectrpc_com::connect::Request`。そのまま使える。
- 危険な関数（`pulse-taint-sinks`）: `procedure_regex` に、上の規則で作った関数名を書く。
  - 名前全体に一致させるため `^` と `$` で囲む。`$` は正規表現の特殊文字なので、JSON の中では `\\$` と書く（例: `"^github_com::uptrace::bun::SelectQuery\\$Where$"`）。
  - `taint_target` の `["ArgumentPositions", [N]]` の N は、`.sil` 上の引数の並びでの番号。メソッドはレシーバが 0 番なので、`Where(query, args...)` の `query` は 1 番、`(*sql.DB).QueryContext(ctx, query, args...)` の `query` は 2 番。
  - 型への変換を危険な関数にするときは、`taint_target` を省略する（`bun.Safe` の例）。
- 実際の名前は `go2sil` の出力で確かめられる。`grep '^declare\|^define' out.sil` で、`.sil` に出た名前を見る。

入力元に `"procedure_regex": ".*"` を使っているので、入力元の型の引数を持つ関数はすべて、解析のとき引数に taint が付く。ハンドラから repository 層の関数にリクエストの値がそのまま渡された場合、その関数の引数も入力元になる。同じ流れが呼び出し元と呼び出し先の両方で報告されることがある。

## テスト

```sh
go test ./... && go vet ./...
```

Infer が見つからないテストは skip する。Infer を使う e2e テスト（`e2e/e2e_test.go`）は、`examples/` を go2sil と Infer にかけ、報告された関数の集合が、サンプル中の `// want: TAINT_ERROR` の付いた関数の集合と一致することを確かめる。go2sil が警告を出した場合も失敗する。サンプルの生成コードを作り直す手順は [examples/README.md](examples/README.md)。

## 変換の対象外の Go の機能

次のものを含む関数は、本体のない宣言として出力され、警告が出る。本体のない宣言の呼び出しは、引数の taint が戻り値に伝わるものとして扱われる。

- `&&` と `||`（SSA の `Phi`）
- 型アサーション（`TypeAssert`）
- 組み込み関数の呼び出し（`append`、`len` など）
- クロージャと関数値の呼び出し
- パッケージの変数（グローバル変数）
- goroutine、channel、`select`、`defer`、`panic` / `recover`
- map
- 複素数、構造体の比較、数値と文字列の間の変換（`string(r)`）、名前のないインタフェース型のメソッド呼び出し

## 既知の制限

- 外部パッケージの関数は本体のない宣言として扱うので、その中で taint が消える・増えるかは見ない。`fmt.Sprintf` だけは、format と引数を連結して返す本体つきのモデルを出力する。
- 入力元を生成コードのパッケージのパス接頭辞で指定しているので、そのパッケージの `Response` などの型を引数に取る関数の引数も入力元になる。
- slice と配列の要素は添字を区別せず 1 つの要素として扱う。配列を値でコピーすると、コピー先と元が同じ領域を指す。
- 名前付きの struct への型変換は本体のない宣言を通るので、フィールドの taint が警告なしに消える。
- e2e テストは報告された関数を Go の関数名だけで `want` と比べるので、パッケージが違う同名の関数は区別されない。
- 無名の struct の型名が、別の無名の struct と衝突する可能性がある。
- `-h` の終了コードは 2 になる。`INFER_BIN` が実行できないファイルを指すと、skip ではなく失敗する。
