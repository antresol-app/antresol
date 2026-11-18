package goodreads

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
)

// ErrNotExport: the file is not a Goodreads library export (no title or
// shelf column).
var ErrNotExport = errors.New("goodreads: not a library export")

// ParseCSV reads a Goodreads library export (My Books → Import and export →
// Export Library). Columns are found by their headers, which Goodreads
// writes in English whatever the site's language. A row on a shelf other than
// the three exclusive ones (a custom exclusive shelf) is not a reading state
// and is skipped; skipped counts them.
func ParseCSV(r io.Reader) (entries []Entry, skipped int, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	cr := csv.NewReader(bytes.NewReader(raw))
	cr.FieldsPerRecord = -1 // a ragged row is read, not an error
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, 0, ErrNotExport
	}
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(h))
		if _, dup := col[h]; !dup {
			col[h] = i
		}
	}
	title, hasTitle := col["title"]
	author, hasAuthor := col["author"]
	shelf, hasShelf := col["exclusive shelf"]
	if !hasTitle || !hasAuthor || !hasShelf {
		return nil, 0, ErrNotExport
	}
	cell := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return entries, skipped, nil
		}
		if err != nil {
			return nil, 0, ErrNotExport
		}
		if len(rec) <= max(title, author, shelf) {
			continue // a blank or cut line
		}
		e := Entry{Title: clean(rec[title]), Author: clean(rec[author]),
			Shelf: strings.ToLower(strings.TrimSpace(rec[shelf]))}
		if e.Title == "" {
			continue
		}
		switch e.Shelf {
		case ShelfRead, ShelfReading, ShelfToRead:
		default:
			skipped++
			continue
		}
		// the ISBN-13 column first; Goodreads writes both as ="…" so a
		// spreadsheet keeps the leading zeros
		if e.ISBN13 = bookmeta.ISBN13(unwrap(cell(rec, "isbn13"))); e.ISBN13 == "" {
			e.ISBN13 = bookmeta.ISBN13(unwrap(cell(rec, "isbn")))
		}
		e.ReadAt = day(cell(rec, "date read"))
		e.AddedAt = day(cell(rec, "date added"))
		entries = append(entries, e)
	}
}

// unwrap is a cell without Goodreads' ="…" wrapper.
func unwrap(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "=")
	return strings.Trim(s, `"' `)
}

// day reads the export's dates (2023/05/14; the others for a file a
// spreadsheet has rewritten). Zero when there is none.
func day(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006/01/02", "2006-01-02", "01/02/2006", "2 Jan 2006", "Jan 2, 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
