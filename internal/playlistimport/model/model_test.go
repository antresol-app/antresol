package model_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLeafImports: model builds on the standard library only (go list
// -deps): the addon copies it as it is, so it must not pull in the core.
func TestLeafImports(t *testing.T) {
	const mod = "github.com/antresol-app/antresol/"
	ok := map[string]bool{mod + "internal/playlistimport/model": true}
	for _, p := range strings.Fields(goList(t, mod+"internal/playlistimport/model", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}")) {
		if strings.HasPrefix(p, mod) && !ok[p] {
			t.Errorf("imports %s: only the standard library are allowed", p)
		}
	}
	for _, p := range strings.Fields(goList(t, mod+"internal/playlistimport/model", "-f", "{{join .Imports \"\\n\"}}")) {
		if strings.Contains(strings.SplitN(p, "/", 2)[0], ".") && !ok[p] {
			t.Errorf("imports %s directly: only the standard library are allowed", p)
		}
	}
}

// goList runs go list at the module root (a relative -modfile in GOFLAGS
// names a file there) on pkg.
func goList(t *testing.T, pkg string, args ...string) string {
	t.Helper()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		up := filepath.Dir(root)
		if up == root {
			t.Fatal("no go.mod above the package")
		}
		root = up
	}
	cmd := exec.Command("go", append(append([]string{"list"}, args...), pkg)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v %s: %v", args, pkg, err)
	}
	return string(out)
}
