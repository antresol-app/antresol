package i18n

import (
	"bufio"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The repository's Go sources: ../../internal and ../../cmd.
var sourceRoots = []string{"../../internal", "../../cmd"}

type literal struct {
	pos  string
	file string // slash path relative to the repo root
	text string
}

// goSources parses every non-test Go file under the roots that this build
// compiles: the core build (built without -tags sources) leaves out the full
// build's files, and their catalog keys with them.
func goSources(t *testing.T) (map[string]*ast.File, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	bctx := build.Default
	if !libraryEdition {
		bctx.BuildTags = append(bctx.BuildTags, "sources")
	}
	for _, root := range sourceRoots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			if ok, err := bctx.MatchFile(filepath.Dir(p), d.Name()); err != nil || !ok {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
			files[rel] = f
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files, fset
}

// keyFuncs: i18n functions whose argument at the index is a catalog key.
var keyFuncs = map[string]int{"T": 1, "TL": 1, "Errorf": 0, "TC": 2}

// pluralFuncs: i18n functions taking the Russian forms at 2, 3, 4.
var pluralFuncs = map[string]int{"Plural": 2, "PluralL": 2, "PluralWord": 2}

func strLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

// TestCatalogsComplete: every key used in the code (a literal given to T,
// TL, TC, Errorf) and every key registered for any language has a
// translation in every language, with the same format verbs.
func TestCatalogsComplete(t *testing.T) {
	if c := Conflicts(); len(c) > 0 {
		t.Errorf("keys registered twice with different translations:\n%s", strings.Join(c, "\n"))
	}
	files, fset := goSources(t)
	used := map[string]string{} // key → position
	for rel, f := range files {
		inI18n := strings.HasPrefix(rel, "internal/i18n/")
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "i18n" {
					name = fn.Sel.Name
				}
			case *ast.Ident:
				if inI18n {
					name = fn.Name
				}
			}
			if name == "" {
				return true
			}
			if i, ok := keyFuncs[name]; ok && i < len(call.Args) {
				if k, ok := strLit(call.Args[i]); ok {
					if name == "TC" {
						if c, ok := strLit(call.Args[1]); ok {
							k = c + ctxSep + k
						}
					}
					used[k] = fset.Position(call.Pos()).String()
				}
			}
			if i, ok := pluralFuncs[name]; ok && i+2 < len(call.Args) {
				var forms [3]string
				all := true
				for j := range 3 {
					forms[j], ok = strLit(call.Args[i+j])
					all = all && ok
				}
				if all {
					for _, l := range Languages[1:] {
						if !HasPlural(l, forms[0], forms[1], forms[2]) {
							t.Errorf("%s: plural %v has no %s forms", fset.Position(call.Pos()), forms, l)
						}
					}
				}
			}
			return true
		})
	}
	all := map[string]bool{}
	for k := range used {
		all[k] = true
	}
	for _, l := range Languages[1:] {
		for _, k := range Keys(l) {
			all[k] = true
		}
	}
	var keys []string
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ruText, _ := lookup(RU, k)
		for _, l := range Languages[1:] {
			tr, ok := Translation(l, k)
			if !ok {
				t.Errorf("%s: %q has no %s translation", used[k], k, l)
				continue
			}
			if strings.TrimSpace(tr) == "" {
				t.Errorf("%q: empty %s translation", k, l)
			}
			if a, b := sortedVerbs(ruText), sortedVerbs(tr); a != b {
				t.Errorf("%q: format verbs differ: ru %q, %s %q (%q)", k, a, l, b, tr)
			}
			if hasCyrillic(tr) {
				t.Errorf("%q: %s translation %q has Cyrillic", k, l, tr)
			}
		}
	}
}

func sortedVerbs(s string) string {
	v := Verbs(s)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return string(v)
}

// keepRules are the Russian literals deliberately left out of the
// catalogs: testdata/keep/*.txt, one rule per line —
//
//	internal/pkg/file.go            # every literal of the file (reason)
//	internal/pkg/file.go: текст     # one literal (reason)
//
// "#" starts the reason; a reason is required.
func keepRules(t *testing.T) (files map[string]bool, lits map[string]map[string]bool) {
	t.Helper()
	files, lits = map[string]bool{}, map[string]map[string]bool{}
	paths, _ := filepath.Glob("testdata/keep/*.txt")
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		ln := 0
		for sc.Scan() {
			ln++
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			rule, reason, ok := strings.Cut(line, " #")
			if !ok || strings.TrimSpace(reason) == "" {
				t.Errorf("%s:%d: a keep rule needs a reason after \" #\"", p, ln)
			}
			rule = strings.TrimSpace(rule)
			file, lit, isLit := strings.Cut(rule, ": ")
			if !isLit {
				files[file] = true
				continue
			}
			if lits[file] == nil {
				lits[file] = map[string]bool{}
			}
			lits[file][lit] = true
		}
		f.Close()
	}
	return files, lits
}

// TestRussianLiteralsTranslated: every Cyrillic string literal in the
// server's code (comments and tests aside) is a catalog key, the Russian
// form of a registered plural, or kept on purpose (testdata/keep).
func TestRussianLiteralsTranslated(t *testing.T) {
	files, fset := goSources(t)
	keepFiles, keepLits := keepRules(t)
	known := map[string]bool{}
	for _, l := range Languages[1:] {
		for _, k := range Keys(l) {
			known[k] = true
			if _, after, ok := strings.Cut(k, ctxSep); ok {
				known[after] = true
			}
		}
	}
	mu.RLock()
	for f := range ruForms {
		known[f] = true
	}
	mu.RUnlock()
	var missing []string
	for rel, f := range files {
		if strings.HasPrefix(rel, "internal/i18n/") || keepFiles[rel] {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			s, ok := n.(*ast.BasicLit)
			if !ok || s.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(s.Value)
			if err != nil || !hasCyrillic(v) || known[v] || keepLits[rel][v] {
				return true
			}
			missing = append(missing, fset.Position(s.Pos()).String()+": "+strconv.Quote(v))
			return true
		})
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("untranslated Russian literal %s", m)
	}
}
