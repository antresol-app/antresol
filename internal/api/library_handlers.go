package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/metadata/ratings"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

const shelfSize = 20

type shelf struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cards []Card `json:"cards"`
}

// EpisodeCard is a Card with episode fields, used in ItemDetail.Seasons.
type EpisodeCard struct {
	Card
	SeasonNo   int    `json:"seasonNo"`
	EpisodeNo  int    `json:"episodeNo"`
	Overview   string `json:"overview,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Watched    bool   `json:"watched"`
}

type SeasonInfo struct {
	ID       int64         `json:"id"`
	Number   int           `json:"number"`
	Title    string        `json:"title"`
	Episodes []EpisodeCard `json:"episodes"`
}

type CastMember struct {
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	// Photo: the actor's TMDB picture, served by this server from its image
	// cache (/api/images/cast/{name}: downloaded with the title's metadata,
	// else on the first view); "" when TMDB has none.
	Photo string `json:"photo,omitempty"`
}

// castPhoto is the server URL of a cast member's TMDB profile path.
// roleIn is the role as shown in lang: TMDB keeps one role name per credit
// as it was entered (the request language translates the actor's name, not
// the role), so a role in another script than the interface's («waitress»
// in Russian, «Лёша» in English) is left out rather than shown in a second
// language.
func roleIn(role, lang string) string {
	var cyrillic, latin, other bool
	for _, r := range role {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic = true
		case unicode.Is(unicode.Latin, r):
			latin = true
		case unicode.IsLetter(r):
			other = true
		}
	}
	switch {
	case !cyrillic && !latin && !other: // digits, symbols
		return role
	case lang == i18n.RU:
		if cyrillic {
			return role
		}
	case latin && !cyrillic && !other:
		return role
	}
	return ""
}

func castPhoto(profile string) string {
	name, ok := strings.CutPrefix(profile, "/")
	if !ok || !tmdb.ProfileFile.MatchString(name) {
		return ""
	}
	return "/api/images/cast/" + name
}

type AudioTrack struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default"`
	// External: an audio file beside the video (index 2000+, extaudio);
	// Src is its stable id (send it back as the play request's audioSrc);
	// Studio is the voice-over studio its name or folder names.
	External bool   `json:"external,omitempty"`
	Src      string `json:"src,omitempty"`
	Studio   string `json:"studio,omitempty"`
	// Kind "cleaned": a voice-over with the original speech subtracted
	// (voclean, the badge «Очищено»); Method how (subtraction | ml);
	// CleanedFrom the file's stream it was made from.
	Kind        string `json:"kind,omitempty"`
	Method      string `json:"method,omitempty"`
	CleanedFrom *int   `json:"cleanedFrom,omitempty"`
}

// SubTrack is a subtitle track. Text tracks have a WebVTT URL; image-based
// ones (PGS, VobSub) are listed with BurnIn and no URL.
type SubTrack struct {
	Index    int    `json:"index"` // ffprobe index; external files: 1000+
	Codec    string `json:"codec"`
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Forced   bool   `json:"forced"`
	Default  bool   `json:"default"`
	External bool   `json:"external"`
	BurnIn   bool   `json:"burnIn"`
	URL      string `json:"url,omitempty"`
}

type FileInfo struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Size       int64        `json:"size"`
	DurationMs int64        `json:"durationMs,omitempty"`
	Resolution string       `json:"resolution,omitempty"`
	Audio      []AudioTrack `json:"audio"`
	Subs       []SubTrack   `json:"subs"`
}

type Actions struct {
	Play     bool `json:"play"`
	ToServer bool `json:"toServer"`
	ToDevice bool `json:"toDevice"`
	WatchNow bool `json:"watchNow"`
}

// ItemDetail is the item page. Files are in playback order (cd1, cd2, ...).
type ItemDetail struct {
	Card
	OriginalTitle string       `json:"originalTitle,omitempty"`
	Overview      string       `json:"overview"`
	Genres        []string     `json:"genres"`
	Runtime       int          `json:"runtime,omitempty"` // minutes
	Rating        string       `json:"rating,omitempty"`
	Cast          []CastMember `json:"cast"`
	Seasons       []SeasonInfo `json:"seasons"`
	Files         []FileInfo   `json:"files"`
	Next          *EpisodeCard `json:"next,omitempty"` // series: episode to play next
	InWatchlist   bool         `json:"inWatchlist"`
	Actions       Actions      `json:"actions"`
	// TmdbID (movies and series): «Смотреть онлайн», and for a series the
	// seasons that are not on the server yet («Добавить сезон…»).
	TmdbID int64 `json:"tmdbId,omitempty"`
	// ImdbID (movies and series, "tt0816692"): its ratings come from
	// /api/ratings/{imdbId}.
	ImdbID string `json:"imdbId,omitempty"`
	// Follow (movies and series): {id, active}, null when not followed;
	// left out without a follow service (no TMDB key) and for kids.
	Follow json.RawMessage `json:"follow,omitempty"`
}

func mountLibrary(r chi.Router, h *lib) {
	r.Get("/api/home", h.home)
	r.Get("/api/items", h.list)
	r.Get("/api/items/{id}", h.detail)
	r.Get("/api/search", h.search)
	r.Get("/api/ratings/{imdbId}", h.ratings)
	r.Get("/api/library/status", h.scanStatus)
	r.With(auth.RequireAdmin).Post("/api/library/scan", h.scan)
	r.Get("/api/progress/{itemId}", h.getProgress)
	r.Put("/api/progress/{itemId}", h.saveProgress)
	r.Post("/api/items/{id}/watched", h.watched)
	r.Get("/api/items/{id}/family", h.familyProgress)
	r.Post("/api/progress/{itemId}/adopt", h.adoptProgress)
	r.Post("/api/watchlist/{itemId}", h.watchlist(true))
	r.Delete("/api/watchlist/{itemId}", h.watchlist(false))
}

func (h *lib) home(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	limit := ratingLimit(u)
	// ?scope=mine: the hero and the «recently added» shelves keep the
	// profile's own titles; «Продолжить», «Хочу посмотреть» and the
	// played/reading shelves are the profile's already
	owner := scopeOwner(r, u)

	recent, _, err := h.Library.List(ctx, library.ListQuery{Sort: "added", Limit: 50, MaxRating: limit, Owner: owner, NoTotal: true})
	if err != nil {
		internalErr(w, r, "home recent", err)
		return
	}
	movies, _, err := h.Library.List(ctx, library.ListQuery{Kind: library.KindMovie, Sort: "added", Limit: shelfSize, MaxRating: limit, Owner: owner, NoTotal: true})
	if err != nil {
		internalErr(w, r, "home movies", err)
		return
	}
	series, err := h.Library.RecentSeriesOf(ctx, shelfSize, limit, owner)
	if err != nil {
		internalErr(w, r, "home series", err)
		return
	}

	entries, err := h.Progress.ContinueWatching(ctx, u.ID, shelfSize)
	if err != nil {
		internalErr(w, r, "home continue", err)
		return
	}
	// what the hero, «Продолжить» and the two shelves show, fetched at once
	var heroItems, batch []library.Item
	for _, it := range recent {
		if it.Backdrop != "" && len(heroItems) < 5 {
			heroItems = append(heroItems, it)
		}
	}
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ItemID)
	}
	watching, werr := h.Library.ItemsByID(ctx, ids)
	for _, id := range ids {
		if it, ok := watching[id]; ok {
			batch = append(batch, it)
		}
	}
	batch = append(append(append(batch, heroItems...), movies...), series...)
	ctx = h.withCards(ctx, u.ID, batch)
	if b := batchOf(ctx); b != nil && werr == nil {
		for _, id := range ids {
			b.askedI[id] = true // one ItemsByID did not return is not shown (visible: false)
		}
	}
	hero := []Card{}
	for _, it := range heroItems {
		hero = append(hero, h.card(ctx, u.ID, it))
	}
	var dated []datedCard
	mine := map[int64]bool{} // the titles of «Продолжить» («Семья смотрит» leaves them out)
	for _, e := range entries {
		if e.SeriesID != 0 {
			mine[e.SeriesID] = true
		} else {
			mine[e.ItemID] = true
		}
		it, ok, err := h.visible(ctx, r, e.ItemID)
		if err != nil {
			internalErr(w, r, "home continue", err)
			return
		}
		if ok {
			c := h.card(ctx, u.ID, it)
			c.Progress = ratio(e)
			dated = append(dated, datedCard{e.UpdatedAt, c})
		}
	}
	onl, err := h.onlineContinue(r, u)
	if err != nil {
		internalErr(w, r, "home continue online", err)
		return
	}
	dated = append(dated, onl...)
	sort.SliceStable(dated, func(i, j int) bool { return dated[i].at.After(dated[j].at) })
	cont := []Card{}
	for _, d := range dated[:min(len(dated), shelfSize)] {
		cont = append(cont, d.card)
	}
	watch, err := h.watchlistCards(ctx, r, isVideoKind)
	if err != nil {
		internalErr(w, r, "home watchlist", err)
		return
	}
	played, err := h.recentlyPlayed(ctx, r)
	if err != nil {
		internalErr(w, r, "home played", err)
		return
	}
	reading, err := h.progressCards(ctx, r, false)
	if err != nil {
		internalErr(w, r, "home reading", err)
		return
	}

	shelves := []shelf{{"continue", i18n.TC(ctx, "shelf", "Продолжить"), cont}}
	// «Вышло новое»: what appeared of the profile's marked titles (only when any)
	if h.Tracking != nil {
		if fresh, err := h.trackedShelf(ctx, u); err != nil {
			log.Printf("home: tracked shelf: %v", err)
		} else if len(fresh) > 0 {
			shelves = append(shelves, shelf{"tracked-new", trackedShelfTitle(ctx), fresh})
		}
	}
	// ?family=1 (the web): «Семья смотрит»; the app's shelves stay as they were
	if r.URL.Query().Get("family") == "1" {
		fam, err := h.familyShelf(ctx, r, u, mine)
		if err != nil {
			internalErr(w, r, "home family", err)
			return
		}
		shelves = append(shelves, shelf{"family", i18n.T(ctx, "Семья смотрит"), fam})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hero": hero,
		"shelves": append(shelves, []shelf{
			{"recently-played", i18n.T(ctx, "Слушали недавно"), played},
			{"reading", i18n.T(ctx, "Читаю сейчас"), reading},
			{"recent-movies", i18n.T(ctx, "Недавно добавленные фильмы"), h.cards(ctx, u.ID, movies)},
			{"recent-series", i18n.T(ctx, "Новые серии"), h.cards(ctx, u.ID, series)},
			{"watchlist", i18n.T(ctx, "Хочу посмотреть"), watch},
		}...),
	})
}

// datedCard is a card of «Продолжить» with when it was last watched.
type datedCard struct {
	at   time.Time
	card Card
}

func isVideoKind(k string) bool {
	switch k {
	case library.KindMovie, library.KindSeries, library.KindSeason, library.KindEpisode:
		return true
	}
	return false
}

// visible loads an item and checks the profile's rating limit; a missing item is (false, nil).
func (h *lib) visible(ctx context.Context, r *http.Request, id int64) (library.Item, bool, error) {
	it, err := h.itemByID(ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		return it, false, nil
	}
	if err != nil {
		return it, false, err
	}
	ok, err := h.allowed(ctx, userOf(r), it)
	return it, ok, err
}

func (h *lib) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && kind != library.KindMovie && kind != library.KindSeries {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	u := userOf(r)
	items, total, err := h.Library.List(r.Context(), library.ListQuery{Kind: kind, Sort: q.Get("sort"), Limit: limit,
		Offset: max(offset, 0), MaxRating: ratingLimit(u), Owner: scopeOwner(r, u)})
	if err != nil {
		internalErr(w, r, "list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.cards(r.Context(), u.ID, items), "total": total})
}

// searchSize is how many results each search section returns (songs: songsSize).
const (
	searchSize = 20
	songsSize  = 10
)

func (h *lib) search(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// the library (films and series), then the other sections, then songs:
	// one pass over the titles for all of them
	keys := []string{"library", "artists", "albums", "books", "audiobooks", "comics"}
	found, err := h.Library.SearchSections(ctx, q, []library.SearchSection{
		{Kinds: []string{library.KindMovie, library.KindSeries}, Limit: 50},
		{Kinds: []string{library.KindArtist}, Limit: searchSize}, {Kinds: []string{library.KindAlbum}, Limit: searchSize},
		{Kinds: []string{library.KindBook}, Limit: searchSize}, {Kinds: []string{library.KindAudiobook}, Limit: searchSize},
		{Kinds: []string{library.KindComic}, Limit: searchSize}, {Kinds: []string{library.KindTrack}, Limit: songsSize},
	}, ratingLimit(u))
	if err != nil {
		internalErr(w, r, "search", err)
		return
	}
	ctx = h.withCards(ctx, u.ID, slices.Concat(found[:len(keys)]...)) // every section's cards at once
	out := map[string]any{}
	for i, key := range keys {
		out[key] = h.cards(ctx, u.ID, found[i])
	}
	songs := found[len(keys)]
	ids := make([]int64, 0, len(songs))
	for _, it := range songs {
		ids = append(ids, it.ID)
	}
	tracks, err := h.Library.Tracks(ctx, ids)
	if err != nil {
		internalErr(w, r, "search songs", err)
		return
	}
	dtos, err := h.trackDTOs(ctx, u, tracks)
	if err != nil {
		internalErr(w, r, "search songs", err)
		return
	}
	out["songs"] = dtos
	writeJSON(w, http.StatusOK, out)
}

// resolution names a video's quality tier by both sides: a scope film
// (1920×800, 2048×858) is 1080p by its width though its height is under
// 1000; the long and short sides are used so portrait videos count too.
func resolution(width, height int) string {
	if width <= 0 {
		width = height // an old probe without the width: by the height alone
	}
	long, short := max(width, height), min(width, height)
	switch {
	case height <= 0:
		return ""
	case long >= 3200 || short >= 2000:
		return "4K"
	case long >= 2300 || short >= 1400:
		return "1440p"
	case long >= 1800 || short >= 1000:
		return "1080p"
	case long >= 1200 || short >= 700:
		return "720p"
	case short >= 560:
		return "576p"
	}
	return "SD"
}

func audioTracks(p media.ProbeResult) []AudioTrack {
	out := make([]AudioTrack, 0, len(p.Audio))
	for _, a := range p.Audio {
		out = append(out, AudioTrack{Index: a.Index, Codec: a.Codec, Lang: a.Lang, Title: a.Title, Channels: a.Channels, Default: a.Default})
	}
	return out
}

func (h *lib) fileInfo(ctx context.Context, f library.File) FileInfo {
	fi := FileInfo{ID: f.ID, Name: baseName(f.Path), Size: f.Size, DurationMs: f.DurationMs, Audio: []AudioTrack{}}
	p, ok := cachedProbe(f)
	if ok {
		fi.Audio = audioTracks(p)
		if p.Video != nil {
			fi.Resolution = resolution(p.Video.Width, p.Video.Height)
		}
		if fi.DurationMs == 0 {
			fi.DurationMs = p.DurationMs
		}
		// the player picks a remembered voice from this list before it plays
		// (the probed duration, as /api/play: one description per track)
		fi.Audio = withExternalAudio(fi.Audio, h.externalAudio(ctx, f, p.DurationMs, p.StartMs))
	}
	fi.Subs = subTracks(f, p, h.DataDir)
	return fi
}

// episodeCard: tr is the series' text in the viewer's language (nil: the
// stored one).
func (h *lib) episodeCard(ctx context.Context, uid int64, ep library.Item, tr *tmdb.Translation) EpisodeCard {
	ec := EpisodeCard{Card: h.card(ctx, uid, ep), SeasonNo: ep.SeasonNo, EpisodeNo: ep.EpisodeNo, Overview: ep.Overview}
	if tr != nil {
		_, ec.Overview = translatedEpisode(tr, ep) // the title is the card's
	}
	ec.Watched = ec.Progress >= 1
	fs, err := h.filesOf(ctx, ep.ID)
	if err != nil {
		log.Printf("api: episode %d files: %v", ep.ID, err) // degrade: no duration
	} else if len(fs) > 0 {
		ec.DurationMs = fs[0].DurationMs
	}
	return ec
}

func (h *lib) detail(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	m := metaOf(it)

	var seasons []library.Item
	if it.Kind == library.KindSeries {
		children, err := h.Library.Children(ctx, it.ID)
		if err != nil {
			internalErr(w, r, "detail seasons", err)
			return
		}
		for _, c := range children {
			if c.Kind == library.KindSeason {
				seasons = append(seasons, c)
			}
		}
	} else if it.Kind == library.KindSeason {
		seasons = []library.Item{it}
	}
	// the text in the viewer's language when the library keeps another:
	// fetched before the cards, which then find it cached
	tr := h.pageTranslation(ctx, it, seasons)

	d := ItemDetail{Card: h.card(ctx, u.ID, it), OriginalTitle: it.OriginalTitle, Overview: it.Overview,
		Genres: h.genreNames(ctx, it.Kind, m), Runtime: m.Runtime, Rating: it.Rating, Cast: []CastMember{}, Seasons: []SeasonInfo{}, Files: []FileInfo{}}
	if d.Genres == nil {
		d.Genres = []string{}
	}
	cast := m.Cast
	if tr != nil {
		switch it.Kind {
		case library.KindMovie, library.KindSeries:
			if tr.Overview != "" {
				d.Overview = tr.Overview
			}
			if len(tr.Cast) > 0 {
				cast = cast[:0:0]
				for _, p := range tr.Cast {
					cast = append(cast, castEntry{p.Name, p.Character, p.Profile})
				}
			}
		case library.KindEpisode:
			_, d.Overview = translatedEpisode(tr, it)
		}
	}
	for _, c := range cast {
		d.Cast = append(d.Cast, CastMember{c.Name, roleIn(c.Character, i18n.FromContext(ctx)), castPhoto(c.Profile)})
	}

	switch it.Kind {
	case library.KindSeries, library.KindSeason:
	default:
		files, err := h.Library.FilesOf(ctx, it.ID)
		if err != nil {
			internalErr(w, r, "detail files", err)
			return
		}
		for _, f := range files {
			d.Files = append(d.Files, h.fileInfo(ctx, f))
		}
	}
	// every season's episodes first: their cards' progress, files and
	// parents are then read in a few queries, not a few per episode
	seasonEps := make([][]library.Item, len(seasons))
	var allEps []library.Item
	for i, s := range seasons {
		eps, err := h.Library.Children(ctx, s.ID)
		if err != nil {
			internalErr(w, r, "detail episodes", err)
			return
		}
		for _, ep := range eps {
			if ep.Kind == library.KindEpisode {
				seasonEps[i] = append(seasonEps[i], ep)
			}
		}
		allEps = append(allEps, seasonEps[i]...)
	}
	ctx = h.withCards(ctx, u.ID, allEps)
	episodes := 0
	for i, s := range seasons {
		si := SeasonInfo{ID: s.ID, Number: s.SeasonNo, Title: scanLabel(ctx, s.Title), Episodes: []EpisodeCard{}}
		for _, ep := range seasonEps[i] {
			si.Episodes = append(si.Episodes, h.episodeCard(ctx, u.ID, ep, tr))
		}
		episodes += len(si.Episodes)
		d.Seasons = append(d.Seasons, si)
	}

	if it.Kind == library.KindSeries {
		next, ok, err := h.Progress.NextEpisode(ctx, u.ID, it.ID)
		if err != nil {
			internalErr(w, r, "detail next", err)
			return
		}
		if ok {
			ep, err := h.itemByID(ctx, next)
			if err != nil {
				log.Printf("api: series %d next episode %d: %v", it.ID, next, err) // degrade: no "next"
			} else {
				ec := h.episodeCard(ctx, u.ID, ep, tr)
				d.Next = &ec
			}
		}
	}
	ids, err := h.Progress.Watchlist(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "detail watchlist", err)
		return
	}
	for _, id := range ids {
		d.InWatchlist = d.InWatchlist || id == it.ID
	}
	// Everything in the library already lives on the server: "На сервер" and
	// "Смотреть сейчас" belong to tracker results (Plan 1, Task 10+).
	// nothing of a category a kid's rules limit goes to the device
	// (screenDownloadAllowed, device copies)
	d.Actions = Actions{Play: len(d.Files) > 0 || episodes > 0, ToDevice: len(d.Files) > 0 && !h.screenRestricts(r, it.Kind)}
	if it.Kind == library.KindSeries || it.Kind == library.KindMovie {
		d.TmdbID = it.TmdbID
		if ratings.ValidID(m.ImdbID) {
			d.ImdbID = m.ImdbID
		}
	}
	d.Follow = h.followOf(ctx, u, it.Kind, it.TmdbID)
	writeJSON(w, http.StatusOK, d)
}

// Ratings are a title's IMDb and Rotten Tomatoes ratings
// (*ratings.Service).
type Ratings interface {
	Get(ctx context.Context, imdbID string) ratings.Ratings
}

// ratings: GET /api/ratings/{imdbId}, what IMDb and Rotten Tomatoes say of
// a title, {} when nothing is known (they arrive after the page, which
// never waits for Wikidata).
func (h *lib) ratings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imdbId")
	if !ratings.ValidID(id) {
		writeErr(w, r, http.StatusBadRequest, "Неверный идентификатор IMDb")
		return
	}
	out := ratings.Ratings{}
	if h.Ratings != nil && h.mayRate(r, id) {
		out = h.Ratings.Get(r.Context(), id)
	}
	// no RT yet may be a lookup that did not run (busy, a recent failure):
	// the browser asks again soon instead of keeping «none» for an hour
	age := 3600
	if out.RottenTomatoes == nil {
		age = 300
	}
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(age))
	writeJSON(w, http.StatusOK, out)
}

// mayRate reports whether the profile may ask for the ratings of id: an
// adult opens tracker titles that are not in the library, a kid's profile
// only the library's titles its rating allows, so it cannot make the
// server look up arbitrary ids outside.
func (h *lib) mayRate(r *http.Request, id string) bool {
	u := userOf(r)
	if !u.IsKid {
		return true
	}
	items, err := h.Library.ItemsByIMDb(r.Context(), id)
	if err != nil {
		log.Printf("api: ratings %s: %v", id, err)
		return false
	}
	for _, it := range items {
		if ok, err := h.allowed(r.Context(), u, it); err == nil && ok {
			return true
		}
	}
	return false
}

func (h *lib) scanStatus(w http.ResponseWriter, r *http.Request) {
	if h.Jobs == nil {
		writeJSON(w, http.StatusOK, library.JobStatus{})
		return
	}
	writeJSON(w, http.StatusOK, localizedScan(r, h.Jobs.Status()))
}

func (h *lib) scan(w http.ResponseWriter, r *http.Request) {
	if h.Jobs == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Обновление медиатеки недоступно")
		return
	}
	h.Jobs.Trigger()
	writeJSON(w, http.StatusAccepted, localizedScan(r, h.Jobs.Status()))
}

// localizedScan: a failed scan's error (a scanner's, maybe in Russian) in
// the request language.
func localizedScan(r *http.Request, st library.JobStatus) library.JobStatus {
	st.Error = i18n.Localize(r.Context(), st.Error)
	return st
}

// wireTime is how times go to the native app: RFC 3339, UTC, milliseconds.
const wireTime = "2006-01-02T15:04:05.000Z07:00"

// getProgress is the caller's position in an item, with the time it was
// recorded (the app reconciles its offline queue against it); without a
// row: zeros and updatedAt null.
func (h *lib) getProgress(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "itemId")
	if !ok {
		return
	}
	e, err := h.Progress.Get(r.Context(), userOf(r).ID, it.ID)
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		internalErr(w, r, "progress get", err)
		return
	}
	out := struct {
		PositionMs int64   `json:"positionMs"`
		DurationMs int64   `json:"durationMs"`
		Finished   bool    `json:"finished"`
		Locator    string  `json:"locator"`
		Fraction   float64 `json:"fraction"`
		UpdatedAt  *string `json:"updatedAt"`
	}{PositionMs: e.PositionMs, DurationMs: e.DurationMs, Finished: e.Finished, Locator: e.Locator}
	switch {
	case e.Locator != "":
		out.Fraction = float64(e.PositionMs) / progress.FractionScale
	case e.DurationMs > 0:
		out.Fraction = float64(e.PositionMs) / float64(e.DurationMs)
	}
	if err == nil {
		t := e.UpdatedAt.UTC().Format(wireTime)
		out.UpdatedAt = &t
	} else {
		// marked unwatched: no position, as of that time (a client compares
		// its queued writes with it)
		at, ok, cerr := h.Progress.ClearedAt(r.Context(), userOf(r).ID, it.ID)
		if cerr != nil {
			internalErr(w, r, "progress get", cerr)
			return
		}
		if ok {
			t := at.UTC().Format(wireTime)
			out.UpdatedAt = &t
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *lib) saveProgress(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "itemId")
	if !ok {
		return
	}
	var in struct {
		PositionMs int64    `json:"positionMs"`
		DurationMs int64    `json:"durationMs"`
		Locator    *string  `json:"locator"`
		Fraction   *float64 `json:"fraction"`
		UpdatedAt  string   `json:"updatedAt"` // RFC3339, when the client recorded it
	}
	if !decode(w, r, &in) {
		return
	}
	var at time.Time
	if in.UpdatedAt != "" {
		t, err := time.Parse(time.RFC3339, in.UpdatedAt)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, "Неверное время записи")
			return
		}
		at = t
	}
	ctx, uid := r.Context(), userOf(r).ID
	var err error
	switch it.Kind {
	case library.KindMovie, library.KindEpisode:
		err = h.Progress.PlayedAt(ctx, uid, it.ID, in.PositionMs, in.DurationMs, at)
	case library.KindAudiobook:
		err = h.Progress.ListenedAt(ctx, uid, it.ID, in.PositionMs, in.DurationMs, at)
	case library.KindBook, library.KindComicVolume:
		if in.Locator == nil || strings.TrimSpace(*in.Locator) == "" {
			writeErr(w, r, http.StatusBadRequest, "Не передана позиция")
			return
		}
		if len(*in.Locator) > 4096 {
			writeErr(w, r, http.StatusBadRequest, "Слишком длинная позиция")
			return
		}
		var f float64
		if in.Fraction != nil {
			f = *in.Fraction
		}
		err = h.Progress.SaveLocator(ctx, uid, it.ID, *in.Locator, f, at)
	default:
		writeErr(w, r, http.StatusBadRequest, "Прогресс для этого элемента не сохраняется")
		return
	}
	if err != nil { // a stale write is not an error: it is ignored
		internalErr(w, r, "progress save", err)
		return
	}
	// playback, when the server has a live session of the title (an
	// offline queue replayed later is not: screentime decides, by the
	// server's own clock)
	h.screenSignal(r, it, true)
	w.WriteHeader(http.StatusNoContent)
}

// playable lists what "watched" marks under it: itself for a movie,
// episode, book, audiobook or comic volume; the leaves under a series,
// season or comic series.
func (h *lib) playable(ctx context.Context, it library.Item) ([]int64, error) {
	switch it.Kind {
	case library.KindMovie, library.KindEpisode, library.KindBook, library.KindAudiobook, library.KindComicVolume:
		return []int64{it.ID}, nil
	case library.KindComic:
		vols, err := h.Library.Volumes(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		out := make([]int64, 0, len(vols))
		for _, v := range vols {
			out = append(out, v.ID)
		}
		return out, nil
	}
	children, err := h.Library.Children(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, c := range children {
		ids, err := h.playable(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

func (h *lib) watched(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Watched   bool   `json:"watched"`
		UpdatedAt string `json:"updatedAt"` // RFC3339, when the viewer marked it (server time); "" = now
	}
	if !decode(w, r, &in) {
		return
	}
	var at time.Time
	if in.UpdatedAt != "" {
		t, err := time.Parse(time.RFC3339, in.UpdatedAt)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, "Неверное время записи")
			return
		}
		at = t
	}
	ids, err := h.playable(r.Context(), it)
	if err != nil {
		internalErr(w, r, "watched", err)
		return
	}
	for _, id := range ids {
		if err := h.Progress.MarkWatchedAt(r.Context(), userOf(r).ID, id, in.Watched, at); err != nil {
			internalErr(w, r, "watched", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *lib) watchlist(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, ok := h.itemParam(w, r, "itemId")
		if !ok {
			return
		}
		uid := userOf(r).ID
		if add && !isVideoKind(it.Kind) && !bookKinds[it.Kind] {
			writeErr(w, r, http.StatusBadRequest, "Это нельзя добавить в список")
			return
		}
		var err error
		if add {
			err = h.Progress.AddToWatchlist(r.Context(), uid, it.ID)
		} else {
			err = h.Progress.RemoveFromWatchlist(r.Context(), uid, it.ID)
		}
		if err != nil {
			internalErr(w, r, "watchlist", err)
			return
		}
		// the mark follows the watchlist (tracking)
		if err := h.watchlistToMark(r.Context(), uid, it, add); err != nil {
			internalErr(w, r, "watchlist mark", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
