package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

func mountBooks(r chi.Router, h *lib) {
	r.Get("/api/books/home", h.booksHome)
	r.Get("/api/books", h.books)
	r.Get("/api/books/{id}", h.bookDetail)
	r.Get("/api/books/{id}/transcript", h.bookTranscript)
	mountKindle(r, h)
	r.With(noKids).Post("/api/books/upload", h.uploadBook)
}

// readingKinds are the kinds of the «Читаю сейчас» / «Прочитано» shelves.
var readingKinds = []string{library.KindBook, library.KindComicVolume, library.KindAudiobook}

// bookKinds are the kinds the books section shows (its watchlist too).
var bookKinds = map[string]bool{library.KindBook: true, library.KindAudiobook: true, library.KindComic: true, library.KindComicVolume: true}

type BookSeries struct {
	Name  string  `json:"name"`
	Index float64 `json:"index"`
}

type BookFile struct {
	ID         int64  `json:"id"`
	Format     string `json:"format"`
	Size       int64  `json:"size"`
	DurationMs int64  `json:"durationMs"`
}

type BookProgress struct {
	Fraction   float64 `json:"fraction"`
	Locator    string  `json:"locator"`
	PositionMs int64   `json:"positionMs"`
	DurationMs int64   `json:"durationMs"`
	Finished   bool    `json:"finished"`
}

// BookChapter is an audiobook chapter; StartMs/EndMs are from the start of
// the book (a file without a known duration counts as 0 long).
type BookChapter struct {
	Idx     int    `json:"idx"`
	Title   string `json:"title"`
	FileID  int64  `json:"fileId"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// VolumeCard is a comic volume in BookDetail.Volumes.
type VolumeCard struct {
	Card
	Pages     int    `json:"pages"`
	Direction string `json:"direction"`
	FileID    int64  `json:"fileId"`
}

// BookDetail is the page of a book, audiobook, comic series or volume.
type BookDetail struct {
	// CanSendToKindle: «Отправить на Kindle» works for this profile and book.
	CanSendToKindle bool `json:"canSendToKindle"`
	// SentToKindle: this profile has sent the book to its present address.
	SentToKindle bool          `json:"sentToKindle"`
	Card         Card          `json:"card"`
	Kind         string        `json:"kind"`
	Authors      []string      `json:"authors"`
	Narrator     string        `json:"narrator"`
	Series       *BookSeries   `json:"series"`
	Description  string        `json:"description"`
	Year         int           `json:"year"`
	Lang         string        `json:"lang"`
	Files        []BookFile    `json:"files"`
	SeriesID     int64         `json:"seriesId"`
	NextID       int64         `json:"nextId"`
	ReadFileID   int64         `json:"readFileId"`
	Progress     *BookProgress `json:"progress"`
	Want         bool          `json:"want"`
	DurationMs   int64         `json:"durationMs"`
	Chapters     []BookChapter `json:"chapters"`
	// ChaptersAuto: the chapters were made from the audiobook's transcript.
	ChaptersAuto bool `json:"chaptersAuto"`
	// Transcript of an audiobook: "pending", "ready" (GET
	// /api/books/{id}/transcript) or "failed"; "" none and none coming.
	Transcript string       `json:"transcript"`
	Volumes    []VolumeCard `json:"volumes"`
	Direction  string       `json:"direction"`
}

func (h *lib) booksHome(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	limit := ratingLimit(u)
	var shelves []shelf
	add := func(id, title string, cards []Card) {
		if len(cards) > 0 {
			shelves = append(shelves, shelf{id, title, cards})
		}
	}
	reading, err := h.progressCards(ctx, r, false)
	if err != nil {
		internalErr(w, r, "books home reading", err)
		return
	}
	add("reading", i18n.T(ctx, "Читаю сейчас"), reading)
	want, err := h.watchlistCards(ctx, r, func(k string) bool { return bookKinds[k] })
	if err != nil {
		internalErr(w, r, "books home want", err)
		return
	}
	add("want", i18n.T(ctx, "Хочу прочитать"), want)
	for _, s := range []struct{ id, title, kind string }{
		{"recent", "Недавно добавленные", library.KindBook},
		{"audiobooks", "Аудиокниги", library.KindAudiobook},
		{"comics", "Манга и комиксы", library.KindComic},
	} {
		items, _, err := h.Library.Books(ctx, library.BookQuery{Kind: s.kind, Limit: shelfSize, MaxRating: limit, Owner: scopeOwner(r, u)})
		if err != nil {
			internalErr(w, r, "books home "+s.id, err)
			return
		}
		add(s.id, i18n.T(ctx, s.title), h.cards(ctx, auth.HistoryID(ctx), items))
	}
	done, err := h.progressCards(ctx, r, true)
	if err != nil {
		internalErr(w, r, "books home done", err)
		return
	}
	add("done", i18n.T(ctx, "Прочитано"), done)
	if shelves == nil {
		shelves = []shelf{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelves": shelves})
}

// progressCards are the visible books, volumes and audiobooks the user is
// reading (finished false) or has read, newest first.
func (h *lib) progressCards(ctx context.Context, r *http.Request, finished bool) ([]Card, error) {
	uid := auth.HistoryID(ctx)
	var entries []progress.Entry
	var err error
	if finished {
		entries, err = h.Progress.FinishedOf(ctx, uid, readingKinds, 5*shelfSize)
	} else {
		entries, err = h.Progress.InProgress(ctx, uid, readingKinds, 5*shelfSize)
	}
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(entries))
	for i, e := range entries {
		ids[i] = e.ItemID
	}
	ctx = h.withItems(ctx, uid, ids)
	out := []Card{}
	for _, e := range entries {
		if len(out) >= shelfSize {
			break
		}
		it, ok, err := h.visible(ctx, r, e.ItemID)
		if err != nil {
			return nil, err
		}
		if ok {
			c := h.card(ctx, uid, it)
			c.Progress = ratio(e)
			out = append(out, c)
		}
	}
	return out, nil
}

// watchlistCards are the visible watchlist items of the kinds keep accepts.
func (h *lib) watchlistCards(ctx context.Context, r *http.Request, keep func(kind string) bool) ([]Card, error) {
	uid := userOf(r).ID
	ids, err := h.Progress.Watchlist(ctx, uid)
	if err != nil {
		return nil, err
	}
	// the person's list, with the marks of the device's history
	hid := auth.HistoryID(ctx)
	ctx = h.withItems(ctx, hid, ids[:min(len(ids), 5*shelfSize)])
	out := []Card{}
	for _, id := range ids {
		if len(out) >= shelfSize {
			break
		}
		it, ok, err := h.visible(ctx, r, id)
		if err != nil {
			return nil, err
		}
		if ok && keep(it.Kind) {
			out = append(out, h.card(ctx, hid, it))
		}
	}
	return out, nil
}

func (h *lib) books(w http.ResponseWriter, r *http.Request) {
	q, u := r.URL.Query(), userOf(r)
	kind := q.Get("kind")
	if kind != library.KindBook && kind != library.KindAudiobook && kind != library.KindComic {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return
	}
	sort := q.Get("sort")
	if sort != "" && sort != "added" && sort != "title" && sort != "author" {
		writeErr(w, r, http.StatusBadRequest, "Неизвестная сортировка")
		return
	}
	limit, offset := pageParams(r, 100)
	items, total, err := h.Library.Books(r.Context(), library.BookQuery{Kind: kind, Sort: sort, Limit: limit, Offset: offset, MaxRating: ratingLimit(u),
		Owner: scopeOwner(r, u)})
	if err != nil {
		internalErr(w, r, "books", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.cards(r.Context(), auth.HistoryID(r.Context()), items), "total": total})
}

// bookFormat is the display format of a book, audiobook or volume file.
func bookFormat(p string) string {
	lp := strings.ToLower(p)
	if strings.HasSuffix(lp, ".fb2.zip") {
		return "FB2"
	}
	switch x := filepath.Ext(lp); x {
	case ".zip", ".cbz":
		return "CBZ"
	case ".rar", ".cbr":
		return "CBR"
	case ".7z", ".cb7":
		return "CB7"
	case "":
		return ""
	default:
		return strings.ToUpper(x[1:])
	}
}

// readOrder is the reader's format preference.
var readOrder = []string{"EPUB", "FB2", "MOBI", "AZW3", "PDF"}

func (h *lib) bookDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindBook, library.KindAudiobook, library.KindComic, library.KindComicVolume)
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	m := bookMetaOf(it)
	d := BookDetail{Card: h.card(ctx, auth.HistoryID(ctx), it), Kind: it.Kind, Authors: m.Authors, Narrator: m.Narrator, Description: it.Overview,
		Year: it.Year, Lang: m.Lang, Files: []BookFile{}, Chapters: []BookChapter{}, Volumes: []VolumeCard{}}
	if d.Authors == nil {
		d.Authors = []string{}
		if m.Writer != "" {
			d.Authors = []string{m.Writer}
		}
	}
	if m.Series != "" {
		d.Series = &BookSeries{m.Series, m.SeriesIndex}
	}
	files, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "book files", err)
		return
	}
	files = library.OrderFiles(files, m.FilesOrder)
	if it.Kind == library.KindBook {
		if _, ok := kindleFile(files); ok {
			_, d.CanSendToKindle = h.kindleReady(ctx, u.KindleEmail)
			if d.CanSendToKindle {
				var err error
				if d.SentToKindle, err = h.Library.SentToKindle(ctx, u.ID, it.ID, u.KindleEmail); err != nil {
					log.Printf("kindle: sent state of book %d: %v", it.ID, err)
				}
			}
		}
	}
	offsets := map[int64]int64{}
	durs := map[int64]int64{}
	rank := len(readOrder)
	for _, f := range files {
		dur := f.Length()
		offsets[f.ID], durs[f.ID] = d.DurationMs, dur
		d.DurationMs += dur
		bf := BookFile{ID: f.ID, Format: bookFormat(f.Path), Size: f.Size, DurationMs: dur}
		d.Files = append(d.Files, bf)
		for i, x := range readOrder {
			if bf.Format == x && i < rank {
				rank, d.ReadFileID = i, f.ID
			}
		}
	}
	switch it.Kind {
	case library.KindAudiobook:
		chs, err := h.Library.Chapters(ctx, it.ID)
		if err != nil {
			internalErr(w, r, "book chapters", err)
			return
		}
		for _, c := range chs {
			off := offsets[c.FileID]
			end := off + durs[c.FileID]
			if c.EndMs != nil {
				end = off + *c.EndMs
			}
			d.Chapters = append(d.Chapters, BookChapter{Idx: c.Idx, Title: library.DisplayChapterTitle(i18n.FromContext(r.Context()), c.Title, c.Idx), FileID: c.FileID, StartMs: off + c.StartMs, EndMs: end})
		}
		stored, current, err := h.storedTranscript(ctx, it.ID, files)
		if err != nil {
			internalErr(w, r, "book transcript", err)
			return
		}
		d.Transcript = h.transcriptState(ctx, stored, current)
		if current && stored.Chapters {
			auto, err := h.autoChapters(ctx, it, files, chs, offsets, d.DurationMs)
			if err != nil {
				internalErr(w, r, "book chapters", err)
				return
			}
			if len(auto) > 0 {
				d.Chapters, d.ChaptersAuto = auto, true
			}
		}
	case library.KindComic:
		d.Direction = directionOr(m.Direction)
		vols, err := h.Library.Volumes(ctx, it.ID)
		if err != nil {
			internalErr(w, r, "book volumes", err)
			return
		}
		for _, v := range vols {
			// a volume the profile may not open is not listed (kid rule)
			if ok, err := h.allowed(ctx, u, v); err != nil {
				internalErr(w, r, "book volumes", err)
				return
			} else if !ok {
				continue
			}
			vm := bookMetaOf(v)
			vc := VolumeCard{Card: h.card(ctx, auth.HistoryID(ctx), v), Pages: vm.Pages, Direction: directionOr(vm.Direction)}
			fs, err := h.Library.FilesOf(ctx, v.ID)
			if err != nil {
				internalErr(w, r, "book volumes", err)
				return
			}
			if len(fs) > 0 {
				vc.FileID = fs[0].ID
			}
			d.Volumes = append(d.Volumes, vc)
		}
	case library.KindComicVolume:
		d.Direction = directionOr(m.Direction)
		d.SeriesID = it.ParentID
		if len(files) > 0 {
			d.ReadFileID = files[0].ID
		}
		if it.ParentID != 0 {
			vols, err := h.Library.Volumes(ctx, it.ParentID)
			if err != nil {
				internalErr(w, r, "book volumes", err)
				return
			}
			for i, v := range vols {
				if v.ID != it.ID {
					continue
				}
				// the next volume the profile may open
				for _, n := range vols[i+1:] {
					ok, err := h.allowed(ctx, u, n)
					if err != nil {
						internalErr(w, r, "book volumes", err)
						return
					}
					if ok {
						d.NextID = n.ID
						break
					}
				}
				break
			}
			if d.Series == nil {
				if s, err := h.Library.Item(ctx, it.ParentID); err == nil {
					d.Series = &BookSeries{s.Title, m.Volume}
				}
			}
		}
	}
	e, err := h.Progress.Get(ctx, auth.HistoryID(ctx), it.ID)
	switch {
	case err == nil:
		d.Progress = &BookProgress{Fraction: ratio(e), Locator: e.Locator, PositionMs: e.PositionMs, DurationMs: e.DurationMs, Finished: e.Finished}
	case !errors.Is(err, progress.ErrNotFound):
		internalErr(w, r, "book progress", err)
		return
	}
	ids, err := h.Progress.Watchlist(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "book watchlist", err)
		return
	}
	for _, id := range ids {
		d.Want = d.Want || id == it.ID
	}
	writeJSON(w, http.StatusOK, d)
}

func directionOr(d string) string {
	if d == "ltr" || d == "rtl" {
		return d
	}
	return "rtl"
}
