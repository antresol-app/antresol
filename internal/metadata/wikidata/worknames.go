package wikidata

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/metadata"
)

// Sizes of a work lookup (WorkNames): the people a name may be, and the
// written works of theirs a title search returns.
const (
	authorCandidates = 5
	workCandidates   = 10
)

// WorkNames is what a written work is called in Russian and English: the
// labels and aliases of its item (and of an edition found alike), and its
// author's names.
type WorkNames struct {
	ID      string   // Q165318
	Titles  []string // «Преступление и наказание», "Crime and Punishment"…
	Authors []string // «Фёдор Михайлович Достоевский», "Fyodor Dostoevsky"…
}

// cyrillic: a name written in Russian is looked up among the Russian
// labels, any other among the English ones.
func cyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func langOf(s string) string {
	if cyrillic(s) {
		return "ru"
	}
	return "en"
}

// names are the Russian and English labels and aliases of an entity.
func (e entity) names() []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, l := range []string{"ru", "en"} {
		add(e.Labels[l].Value)
		for _, a := range e.Aliases[l] {
			add(a.Value)
		}
	}
	return out
}

// WorkNames finds the written work called title (in any language Wikidata
// has a label or an alias in) by an author called author, and returns what
// the work is called in Russian and English. Three small requests: the
// people named author (wbsearchentities), the works one of them wrote that
// a title search finds (a statement search: P50 is one of them; films and
// scholarly articles left out), and the Russian and English labels and
// aliases of those works and people. A work counts only when one of its
// names is title (normalised): the search's best guess is never taken on
// trust. Nothing found is an empty list; no author, no lookup.
func (c *Client) WorkNames(ctx context.Context, title, author string) ([]WorkNames, error) {
	t, a := metadata.NormTitle(title), strings.TrimSpace(author)
	if t == "" || metadata.NormTitle(a) == "" {
		return []WorkNames{}, nil
	}
	lang := langOf(a)
	var people struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	q := url.Values{"action": {"wbsearchentities"}, "search": {a}, "language": {lang}, "uselang": {lang},
		"type": {"item"}, "limit": {"5"}}
	if err := c.get(ctx, q, &people); err != nil {
		return nil, err
	}
	var authorIDs, terms []string
	for _, p := range people.Search {
		if len(authorIDs) < authorCandidates && strings.HasPrefix(p.ID, "Q") {
			authorIDs = append(authorIDs, p.ID)
			terms = append(terms, "P50="+p.ID)
		}
	}
	if len(authorIDs) == 0 {
		return []WorkNames{}, nil
	}
	// a pair who wrote together is an author of its own («Пикник на
	// обочине» is by the Strugatsky brothers, not by Arkady): the groups
	// one of the people is a part of (P527) write the works too
	var groups struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	var parts []string
	for _, id := range authorIDs {
		parts = append(parts, "P527="+id)
	}
	q = url.Values{"action": {"query"}, "list": {"search"}, "srprop": {""}, "srlimit": {"5"},
		"srsearch": {"haswbstatement:" + strings.Join(parts, "|")}}
	if err := c.get(ctx, q, &groups); err != nil {
		return nil, err
	}
	for _, g := range groups.Query.Search {
		if strings.HasPrefix(g.Title, "Q") && !slices.Contains(authorIDs, g.Title) {
			authorIDs = append(authorIDs, g.Title)
			terms = append(terms, "P50="+g.Title)
		}
	}
	var found struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	q = url.Values{"action": {"query"}, "list": {"search"}, "srprop": {""}, "srlimit": {"10"},
		"srsearch": {strings.Join(searchWords(t), " ") + " haswbstatement:" + strings.Join(terms, "|") + " -haswbstatement:P57 -haswbstatement:P31=" + scholarlyArticle}}
	if err := c.get(ctx, q, &found); err != nil {
		return nil, err
	}
	var workIDs []string
	for _, s := range found.Query.Search {
		if len(workIDs) < workCandidates && strings.HasPrefix(s.Title, "Q") && !slices.Contains(workIDs, s.Title) {
			workIDs = append(workIDs, s.Title)
		}
	}
	if len(workIDs) == 0 {
		return []WorkNames{}, nil
	}
	ents, err := c.entities(ctx, append(slices.Clone(workIDs), authorIDs...), "labels|aliases")
	if err != nil {
		return nil, err
	}
	// the people of the search whose name is the author's (a search by
	// prefix finds "Leo Tolstoy Jr." for "Leo Tolstoy"): their names; the
	// statement search does not say which of them wrote which work
	var authors []string
	for _, id := range authorIDs {
		ns := ents[id].names()
		if sameName(a, ns) {
			for _, n := range ns {
				if !slices.Contains(authors, n) {
					authors = append(authors, n)
				}
			}
		}
	}
	// the works one of whose names is the title; else the one work whose
	// names hold all the title's words ("Harry Potter and the Sorcerer's
	// Stone": the label is "…Philosopher's Stone", an alias "Sorcerer's
	// Stone"), when only one does
	out := []WorkNames{}
	var loose []WorkNames
	for _, id := range workIDs {
		ns := ents[id].names()
		w := WorkNames{ID: id, Titles: ns, Authors: authors}
		switch {
		case slices.ContainsFunc(ns, func(n string) bool { return sameTitle(n, t) }):
			out = append(out, w)
		case hasWords(ns, t):
			loose = append(loose, w)
		}
	}
	if len(out) == 0 && len(loose) == 1 {
		out = loose
	}
	return out, nil
}

// articles: an English title is listed with its article or without ("Heart
// of a Dog", "The Heart of a Dog").
var articles = []string{"the ", "a ", "an "}

func bare(t string) string {
	for _, a := range articles {
		if s, ok := strings.CutPrefix(t, a); ok && s != "" {
			return s
		}
	}
	return t
}

// sameTitle: name is the normalised title t, articles aside.
func sameTitle(name, t string) bool {
	n := metadata.NormTitle(name)
	return n == t || bare(n) == bare(t)
}

// searchWords are the words of t a search needs: a letter alone (the "s"
// of "Sorcerer's") matches nothing.
func searchWords(t string) []string {
	var out []string
	for _, w := range strings.Fields(t) {
		if len([]rune(w)) > 1 {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return strings.Fields(t)
	}
	return out
}

// hasWords: every word of t of 2+ letters is a word of one of names.
func hasWords(names []string, t string) bool {
	have := map[string]bool{}
	for _, n := range names {
		for _, w := range strings.Fields(metadata.NormTitle(n)) {
			have[w] = true
		}
	}
	for _, w := range searchWords(t) {
		if !have[w] && !slices.Contains(articles, w+" ") {
			return false
		}
	}
	return true
}

// sameName: one of names is author, or ends in its last word (a surname
// as the search language writes it: "Dostoevsky" in "Fyodor Dostoevsky").
func sameName(author string, names []string) bool {
	w := strings.Fields(metadata.NormTitle(author))
	if len(w) == 0 {
		return false
	}
	last := w[len(w)-1]
	for _, n := range names {
		for _, x := range strings.Fields(metadata.NormTitle(n)) {
			if x == last {
				return true
			}
		}
	}
	return false
}
