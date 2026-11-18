// Package goodreads brings a profile's Goodreads shelves into Antresol's
// books: «Читаю сейчас», «Хочу прочитать» and «Прочитано».
//
// Goodreads has no API a new application can use (no keys since December
// 2020) and retired its shelf RSS feeds in October 2026, so the link is
// built on what is left, and is read only:
//
//   - the CSV export of the library (My Books, Import and export): the whole
//     history, with ISBNs and the dates read, uploaded by the profile;
//   - the public shelf widget (the embeddable list every public profile has):
//     the 100 newest books of a shelf, titles and authors only, pulled
//     periodically for a linked profile.
//
// Nothing is sent to Goodreads: it has no way in that does not need the
// profile's password.
package goodreads

import (
	"strings"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/release"
)

// Provider is the provider name of Goodreads links (the link's external id
// is the Goodreads user id, its nickname the name part of the profile URL).
const Provider = "goodreads"

// The exclusive shelves every Goodreads book is on one of.
const (
	ShelfRead    = "read"
	ShelfReading = "currently-reading"
	ShelfToRead  = "to-read"
)

// Shelves are the shelves that are brought over, in pull order.
var Shelves = []string{ShelfReading, ShelfToRead, ShelfRead}

// Entry is one book of a Goodreads shelf.
type Entry struct {
	Title, Author string
	// ISBN13 is the book's ISBN as 13 digits ("" = unknown: the widget has
	// none).
	ISBN13 string
	Shelf  string
	// ReadAt and AddedAt are the day read and the day put on the shelf
	// (zero = unknown).
	ReadAt, AddedAt time.Time
}

// baseTitle is a title without its series note: Goodreads writes
// «Dune (Dune, #1)», a library file «Dune».
func baseTitle(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.LastIndex(t, " ("); i > 0 && strings.HasSuffix(t, ")") && strings.Contains(t[i:], "#") {
		return strings.TrimSpace(t[:i])
	}
	return t
}

// titleForms are the spellings of a title to look a library book up by: as
// written, then without the series note. A subtitle is not cut off: «Foundation:
// Prelude» is not the book called «Foundation».
func titleForms(t string) []string {
	t, b := strings.TrimSpace(t), baseTitle(t)
	if b == "" || b == t {
		return []string{t}
	}
	return []string{t, b}
}

// key identifies a book of a profile whatever the source: the same title
// and first author, normalized, give the CSV's and the widget's row alike.
func (e Entry) key() string {
	return release.Normalize(baseTitle(e.Title)) + "|" + release.Normalize(e.Author)
}

// clean drops control characters (a CSV cell may carry anything).
func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != ' ' {
			return ' '
		}
		return r
	}, s))
}
