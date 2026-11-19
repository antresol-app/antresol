// Package booknames finds what a book is called in other languages: the
// work it is an edition of, with the work's titles in Russian and English,
// its author's names, and the ISBNs of its other editions. A reading list
// kept abroad (Goodreads writes «The Master and Margarita», "Mikhail
// Bulgakov") then finds the library's Russian edition («Мастер и
// Маргарита», «Булгаков М. А.»), and a Russian list an English one.
//
// The sources are free and need no key, and are asked in turn, the answers
// put together:
//
//   - Wikidata: the work item of the author's that has the title among its
//     labels and aliases (wikidata.Client.WorkNames);
//   - Open Library: the work of the book's ISBN, else of its title and
//     author, with the ISBNs of all its editions;
//   - the AI helper's light model, only when both know nothing of the book
//     and the server has one.
//
// Answers are kept in book_names: a found work is asked again after half a
// year, a book nobody knew after a month.
package booknames

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/openlibrary"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

// How long an answer is kept before the sources are asked again.
const (
	foundFor  = 180 * 24 * time.Hour
	missFor   = 30 * 24 * time.Hour
	aiFailFor = 24 * time.Hour // the AI helper failed: ask it again tomorrow
)

// Sources.
const (
	SourceWikidata    = "wikidata"
	SourceOpenLibrary = "openlibrary"
	SourceAI          = "ai"
)

// Book is a book as another catalog lists it.
type Book struct {
	Title, Author string
	ISBN13        string // "" = unknown
}

func (b Book) key() string {
	return metadata.NormTitle(b.Title) + "|" + metadata.NormTitle(b.Author) + "|" + b.ISBN13
}

// Names are what the book's work is called elsewhere.
type Names struct {
	Titles  []string `json:"t,omitempty"`
	Authors []string `json:"a,omitempty"`
	ISBNs   []string `json:"i,omitempty"` // ISBN-13 of the work's editions
	Works   []string `json:"w,omitempty"` // Open Library works ("/works/OL676009W")
	Sources []string `json:"s,omitempty"`
}

// Empty: nobody knew the book.
func (n Names) Empty() bool {
	return len(n.Titles) == 0 && len(n.ISBNs) == 0 && len(n.Works) == 0
}

func addTo(list *[]string, vs ...string) {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(*list, v) {
			*list = append(*list, v)
		}
	}
}

// wikidataOn: w is set and not switched off in the settings
// (*wikidata.Client's Active, «Внешние сервисы»).
func wikidataOn(w Wikidata) bool {
	if w == nil {
		return false
	}
	if a, ok := w.(interface{ Active() bool }); ok {
		return a.Active()
	}
	return true
}

// Wikidata is the part of *wikidata.Client a lookup uses.
type Wikidata interface {
	WorkNames(ctx context.Context, title, author string) ([]wikidata.WorkNames, error)
}

// OpenLibrary is the part of *openlibrary.Client a lookup uses.
type OpenLibrary interface {
	Works(ctx context.Context, title, author, isbn string) ([]openlibrary.Doc, error)
}

// Asker is a language model asked for one JSON object (*channels.LLM).
type Asker interface {
	AskJSON(ctx context.Context, system, user string) (string, error)
}

// Resolver looks books up and keeps the answers. A nil source is not
// asked; AI is asked for the model of the moment (nil: none configured).
type Resolver struct {
	DB          *sql.DB
	Wikidata    Wikidata
	OpenLibrary OpenLibrary
	AI          func(ctx context.Context) Asker
	Now         func() time.Time
	Log         *slog.Logger
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Resolver) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Cached is the kept answer for b; ok is false when there is none yet or
// it is due to be asked again (an old answer is still returned then).
func (r *Resolver) Cached(ctx context.Context, b Book) (Names, bool, error) {
	var raw string
	var due int64
	err := r.DB.QueryRowContext(ctx, `SELECT names_json, due_at FROM book_names WHERE key=?`, b.key()).Scan(&raw, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return Names{}, false, nil
	}
	if err != nil {
		return Names{}, false, err
	}
	var n Names
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return Names{}, false, nil // unreadable: ask again
	}
	return n, r.now().Unix() < due, nil
}

// Lookup asks the sources about b and keeps what they say. A source that
// cannot be reached or answers badly is an error and nothing is kept (the
// caller asks again later); a source that does not know the book is no
// error.
func (r *Resolver) Lookup(ctx context.Context, b Book) (Names, error) {
	b.Title, b.Author = strings.TrimSpace(b.Title), strings.TrimSpace(b.Author)
	var n Names
	if wikidataOn(r.Wikidata) {
		works, err := r.Wikidata.WorkNames(ctx, b.Title, b.Author)
		if err := sourceErr(err); err != nil {
			return Names{}, err
		}
		for _, w := range works {
			addTo(&n.Titles, w.Titles...)
			addTo(&n.Authors, w.Authors...)
		}
		if len(works) > 0 {
			addTo(&n.Sources, SourceWikidata)
		}
	}
	if r.OpenLibrary != nil {
		if err := r.openLibrary(ctx, b, &n); err != nil {
			return Names{}, err
		}
	}
	keep := foundFor
	if n.Empty() {
		keep = missFor
		if ai := r.ai(ctx); ai != nil {
			if err := askAI(ctx, ai, b, &n); err != nil {
				if ctx.Err() != nil {
					return Names{}, ctx.Err()
				}
				r.log().Info("booknames: ai", "err", err)
				keep = aiFailFor
			} else if !n.Empty() {
				keep = foundFor
			}
		}
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return Names{}, err
	}
	if _, err := r.DB.ExecContext(ctx, `INSERT INTO book_names(key, names_json, due_at) VALUES(?,?,?)
	  ON CONFLICT(key) DO UPDATE SET names_json=excluded.names_json, due_at=excluded.due_at`,
		b.key(), string(raw), r.now().Add(keep).Unix()); err != nil {
		return Names{}, err
	}
	return n, nil
}

func (r *Resolver) ai(ctx context.Context) Asker {
	if r.AI == nil {
		return nil
	}
	return r.AI(ctx)
}

// sourceErr: a definite "no such thing" (404, another 4xx) is no answer,
// anything else (no connection, 5xx, 429, garbage) an error.
func sourceErr(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if metadata.Classify(err) == metadata.Miss {
		return nil
	}
	return err
}

// openLibrary adds the Open Library work of b's ISBN (an ISBN is the
// edition itself: no further check), else the works of its title whose
// title is b's and whose author shares a name with b's (in any script).
func (r *Resolver) openLibrary(ctx context.Context, b Book, n *Names) error {
	var docs []openlibrary.Doc
	if b.ISBN13 != "" {
		found, err := r.OpenLibrary.Works(ctx, "", "", b.ISBN13)
		if err := sourceErr(err); err != nil {
			return err
		}
		docs = append(docs, found...)
	}
	if len(docs) == 0 && b.Title != "" && b.Author != "" {
		found, err := r.OpenLibrary.Works(ctx, b.Title, b.Author, "")
		if err := sourceErr(err); err != nil {
			return err
		}
		t := metadata.NormTitle(b.Title)
		for _, d := range found {
			if metadata.NormTitle(d.Title) == t && library.SameAuthor([]string{b.Author}, d.Authors) {
				docs = append(docs, d)
			}
		}
	}
	for _, d := range docs {
		if d.Key == "" {
			continue
		}
		addTo(&n.Works, d.Key)
		addTo(&n.Titles, d.Title)
		addTo(&n.Authors, d.Authors...)
		for _, i := range d.ISBN {
			addTo(&n.ISBNs, bookmeta.ISBN13(i))
		}
		addTo(&n.Sources, SourceOpenLibrary)
	}
	return nil
}

const aiSystem = `You identify books. The user gives a book's title and author as one catalog lists them.
Answer with one JSON object: {"known": true or false, "title_ru": the title the book is published under in Russian, "title_en": its title in English, "author_ru": the author's name in Russian, "author_en": the author's name in English}.
Answer {"known": false} when you are not sure which book it is. Never make a title up.`

// maxAIName: a longer "title" is not one.
const maxAIName = 200

// askAI asks the model for the book's Russian and English titles.
func askAI(ctx context.Context, ai Asker, b Book, n *Names) error {
	out, err := ai.AskJSON(ctx, aiSystem, fmt.Sprintf("Title: %s\nAuthor: %s", b.Title, b.Author))
	if err != nil {
		return err
	}
	var a struct {
		Known    bool   `json:"known"`
		TitleRU  string `json:"title_ru"`
		TitleEN  string `json:"title_en"`
		AuthorRU string `json:"author_ru"`
		AuthorEN string `json:"author_en"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return fmt.Errorf("answer is not the object asked: %w", err)
	}
	if !a.Known {
		return nil
	}
	short := func(s string) string {
		if len([]rune(s)) > maxAIName {
			return ""
		}
		return s
	}
	before := len(n.Titles)
	addTo(&n.Titles, short(a.TitleRU), short(a.TitleEN))
	if len(n.Titles) > before {
		addTo(&n.Authors, short(a.AuthorRU), short(a.AuthorEN))
		addTo(&n.Sources, SourceAI)
	}
	return nil
}
