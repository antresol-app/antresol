package channels

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestDisplayNamesInViewerLanguage(t *testing.T) {
	for _, c := range []struct {
		ch         Channel
		lang, want string
	}{
		{Channel{Name: "Все сериалы", AutoKey: "series"}, "en", "All Series"},
		{Channel{Name: "Мультсериалы", AutoKey: "toons"}, "en", "Animated Series"},
		{Channel{Name: "Все фильмы", AutoKey: "movies"}, "en", "All Movies"},
		{Channel{Name: "Форс-мажоры", AutoKey: "series:12"}, "en", "Форс-мажоры"},
		{Channel{Name: "Кино 90-х", AutoKey: "decade:1990"}, "en", "90s Movies"},
		{Channel{Name: "Мой канал", AutoKey: "movies", Edited: true}, "en", "Мой канал"},
		{Channel{Name: "Все сериалы", AutoKey: "series"}, "ru", "Все сериалы"},
	} {
		if got, _ := Display(c.lang, c.ch); got != c.want {
			t.Errorf("%s in %s: %s, want %s", c.ch.AutoKey, c.lang, got, c.want)
		}
	}
}

// Every generated channel's name and group has an English one: generate.go
// is exempt from the Russian-literals scan (its names are stored data), so
// a channel added there without a catalog entry would show in Russian.
func TestGeneratedChannelNamesAreTranslated(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "generate.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{} // name -> auto key (a prefix is enough)
	for _, n := range genreChannel {
		names[n] = "genre:x"
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, isID := call.Fun.(*ast.Ident); !isID || id.Name != "add" || len(call.Args) < 3 {
			return true
		}
		key, _ := call.Args[0].(*ast.BasicLit)
		name, _ := call.Args[1].(*ast.BasicLit)
		if key != nil && name != nil && name.Kind == token.STRING {
			k, _ := strconv.Unquote(key.Value)
			v, _ := strconv.Unquote(name.Value)
			names[v] = k
		}
		return true
	})
	if len(names) < 15 {
		t.Fatalf("found %d names in generate.go: the scan is broken", len(names))
	}
	for name, key := range names {
		if got, _ := Display("en", Channel{Name: name, AutoKey: key}); got == name || strings.ContainsAny(got, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
			t.Errorf("%q (%s) has no English name: %q", name, key, got)
		}
	}
	for _, cat := range []string{CatGeneral, CatGenres, CatDecades, CatKids, CatDocs, CatSeries} {
		if _, got := Display("en", Channel{Name: "x", Category: cat, AutoKey: "movies"}); got == cat {
			t.Errorf("group %q has no English name", cat)
		}
	}
}
