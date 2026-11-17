// Command go2sil converts Go packages to Infer Textual (.sil).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/nabetama/infer-go-proto/internal/translate"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("go2sil", flag.ContinueOnError)
	dir := fs.String("C", ".", "directory used to load packages")
	out := fs.String("o", "", "output file (default stdout)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: go2sil [-C dir] [-o out.sil] <package patterns...>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	_, pkgs, err := translate.Load(fs.Args(), *dir)
	if err != nil {
		return fail(err)
	}
	for _, p := range pkgs {
		if p == nil {
			return fail(fmt.Errorf("a package matching %v could not be built", fs.Args()))
		}
	}
	mod, err := translate.Translate(pkgs, os.Stderr)
	if err != nil {
		return fail(err)
	}
	var buf bytes.Buffer
	if _, err := mod.WriteTo(&buf); err != nil {
		return fail(err)
	}
	if *out == "" {
		if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
			return fail(err)
		}
		return 0
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "go2sil: %v\n", err)
	return 1
}
