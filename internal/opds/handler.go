package opds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/comics"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/screentime"
)

// Handler serves /opds. Every request carries HTTP Basic credentials
// (authenticate); feeds show only what the profile's rating limit allows.
// ImageDir is the image cache (covers fetched by the enrichers).
type Handler struct {
	Library  *library.Repo
	Users    *auth.Store
	Network  *auth.Network
	Throttle *auth.Throttle
	ImageDir string
	// ScreenTime: a kid whose audiobooks are limited («Экранное время»)
	// downloads no audiobook files (books and comics stay free); nil: no limits.
	ScreenTime *screentime.Service

	once sync.Once
	mux  http.Handler
}

func itoa(n int) string { return strconv.Itoa(n) }

func (h *Handler) init() {
	if h.Network == nil {
		h.Network = &auth.Network{}
	}
	if h.Throttle == nil {
		h.Throttle = auth.NewThrottle()
	}
	r := chi.NewRouter()
	r.Use(middleware.GetHead) // HEAD runs the GET handler; net/http drops the body
	r.Get("/opds", h.root)
	r.Get("/opds/", h.root)
	r.Get("/opds/opensearch.xml", h.openSearch)
	r.Get("/opds/new", h.newBooks)
	r.Get("/opds/authors", h.authors)
	r.Get("/opds/authors/{name}", h.author)
	r.Get("/opds/series", h.seriesList)
	r.Get("/opds/series/{name}", h.series)
	r.Get("/opds/audiobooks", h.audiobooks)
	r.Get("/opds/comics", h.comicsList)
	r.Get("/opds/comics/{id}", h.comic)
	r.Get("/opds/search", h.search)
	r.Get("/opds/files/{fileId}", h.file)
	r.Get("/opds/files/{fileId}/{name}", h.file) // the name is for the reader only
	r.Get("/opds/covers/{itemId}", h.cover)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { notFound(w, r) })
	h.mux = r
}

// ServeHTTP authenticates, then routes (GET and HEAD only).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(h.init)
	// the request language (idempotent under cmd/vault's i18n.Middleware)
	r = r.WithContext(i18n.WithLanguage(r.Context(), i18n.Resolve(r)))
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, i18n.T(r.Context(), "Метод не поддерживается"), http.StatusMethodNotAllowed)
		return
	}
	u, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	h.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
}

func notFound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, i18n.T(r.Context(), "Не найдено"), http.StatusNotFound)
}

func internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	log.Printf("opds: %s: %v", what, err)
	http.Error(w, i18n.T(r.Context(), "Внутренняя ошибка"), http.StatusInternalServerError)
}

func limit(r *http.Request) string { return userOf(r.Context()).RatingLimit() }

func pageOf(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 0 || p > 100000 {
		return 0
	}
	return p
}

// meta is what the book, audiobook and comic scanners keep in meta_json.
type meta struct {
	Authors     []string `json:"authors"`
	Series      string   `json:"series"`
	SeriesIndex float64  `json:"series_index"`
	Lang        string   `json:"lang"`
	Writer      string   `json:"writer"`
	FilesOrder  []int64  `json:"files_order"`
	Volume      float64  `json:"volume"`
}

func metaOf(it library.Item) meta {
	var m meta
	if it.MetaJSON != "" {
		if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
			log.Printf("opds: item %d meta_json: %v", it.ID, err) // degrade: no authors
		}
	}
	return m
}

func (h *Handler) root(w http.ResponseWriter, r *http.Request) {
	f := newFeed(r.Context(), "root", "Vault", "/opds", TypeNavigation)
	t := func(s string) string { return i18n.T(r.Context(), s) }
	f.Entries = []entry{
		navEntry("new", t("Новые книги"), "/opds/new", TypeAcquisition, t("Недавно добавленные книги")),
		navEntry("authors", t("Авторы"), "/opds/authors", TypeNavigation, t("Книги по авторам")),
		navEntry("series", t("Серии"), "/opds/series", TypeNavigation, t("Книги по сериям")),
		navEntry("audiobooks", t("Аудиокниги"), "/opds/audiobooks", TypeAcquisition, t("Аудиокниги одним файлом можно скачать")),
		navEntry("comics", t("Манга и комиксы"), "/opds/comics", TypeNavigation, t("Тома по сериям")),
	}
	writeFeed(w, r, TypeNavigation, f)
}

func (h *Handler) openSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", typeOpenSearch+"; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
<ShortName>Vault</ShortName>
<Description>%s</Description>
<InputEncoding>UTF-8</InputEncoding>
<OutputEncoding>UTF-8</OutputEncoding>
<Url type="%s" template="%s/opds/search?q={searchTerms}"/>
</OpenSearchDescription>
`, html.EscapeString(i18n.T(r.Context(), "Поиск книг, аудиокниг и комиксов")), TypeAcquisition, html.EscapeString(h.Network.Origin(r)))
}

// contentDisposition names a download for every reader: an ASCII filename
// (the name itself when it is ASCII, else "vault-<id><ext>") and the
// UTF-8 filename* of RFC 6266.
func contentDisposition(id int64, name string) string {
	ascii := true
	for _, c := range name {
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			ascii = false
			break
		}
	}
	fallback := name
	if !ascii {
		ext := strings.ToLower(filepath.Ext(name))
		if strings.HasSuffix(strings.ToLower(name), ".fb2.zip") {
			ext = ".fb2.zip"
		}
		fallback = "vault-" + strconv.FormatInt(id, 10) + ext
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + extValue(name)
}

// extValue percent-encodes s for an RFC 5987 ext-value (attr-char kept).
func extValue(s string) string {
	const keep = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// --- entries ---

// coverPath is the file of an item's poster that cover serves ("" none).
func (h *Handler) coverPath(poster string) string {
	if p, ok := strings.CutPrefix(poster, "file:"); ok {
		if filepath.IsAbs(p) && coverExt[strings.ToLower(filepath.Ext(p))] {
			return p
		}
	} else if library.ImageCacheName.MatchString(poster) && h.ImageDir != "" {
		return filepath.Join(h.ImageDir, poster)
	}
	return ""
}

// coverLinks: image links only for a poster cover can serve.
func (h *Handler) coverLinks(it library.Item) []link {
	if h.coverPath(it.Poster) == "" {
		return nil
	}
	href := "/opds/covers/" + strconv.FormatInt(it.ID, 10)
	t := imageType(strings.TrimPrefix(it.Poster, "file:"))
	return []link{{Rel: relImage, Href: href, Type: t}, {Rel: relThumbnail, Href: href, Type: t}}
}

func fileLink(f library.File) (link, bool) {
	t := fileType(f.Path)
	if t == "" {
		return link{}, false
	}
	// the name at the end: readers that ignore Content-Disposition save
	// the file under the last path element
	href := "/opds/files/" + strconv.FormatInt(f.ID, 10) + "/" + url.PathEscape(filepath.Base(f.Path))
	return link{Rel: relAcquisition, Href: href, Type: t, Length: f.Size,
		Title: strings.TrimPrefix(strings.ToUpper(filepath.Ext(f.Path)), ".")}, true
}

// itemEntry is the entry of a book (every format, EPUB first), an audiobook
// (one file: downloadable; a folder of files: cover and description only),
// or a comic volume (its archive).
func (h *Handler) itemEntry(ctx context.Context, it library.Item, title string) (entry, error) {
	m := metaOf(it)
	e := entry{Title: title, ID: "urn:vault:item:" + strconv.FormatInt(it.ID, 10), Updated: stamp(it.UpdatedAt), Lang: m.Lang}
	if e.Title == "" {
		e.Title = it.Title
	}
	e.Title = i18n.Localize(ctx, e.Title) // «Том %s · Глава %s» and the like, stored in Russian
	for _, a := range m.Authors {
		e.Authors = append(e.Authors, person{Name: a})
	}
	if len(e.Authors) == 0 && m.Writer != "" {
		e.Authors = []person{{Name: m.Writer}}
	}
	if it.Year > 0 {
		e.Issued = strconv.Itoa(it.Year)
	}
	if it.Overview != "" {
		e.Content = &text{Type: "text", Body: it.Overview}
	}
	files, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		return e, err
	}
	nFiles := len(files)
	switch it.Kind {
	case library.KindBook:
		sort.SliceStable(files, func(i, j int) bool { return bookRank(files[i].Path) < bookRank(files[j].Path) })
	case library.KindAudiobook:
		if len(files) != 1 {
			files = nil // several parts: no single file to hand over
		}
	case library.KindComicVolume:
		var keep []library.File
		for _, f := range files {
			if comics.IsArchive(f.Path) || strings.EqualFold(filepath.Ext(f.Path), ".pdf") {
				keep = append(keep, f) // a folder of pages has no file to download
			}
		}
		files = keep
	}
	for _, f := range files {
		if l, ok := fileLink(f); ok {
			e.Links = append(e.Links, l)
		}
	}
	cover := it
	if cover.Poster == "" && it.Kind == library.KindComicVolume && it.ParentID != 0 {
		if p, err := h.Library.Item(ctx, it.ParentID); err == nil {
			cover.Poster = p.Poster // the volume's link serves the series cover
		}
	}
	if e.Content == nil {
		// RFC 4287 §4.1.2: an entry without an alternate link needs content
		e.Content = &text{Type: "text", Body: placeholder(ctx, it.Kind, len(e.Links) > 0, nFiles)}
	}
	e.Links = append(e.Links, h.coverLinks(cover)...)
	return e, nil
}

func (h *Handler) entries(ctx context.Context, items []library.Item) ([]entry, error) {
	out := make([]entry, 0, len(items))
	for _, it := range items {
		var e entry
		var err error
		if it.Kind == library.KindComic {
			e = h.comicEntry(ctx, it)
		} else {
			e, err = h.itemEntry(ctx, it, "")
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (h *Handler) comicEntry(ctx context.Context, it library.Item) entry {
	n, err := h.Library.VolumeCount(ctx, it.ID)
	if err != nil {
		log.Printf("opds: volumes of %d: %v", it.ID, err)
	}
	e := navEntry("comic:"+strconv.FormatInt(it.ID, 10), i18n.Localize(ctx, it.Title), "/opds/comics/"+strconv.FormatInt(it.ID, 10), TypeAcquisition,
		i18n.T(ctx, "Томов: %d", n))
	if it.Overview != "" {
		e.Content = &text{Type: "text", Body: it.Overview}
	}
	if w := metaOf(it).Writer; w != "" {
		e.Authors = []person{{Name: w}}
	}
	e.Links = append(e.Links, h.coverLinks(it)...)
	return e
}

// placeholder is the content of an entry without a description.
func placeholder(ctx context.Context, kind string, downloadable bool, files int) string {
	switch {
	case kind == library.KindAudiobook && !downloadable:
		return i18n.T(ctx, "Аудиокнига из %d файлов — слушайте в Antresol", files)
	case kind == library.KindAudiobook:
		return i18n.T(ctx, "Аудиокнига")
	case kind == library.KindComicVolume:
		return i18n.T(ctx, "Том")
	}
	return i18n.T(ctx, "Книга")
}

// --- feeds ---

// listFeed is a page of Books of kind.
func (h *Handler) listFeed(w http.ResponseWriter, r *http.Request, kind, id, title, self, feedKind string) {
	page := pageOf(r)
	items, total, err := h.Library.Books(r.Context(), library.BookQuery{Kind: kind, Sort: sortFor(kind), Limit: pageSize, Offset: page * pageSize, MaxRating: limit(r)})
	if err != nil {
		internal(w, r, self, err)
		return
	}
	f := newFeed(r.Context(), id, i18n.T(r.Context(), title), self, feedKind)
	if f.Entries, err = h.entries(r.Context(), items); err != nil {
		internal(w, r, self, err)
		return
	}
	f.pages(self, page, (page+1)*pageSize < total, feedKind)
	writeFeed(w, r, feedKind, f)
}

func sortFor(kind string) string {
	if kind == library.KindComic {
		return "title"
	}
	return "added"
}

func (h *Handler) newBooks(w http.ResponseWriter, r *http.Request) {
	h.listFeed(w, r, library.KindBook, "new", "Новые книги", "/opds/new", TypeAcquisition)
}

func (h *Handler) audiobooks(w http.ResponseWriter, r *http.Request) {
	h.listFeed(w, r, library.KindAudiobook, "audiobooks", "Аудиокниги", "/opds/audiobooks", TypeAcquisition)
}

func (h *Handler) comicsList(w http.ResponseWriter, r *http.Request) {
	h.listFeed(w, r, library.KindComic, "comics", "Манга и комиксы", "/opds/comics", TypeNavigation)
}

// allBooks are the e-books the profile may see.
func (h *Handler) allBooks(r *http.Request) ([]library.Item, error) {
	items, _, err := h.Library.Books(r.Context(), library.BookQuery{Kind: library.KindBook, Sort: "title", MaxRating: limit(r)})
	return items, err
}

type group struct {
	name  string
	books []library.Item
	metas []meta // of books, decoded once
}

// groups collects books under keys (authors or the series), case and ё
// folded; the first spelling names the group. Sorted by name.
func groups(items []library.Item, keys func(meta) []string) []*group {
	by := map[string]*group{}
	var out []*group
	for _, it := range items {
		m := metaOf(it)
		for _, k := range keys(m) {
			k = strings.TrimSpace(k)
			n := release.Normalize(k)
			if n == "" {
				continue
			}
			g := by[n]
			if g == nil {
				g = &group{name: k}
				by[n] = g
				out = append(out, g)
			}
			g.books = append(g.books, it)
			g.metas = append(g.metas, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return natsort.Less(strings.ToLower(out[i].name), strings.ToLower(out[j].name)) })
	return out
}

func authorsOf(m meta) []string { return m.Authors }
func seriesOf(m meta) []string  { return []string{m.Series} }

// groupFeed is the navigation feed of the groups (paged).
func (h *Handler) groupFeed(w http.ResponseWriter, r *http.Request, keys func(meta) []string, id, title, self string) {
	items, err := h.allBooks(r)
	if err != nil {
		internal(w, r, self, err)
		return
	}
	gs := groups(items, keys)
	page := pageOf(r)
	f := newFeed(r.Context(), id, i18n.T(r.Context(), title), self, TypeNavigation)
	for i := page * pageSize; i < len(gs) && i < (page+1)*pageSize; i++ {
		g := gs[i]
		f.Entries = append(f.Entries, navEntry(id+":"+release.Normalize(g.name), g.name, self+"/"+url.PathEscape(g.name), TypeAcquisition,
			i18n.T(r.Context(), "Книг: %d", len(g.books))))
	}
	f.pages(self, page, (page+1)*pageSize < len(gs), TypeNavigation)
	writeFeed(w, r, TypeNavigation, f)
}

// groupBooks is the acquisition feed of one group's books.
func (h *Handler) groupBooks(w http.ResponseWriter, r *http.Request, keys func(meta) []string, id, self string, bySeries bool) {
	name := chi.URLParam(r, "name")
	if n, err := url.PathUnescape(name); err == nil {
		name = n
	}
	items, err := h.allBooks(r)
	if err != nil {
		internal(w, r, self, err)
		return
	}
	want := release.Normalize(name)
	var g *group
	for _, x := range groups(items, keys) {
		if release.Normalize(x.name) == want {
			g = x
			break
		}
	}
	if g == nil || want == "" {
		notFound(w, r)
		return
	}
	type book struct {
		it library.Item
		m  meta
	}
	list := make([]book, len(g.books))
	for i := range g.books {
		list[i] = book{g.books[i], g.metas[i]}
	}
	sort.SliceStable(list, func(i, j int) bool {
		mi, mj := list[i].m, list[j].m
		if bySeries || mi.Series == mj.Series {
			if mi.SeriesIndex != mj.SeriesIndex {
				return mi.SeriesIndex < mj.SeriesIndex
			}
		} else {
			return natsort.Less(strings.ToLower(mi.Series), strings.ToLower(mj.Series))
		}
		return natsort.Less(strings.ToLower(list[i].it.Title), strings.ToLower(list[j].it.Title))
	})
	books := make([]library.Item, len(list))
	for i := range list {
		books[i] = list[i].it
	}
	page := pageOf(r)
	path := self + "/" + url.PathEscape(g.name)
	f := newFeed(r.Context(), id+":"+want, g.name, path, TypeAcquisition)
	end := min(len(books), (page+1)*pageSize)
	if page*pageSize < end {
		if f.Entries, err = h.entries(r.Context(), books[page*pageSize:end]); err != nil {
			internal(w, r, path, err)
			return
		}
	}
	f.pages(path, page, end < len(books), TypeAcquisition)
	writeFeed(w, r, TypeAcquisition, f)
}

func (h *Handler) authors(w http.ResponseWriter, r *http.Request) {
	h.groupFeed(w, r, authorsOf, "authors", "Авторы", "/opds/authors")
}

func (h *Handler) author(w http.ResponseWriter, r *http.Request) {
	h.groupBooks(w, r, authorsOf, "author", "/opds/authors", false)
}

func (h *Handler) seriesList(w http.ResponseWriter, r *http.Request) {
	h.groupFeed(w, r, seriesOf, "series", "Серии", "/opds/series")
}

func (h *Handler) series(w http.ResponseWriter, r *http.Request) {
	h.groupBooks(w, r, seriesOf, "series", "/opds/series", true)
}

// visibleItem loads an item of one of kinds the profile may see; ok is
// false after a 404 (hidden items are not revealed) or 500.
func (h *Handler) visibleItem(w http.ResponseWriter, r *http.Request, id int64, kinds ...string) (library.Item, bool) {
	it, err := h.Library.Item(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		notFound(w, r)
		return it, false
	}
	if err != nil {
		internal(w, r, "item", err)
		return it, false
	}
	kindOK := false
	for _, k := range kinds {
		kindOK = kindOK || it.Kind == k
	}
	if !kindOK {
		notFound(w, r)
		return it, false
	}
	ok, err := h.Library.Visible(r.Context(), limit(r), it)
	if err != nil {
		internal(w, r, "item access", err)
		return it, false
	}
	if !ok {
		notFound(w, r)
		return it, false
	}
	return it, true
}

func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id > 0
}

// comic is the acquisition feed of a series' volumes.
func (h *Handler) comic(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		notFound(w, r)
		return
	}
	it, ok := h.visibleItem(w, r, id, library.KindComic)
	if !ok {
		return
	}
	vols, err := h.Library.Volumes(r.Context(), id)
	if err != nil {
		internal(w, r, "volumes", err)
		return
	}
	self := "/opds/comics/" + strconv.FormatInt(id, 10)
	f := newFeed(r.Context(), "comic:"+strconv.FormatInt(id, 10), i18n.Localize(r.Context(), it.Title), self, TypeAcquisition)
	for _, v := range vols {
		e, err := h.itemEntry(r.Context(), v, it.Title+" — "+v.Title)
		if err != nil {
			internal(w, r, "volume", err)
			return
		}
		f.Entries = append(f.Entries, e)
	}
	writeFeed(w, r, TypeAcquisition, f)
}

// search finds books, audiobooks and comic series (paged).
func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := pageOf(r)
	self := "/opds/search?q=" + url.QueryEscape(q)
	f := newFeed(r.Context(), "search", i18n.T(r.Context(), "Поиск: %s", q), self, TypeAcquisition)
	if q != "" {
		items, err := h.Library.SearchKinds(r.Context(), q, []string{library.KindBook, library.KindAudiobook, library.KindComic},
			(page+1)*pageSize+1, limit(r))
		if err != nil {
			internal(w, r, "search", err)
			return
		}
		more := len(items) > (page+1)*pageSize
		if start := page * pageSize; start < len(items) {
			if f.Entries, err = h.entries(r.Context(), items[start:min(len(items), (page+1)*pageSize)]); err != nil {
				internal(w, r, "search", err)
				return
			}
		}
		f.pages(self, page, more, TypeAcquisition)
	}
	writeFeed(w, r, TypeAcquisition, f)
}

// file sends the original file of a book, audiobook or comic volume.
func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "fileId")
	if !ok {
		notFound(w, r)
		return
	}
	f, err := h.Library.FileByID(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		notFound(w, r)
		return
	}
	if err != nil {
		internal(w, r, "file", err)
		return
	}
	it, ok := h.visibleItem(w, r, f.ItemID, library.KindBook, library.KindAudiobook, library.KindComicVolume)
	if !ok {
		return
	}
	if it.Kind == library.KindAudiobook && h.ScreenTime.Restricted(r.Context(), userOf(r.Context()), screentime.Audio) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(i18n.T(r.Context(), "Скачивание выключено родителем")))
		return
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		notFound(w, r)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || !st.Mode().IsRegular() {
		notFound(w, r)
		return
	}
	t := fileType(f.Path)
	if t == "" {
		t = "application/octet-stream"
	}
	hd := w.Header()
	hd.Set("Content-Type", t)
	hd.Set("Content-Disposition", contentDisposition(f.ID, filepath.Base(f.Path)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

// coverExt: the image files a "file:" poster may name.
var coverExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}

// cover sends the cover of a book, audiobook, comic or volume (a volume
// without its own: the series').
func (h *Handler) cover(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "itemId")
	if !ok {
		notFound(w, r)
		return
	}
	it, ok := h.visibleItem(w, r, id, library.KindBook, library.KindAudiobook, library.KindComic, library.KindComicVolume)
	if !ok {
		return
	}
	if it.Poster == "" && it.Kind == library.KindComicVolume && it.ParentID != 0 {
		if p, err := h.Library.Item(r.Context(), it.ParentID); err == nil {
			it.Poster = p.Poster
		}
	}
	path := h.coverPath(it.Poster)
	if path == "" {
		notFound(w, r)
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		notFound(w, r)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || !st.Mode().IsRegular() {
		notFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("Cache-Control", "private, max-age=604800")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), fh)
}
