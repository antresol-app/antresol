package api

import (
	"archive/zip"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/comics"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/go-chi/chi/v5"
)

// comicArchivesOpen is how many comic volumes the page API keeps open.
const comicArchivesOpen = 8

// comicOpensAtOnce bounds the volume opens (RAR/7z unpacks) running at once.
const comicOpensAtOnce = 2

// openComic opens a volume (tests count the opens).
var openComic = comics.Open

// errComicAbandoned: every request waiting for an open went away before it
// started, so it was not run.
var errComicAbandoned = errors.New("comic open abandoned")

func mountComics(r chi.Router, h *lib) {
	r.Get("/api/comics/{fileId}/pages", h.comicPages)
	r.Get("/api/comics/{fileId}/page/{n}", h.comicPage)
	r.Get("/api/comics/{fileId}/pages.zip", h.comicPagesZip)
}

// comicArchives is an LRU of open volumes by file id. An entry is opened
// once (concurrent requests wait for the same open, each as long as its
// own request lives; the open itself goes on, filling the unpack cache),
// reopened when the file changed (size or mtime), and closed when evicted
// and no request uses it any more.
type comicArchives struct {
	max   int
	cache string
	slots chan struct{} // comicOpensAtOnce

	mu   sync.Mutex
	lru  *list.List // of *comicEntry, most recently used first
	byID map[int64]*list.Element
}

type comicEntry struct {
	id    int64
	size  int64
	mtime time.Time
	ready chan struct{} // closed when the open finished

	a       *comics.Archive
	err     error
	refs    int  // requests using it
	evicted bool // out of the LRU: close when refs drops to 0
}

func newComicArchives(max int, cacheDir string) *comicArchives {
	return &comicArchives{max: max, cache: cacheDir, slots: make(chan struct{}, comicOpensAtOnce), lru: list.New(), byID: map[int64]*list.Element{}}
}

func (c *comicArchives) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// get returns the open volume of fileID at path; done must be called when
// the caller has finished with it.
func (c *comicArchives) get(ctx context.Context, fileID int64, path string) (*comics.Archive, func(), error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	var e *comicEntry
	if el, ok := c.byID[fileID]; ok {
		e = el.Value.(*comicEntry)
		if e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
			c.lru.MoveToFront(el)
		} else {
			c.evictLocked(el)
			e = nil
		}
	}
	if e == nil {
		e = &comicEntry{id: fileID, size: st.Size(), mtime: st.ModTime(), ready: make(chan struct{})}
		c.byID[fileID] = c.lru.PushFront(e)
		go c.open(e, path)
		for c.lru.Len() > c.max {
			c.evictLocked(c.lru.Back())
		}
	}
	e.refs++
	c.mu.Unlock()

	done := func() { c.release(e) }
	select {
	case <-e.ready:
	case <-ctx.Done():
		done()
		return nil, nil, ctx.Err()
	}
	if e.err != nil {
		done()
		return nil, nil, e.err
	}
	return e.a, done, nil
}

func (c *comicArchives) open(e *comicEntry, path string) {
	c.slots <- struct{}{}
	defer func() { <-c.slots }()
	c.mu.Lock()
	abandoned := e.refs == 0
	c.mu.Unlock()
	var a *comics.Archive
	err := errComicAbandoned
	if !abandoned {
		a, err = openComic(path, c.cache)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e.a, e.err = a, err
	close(e.ready)
	if err != nil { // not kept: the next request tries again
		if el, ok := c.byID[e.id]; ok && el.Value == e {
			c.evictLocked(el)
		}
		return
	}
	c.closeIfUnused(e)
}

func (c *comicArchives) evictLocked(el *list.Element) {
	e := el.Value.(*comicEntry)
	c.lru.Remove(el)
	if cur, ok := c.byID[e.id]; ok && cur == el {
		delete(c.byID, e.id)
	}
	e.evicted = true
	c.closeIfUnused(e)
}

func (c *comicArchives) release(e *comicEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.refs--
	c.closeIfUnused(e)
}

// closeIfUnused closes an evicted, opened entry nobody uses (under mu).
func (c *comicArchives) closeIfUnused(e *comicEntry) {
	if !e.evicted || e.refs > 0 || e.a == nil {
		return
	}
	select {
	case <-e.ready:
	default:
		return // open() closes it when it finishes
	}
	if err := e.a.Close(); err != nil {
		log.Printf("api: comic file %d close: %v", e.id, err)
	}
	e.a = nil
}

// comicFile loads the {fileId} file of a comic volume visible to the user.
func (h *lib) comicFile(w http.ResponseWriter, r *http.Request) (library.File, library.Item, bool) {
	f, it, ok := h.file(w, r)
	if !ok {
		return f, it, false
	}
	if it.Kind != library.KindComicVolume {
		writeErr(w, r, http.StatusNotFound, "Это не том манги или комикса")
		return f, it, false
	}
	return f, it, true
}

// comicArchive opens the volume through the LRU, or writes the error.
func (h *lib) comicArchive(w http.ResponseWriter, r *http.Request, f library.File) (*comics.Archive, func(), bool) {
	a, done, err := h.comics.get(r.Context(), f.ID, f.Path)
	if err != nil {
		if r.Context().Err() != nil {
			return nil, nil, false // the client went away
		}
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, r, http.StatusNotFound, "Файл не найден")
		} else {
			log.Printf("api: comic file %d: %v", f.ID, err)
			writeErr(w, r, http.StatusUnprocessableEntity, "Не удалось открыть том")
		}
		return nil, nil, false
	}
	return a, done, true
}

// comicDirection is the reading direction of a volume: its scanned
// direction, else ComicInfo, else right to left (manga).
func comicDirection(it library.Item, a *comics.Archive) string {
	var m struct {
		Direction string `json:"direction"`
	}
	_ = json.Unmarshal([]byte(it.MetaJSON), &m)
	if m.Direction == "rtl" || m.Direction == "ltr" {
		return m.Direction
	}
	if a != nil {
		if ci, ok := a.Info(); ok {
			if d := ci.Direction(); d != "" {
				return d
			}
		}
	}
	return "rtl"
}

func (h *lib) comicPages(w http.ResponseWriter, r *http.Request) {
	f, it, ok := h.comicFile(w, r)
	if !ok {
		return
	}
	a, done, ok := h.comicArchive(w, r, f)
	if !ok {
		return
	}
	defer done()
	writeJSON(w, http.StatusOK, map[string]any{"count": len(a.Pages()), "direction": comicDirection(it, a), "sizes": pageSizes(a)})
}

// pageHeaderLimit bounds what is read of a page to learn its size (a JPEG
// with a large EXIF block puts its frame header late).
const pageHeaderLimit = 512 << 10

// pageSizes is [width, height] of every page from its image header only
// (no decoding); [0, 0] when a page cannot be read or its format is unknown
// (AVIF). The web reader sizes its ribbon placeholders with them.
func pageSizes(a *comics.Archive) [][2]int {
	out := make([][2]int, len(a.Pages()))
	for i := range out {
		rc, _, err := a.Page(i)
		if err != nil {
			continue
		}
		if cfg, _, err := image.DecodeConfig(io.LimitReader(rc, pageHeaderLimit)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			out[i] = [2]int{cfg.Width, cfg.Height}
		}
		rc.Close()
	}
	return out
}

// sniffLen is how much of a page is read to check that it is an image.
const sniffLen = 512

// errBadPage: a page whose bytes are not an image.
var errBadPage = errors.New("comic page is not an image")

// pageType sniffs a page's image type from its first bytes (declared is
// the archive's type by extension): a page that is not an image (HTML
// named .jpg) is never served.
func pageType(head []byte, declared string) (string, bool) {
	ct := http.DetectContentType(head)
	switch {
	case strings.HasPrefix(ct, "image/") && ct != "image/svg+xml":
		return ct, true
	case ct == "application/octet-stream" && declared == "image/avif": // not sniffed by net/http
		return declared, true
	}
	return "", false
}

// zipPageExt is the extension of a page in pages.zip, by its sniffed type.
var zipPageExt = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/avif": "avif", "image/gif": "gif"}

// comicPagesZip serves every page of a volume in one store-only zip
// (page-0001.jpg, …) for the native app's offline reader; it is built once
// per volume version and kept with the device copies (Range-capable).
func (h *lib) comicPagesZip(w http.ResponseWriter, r *http.Request) {
	f, _, ok := h.comicFile(w, r)
	if !ok {
		return
	}
	if h.DeviceCopies == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Перекодирование недоступно")
		return
	}
	st, err := os.Stat(f.Path)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Файл не найден")
		return
	}
	// the volume is opened only when its zip is not there yet (the build
	// runs in this goroutine, so the archive outlives it)
	p, err := h.DeviceCopies.ComicZip(r.Context(), f.ID, st.Size(), st.ModTime(), func(out io.Writer) error {
		a, done, err := h.comics.get(r.Context(), f.ID, f.Path)
		if err != nil {
			return err
		}
		defer done()
		return writePagesZip(out, a)
	})
	switch {
	case r.Context().Err() != nil:
		return
	case errors.Is(err, os.ErrNotExist):
		writeErr(w, r, http.StatusNotFound, "Файл не найден")
		return
	case errors.Is(err, media.ErrNoSpace):
		writeErr(w, r, http.StatusInsufficientStorage, "На сервере не хватает места для копии")
		return
	case errors.Is(err, errBadPage):
		log.Printf("api: comic file %d zip: %v", f.ID, err)
		writeErr(w, r, http.StatusUnprocessableEntity, "Страница повреждена")
		return
	case err != nil:
		log.Printf("api: comic file %d zip: %v", f.ID, err)
		writeErr(w, r, http.StatusUnprocessableEntity, "Не удалось открыть том")
		return
	}
	fh, err := os.Open(p)
	if err != nil {
		internalErr(w, r, "comic zip", err)
		return
	}
	defer fh.Close()
	zs, err := fh.Stat()
	if err != nil {
		internalErr(w, r, "comic zip stat", err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "pages.zip", zs.ModTime(), fh)
}

// writePagesZip writes the pages of a in order, stored (images do not
// compress), as page-0001.<ext> with the extension of the sniffed type.
func writePagesZip(out io.Writer, a *comics.Archive) error {
	zw := zip.NewWriter(out)
	for i := range a.Pages() {
		if err := writeZipPage(zw, a, i); err != nil {
			return fmt.Errorf("page %d: %w", i, err)
		}
	}
	return zw.Close()
}

func writeZipPage(zw *zip.Writer, a *comics.Archive, i int) error {
	rc, declared, err := a.Page(i)
	if err != nil {
		return err
	}
	defer rc.Close()
	head := make([]byte, sniffLen)
	k, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	head = head[:k]
	ct, ok := pageType(head, declared)
	ext := zipPageExt[ct]
	if !ok || ext == "" {
		return errBadPage
	}
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("page-%04d.%s", i+1, ext), Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err := zf.Write(head); err != nil {
		return err
	}
	_, err = io.Copy(zf, rc)
	return err
}

func (h *lib) comicPage(w http.ResponseWriter, r *http.Request) {
	f, _, ok := h.comicFile(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil || n < 0 {
		writeErr(w, r, http.StatusNotFound, "Страница не найдена")
		return
	}
	a, done, ok := h.comicArchive(w, r, f)
	if !ok {
		return
	}
	defer done()
	if n >= len(a.Pages()) {
		writeErr(w, r, http.StatusNotFound, "Страница не найдена")
		return
	}
	rc, declared, err := a.Page(n)
	if err != nil {
		log.Printf("api: comic file %d page %d: %v", f.ID, n, err)
		writeErr(w, r, http.StatusUnprocessableEntity, "Не удалось открыть страницу")
		return
	}
	defer rc.Close()
	head := make([]byte, sniffLen)
	k, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		log.Printf("api: comic file %d page %d: %v", f.ID, n, err)
		writeErr(w, r, http.StatusUnprocessableEntity, "Не удалось открыть страницу")
		return
	}
	head = head[:k]
	// the type comes from the bytes, not the name
	ct, ok := pageType(head, declared)
	if !ok {
		writeErr(w, r, http.StatusUnprocessableEntity, "Страница повреждена")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", ct)
	hd.Set("Cache-Control", "private, max-age=86400")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(head); err != nil {
		return
	}
	if _, err := io.Copy(w, rc); err != nil && r.Context().Err() == nil {
		log.Printf("api: comic file %d page %d: %v", f.ID, n, err)
	}
}
