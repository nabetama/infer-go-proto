// Package translate converts go/ssa programs into Textual (internal/sil).
package translate

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Load loads the packages matching patterns (relative to dir) together with
// their dependencies and builds SSA for all of them. It returns an error if
// any package has a load or type error.
func Load(patterns []string, dir string) (*ssa.Program, []*ssa.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}
	var msgs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			msgs = append(msgs, e.Error())
		}
	})
	if len(msgs) > 0 {
		return nil, nil, fmt.Errorf("loading packages: %s", strings.Join(msgs, "; "))
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.NaiveForm|ssa.InstantiateGenerics)
	prog.Build()
	return prog, ssaPkgs, nil
}
