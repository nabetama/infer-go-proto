package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "go2sil")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "go2sil")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, so.String(), se.String()
}

func flowDir() string {
	return filepath.Join("..", "..", "internal", "translate", "testdata", "src", "flow")
}

func TestWritesFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.sil")
	code, _, stderr := runCmd(t, "-C", flowDir(), "-o", out, ".")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), ".source_language") {
		t.Errorf("output starts with %.40q", b)
	}
}

func TestStdout(t *testing.T) {
	code, stdout, stderr := runCmd(t, "-C", flowDir(), ".")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, ".source_language") {
		t.Fatalf("code=%d stderr=%q stdout=%.40q", code, stderr, stdout)
	}
}

func TestMissingPackage(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.sil")
	code, _, stderr := runCmd(t, "-C", flowDir(), "-o", out, "./nonexistent")
	if code != 1 || !strings.HasPrefix(stderr, "go2sil: ") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("output file created on failure")
	}
}

func TestNoPatterns(t *testing.T) {
	code, _, stderr := runCmd(t)
	if code != 2 || stderr == "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestTypeNameCollision(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "translate", "testdata", "src", "dotpkg")
	code, stdout, stderr := runCmd(t, "-C", dir, "./a.b", "./a_b")
	if code != 1 || !strings.HasPrefix(stderr, "go2sil: types ") || !strings.Contains(stderr, "both map to") || stdout != "" {
		t.Fatalf("code=%d stderr=%q stdout=%.40q", code, stderr, stdout)
	}
}

func TestNoPackagesMatched(t *testing.T) {
	// docs/ has no Go files, so ./docs/... matches no package.
	out := filepath.Join(t.TempDir(), "out.sil")
	code, _, stderr := runCmd(t, "-C", filepath.Join("..", ".."), "-o", out, "./docs/...")
	if code != 1 || !strings.HasPrefix(stderr, "go2sil: ") || !strings.Contains(stderr, "no packages") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("output file created on failure")
	}
}
