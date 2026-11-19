package library

import "testing"

func TestNameKeyIsScriptBlind(t *testing.T) {
	for _, c := range [][]string{
		{"булгаков", "bulgakov"},
		{"достоевский", "dostoevsky", "dostoyevsky", "dostoevskii", "dostoievski"},
		{"толстой", "tolstoy", "tolstoi"},
		{"чехов", "chekhov", "tchekhov"},
		{"горький", "gorky"},
		{"гоголь", "gogol"},
		{"солженицын", "solzhenitsyn"},
		{"стругацкий", "strugatsky", "strugatski"},
		{"замятин", "zamyatin"},
		{"шолохов", "sholokhov"},
		{"пастернак", "pasternak"},
	} {
		for _, v := range c[1:] {
			if a, b := nameKey(c[0]), nameKey(v); a != b {
				t.Errorf("%s=%q, %s=%q", c[0], a, v, b)
			}
		}
	}
	for _, c := range [][2]string{{"булгаков", "bulgarin"}, {"толстой", "tolkien"}, {"гоголь", "google"}} {
		if nameKey(c[0]) == nameKey(c[1]) {
			t.Errorf("%s = %s", c[0], c[1])
		}
	}
}

func TestSameAuthor(t *testing.T) {
	for _, c := range []struct {
		names, authors []string
		want           bool
	}{
		{[]string{"Mikhail Bulgakov"}, []string{"Булгаков М. А."}, true},
		{[]string{"Fyodor Dostoevsky"}, []string{"Достоевский Фёдор Михайлович"}, true},
		{[]string{"George Orwell", "Джордж Оруэлл"}, []string{"Оруэлл Джордж"}, true},
		{[]string{"Leo Tolstoy"}, []string{"Чехов А. П."}, false},
		// initials alone tell nobody apart
		{[]string{"J. R. R."}, []string{"J. R. R. Tolkien"}, false},
		// no name on a side: no match (a translated title needs one)
		{nil, []string{"Булгаков"}, false},
		{[]string{"Bulgakov"}, nil, false},
	} {
		if got := SameAuthor(c.names, c.authors); got != c.want {
			t.Errorf("SameAuthor(%q, %q) = %v", c.names, c.authors, got)
		}
	}
}
