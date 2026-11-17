package library

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNamesAreNeutral: no exported identifier of the package holds one of
// familyWords' words (none where no word list is supplied), and the old
// download column is read in one place only, the Repacker's skip
// (repack.go), until stage 4 decides on it.
func TestNamesAreNeutral(t *testing.T) {
	words := familyWords(t)
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "torrent_infohash") && n != "repack.go" {
			t.Errorf("%s names the column torrent_infohash (only repack.go reads it)", n)
		}
		f, err := parser.ParseFile(fset, n, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(x ast.Node) bool {
			var ids []*ast.Ident
			switch d := x.(type) {
			case *ast.FuncDecl:
				ids = []*ast.Ident{d.Name}
			case *ast.TypeSpec:
				ids = []*ast.Ident{d.Name}
			case *ast.ValueSpec:
				ids = d.Names
			case *ast.Field:
				ids = d.Names
			}
			for _, id := range ids {
				for _, w := range words {
					if id.IsExported() && strings.Contains(strings.ToLower(id.Name), w) {
						t.Errorf("%s: exported %s names %s", n, id.Name, w)
					}
				}
			}
			return true
		})
	}
}
