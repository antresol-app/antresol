package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

// Card is the compact item representation used by shelves, lists and search.
type Card struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"`
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Year     int     `json:"year,omitempty"`
	Poster   string  `json:"poster"`
	Backdrop string  `json:"backdrop"`
	Logo     string  `json:"logo"`
	Progress float64 `json:"progress"` // 0..1
	// Badges: the facts worth a chip for the kind (see badges.go).
	Badges   []string `json:"badges,omitempty"`
	OnServer bool     `json:"onServer"`
	// Watched: the profile finished the movie or every episode of the
	// series (set on the cards of /api/items/{id}/related only).
	Watched  bool  `json:"watched,omitempty"`
	SeriesID int64 `json:"seriesId,omitempty"` // episodes, seasons and comic volumes
	// Online cards of «Продолжить» (a title watched online, not in the
	// library; ID 0, only for /api/home?online=1): the TMDB title, the
	// episode and where to resume. A missing season or episode is 0
	// (specials are season 0).
	Online    bool    `json:"online,omitempty"`
	TmdbKind  string  `json:"tmdbKind,omitempty"` // "movie" | "series"
	TmdbID    int     `json:"tmdbId,omitempty"`
	Season    int     `json:"season,omitempty"`
	Episode   int     `json:"episode,omitempty"`
	ResumeSec float64 `json:"resumeSec,omitempty"`
}

// cacheName is a file of the image cache (metadata.Images.Fetch).
var cacheName = library.ImageCacheName

// imageURL maps a stored artwork reference to a URL the browser may load
// (library.ImageURL).
func imageURL(itemID int64, slot, v string) string { return library.ImageURL(itemID, slot, v) }

type itemMeta struct {
	Logo   string   `json:"logo"`
	Genres []string `json:"genres"`
	// GenreIDs: TMDB's ids of Genres (Deps.Genres names them in the viewer's language).
	GenreIDs []int `json:"genreIds"`
	Runtime  int   `json:"runtime"`
	// TmdbLang: the TMDB language of the stored text ("" = the metadata
	// language; Deps.Translations shows it in the viewer's).
	TmdbLang string      `json:"tmdb_lang"`
	Cast     []castEntry `json:"cast"`
	// ImdbID: from TMDB or a .nfo ("tt0816692"; "" = none).
	ImdbID string `json:"imdb_id"`
}

type castEntry struct {
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Profile   string `json:"profile,omitempty"` // TMDB path of the photo
}

func metaOf(it library.Item) itemMeta {
	var m itemMeta
	if it.MetaJSON != "" {
		if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
			log.Printf("api: item %d meta_json: %v", it.ID, err) // degrade: card without logo/genres/cast
		}
	}
	return m
}

// lib is the shared state of the library, playback and image handlers.
type lib struct {
	Deps
	guard      *auth.Guard        // the router's one session check
	subJobs    detached[struct{}] // subtitle extractions
	sessions   hlsOwners
	comics     *comicArchives     // open comic volumes of the page API
	langs      langsCache         // «Мои языки» for the badges
	shows      showCache          // series and season badges (showBadges)
	kindleJobs detached[struct{}] // «Отправить на Kindle»
	uploads    detached[struct{}] // indexing of «Добавить книги»
	extAudio   extAudioCache      // external audio tracks described (extaudio)
	// requests is «Попросить» (nil without the database or a catalog):
	// another build's search results and title pages of a kid carry its
	// state, and its admin approves it there.
	requests *kidRequests
}

func userOf(r *http.Request) auth.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

// scopeOwner is the profile whose titles a library list keeps: the caller
// with ?scope=mine («Моя медиатека»), else 0 (the shared library: the
// default, so the apps see every title). A kid's profile always sees the
// shared library (filtered by its rating).
func scopeOwner(r *http.Request, u auth.User) int64 {
	if u.IsKid || r.URL.Query().Get("scope") != auth.ScopeMine {
		return 0
	}
	return u.ID
}

// ratingLimit is the profile's effective maximum rating ("" = no limit).
// This is the only place the API derives it (auth.User.RatingLimit).
func ratingLimit(u auth.User) string { return u.RatingLimit() }

// root walks up to the top of an item's hierarchy (library.Repo.Root).
// The parents come from the request's card batch when it has them.
func (h *lib) root(ctx context.Context, it library.Item) (library.Item, error) {
	if batchOf(ctx) == nil {
		return h.Library.Root(ctx, it)
	}
	for i := 0; i < 4 && library.HasRatedParent(it.Kind) && it.ParentID != 0; i++ { // as Repo.Root
		p, err := h.itemByID(ctx, it.ParentID)
		if err != nil {
			return it, err
		}
		it = p
	}
	return it, nil
}

// allowed reports whether the user may see it: library.Repo.Visible, the
// rule shared with Subsonic.
func (h *lib) allowed(ctx context.Context, u auth.User, it library.Item) (bool, error) {
	return h.Library.Visible(ctx, ratingLimit(u), it)
}

// item loads an item visible to the user, or writes 404/500 and returns false.
func (h *lib) item(w http.ResponseWriter, r *http.Request, id int64) (library.Item, bool) {
	it, err := h.Library.Item(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return it, false
	}
	if err != nil {
		internalErr(w, r, "item", err)
		return it, false
	}
	ok, err := h.allowed(r.Context(), userOf(r), it)
	if err != nil {
		internalErr(w, r, "item access", err)
		return it, false
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, "Не найдено") // do not reveal hidden items
		return it, false
	}
	return it, true
}

// itemParam loads the item named by the {param} URL parameter.
func (h *lib) itemParam(w http.ResponseWriter, r *http.Request, param string) (library.Item, bool) {
	id, ok := idParam(w, r, param)
	if !ok {
		return library.Item{}, false
	}
	return h.item(w, r, id)
}

// file loads the file named by {fileId} if its item is visible to the user.
func (h *lib) file(w http.ResponseWriter, r *http.Request) (library.File, library.Item, bool) {
	id, ok := idParam(w, r, "fileId")
	if !ok {
		return library.File{}, library.Item{}, false
	}
	return h.fileByID(w, r, id)
}

// fileByID loads file id if its item is visible to the user, or writes
// 404/500 and returns false.
func (h *lib) fileByID(w http.ResponseWriter, r *http.Request, id int64) (library.File, library.Item, bool) {
	f, err := h.Library.FileByID(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Файл не найден")
		return f, library.Item{}, false
	}
	if err != nil {
		internalErr(w, r, "file", err)
		return f, library.Item{}, false
	}
	it, ok := h.item(w, r, f.ItemID)
	return f, it, ok
}

func idParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return 0, false
	}
	return id, true
}

func internalErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	log.Printf("api: %s: %v", what, err)
	writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
}

func ratio(e progress.Entry) float64 {
	if e.Finished {
		return 1
	}
	if e.DurationMs <= 0 {
		return 0
	}
	return min(float64(e.PositionMs)/float64(e.DurationMs), 1)
}

// progressOf returns the user's progress (0..1) on an item (the read
// share of a book).
func (h *lib) progressOf(ctx context.Context, uid, itemID int64) float64 {
	e, err := h.progressEntry(ctx, uid, itemID)
	if err != nil {
		if !errors.Is(err, progress.ErrNotFound) {
			log.Printf("api: progress %d: %v", itemID, err)
		}
		return 0
	}
	return ratio(e)
}

func episodeLabel(ctx context.Context, season, ep int) string {
	if season == 0 {
		return i18n.T(ctx, "Спецвыпуск %d", ep)
	}
	return i18n.T(ctx, "Сезон %d, серия %d", season, ep)
}

// card builds a Card. Episodes and seasons borrow missing artwork from
// their series and carry the series title in the subtitle.
func (h *lib) card(ctx context.Context, uid int64, it library.Item) Card {
	m := metaOf(it)
	c := Card{ID: it.ID, Kind: it.Kind, Title: it.Title, Year: it.Year, OnServer: true,
		Poster: imageURL(it.ID, "poster", it.Poster), Backdrop: imageURL(it.ID, "backdrop", it.Backdrop),
		Logo: imageURL(it.ID, "logo", m.Logo)}
	switch it.Kind {
	case library.KindMovie:
		tr := h.cachedTranslation(ctx, it, m)
		c.Title = translatedTitle(tr, it.Title)
		h.translatedArt(&c, tr, it.Poster, m.Logo)
		c.Subtitle = strings.Join(firstN(h.genreNames(ctx, it.Kind, m), 2), " · ")
		c.Progress = h.progressOf(ctx, uid, it.ID)
	case library.KindSeries:
		tr := h.cachedTranslation(ctx, it, m)
		c.Title = translatedTitle(tr, it.Title)
		h.translatedArt(&c, tr, it.Poster, m.Logo)
		c.Subtitle = strings.Join(firstN(h.genreNames(ctx, it.Kind, m), 2), " · ")
	case library.KindSeason, library.KindEpisode:
		if it.Kind == library.KindEpisode {
			c.Progress = h.progressOf(ctx, uid, it.ID)
		}
		s, err := h.root(ctx, it)
		if err != nil {
			log.Printf("api: card %d: series lookup: %v", it.ID, err) // degrade: card without series art/title
			break
		}
		if s.Kind != library.KindSeries {
			break
		}
		sm := metaOf(s)
		st := h.cachedTranslation(ctx, s, sm)
		c.SeriesID = s.ID
		c.Year = s.Year
		// the series' art (in the viewer's language) where the item has none
		var art Card
		art.Poster, art.Logo = imageURL(s.ID, "poster", s.Poster), imageURL(s.ID, "logo", sm.Logo)
		h.translatedArt(&art, st, s.Poster, sm.Logo)
		if c.Poster == "" {
			c.Poster = art.Poster
		}
		if c.Backdrop == "" {
			c.Backdrop = imageURL(s.ID, "backdrop", s.Backdrop)
		}
		if c.Logo == "" {
			c.Logo = art.Logo
		}
		series := translatedTitle(st, s.Title)
		if it.Kind == library.KindEpisode {
			c.Title, _ = translatedEpisode(st, it)
			c.Subtitle = series + " · " + episodeLabel(ctx, it.SeasonNo, it.EpisodeNo)
		} else {
			c.Subtitle = series
		}
	case library.KindAlbum, library.KindArtist, library.KindBook, library.KindAudiobook, library.KindComic, library.KindComicVolume:
		h.mediaCard(ctx, uid, it, &c)
	}
	c.Badges = h.badges(ctx, it)
	c.Title, c.Subtitle = scanLabel(ctx, c.Title), scanLabel(ctx, c.Subtitle)
	return c
}

// translatedArt puts t's poster and logo (TMDB's, in the viewer's
// language) over c's, the stored poster and logo, once they are in the
// image cache (tmdb.Translations.Art downloads them in the background:
// a card never waits for the image host, nor shows a picture it may not
// reach). Local artwork ("file:") stays, someone chose it. Without
// Artwork (no TMDB) nothing changes.
func (h *lib) translatedArt(c *Card, t *tmdb.Translation, poster, logo string) {
	if t == nil || h.Artwork == nil {
		return
	}
	if u := h.artURL(t.Poster); u != "" && !strings.HasPrefix(poster, "file:") {
		c.Poster = u
	}
	if u := h.artURL(t.Logo); u != "" && !strings.HasPrefix(logo, "file:") {
		c.Logo = u
	}
}

// artURL is the address of a TMDB poster or logo path ("/abc.png") in the
// image cache, served by /api/images/art/{name}; "" for anything else or
// one not downloaded yet.
func (h *lib) artURL(path string) string {
	name, ok := strings.CutPrefix(path, "/")
	if !ok || !tmdb.ArtFile.MatchString(name) {
		return ""
	}
	if _, ok := h.Artwork.Cached(path); !ok {
		return ""
	}
	return "/api/images/art/" + name
}

// scanLabelPrefixes start the titles the scanner makes itself when a
// file names none («Сезон 2», «Серия 3», «Трек 4», «Том 1 · Глава 5»).
var scanLabelPrefixes = []string{"Сезон", "Серия", "Трек", "Том", "Глава"}

// scanLabel is a title the scanner stored in Russian («Спецвыпуски»,
// «Сезон %d», «Без альбома», «Разные исполнители»…) in the request
// language. Any other title (from tags, files or TMDB) is left as it is,
// even when it happens to read like another catalog key.
func scanLabel(ctx context.Context, s string) string {
	switch s {
	case "Спецвыпуски", "Без альбома", "Разные исполнители", "Неизвестный исполнитель":
		return i18n.Localize(ctx, s)
	}
	for _, p := range scanLabelPrefixes {
		if strings.HasPrefix(s, p+" ") {
			return i18n.Localize(ctx, s)
		}
	}
	return s
}

// bookMeta is what the book, audiobook and comic scanners keep in meta_json.
type bookMeta struct {
	Authors     []string `json:"authors"`
	Narrator    string   `json:"narrator"`
	Series      string   `json:"series"`
	SeriesIndex float64  `json:"series_index"`
	Lang        string   `json:"lang"`
	Writer      string   `json:"writer"`
	Direction   string   `json:"direction"`
	Pages       int      `json:"pages"`
	Volume      float64  `json:"volume"`
	FilesOrder  []int64  `json:"files_order"`
}

func bookMetaOf(it library.Item) bookMeta {
	var m bookMeta
	if it.MetaJSON != "" {
		if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
			log.Printf("api: item %d meta_json: %v", it.ID, err) // degrade: no authors
		}
	}
	return m
}

// mediaCard fills the music and book parts of a card: album → artist,
// artist → its newest album's cover, book → authors, audiobook → author,
// comic → «N томов», comic_volume → the series title with the volume
// title as subtitle; books, audiobooks and volumes carry the read share.
func (h *lib) mediaCard(ctx context.Context, uid int64, it library.Item, c *Card) {
	m := bookMetaOf(it)
	switch it.Kind {
	case library.KindAlbum:
		if it.ParentID != 0 {
			if ar, err := h.itemByID(ctx, it.ParentID); err == nil {
				c.Subtitle = ar.Title
			} else {
				log.Printf("api: card %d: artist: %v", it.ID, err) // degrade: no subtitle
			}
		}
	case library.KindArtist:
		row, err := h.Library.ArtistInfo(ctx, it)
		if err != nil {
			log.Printf("api: card %d: artist cover: %v", it.ID, err)
		} else if c.Poster == "" {
			c.Poster = row.Cover
		}
	case library.KindBook:
		c.Subtitle = strings.Join(m.Authors, ", ")
		c.Progress = h.progressOf(ctx, uid, it.ID)
	case library.KindAudiobook:
		if len(m.Authors) > 0 {
			c.Subtitle = m.Authors[0]
		}
		c.Progress = h.progressOf(ctx, uid, it.ID)
	case library.KindComic:
		n, err := h.Library.VolumeCount(ctx, it.ID)
		if err != nil {
			log.Printf("api: card %d: volumes: %v", it.ID, err)
		}
		c.Subtitle = i18n.Plural(ctx, n, "том", "тома", "томов")
	case library.KindComicVolume:
		c.Subtitle = it.Title
		c.Progress = h.progressOf(ctx, uid, it.ID)
		if it.ParentID == 0 {
			break
		}
		s, err := h.itemByID(ctx, it.ParentID)
		if err != nil {
			log.Printf("api: card %d: series: %v", it.ID, err) // degrade: the volume's own title
			c.Title, c.Subtitle = it.Title, ""
			break
		}
		c.Title, c.SeriesID = s.Title, s.ID
		if c.Poster == "" {
			c.Poster = imageURL(s.ID, "poster", s.Poster)
		}
	}
}

func (h *lib) cards(ctx context.Context, uid int64, items []library.Item) []Card {
	ctx = h.withCards(ctx, uid, items)
	out := make([]Card, 0, len(items))
	for _, it := range items {
		out = append(out, h.card(ctx, uid, it))
	}
	return out
}
