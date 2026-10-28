package infertest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeLang は、TestProbeSourceLanguage の結果から選んだ .source_language。
// 以降の Task の sil.Lang と同じ値にする。
const probeLang = "c"

const directConfig = `{
  "pulse-taint-sources": [{ "procedure": "__taint_source" }],
  "pulse-taint-sinks": [{ "procedure": "__taint_sink" }]
}`

// 入力元は *Req 型の引数（関数を問わない）、危険な関数は sink。
const formalByTypeConfig = `{
  "pulse-taint-sources": [
    { "procedure_regex": ".*", "taint_target": ["ArgumentsMatchingTypes", ["Req"]] }
  ],
  "pulse-taint-sinks": [{ "procedure": "sink" }]
}`

// 入力元は source の戻り値、危険な関数は sink。
const sourceSinkConfig = `{
  "pulse-taint-sources": [{ "procedure": "source" }],
  "pulse-taint-sinks": [{ "procedure": "sink" }]
}`

// whereConfig は、Where の pos 番目の引数を危険な関数の引数にする設定を返す。
func whereConfig(pos string) string {
	return `{
  "pulse-taint-sources": [{ "procedure": "source" }],
  "pulse-taint-sinks": [
    { "procedure_regex": "^Where$", "taint_target": ["ArgumentPositions", [` + pos + `]] }
  ]
}`
}

// 入力元から危険な関数への直接の流れが、選んだ言語で報告される。
// 言語ごとの結果（2026-10-28、Infer v1.3.0）: c・rust・hack・swift は報告あり、
// java は報告なし、python は取り込みで失敗（トップレベルの define を受け付けない）。
func TestProbeSourceLanguage(t *testing.T) {
	issues := runProbe(t, "direct.sil", directConfig)
	wantTaint(t, issues, "basic_flow", 1)
}

// *Req 型の引数に taint が付き、そのまま危険な関数に渡すと報告される。
func TestProbeFormalByType(t *testing.T) {
	wantTaint(t, runProbe(t, "formal_type_bad.sil", formalByTypeConfig), "handler", 1)
	wantTaint(t, runProbe(t, "formal_type_good.sil", formalByTypeConfig), "handler", 0)
}

// taint の付いた *Req から load したフィールドの値も、危険な関数で報告される。
func TestProbeFieldOfTaintedPointer(t *testing.T) {
	wantTaint(t, runProbe(t, "field_bad.sil", formalByTypeConfig), "handler", 1)
	wantTaint(t, runProbe(t, "field_good.sil", formalByTypeConfig), "handler", 0)
}

// 本体のない宣言 concat の引数の taint は、戻り値に伝わる。
func TestProbeUnknownCallPropagates(t *testing.T) {
	wantTaint(t, runProbe(t, "unknown_call_bad.sil", sourceSinkConfig), "f", 1)
	wantTaint(t, runProbe(t, "unknown_call_good.sil", sourceSinkConfig), "f", 0)
}

// *Slice のフィールドに入れた taint は、本体のない宣言 fmt_sprintf の戻り値には伝わらない。
// 計画の期待（伝わる）とは逆なので、TestProbeSprintfModel の本体つきモデルを使う。
func TestProbeUnknownCallThroughField(t *testing.T) {
	wantTaint(t, runProbe(t, "unknown_call_field_bad.sil", sourceSinkConfig), "f", 0)
	wantTaint(t, runProbe(t, "unknown_call_field_good.sil", sourceSinkConfig), "f", 0)
}

// fmt_sprintf に「args.elem を読んで format と concat する」本体を与えると、taint が戻り値に伝わる。
// 関数名を C の標準ライブラリと同じ sprintf にすると、定義した本体が使われず伝わらない。
func TestProbeSprintfModel(t *testing.T) {
	wantTaint(t, runProbe(t, "sprintf_model_bad.sil", sourceSinkConfig), "f", 1)
	wantTaint(t, runProbe(t, "sprintf_model_good.sil", sourceSinkConfig), "f", 0)
	wantTaint(t, runProbe(t, "sprintf_model_libc_name.sil", sourceSinkConfig), "f", 0)
}

// Q.Where(this, query, args) の query は ArgumentPositions の 1 番（0 番が this）。
// 本体のない宣言でも、本体つきの定義でも同じ。
func TestProbeArgPositionWithReceiver(t *testing.T) {
	for _, file := range []string{"where_query.sil", "where_query_define.sil"} {
		t.Run(file, func(t *testing.T) {
			wantTaint(t, runProbe(t, file, whereConfig("1")), "f", 1)
			wantTaint(t, runProbe(t, file, whereConfig("0")), "f", 0)
		})
	}
}

// taint の付いた値を args の要素にだけ入れた場合、query（1 番）を危険な引数にしても報告されない。
func TestProbeArgsNotSink(t *testing.T) {
	for _, file := range []string{"where_args.sil", "where_args_define.sil"} {
		t.Run(file, func(t *testing.T) {
			wantTaint(t, runProbe(t, file, whereConfig("1")), "f", 0)
		})
	}
	// 参考: args（2 番）を危険な引数にすると、本体つきの定義では要素の taint が報告され、
	// 本体のない宣言では報告されない。
	wantTaint(t, runProbe(t, "where_args_define.sil", whereConfig("2")), "f", 1)
	wantTaint(t, runProbe(t, "where_args.sil", whereConfig("2")), "f", 0)
}

// Textual の Cls.m という名前は、Infer の中ではクラスが落ちて m だけになる。
// report.json の procedure も、設定の procedure / procedure_regex が照合する名前も m になる。
func TestProbeProcedureNames(t *testing.T) {
	t.Run("class qualified", func(t *testing.T) {
		cfg := func(sink string) string {
			return `{
  "pulse-taint-sources": [
    { "procedure_regex": ".*", "taint_target": ["ArgumentsMatchingTypes", ["Req"]] }
  ],
  "pulse-taint-sinks": [
    { "procedure_regex": "` + sink + `", "taint_target": ["ArgumentPositions", [1]] }
  ]
}`
		}
		wantTaint(t, runProbe(t, "method_handler.sil", cfg(`^Where$`)), "Handle", 1)
		wantTaint(t, runProbe(t, "method_handler.sil", cfg(`Q\\.Where`)), "Handle", 0)
	})

	t.Run("class qualified collision", func(t *testing.T) {
		out, err := capture(t, Bin(t), t.TempDir(), []string{withLang(t, "cls_method_collision.sil")})
		if err == nil {
			t.Fatal("capture succeeded; want failure because a::A.get and b::B.get become the same procedure")
		}
		if !strings.Contains(string(out), "Creating two procdescs for the same procname") {
			t.Fatalf("unexpected capture error: %v\n%s", err, out)
		}
	})

	t.Run("toplevel", func(t *testing.T) {
		cfg := `{
  "pulse-taint-sources": [
    { "procedure_regex": "Svc\\$Handle$", "taint_target": ["ArgumentsMatchingTypes", ["Req"]] }
  ],
  "pulse-taint-sinks": [
    { "procedure_regex": "^example_com::orm::Q\\$Where$", "taint_target": ["ArgumentPositions", [1]] }
  ]
}`
		issues := runProbe(t, "toplevel_names.sil", cfg)
		wantTaint(t, issues, "example_com::app::Svc$Handle", 1)
		if len(issues) == 1 && !strings.Contains(issues[0].Qualifier, "`example_com::orm::Q$Where`") {
			t.Errorf("qualifier does not name example_com::orm::Q$Where: %s", issues[0].Qualifier)
		}
	})
}

// runProbe は testdata/probe/<name> に probeLang を付けて Infer にかける。
func runProbe(t *testing.T, name, inferconfig string) []Issue {
	t.Helper()
	return Run(t, []string{withLang(t, name)}, inferconfig)
}

// wantTaint は、procedure で報告された TAINT_ERROR がちょうど want 件で、ほかの報告がないことを確かめる。
func wantTaint(t *testing.T, issues []Issue, procedure string, want int) {
	t.Helper()
	got := 0
	for _, is := range issues {
		if is.BugType == "TAINT_ERROR" && is.Procedure == procedure {
			got++
		}
	}
	if got != want || len(issues) != want {
		t.Errorf("want %d TAINT_ERROR in %q and nothing else, got %+v", want, procedure, issues)
	}
}

// withLang は testdata/probe/<name> の先頭に .source_language = probeLang を付けたファイルを書き、その場所を返す。
func withLang(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "probe", name))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), name)
	body := append([]byte(`.source_language = "`+probeLang+`"`+"\n\n"), src...)
	if err := os.WriteFile(out, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}
