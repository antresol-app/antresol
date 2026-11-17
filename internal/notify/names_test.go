package notify

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The core's letters name no addon's ways: an addon tells its own topics
// through QueueAddon, so no exported helper of this package (in either
// build) speaks for one.
func TestNoAddonHelpersExported(t *testing.T) {
	gone := map[string]bool{"Downloaded": true, "Download": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var name *ast.Ident
			switch d := n.(type) {
			case *ast.FuncDecl:
				name = d.Name
			case *ast.TypeSpec:
				name = d.Name
			}
			if name != nil && gone[name.Name] {
				t.Errorf("%s: %s is exported", e.Name(), name.Name)
			}
			return true
		})
	}
	for _, topic := range AllTopics {
		if topic == "downloads" {
			t.Errorf("AllTopics lists %q", topic)
		}
	}
}
