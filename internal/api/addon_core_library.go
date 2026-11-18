package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/releaseprefs"
	"github.com/go-chi/chi/v5"
)

// The core API's library read (library.read) and users (users.read).

const (
	msgAddonOutsideLibrary = "Путь вне медиатеки"
	msgAddonTooManyIDs     = "Не больше 500 id за раз"
	msgAddonTooManyResults = "Не больше 50 результатов за раз"
	msgAddonNotFoundItem   = "Не найдено"
	msgAddonNotFoundFile   = "Файл не найден"
	msgAddonNoSettings     = "Настройки недоступны"
	addonMaxTMDBIDs        = 500
	addonMaxSearch         = 50
)

func mountAddonCoreLibrary(r chi.Router, a *addonsAPI) {
	r.Get("/library/items/{id}", a.coreItem)
	r.Get("/library/items/{id}/files", a.coreItemFiles)
	r.Get("/library/tmdb/{kind}", a.coreTMDBIDs)
	r.Get("/library/tmdb/{kind}/{tmdbId}", a.coreTMDB)
	r.Get("/library/find/title", a.coreFindTitle)
	r.Get("/library/find/album", a.coreFindAlbum)
	r.Get("/library/find/book", a.coreFindBook)
	r.Get("/library/series/{tmdbId}/episodes", a.coreSeriesEpisodes)
	r.Get("/library/series/{tmdbId}/episode-items", a.coreSeriesEpisodeItems)
	r.Get("/library/series/{tmdbId}/folder", a.coreSeriesFolder)
	r.Get("/library/series/{tmdbId}/audio", a.coreSeriesAudio)
	r.Get("/library/albums/{id}/dir", a.coreAlbumDir)
	r.Get("/library/albums/{id}/tracks", a.coreAlbumTracks)
	r.Get("/library/files", a.coreFiles)
	r.Get("/library/files/{id}", a.coreFileByID)
	r.Get("/library/paths", a.corePaths)
	r.Get("/library/search", a.coreSearch)
	r.Get("/library/refs/{ref}", a.coreRefFiles)
	r.Get("/library/tracks", a.coreRefTracks)
	r.Get("/users", a.coreUsers)
	r.Get("/watched", a.coreWatched) // addon_core_watched.go
	r.Get("/settings/studios", a.coreStudios)
}

// readLibrary: the calling addon has library.read and the server a
// library; else the answer is written and ok is false.
func (a *addonsAPI) readLibrary(w http.ResponseWriter, r *http.Request) (addons.Installed, bool) {
	in, ok := a.callingAddon(w, r, "library.read")
	if !ok {
		return in, false
	}
	if a.lib == nil {
		writeErr(w, r, http.StatusServiceUnavailable, addons.ErrNoLibrary.Error())
		return in, false
	}
	return in, true
}

// libFail answers a library error: ErrNotFound is 404, anything else is
// logged and a 500.
func libFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, msgAddonNotFoundItem)
		return
	}
	log.Printf("api: addon core %s: %v", r.URL.Path, err)
	writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
}

func badRequest(w http.ResponseWriter, r *http.Request) {
	writeErr(w, r, http.StatusBadRequest, msgAddonBadPath)
}

// positiveID parses a positive integer (a path parameter or query value).
func positiveID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// videoKind: the kinds the TMDB routes know.
func videoKind(k string) bool { return k == library.KindMovie || k == library.KindSeries }

// rawJSON is s when it is valid JSON, else nothing.
func rawJSON(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}

func itemDTO(it library.Item) addonkit.Item {
	return addonkit.Item{ID: it.ID, Kind: it.Kind, ParentID: it.ParentID, Title: it.Title, OriginalTitle: it.OriginalTitle,
		Year: it.Year, Rating: it.Rating, TMDBID: it.TmdbID, SeasonNo: it.SeasonNo, EpisodeNo: it.EpisodeNo,
		Poster: it.Poster, AddedAt: it.AddedAt, Meta: rawJSON(it.MetaJSON)}
}

func itemsDTO(items []library.Item) []addonkit.Item {
	out := make([]addonkit.Item, 0, len(items))
	for _, it := range items {
		out = append(out, itemDTO(it))
	}
	return out
}

func fileDTO(f library.File) addonkit.File {
	return addonkit.File{ID: f.ID, ItemID: f.ItemID, Path: f.Path, Size: f.Size, Mtime: f.Mtime, DurationMs: f.DurationMs,
		Probe: rawJSON(f.ProbeJSON)}
}

func filesDTO(files []library.File) []addonkit.File {
	out := make([]addonkit.File, 0, len(files))
	for _, f := range files {
		out = append(out, fileDTO(f))
	}
	return out
}

func tracksDTO(tracks []library.Track) []addonkit.Track {
	out := make([]addonkit.Track, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, addonkit.Track{Item: itemDTO(t.Item), AlbumID: t.AlbumID, ArtistID: t.ArtistID, FileID: t.FileID,
			Artist: t.Artist, Album: t.Album, Disc: t.Disc, No: t.No, StartMs: t.StartMs, EndMs: t.EndMs})
	}
	return out
}

// writeItem answers a find: the item, or 404 when !found.
func writeItem(w http.ResponseWriter, r *http.Request, it library.Item, found bool, err error) {
	switch {
	case err != nil:
		libFail(w, r, err)
	case !found:
		writeErr(w, r, http.StatusNotFound, msgAddonNotFoundItem)
	default:
		writeJSON(w, http.StatusOK, itemDTO(it))
	}
}

// withAliases fills the items' Aliases (E21: [] when none).
func (a *addonsAPI) withAliases(ctx context.Context, items []library.Item, out []addonkit.Item) error {
	m, err := a.lib.Aliases(ctx, items...)
	if err != nil {
		return err
	}
	for i := range out {
		out[i].Aliases = m[out[i].ID]
	}
	return nil
}

// writeItemAliases is writeItem with the item's Aliases.
func (a *addonsAPI) writeItemAliases(w http.ResponseWriter, r *http.Request, it library.Item, err error) {
	if err != nil {
		writeItem(w, r, it, false, err)
		return
	}
	out := []addonkit.Item{itemDTO(it)}
	if err := a.withAliases(r.Context(), []library.Item{it}, out); err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out[0])
}

func (a *addonsAPI) coreItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		badRequest(w, r)
		return
	}
	it, err := a.lib.Item(r.Context(), id)
	a.writeItemAliases(w, r, it, err)
}

// coreItemFiles: the item's own files, then, for a series or a season,
// those of its episodes (by season, episode, path) with their numbers.
func (a *addonsAPI) coreItemFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		badRequest(w, r)
		return
	}
	ctx := r.Context()
	it, err := a.lib.Item(ctx, id)
	if err != nil {
		libFail(w, r, err)
		return
	}
	own, err := a.lib.FilesOf(ctx, id)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := filesDTO(own)
	var seasons []library.Item
	switch it.Kind {
	case library.KindSeries:
		if seasons, err = a.lib.Children(ctx, id); err != nil {
			libFail(w, r, err)
			return
		}
	case library.KindSeason:
		seasons = []library.Item{it}
	}
	var eps []library.Item
	for _, s := range seasons {
		children, err := a.lib.Children(ctx, s.ID)
		if err != nil {
			libFail(w, r, err)
			return
		}
		for _, e := range children {
			if e.SeasonNo == 0 {
				e.SeasonNo = s.SeasonNo
			}
			eps = append(eps, e)
		}
	}
	if len(eps) > 0 {
		ids := make([]int64, len(eps))
		for i, e := range eps {
			ids[i] = e.ID
		}
		byItem, err := a.lib.FilesOfItems(ctx, ids)
		if err != nil {
			libFail(w, r, err)
			return
		}
		var epFiles []addonkit.File
		for _, e := range eps {
			for _, f := range byItem[e.ID] {
				d := fileDTO(f)
				d.Season, d.Episode = e.SeasonNo, e.EpisodeNo
				epFiles = append(epFiles, d)
			}
		}
		slices.SortStableFunc(epFiles, func(x, y addonkit.File) int {
			return cmp.Or(cmp.Compare(x.Season, y.Season), cmp.Compare(x.Episode, y.Episode), strings.Compare(x.Path, y.Path))
		})
		out = append(out, epFiles...)
	}
	writeJSON(w, http.StatusOK, out)
}

// coreTMDB: the top-level movie or series with this TMDB id (stage 1
// answered {id, title}; the full Item keeps both names).
func (a *addonsAPI) coreTMDB(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	n, ok := positiveID(chi.URLParam(r, "tmdbId"))
	if !videoKind(kind) || !ok {
		badRequest(w, r)
		return
	}
	it, err := a.lib.ItemByTMDB(r.Context(), kind, n)
	a.writeItemAliases(w, r, it, err)
}

// coreTMDBIDs: ?ids=1,2 (at most 500) → {"<tmdbId>": itemId} of those
// the library has.
func (a *addonsAPI) coreTMDBIDs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	raw := r.URL.Query().Get("ids")
	if !videoKind(kind) || raw == "" {
		badRequest(w, r)
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) > addonMaxTMDBIDs {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyIDs)
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, ok := positiveID(strings.TrimSpace(p))
		if !ok {
			badRequest(w, r)
			return
		}
		ids = append(ids, n)
	}
	m, err := a.lib.ItemIDsByTMDB(r.Context(), kind, ids)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := make(map[string]int64, len(m))
	for tmdb, id := range m {
		out[strconv.FormatInt(tmdb, 10)] = id
	}
	writeJSON(w, http.StatusOK, out)
}

// coreFindTitle: ?kind&tmdbId&title&creator (Repo.FindTitle).
func (a *addonsAPI) coreFindTitle(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	q := r.URL.Query()
	kind, title, creator := q.Get("kind"), q.Get("title"), q.Get("creator")
	var tmdbID int64
	if s := q.Get("tmdbId"); s != "" {
		n, ok := positiveID(s)
		if !ok {
			badRequest(w, r)
			return
		}
		tmdbID = n
	}
	var valid bool
	switch kind {
	case library.KindMovie, library.KindSeries:
		valid = tmdbID > 0
	case library.KindMusic:
		valid = title != "" && creator != ""
	case library.KindBook, library.KindAudiobook:
		valid = title != ""
	}
	if !valid {
		badRequest(w, r)
		return
	}
	it, found, err := a.lib.FindTitle(r.Context(), kind, tmdbID, title, creator)
	writeItem(w, r, it, found, err)
}

// coreFindAlbum: ?artist&album, or ?mbid= (a MusicBrainz release group).
func (a *addonsAPI) coreFindAlbum(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	q := r.URL.Query()
	var (
		it    library.Item
		found bool
		err   error
	)
	switch mbid, artist, album := q.Get("mbid"), q.Get("artist"), q.Get("album"); {
	case mbid != "" && artist == "" && album == "":
		it, found, err = a.lib.FindAlbumByMBID(r.Context(), mbid)
	case mbid == "" && artist != "" && album != "":
		it, found, err = a.lib.FindAlbum(r.Context(), artist, album)
	default:
		badRequest(w, r)
		return
	}
	writeItem(w, r, it, found, err)
}

// coreFindBook: ?kind=book|audiobook&author&title.
func (a *addonsAPI) coreFindBook(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if (kind != library.KindBook && kind != library.KindAudiobook) || q.Get("title") == "" {
		badRequest(w, r)
		return
	}
	it, found, err := a.lib.FindBook(r.Context(), kind, q.Get("author"), q.Get("title"))
	writeItem(w, r, it, found, err)
}

// seriesParam: the {tmdbId} of a series the library has; else the answer
// is written (400, 404) and ok is false.
func (a *addonsAPI) seriesParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, ok := positiveID(chi.URLParam(r, "tmdbId"))
	if !ok {
		badRequest(w, r)
		return 0, false
	}
	if _, err := a.lib.ItemByTMDB(r.Context(), library.KindSeries, n); err != nil {
		libFail(w, r, err)
		return 0, false
	}
	return n, true
}

// coreSeriesEpisodes: the [season, episode] pairs with a file, in order.
func (a *addonsAPI) coreSeriesEpisodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	n, ok := a.seriesParam(w, r)
	if !ok {
		return
	}
	have, err := a.lib.HaveEpisodes(r.Context(), n)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := make([][2]int, 0, len(have))
	for k := range have {
		out = append(out, k)
	}
	slices.SortFunc(out, func(x, y [2]int) int { return cmp.Or(cmp.Compare(x[0], y[0]), cmp.Compare(x[1], y[1])) })
	writeJSON(w, http.StatusOK, out)
}

// coreSeriesEpisodeItems is GET /library/series/{tmdbId}/episode-items
// ?userId (library.read): the pairs of coreSeriesEpisodes with the library
// item of each, {"episodes": [{"season", "episode", "itemId"}]} in season
// and episode order, for an adult profile (userId: as GET /progress, a kid
// or an unknown one is 400). What the profile may not see (the one rule,
// progressAccess) is left out: a series over its rating limit answers an
// empty list, not an error, so no hidden title leaks its episodes.
func (a *addonsAPI) coreSeriesEpisodeItems(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	if a.users == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Профили недоступны")
		return
	}
	uid, _ := positiveID(r.URL.Query().Get("userId"))
	u, ok := a.progressUser(w, r, uid)
	if !ok {
		return
	}
	n, ok := a.seriesParam(w, r)
	if !ok {
		return
	}
	refs, err := a.lib.EpisodeItems(r.Context(), n)
	if err != nil {
		libFail(w, r, err)
		return
	}
	// an episode takes its series' rating: one check per series
	seen := map[int64]bool{}
	out := make([]addonkit.EpisodeItem, 0, len(refs))
	for _, ref := range refs {
		visible, checked := seen[ref.SeriesID]
		if !checked {
			access, err := a.progressAccess(r, u, ref.ItemID)
			if err != nil {
				libFail(w, r, err)
				return
			}
			visible = access == itemOpen
			seen[ref.SeriesID] = visible
		}
		if visible {
			out = append(out, addonkit.EpisodeItem{Season: ref.Season, Episode: ref.Episode, ItemID: ref.ItemID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": out})
}

// coreSeriesFolder: ?root= (exactly one of the library roots) → {path},
// the absolute folder directly under root that holds the series.
func (a *addonsAPI) coreSeriesFolder(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	root := r.URL.Query().Get("root")
	if root == "" || !slices.Contains(a.libraryRoots(), root) {
		writeErr(w, r, http.StatusBadRequest, msgAddonOutsideLibrary)
		return
	}
	n, ok := positiveID(chi.URLParam(r, "tmdbId"))
	if !ok {
		badRequest(w, r)
		return
	}
	name, err := a.lib.SeriesFolder(r.Context(), root, n)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": filepath.Join(root, name)})
}

// coreSeriesAudio: the episodes' audio titles; the profiles' choices
// (user ids, kids too) only for an addon that may also read the users.
func (a *addonsAPI) coreSeriesAudio(w http.ResponseWriter, r *http.Request) {
	in, ok := a.readLibrary(w, r)
	if !ok {
		return
	}
	n, ok := a.seriesParam(w, r)
	if !ok {
		return
	}
	sa, err := a.lib.SeriesAudio(r.Context(), n)
	if err != nil {
		libFail(w, r, err)
		return
	}
	if !slices.Contains(in.Manifest.Permissions, "users.read") {
		sa.Profiles = nil
	}
	writeJSON(w, http.StatusOK, sa)
}

// albumParam: the {id} of an album; its folder ("" when unknown).
func (a *addonsAPI) albumParam(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		badRequest(w, r)
		return 0, "", false
	}
	dir, err := a.lib.AlbumDir(r.Context(), id)
	if err != nil {
		libFail(w, r, err)
		return 0, "", false
	}
	return id, dir, true
}

func (a *addonsAPI) coreAlbumDir(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	_, dir, ok := a.albumParam(w, r)
	if !ok {
		return
	}
	if dir == "" {
		writeErr(w, r, http.StatusNotFound, msgAddonNotFoundItem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": dir})
}

func (a *addonsAPI) coreAlbumTracks(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	id, _, ok := a.albumParam(w, r)
	if !ok {
		return
	}
	tracks, err := a.lib.AlbumFolderTracks(r.Context(), id)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tracksDTO(tracks))
}

// coreFiles: ?under=<dir> (the files below it, without probe) or
// ?path=<file> (that file, with it). The path must be absolute, clean
// and inside a library root once symlinks are resolved (inLibrary); one
// that is not on disk answers [].
func (a *addonsAPI) coreFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	q := r.URL.Query()
	under, path := q.Get("under"), q.Get("path")
	if (under == "") == (path == "") {
		badRequest(w, r)
		return
	}
	p := cmp.Or(under, path)
	exists, ok := a.inLibrary(p)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonOutsideLibrary)
		return
	}
	out := []addonkit.File{}
	if !exists {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if under != "" {
		files, err := a.lib.FilesUnderNoProbe(r.Context(), under)
		if err != nil {
			libFail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, filesDTO(files))
		return
	}
	f, err := a.lib.FileByPath(r.Context(), path)
	switch {
	case errors.Is(err, library.ErrNotFound):
	case err != nil:
		libFail(w, r, err)
		return
	default:
		out = append(out, fileDTO(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// coreFileByID: one library file (not one missing from disk), with its
// probe, as the other calls give it.
func (a *addonsAPI) coreFileByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		badRequest(w, r)
		return
	}
	f, err := a.lib.FileByID(r.Context(), id)
	switch {
	case errors.Is(err, library.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, msgAddonNotFoundFile)
	case err != nil:
		libFail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, fileDTO(f))
	}
}

// coreStudios: the household's favourite studios («Любимые озвучки»),
// best first, as the Settings screen saves them (release preferences).
func (a *addonsAPI) coreStudios(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.callingAddon(w, r, "library.read"); !ok {
		return
	}
	if a.db == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNoSettings)
		return
	}
	studios, err := releaseprefs.Store{DB: a.db}.Studios(r.Context())
	if err != nil {
		libFail(w, r, err)
		return
	}
	if studios == nil {
		studios = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"studios": studios})
}

// corePaths: the path of every file the library has (missing ones too),
// in path order, written as it goes.
func (a *addonsAPI) corePaths(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	paths, err := a.lib.FilePaths(r.Context())
	if err != nil {
		libFail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 0, 64<<10)
	buf = append(buf, '[')
	for i, p := range paths {
		if i > 0 {
			buf = append(buf, ',')
		}
		b, _ := json.Marshal(p)
		buf = append(buf, b...)
		if len(buf) >= 60<<10 {
			if _, err := w.Write(buf); err != nil {
				return // the addon went away
			}
			if fl != nil {
				fl.Flush()
			}
			buf = buf[:0]
		}
	}
	buf = append(buf, ']', '\n')
	w.Write(buf)
}

// coreSearch: ?kind&title&limit (at most 50, the default) — items by
// normalised title.
func (a *addonsAPI) coreSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	q := r.URL.Query()
	kind, title := q.Get("kind"), q.Get("title")
	limit := addonMaxSearch
	if s := q.Get("limit"); s != "" {
		n, ok := positiveID(s)
		if !ok {
			badRequest(w, r)
			return
		}
		if n > addonMaxSearch {
			writeErr(w, r, http.StatusBadRequest, msgAddonTooManyResults)
			return
		}
		limit = int(n)
	}
	if kind == "" || title == "" {
		badRequest(w, r)
		return
	}
	items, err := a.lib.SearchTitles(r.Context(), kind, title, limit)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := itemsDTO(items)
	if err := a.withAliases(r.Context(), items, out); err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// refParam is the {ref} path parameter decoded once: chi matched it in
// the escaped path when the request had one (a ref may hold '/', '%').
func refParam(r *http.Request) (string, bool) {
	ref := chi.URLParam(r, "ref")
	if r.URL.RawPath != "" {
		var err error
		if ref, err = url.PathUnescape(ref); err != nil {
			return "", false
		}
	}
	return ref, addons.ValidRef(ref)
}

// coreRefFiles: the calling addon's files of a ref (never another's).
func (a *addonsAPI) coreRefFiles(w http.ResponseWriter, r *http.Request) {
	in, ok := a.readLibrary(w, r)
	if !ok {
		return
	}
	ref, ok := refParam(r)
	if !ok {
		badRequest(w, r)
		return
	}
	files, err := a.lib.FilesOfRef(r.Context(), in.ID, ref)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := make([]addonkit.RefFile, 0, len(files))
	for _, f := range files {
		out = append(out, addonkit.RefFile{FileID: f.ID, ItemID: f.ItemID, Path: f.Path})
	}
	writeJSON(w, http.StatusOK, out)
}

// coreRefTracks: ?ref= → the calling addon's tracks of the ref.
func (a *addonsAPI) coreRefTracks(w http.ResponseWriter, r *http.Request) {
	in, ok := a.readLibrary(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	if !addons.ValidRef(ref) {
		badRequest(w, r)
		return
	}
	tracks, err := a.lib.TracksOfRef(r.Context(), in.ID, ref)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tracksDTO(tracks))
}

// coreUsers: the profiles (users.read), each with the interface language
// the apps show it in (profileLanguages) and its rating limit, if any
// (ratingLimit, as in the X-Antresol-User header).
func (a *addonsAPI) coreUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.callingAddon(w, r, "users.read"); !ok {
		return
	}
	if a.users == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Профили недоступны")
		return
	}
	list, err := a.users.List(r.Context())
	if err != nil {
		libFail(w, r, err)
		return
	}
	langs, lists, err := a.profileLanguages(r.Context())
	if err != nil {
		libFail(w, r, err)
		return
	}
	householdList := a.householdLanguages(r.Context())
	household := cmp.Or(firstUILanguage(householdList), i18n.Default)
	out := make([]addonkit.User, 0, len(list))
	for _, u := range list {
		l := cmp.Or(langs[u.ID], household)
		ls := lists[u.ID]
		if len(ls) == 0 {
			ls = householdList
		}
		out = append(out, addonkit.User{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid, Lang: l,
			Languages: append([]string{}, ls...), MaxRating: ratingLimit(u)})
	}
	writeJSON(w, http.StatusOK, out)
}

// profileLanguages is, in one query, the interface language of each
// profile that has one of its own, as effectiveUILanguage: its choice,
// else the first of its own audio languages the apps speak (profiles
// without either are left out: householdLanguages), and each profile's
// own audio languages, in order (those with none are left out).
func (a *addonsAPI) profileLanguages(ctx context.Context) (map[int64]string, map[int64][]string, error) {
	out, lists := map[int64]string{}, map[int64][]string{}
	if a.db == nil {
		return out, lists, nil
	}
	rows, err := a.db.QueryContext(ctx, `SELECT u.id, COALESCE(ui.lang,''), COALESCE(pp.audio_langs,''), pp.user_id IS NOT NULL
	  FROM users u LEFT JOIN ui_prefs ui ON ui.user_id=u.id LEFT JOIN playback_prefs pp ON pp.user_id=u.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var chosen, audio string
		var custom bool
		if err := rows.Scan(&id, &chosen, &audio, &custom); err != nil {
			return nil, nil, err
		}
		for _, c := range strings.Split(audio, ",") {
			if c = strings.TrimSpace(c); c != "" {
				lists[id] = append(lists[id], c)
			}
		}
		switch {
		case chosen != "":
			out[id] = chosen
		case custom:
			// a profile with its own audio languages keeps them, even
			// when none is an interface language (then the server's)
			out[id] = cmp.Or(firstUILanguage(strings.Split(audio, ",")), i18n.Default)
		}
	}
	return out, lists, rows.Err()
}

// householdLanguages is the household's «Мои языки» (LanguagesOrDefault);
// its first one the apps speak is the language of profiles without their
// own (else the server's).
func (a *addonsAPI) householdLanguages(ctx context.Context) []string {
	if a.db != nil {
		if s, err := LoadAppSettings(ctx, a.db, a.settings); err == nil {
			return s.LanguagesOrDefault()
		}
	}
	return defaultLanguages
}

// libraryRoots are the library's folders that are set.
func (a *addonsAPI) libraryRoots() []string {
	lr := a.info.LibraryRoots
	var out []string
	for _, s := range []string{lr.Movies, lr.Series, lr.Music, lr.Ebooks, lr.Audiobooks, lr.Comics} {
		if s != "" {
			out = append(out, filepath.Clean(s))
		}
	}
	return out
}

// inLibrary checks a path an addon names: absolute, clean, inside a
// library root as written and, with every symlink resolved, inside that
// root resolved. A path not on disk is resolved through its deepest
// existing parent (exists is then false): what does not exist yet is
// inside the library when the folder it would be made in is. ok is false
// for anything else, including an entry that exists but cannot be
// resolved (a dangling symlink).
func (a *addonsAPI) inLibrary(p string) (exists, ok bool) {
	return a.newLibGuard().check(p)
}

// resolveDeepest is p with its symlinks resolved; when p is not on disk,
// its deepest existing parent's resolution joined with the rest.
func resolveDeepest(p string) (real string, exists, ok bool) {
	var rest []string
	for cur := p; ; {
		_, lerr := os.Lstat(cur)
		switch {
		case lerr == nil:
			r, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", false, false
			}
			slices.Reverse(rest)
			return filepath.Join(append([]string{r}, rest...)...), len(rest) == 0, true
		case errors.Is(lerr, fs.ErrNotExist) || errors.Is(lerr, syscall.ENOTDIR):
			parent := filepath.Dir(cur)
			if parent == cur {
				return "", false, false
			}
			rest = append(rest, filepath.Base(cur))
			cur = parent
		default:
			return "", false, false
		}
	}
}

// within: p is root or below it (both clean and absolute).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
