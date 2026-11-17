package infertest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureReportsTextualErrors(t *testing.T) {
	Bin(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(".source_language = \"c\"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.sil", "define x::f() : void {\n  #b0:\n    ret null\n}\n")
	if err := Capture(t, []string{good}); err != nil {
		t.Fatalf("good.sil: %v", err)
	}
	// infer capture exits with 0 for both of these; Capture must still fail.
	bad := map[string]string{
		"syntax.sil":     "define x::f() : void {\n  #b0:\n    n0 = 1 +\n    ret null\n}\n",
		"undeclared.sil": "define x::f() : void {\n  #b0:\n    n0 = y::g()\n    ret null\n}\n",
	}
	for name, body := range bad {
		if err := Capture(t, []string{write(name, body)}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
