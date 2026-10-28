# Go コードを Infer の taint 解析にかける試作 設計

- 作成日: 2026-10-28
- 状態: レビュー待ち

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

### 関数名の変換規則

Infer の設定は関数名で入力元と危険な関数を指定する。そのため、Go の関数・メソッドを `.sil` 上でどんな名前にするかが設計の中心になる。次の条件を満たす規則にする。

- Go のパッケージパス・レシーバの型・メソッド名から一意に決まり、`.inferconfig` に書いたとき人が読める。
- `procedure_regex` で「あるパッケージのあるメソッド」を指定できる。

具体的な文字の置き換え方は実装時に決める（作る人に任せる）。決めた規則はこのドキュメントに追記する。

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
