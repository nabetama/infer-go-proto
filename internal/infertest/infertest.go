// Package infertest は、テストから Infer を呼び出すための補助関数を提供する。
package infertest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Issue は infer-out/report.json の 1 件の報告のうち、テストで使う項目。
type Issue struct {
	BugType   string `json:"bug_type"`
	Procedure string `json:"procedure"`
	Qualifier string `json:"qualifier"`
	File      string `json:"file"`
}

// Bin は Infer の実行ファイルの場所を返す。
// 環境変数 INFER_BIN、リポジトリの .tools/infer/bin/infer、PATH 上の infer の順に探し、
// どれも見つからなければテストを skip する。
func Bin(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("INFER_BIN"); p != "" {
		if isExecutable(p) {
			return p
		}
		t.Fatalf("INFER_BIN=%s is not an executable file", p)
	}
	if p := filepath.Join(repoRoot(), ".tools", "infer", "bin", "infer"); isExecutable(p) {
		return p
	}
	if p, err := exec.LookPath("infer"); err == nil {
		return p
	}
	t.Skip("infer not found: set INFER_BIN or run scripts/install-infer.sh")
	return ""
}

// Run は一時ディレクトリで silFiles を Infer に取り込み、Pulse で解析して報告を返す。
// inferconfig は一時ディレクトリに .inferconfig として書く。
func Run(t testing.TB, silFiles []string, inferconfig string) []Issue {
	t.Helper()
	bin := Bin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".inferconfig"), []byte(inferconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := capture(t, bin, dir, silFiles); err != nil {
		t.Fatalf("infer capture: %v\n%s", err, out)
	}
	if out, err := runInfer(bin, dir, "analyze", "--pulse-only", "--no-progress-bar"); err != nil {
		t.Fatalf("infer analyze: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(dir, "infer-out", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var issues []Issue
	if err := json.Unmarshal(data, &issues); err != nil {
		t.Fatalf("parse report.json: %v", err)
	}
	return issues
}

// capture は dir で `infer capture --capture-textual <silFiles...>` を実行し、出力を返す。
func capture(t testing.TB, bin, dir string, silFiles []string) ([]byte, error) {
	t.Helper()
	args := []string{"capture", "--no-progress-bar"}
	for _, f := range silFiles {
		abs, err := filepath.Abs(f)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "--capture-textual", abs)
	}
	return runInfer(bin, dir, args...)
}

func runInfer(bin, dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// repoRoot はこのファイルの位置からリポジトリのルートを求める。
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
