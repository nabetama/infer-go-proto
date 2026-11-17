# examples

`e2e/e2e_test.go` が go2sil と Infer にかけるサンプル。

- `proto/user/v1/user.proto`: `UserService`（`GetUser`、`SearchUsers`）の定義。
- `gen/user/v1/`: `user.proto` から生成したコード。手で編集しない。
- `connectsvc/`: connect のハンドラ。
- `grpcsvc/`: grpc-go のハンドラ。
- `userrepo/`: bun を使う repository。

各ハンドラの関数には、Infer に報告されるべきものに `// want: TAINT_ERROR`、報告されてはいけないものに `// want: none` を付けている。e2e テストは `want: TAINT_ERROR` の付いた関数の集合と、`TAINT_ERROR` が報告された関数の集合が一致することを確かめる。

## コードの生成

`buf`、`protoc-gen-go`、`protoc-gen-go-grpc`、`protoc-gen-connect-go` を `PATH` に置き、このディレクトリで次を実行する。

```sh
cd examples
buf generate
```

`buf.gen.yaml` に従い、`gen/user/v1/user.pb.go`、`gen/user/v1/user_grpc.pb.go`、`gen/user/v1/userv1connect/user.connect.go` が出力される。

インストールの例:

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
```
