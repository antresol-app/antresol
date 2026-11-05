package mediaimport

import "testing"

func TestPathMapLongestPrefix(t *testing.T) {
	m, err := ParsePathMap("/books=/a,/books/manga=/b")
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"/books/manga/x.cbz": "/b/x.cbz",
		"/booksX/y":          "/booksX/y",
		"/books":             "/a",
		"/books/ebooks/z":    "/a/ebooks/z",
		"/other":             "/other",
	} {
		if got := m.Map(in); got != want {
			t.Errorf("Map(%q) = %q, want %q", in, got, want)
		}
	}
	if empty, err := ParsePathMap(""); err != nil || len(empty) != 0 || empty.Map("/x") != "/x" {
		t.Fatalf("empty map: %v %v", empty, err)
	}
}

func TestParsePathMapBad(t *testing.T) {
	for _, s := range []string{"nofrom", "=/a", "/a=", "/a=/b,oops"} {
		if _, err := ParsePathMap(s); err == nil {
			t.Errorf("ParsePathMap(%q) accepted", s)
		}
	}
}
