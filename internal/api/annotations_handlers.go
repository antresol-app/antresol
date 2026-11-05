package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

// Bookmarks, highlights and notes of the caller in a book, and bookmarks in
// an audiobook (migration 0060):
//
//	GET    /api/annotations/{itemId}            {annotations: [...]}, tombstones included
//	POST   /api/annotations/{itemId}            create (the id is made when not given) → 201
//	PUT    /api/annotations/{itemId}/{id}       create or change → 200, the row as stored
//	DELETE /api/annotations/{itemId}/{id}       → 200, the tombstone (a delete always wins)
//	GET    /api/annotations/{itemId}/export.md  the highlights, notes and bookmarks as Markdown
//
// Order is the server's, never a device clock: an accepted write gets the
// server's time (updatedAt) and the profile's next revision (rev), and the
// client adopts the row it gets back. A write sends the rev of the copy it
// changed (0 for a new one) and its device time as updatedAt, a hint only
// (progress.applies): an offline device cannot bring back what was
// deleted elsewhere, and a delete wins whatever its device's clock says.
func mountAnnotations(r chi.Router, h *lib) {
	r.Get("/api/annotations/{itemId}", h.annotations)
	r.Post("/api/annotations/{itemId}", h.putAnnotation)
	r.Get("/api/annotations/{itemId}/export.md", h.exportAnnotations)
	r.Put("/api/annotations/{itemId}/{id}", h.putAnnotation)
	r.Delete("/api/annotations/{itemId}/{id}", h.deleteAnnotation)
}

// annotationJSON is an annotation on the wire.
type annotationJSON struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Locator   string  `json:"locator"`
	Fraction  float64 `json:"fraction"`
	Label     string  `json:"label"`
	Excerpt   string  `json:"excerpt"`
	Note      string  `json:"note"`
	Color     string  `json:"color"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Rev       int64   `json:"rev"`
	Deleted   bool    `json:"deleted"`
}

func annotationOutJSON(a progress.Annotation) annotationJSON {
	return annotationJSON{ID: a.ID, Kind: a.Kind, Locator: a.Locator, Fraction: a.Fraction, Label: a.Label, Excerpt: a.Excerpt,
		Note: a.Note, Color: a.Color, CreatedAt: a.CreatedAt.UTC().Format(wireTime), UpdatedAt: a.UpdatedAt.UTC().Format(wireTime), Rev: a.Rev, Deleted: a.Deleted}
}

var audiobookLocator = regexp.MustCompile(`^ms:\d{1,12}$`)

// annotatable is the item of {itemId} when it takes annotations: books all
// three kinds, comic volumes and audiobooks bookmarks only (an
// audiobook's at "ms:N").
func (h *lib) annotatable(w http.ResponseWriter, r *http.Request) (library.Item, bool) {
	it, ok := h.itemParam(w, r, "itemId")
	if !ok {
		return it, false
	}
	switch it.Kind {
	case library.KindBook, library.KindComicVolume, library.KindAudiobook:
		return it, true
	}
	writeErr(w, r, http.StatusBadRequest, "Заметки для этого элемента не сохраняются")
	return it, false
}

func (h *lib) annotations(w http.ResponseWriter, r *http.Request) {
	it, ok := h.annotatable(w, r)
	if !ok {
		return
	}
	list, err := h.Progress.Annotations(r.Context(), userOf(r).ID, it.ID)
	if err != nil {
		internalErr(w, r, "annotations", err)
		return
	}
	out := make([]annotationJSON, 0, len(list))
	for _, a := range list {
		out = append(out, annotationOutJSON(a))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"annotations": out})
}

// parseWireTime reads a client's RFC 3339 time ("": zero, the server's now).
func parseWireTime(w http.ResponseWriter, r *http.Request, v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Неверное время записи")
		return t, false
	}
	return t, true
}

func newAnnotationID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *lib) putAnnotation(w http.ResponseWriter, r *http.Request) {
	it, ok := h.annotatable(w, r)
	if !ok {
		return
	}
	var in struct {
		ID        string  `json:"id"`
		Kind      string  `json:"kind"`
		Locator   string  `json:"locator"`
		Fraction  float64 `json:"fraction"`
		Label     string  `json:"label"`
		Excerpt   string  `json:"excerpt"`
		Note      string  `json:"note"`
		Color     string  `json:"color"`
		CreatedAt string  `json:"createdAt"`
		UpdatedAt string  `json:"updatedAt"`
		Rev       int64   `json:"rev"`
		Deleted   bool    `json:"deleted"`
	}
	if !decode(w, r, &in) {
		return
	}
	created := r.Method == http.MethodPost
	if id := chi.URLParam(r, "id"); id != "" {
		in.ID = id
	} else if in.ID == "" {
		in.ID = newAnnotationID()
	}
	if it.Kind != library.KindBook && in.Kind != progress.KindBookmark {
		writeErr(w, r, http.StatusBadRequest, "Здесь можно ставить только закладки")
		return
	}
	if it.Kind == library.KindAudiobook && !audiobookLocator.MatchString(in.Locator) {
		writeErr(w, r, http.StatusBadRequest, "Неверная позиция")
		return
	}
	if (in.Kind == progress.KindHighlight || in.Kind == progress.KindNote) && !strings.HasPrefix(in.Locator, "epubcfi(") {
		writeErr(w, r, http.StatusBadRequest, "Неверная позиция")
		return
	}
	at, ok := parseWireTime(w, r, in.UpdatedAt)
	if !ok {
		return
	}
	a, err := h.Progress.PutAnnotation(r.Context(), userOf(r).ID, it.ID, progress.Annotation{
		ID: in.ID, Kind: in.Kind, Locator: in.Locator, Fraction: in.Fraction, Label: in.Label, Excerpt: in.Excerpt,
		Note: in.Note, Color: in.Color, UpdatedAt: at, Rev: in.Rev, Deleted: in.Deleted,
	})
	var bad *progress.AnnotationError
	if errors.As(err, &bad) {
		writeErr(w, r, http.StatusBadRequest, bad.Msg)
		return
	}
	if err != nil {
		internalErr(w, r, "annotation put", err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, annotationOutJSON(a))
}

func (h *lib) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	it, ok := h.annotatable(w, r)
	if !ok {
		return
	}
	a, err := h.Progress.DeleteAnnotation(r.Context(), userOf(r).ID, it.ID, chi.URLParam(r, "id"))
	var bad *progress.AnnotationError
	if errors.As(err, &bad) {
		writeErr(w, r, http.StatusBadRequest, bad.Msg)
		return
	}
	if err != nil {
		internalErr(w, r, "annotation delete", err)
		return
	}
	writeJSON(w, http.StatusOK, annotationOutJSON(a))
}

// mdSpecial keeps a line of the book's text from turning into Markdown markup.
var mdSpecial = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "#", `\#`, "[", `\[`, "]", `\]`, "<", `\<`)

// mdBlockStart is what turns a line into a list, a quote, a heading, a rule or a table.
var mdBlockStart = regexp.MustCompile(`^(\d+[.)]|[-+>=|~]|---)`)

// mdLine is one line of the book's text or a note as plain text: inline
// markup escaped, and a line start that would open a block (- 1. > ---) too.
func mdLine(s string) string {
	s = mdSpecial.Replace(strings.TrimSpace(s))
	if m := mdBlockStart.FindString(s); m != "" {
		if d := strings.IndexAny(m, ".)"); d > 0 {
			return s[:d] + `\` + s[d:] // 1. → 1\.
		}
		return `\` + s
	}
	return s
}

// mdText is lines of text, each kept plain (with a hard break between them).
func mdText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = mdLine(l)
	}
	return strings.Join(lines, "  \n")
}

func mdQuote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + mdLine(l)
	}
	return strings.Join(lines, "\n")
}

// where is a line naming the place: its label and how far into the book.
func annotationWhere(a progress.Annotation) string {
	pct := fmt.Sprintf("%d %%", int(a.Fraction*100+0.5))
	if a.Label == "" {
		return pct
	}
	return mdSpecial.Replace(a.Label) + " · " + pct
}

// exportAnnotations is the item's highlights and notes, then its bookmarks,
// in reading order, as a Markdown file.
func (h *lib) exportAnnotations(w http.ResponseWriter, r *http.Request) {
	it, ok := h.annotatable(w, r)
	if !ok {
		return
	}
	list, err := h.Progress.Annotations(r.Context(), userOf(r).ID, it.ID)
	if err != nil {
		internalErr(w, r, "annotations export", err)
		return
	}
	ctx := r.Context()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", mdSpecial.Replace(it.Title))
	var marks []progress.Annotation
	wroteHeading := false
	for _, a := range list {
		if a.Deleted {
			continue
		}
		if a.Kind == progress.KindBookmark {
			marks = append(marks, a)
			continue
		}
		if !wroteHeading {
			fmt.Fprintf(&b, "\n## %s\n", i18n.T(ctx, "Выделения и заметки"))
			wroteHeading = true
		}
		fmt.Fprintf(&b, "\n%s\n", mdQuote(a.Excerpt))
		if note := strings.TrimSpace(a.Note); note != "" {
			fmt.Fprintf(&b, "\n%s\n", mdText(note))
		}
		fmt.Fprintf(&b, "\n*%s*\n", annotationWhere(a))
	}
	if len(marks) > 0 {
		fmt.Fprintf(&b, "\n## %s\n\n", i18n.T(ctx, "Закладки"))
		for _, a := range marks {
			line := "- " + annotationWhere(a)
			if note := strings.TrimSpace(a.Note); note != "" {
				line += " — " + mdSpecial.Replace(strings.Join(strings.Fields(note), " "))
			}
			b.WriteString(line + "\n")
		}
	}
	if !wroteHeading && len(marks) == 0 {
		fmt.Fprintf(&b, "\n%s\n", i18n.T(ctx, "Заметок пока нет."))
	}
	name := strings.NewReplacer("/", " ", `\`, " ", ":", " ").Replace(it.Title) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(b.String()))
}
