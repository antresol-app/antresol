package files

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportedNames lists the exported identifiers declared in the package's
// non-test files in dir: types, functions, methods, constants, variables,
// struct fields and interface methods.
func exportedNames(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		add := func(id *ast.Ident) {
			if id != nil && id.IsExported() {
				out[id.Name] = n
			}
		}
		ast.Inspect(f, func(x ast.Node) bool {
			switch d := x.(type) {
			case *ast.FuncDecl:
				add(d.Name)
			case *ast.TypeSpec:
				add(d.Name)
			case *ast.ValueSpec:
				for _, id := range d.Names {
					add(id)
				}
			case *ast.Field:
				for _, id := range d.Names {
					add(id)
				}
			}
			return true
		})
	}
	return out
}

// TestNamesAreNeutral: the package names held data and its holders, not
// what a particular addon keeps there.
func TestNamesAreNeutral(t *testing.T) {
	words := familyWords(t) // none where no word list is supplied
	for name, file := range exportedNames(t, ".") {
		if strings.Contains(name, "Seed") {
			t.Errorf("%s: exported %s names %q", file, name, "Seed")
		}
		for _, w := range words {
			if strings.Contains(strings.ToLower(name), w) {
				t.Errorf("%s: exported %s names %q", file, name, w)
			}
		}
	}
}
