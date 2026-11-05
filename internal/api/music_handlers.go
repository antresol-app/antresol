package api

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/lyrics"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/go-chi/chi/v5"
)

func mountMusic(r chi.Router, h *lib) {
	r.Get("/api/music/home", h.musicHome)
	r.Get("/api/music/albums", h.albums)
	r.Get("/api/music/albums/{id}", h.albumDetail)
	r.Get("/api/music/albums/{id}/zip", h.albumZip)
	r.Get("/api/music/artists", h.artists)
	r.Get("/api/music/artists/{id}", h.artistDetail)
	r.Get("/api/music/songs", h.songs)
	r.Get("/api/music/shuffle", h.shuffle)
	r.Get("/api/music/genres", h.genres)
	r.Get("/api/music/tracks/{id}", h.trackDetail)
	r.Get("/api/music/tracks/{id}/lyrics", h.lyrics)
	r.With(noKids).Put("/api/music/tracks/{id}/lyrics/offset", h.lyricsOffset)
	r.Post("/api/music/plays", h.played)
	r.Get("/api/music/playlists", h.playlists)
	r.Post("/api/music/playlists", h.createPlaylist)
	r.Get("/api/music/playlists/{id}", h.playlist)
	r.Patch("/api/music/playlists/{id}", h.updatePlaylist)
	r.Delete("/api/music/playlists/{id}", h.deletePlaylist)
	r.Post("/api/music/star/{itemId}", h.star(true))
	r.Delete("/api/music/star/{itemId}", h.star(false))
}

// TrackDTO is a track in lists, albums and playlists.
type TrackDTO struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	ArtistID   int64  `json:"artistId"`
	AlbumID    int64  `json:"albumId"`
	Album      string `json:"album"`
	DiscNo     int    `json:"discNo"`
	TrackNo    int    `json:"trackNo"`
	DurationMs int64  `json:"durationMs"`
	FileID     int64  `json:"fileId"`
	Lossless   bool   `json:"lossless"`
	HasLyrics  bool   `json:"hasLyrics"`
	Cover      string `json:"cover"`
	Starred    bool   `json:"starred"`
	// Cue: the track is a slice of a CUE image (the file is the whole disc),
	// so «Скачать песню» would download the album image.
	Cue bool `json:"cue"`
	// «Выравнивание громкости» (ReplayGain): gains in dB on the ReplayGain
	// reference (-18 LUFS), peaks linear (1 = full scale); null: unknown
	// (no tag and not measured yet; the album: not every track known).
	GainTrackDb *float64 `json:"gainTrackDb"`
	GainAlbumDb *float64 `json:"gainAlbumDb"`
	PeakTrack   *float64 `json:"peakTrack"`
	PeakAlbum   *float64 `json:"peakAlbum"`
}

// cueSlice: a track cut from a bigger file (a start offset or an end).
func cueSlice(t library.Track) bool { return t.StartMs > 0 || t.EndMs != nil }

type ArtistRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type AlbumDetail struct {
	Card       Card       `json:"card"`
	Artist     ArtistRef  `json:"artist"`
	Year       int        `json:"year"`
	Genres     []string   `json:"genres"`
	DurationMs int64      `json:"durationMs"`
	TrackCount int        `json:"trackCount"`
	Quality    string     `json:"quality"`
	Tracks     []TrackDTO `json:"tracks"`
	Starred    bool       `json:"starred"`
}

type ArtistDetail struct {
	Card      Card       `json:"card"`
	Albums    []Card     `json:"albums"`
	TopTracks []TrackDTO `json:"topTracks"`
	AppearsOn []Card     `json:"appearsOn"`
}

type PlaylistDTO struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	TrackCount int       `json:"trackCount"`
	DurationMs int64     `json:"durationMs"`
	Cover      string    `json:"cover"`
	Covers     []string  `json:"covers"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// topTracksN is how many tracks ArtistDetail lists.
const topTracksN = 10

func playlistDTO(p music.Playlist) PlaylistDTO {
	d := PlaylistDTO{ID: p.ID, Name: p.Name, TrackCount: p.TrackCount, DurationMs: p.DurationMs, Covers: p.Covers, UpdatedAt: p.UpdatedAt}
	if d.Covers == nil {
		d.Covers = []string{}
	}
	if len(d.Covers) > 0 {
		d.Cover = d.Covers[0]
	}
	return d
}

func playlistCard(ctx context.Context, p music.Playlist) Card {
	d := playlistDTO(p)
	return Card{ID: p.ID, Kind: "playlist", Title: p.Name, Subtitle: i18n.Plural(ctx, p.TrackCount, "песня", "песни", "песен"), Poster: d.Cover, OnServer: true}
}

func albumCard(ctx context.Context, a library.Album) Card {
	return Card{ID: a.ID, Kind: library.KindAlbum, Title: scanLabel(ctx, a.Title), Subtitle: scanLabel(ctx, a.Artist), Year: a.Year, OnServer: true,
		Poster: imageURL(a.ID, "poster", a.Poster), Backdrop: imageURL(a.ID, "backdrop", a.Backdrop)}
}

func albumCards(ctx context.Context, as []library.Album) []Card {
	out := make([]Card, 0, len(as))
	for _, a := range as {
		out = append(out, albumCard(ctx, a))
	}
	return out
}

func artistCard(ctx context.Context, a library.ArtistRow) Card {
	c := Card{ID: a.ID, Kind: library.KindArtist, Title: scanLabel(ctx, a.Title), OnServer: true, Poster: imageURL(a.ID, "poster", a.Poster)}
	if c.Poster == "" {
		c.Poster = a.Cover
	}
	return c
}

// trackView renders tracks for one user: only tracks whose album is
// visible to the profile, with covers and stars.
type trackView struct {
	h       *lib
	u       auth.User
	starred map[int64]bool
	albums  map[int64]*library.Item // nil: hidden
}

func (h *lib) trackView(ctx context.Context, u auth.User) (*trackView, error) {
	v := &trackView{h: h, u: u, starred: map[int64]bool{}, albums: map[int64]*library.Item{}}
	if h.Music != nil {
		ids, err := h.Music.Starred(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			v.starred[id] = true
		}
	}
	return v, nil
}

func (v *trackView) album(ctx context.Context, id int64) (*library.Item, error) {
	if a, ok := v.albums[id]; ok {
		return a, nil
	}
	it, err := v.h.Library.Item(ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		v.albums[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ok, err := v.h.allowed(ctx, v.u, it)
	if err != nil {
		return nil, err
	}
	if !ok {
		v.albums[id] = nil
		return nil, nil
	}
	v.albums[id] = &it
	return &it, nil
}

// prefetch loads the albums of tracks album doesn't know yet in one query
// (a page of songs is mostly of different albums); a failure leaves them
// to album, one by one.
func (v *trackView) prefetch(ctx context.Context, tracks []library.Track) {
	var need []int64
	seen := map[int64]bool{}
	for _, t := range tracks {
		if _, ok := v.albums[t.AlbumID]; !ok && !seen[t.AlbumID] {
			seen[t.AlbumID] = true
			need = append(need, t.AlbumID)
		}
	}
	if len(need) < 2 {
		return
	}
	got, err := v.h.Library.ItemsByID(ctx, need)
	if err != nil {
		return
	}
	for _, id := range need {
		it, ok := got[id]
		if !ok {
			v.albums[id] = nil
			continue
		}
		if allowed, err := v.h.allowed(ctx, v.u, it); err == nil {
			if allowed {
				v.albums[id] = &it
			} else {
				v.albums[id] = nil
			}
		}
	}
}

func (v *trackView) dtos(ctx context.Context, tracks []library.Track) ([]TrackDTO, error) {
	out := make([]TrackDTO, 0, len(tracks))
	ids := make([]int64, len(tracks))
	for i, t := range tracks {
		ids[i] = t.ID
	}
	// words kept from LRCLIB count; its «none» hides the button (unless the
	// file has words)
	kept, err := v.h.Library.TrackLyricsStatus(ctx, ids)
	if err != nil {
		return nil, err
	}
	v.prefetch(ctx, tracks)
	for _, t := range tracks {
		al, err := v.album(ctx, t.AlbumID)
		if err != nil {
			return nil, err
		}
		if al == nil {
			continue
		}
		out = append(out, TrackDTO{ID: t.ID, Title: scanLabel(ctx, t.Title), Artist: scanLabel(ctx, t.Artist), ArtistID: t.ArtistID, AlbumID: t.AlbumID, Album: scanLabel(ctx, t.Album),
			DiscNo: t.Disc, TrackNo: t.No, DurationMs: t.DurationMs, FileID: t.FileID, Lossless: t.Lossless,
			HasLyrics: strings.TrimSpace(t.Lyrics) != "" || kept[t.ID] == library.LyricsFound || (v.h.Lyrics != nil && kept[t.ID] != library.LyricsNone), Cover: imageURL(al.ID, "poster", al.Poster), Starred: v.starred[t.ID], Cue: cueSlice(t),
			GainTrackDb: t.Gain.TrackGain, GainAlbumDb: t.Gain.AlbumGain, PeakTrack: t.Gain.TrackPeak, PeakAlbum: t.Gain.AlbumPeak})
	}
	return out, nil
}

func (h *lib) trackDTOs(ctx context.Context, u auth.User, tracks []library.Track) ([]TrackDTO, error) {
	v, err := h.trackView(ctx, u)
	if err != nil {
		return nil, err
	}
	return v.dtos(ctx, tracks)
}

// visibleAlbums keeps the albums the user may see (in order).
func (h *lib) visibleAlbums(ctx context.Context, r *http.Request, ids []int64) ([]Card, error) {
	out := []Card{}
	for _, id := range ids {
		a, err := h.Library.AlbumByID(ctx, id)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ok, err := h.allowed(ctx, userOf(r), a.Item)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, albumCard(ctx, a))
		}
	}
	return out, nil
}

func pageParams(r *http.Request, def int) (limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = def
	}
	offset, _ = strconv.Atoi(q.Get("offset"))
	return limit, max(offset, 0)
}

func (h *lib) musicHome(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	var shelves []shelf
	add := func(id, title string, cards []Card) {
		if len(cards) > 0 {
			shelves = append(shelves, shelf{id, title, cards})
		}
	}
	// ?scope=mine: the profile's own albums and artists (plays and
	// playlists are the profile's already)
	owner := scopeOwner(r, u)
	recent, _, err := h.Library.Albums(ctx, library.AlbumQuery{Sort: "added", Limit: shelfSize, MaxRating: ratingLimit(u), Owner: owner})
	if err != nil {
		internalErr(w, r, "music home albums", err)
		return
	}
	add("recent-albums", i18n.T(ctx, "Недавно добавленные"), albumCards(ctx, recent))
	played, err := h.recentlyPlayed(ctx, r)
	if err != nil {
		internalErr(w, r, "music home played", err)
		return
	}
	add("recently-played", i18n.T(ctx, "Слушали недавно"), played)
	artists, _, err := h.Library.ArtistsOf(ctx, shelfSize, 0, owner)
	if err != nil {
		internalErr(w, r, "music home artists", err)
		return
	}
	var ac []Card
	for _, a := range artists {
		if ok, err := h.allowed(ctx, u, a.Item); err != nil {
			internalErr(w, r, "music home artists", err)
			return
		} else if ok {
			ac = append(ac, artistCard(ctx, a))
		}
	}
	add("artists", i18n.T(ctx, "Артисты"), ac)
	pls, err := h.Music.Playlists(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "music home playlists", err)
		return
	}
	var pc []Card
	for _, p := range pls {
		pc = append(pc, playlistCard(ctx, p))
	}
	add("playlists", i18n.T(ctx, "Плейлисты"), pc)
	if shelves == nil {
		shelves = []shelf{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelves": shelves})
}

// recentlyPlayed is the «Слушали недавно» shelf (albums by last play).
func (h *lib) recentlyPlayed(ctx context.Context, r *http.Request) ([]Card, error) {
	if h.Music == nil {
		return []Card{}, nil
	}
	ids, err := h.Music.RecentAlbums(ctx, userOf(r).ID, shelfSize)
	if err != nil {
		return nil, err
	}
	return h.visibleAlbums(ctx, r, ids)
}

var albumSorts = map[string]bool{"": true, "added": true, "title": true, "year": true, "artist": true, "random": true, "frequent": true, "recent": true}

func (h *lib) albums(w http.ResponseWriter, r *http.Request) {
	q, u := r.URL.Query(), userOf(r)
	sort := q.Get("sort")
	if !albumSorts[sort] {
		writeErr(w, r, http.StatusBadRequest, "Неизвестная сортировка")
		return
	}
	limit, offset := pageParams(r, 100)
	aq := library.AlbumQuery{Sort: sort, UserID: u.ID, Limit: limit, Offset: offset, MaxRating: ratingLimit(u),
		Starred: q.Get("starred") == "1", Genre: q.Get("genre"), Owner: scopeOwner(r, u)}
	if s := q.Get("artist"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, r, http.StatusBadRequest, "Неверный id")
			return
		}
		aq.ArtistID = id
	}
	as, total, err := h.Library.Albums(r.Context(), aq)
	if err != nil {
		internalErr(w, r, "albums", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": albumCards(r.Context(), as), "total": total})
}

// kindParam loads the {param} item and requires the kind (404 otherwise).
func (h *lib) kindParam(w http.ResponseWriter, r *http.Request, param string, kinds ...string) (library.Item, bool) {
	it, ok := h.itemParam(w, r, param)
	if !ok {
		return it, false
	}
	for _, k := range kinds {
		if it.Kind == k {
			return it, true
		}
	}
	writeErr(w, r, http.StatusNotFound, "Не найдено")
	return it, false
}

func (h *lib) albumDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindAlbum)
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	a, err := h.Library.AlbumByID(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album", err)
		return
	}
	tracks, err := h.Library.AlbumTracks(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album tracks", err)
		return
	}
	dtos, err := h.trackDTOs(ctx, u, tracks)
	if err != nil {
		internalErr(w, r, "album tracks", err)
		return
	}
	starred, err := h.Music.IsStarred(ctx, u.ID, it.ID)
	if err != nil {
		internalErr(w, r, "album star", err)
		return
	}
	d := AlbumDetail{Card: albumCard(ctx, a), Artist: ArtistRef{a.ArtistID, scanLabel(ctx, a.Artist)}, Year: a.Year, Genres: metaOf(it).Genres,
		DurationMs: a.DurationMs, TrackCount: len(dtos), Tracks: dtos, Starred: starred}
	if d.Genres == nil {
		d.Genres = []string{}
	}
	if len(tracks) > 0 {
		f, err := h.Library.FileByID(ctx, tracks[0].FileID)
		if err != nil && !errors.Is(err, library.ErrNotFound) {
			internalErr(w, r, "album quality", err)
			return
		}
		d.Quality = qualityLabel(i18n.FromContext(ctx), f.ProbeJSON, tracks[0].Codec, tracks[0].Lossless)
	}
	writeJSON(w, http.StatusOK, d)
}

// codecNames are the display names of the scanner/ffprobe codec names.
var codecNames = map[string]string{"flac": "FLAC", "alac": "ALAC", "wv": "WavPack", "wavpack": "WavPack", "ape": "APE",
	"wav": "WAV", "aiff": "AIFF", "mp3": "MP3", "aac": "AAC", "opus": "Opus", "vorbis": "Vorbis"}

// qualityLabel describes an album's audio by its first track: «Lossless ·
// FLAC», with bit depth and rate when above CD quality («Lossless · FLAC
// 24 бит / 96 кГц»), or a lossy codec with its bit rate rounded to 32
// («MP3 320 кбит/с»).
func qualityLabel(lang, probeJSON, codec string, lossless bool) string {
	p, ok := cachedProbe(library.File{ProbeJSON: probeJSON})
	var sr, depth int
	var br int64
	if ok && len(p.Audio) > 0 {
		a := p.Audio[0]
		if c := strings.ToLower(a.Codec); c != "" && !strings.HasPrefix(c, "pcm_") {
			codec = c
		}
		sr, depth, br = a.SampleRate, a.BitDepth, a.BitRate
	}
	name := codecNames[strings.ToLower(codec)]
	if name == "" {
		name = strings.ToUpper(codec)
	}
	if lossless {
		s := "Lossless · " + name
		if depth > 16 || sr > 48000 {
			var parts []string
			if depth > 0 {
				parts = append(parts, i18n.TL(lang, "%d бит", depth))
			}
			if sr > 0 {
				parts = append(parts, i18n.TL(lang, "%s кГц", decimal(lang, strconv.FormatFloat(float64(sr)/1000, 'f', -1, 64))))
			}
			s += " " + strings.Join(parts, " / ")
		}
		return strings.TrimSpace(s)
	}
	if kbps := int64(float64(br)/1000/32+0.5) * 32; kbps > 0 {
		return name + " " + i18n.TL(lang, "%d кбит/с", kbps)
	}
	return name
}

// cdFolder is a disc subfolder whose sidecar files the album ZIP takes.
var cdFolder = regexp.MustCompile(`(?i)^cd\s*\d+$`)

// zipSidecar is what the album ZIP takes from the album folder besides
// the audio.
var zipSidecar = map[string]bool{".cue": true, ".log": true, ".jpg": true, ".jpeg": true, ".png": true}

// sidecars lists the .cue/.log/cover files of an album folder and of its
// CD<n> subfolders (regular files only).
func sidecars(dir string) []string {
	var out []string
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range es {
		name := e.Name()
		switch {
		case e.IsDir() && cdFolder.MatchString(name):
			sub, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for _, se := range sub {
				if se.Type().IsRegular() && zipSidecar[strings.ToLower(filepath.Ext(se.Name()))] {
					out = append(out, filepath.Join(dir, name, se.Name()))
				}
			}
		case e.Type().IsRegular() && zipSidecar[strings.ToLower(filepath.Ext(name))]:
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

func safeZipName(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "\x00", "").Replace(strings.TrimSpace(s))
}

// albumZip streams the album as an uncompressed ZIP: the files of its
// tracks (and its CUE image) plus the .cue/.log/cover files of the album
// folder and its CD<n> subfolders.
func (h *lib) albumZip(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindAlbum)
	if !ok {
		return
	}
	ctx := r.Context()
	a, err := h.Library.AlbumByID(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album zip", err)
		return
	}
	dir, err := h.Library.AlbumDir(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album zip", err)
		return
	}
	tracks, err := h.Library.AlbumTracks(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album zip", err)
		return
	}
	var paths []string
	seen := map[string]bool{}
	side := map[string]bool{} // sidecar paths (opened through the folder root)
	addPath := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	seenFile := map[int64]bool{}
	for _, t := range tracks {
		if seenFile[t.FileID] {
			continue
		}
		seenFile[t.FileID] = true
		f, err := h.Library.FileByID(ctx, t.FileID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			internalErr(w, r, "album zip", err)
			return
		}
		addPath(f.Path)
	}
	own, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "album zip", err)
		return
	}
	for _, f := range own {
		addPath(f.Path)
	}
	if dir != "" {
		for _, p := range sidecars(dir) {
			side[p] = true
			addPath(p)
		}
	}
	if len(paths) == 0 {
		writeErr(w, r, http.StatusNotFound, "Файлы альбома не найдены")
		return
	}
	name := safeZipName(a.Artist) + " - " + safeZipName(a.Title) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	var root *os.Root // sidecars are opened inside the album folder only
	if dir != "" {
		if root, err = os.OpenRoot(dir); err == nil {
			defer root.Close()
		}
	}
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	for _, p := range paths {
		if err := addZipFile(zw, dir, p, side[p], root, used); err != nil {
			if ctx.Err() == nil {
				log.Printf("api: album %d zip %s: %v", it.ID, p, err)
			}
			if errors.Is(err, errZipWrite) {
				return // the response is broken: no point going on
			}
		}
	}
	if err := zw.Close(); err != nil && ctx.Err() == nil {
		log.Printf("api: album %d zip: %v", it.ID, err)
	}
}

var errZipWrite = errors.New("zip write failed")

// addZipFile stores p in the ZIP under its path relative to dir (its base
// name when outside it; a name already used gets « (2)», « (3)»…). A
// sidecar is opened through root (no symlink out of the album folder). A
// file that cannot be opened is skipped.
func addZipFile(zw *zip.Writer, dir, p string, sidecar bool, root *os.Root, used map[string]bool) error {
	name := filepath.Base(p)
	if dir != "" {
		if rel, err := filepath.Rel(dir, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			name = filepath.ToSlash(rel)
		}
	}
	var f *os.File
	var err error
	switch {
	case sidecar && root == nil:
		return errors.New("album folder not open")
	case sidecar:
		f, err = root.Open(filepath.FromSlash(name))
	default:
		f, err = os.Open(p)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if used[name] {
		ext := path.Ext(name)
		for n := 2; ; n++ {
			if c := strings.TrimSuffix(name, ext) + " (" + strconv.Itoa(n) + ")" + ext; !used[c] {
				name = c
				break
			}
		}
	}
	used[name] = true
	hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: st.ModTime()}
	hdr.SetMode(0o644)
	zf, err := zw.CreateHeader(hdr)
	if err != nil {
		return errors.Join(errZipWrite, err)
	}
	if _, err := io.Copy(zf, f); err != nil {
		return errors.Join(errZipWrite, err)
	}
	return nil
}

func (h *lib) artists(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 100)
	rows, total, err := h.Library.ArtistsOf(r.Context(), limit, offset, scopeOwner(r, userOf(r)))
	if err != nil {
		internalErr(w, r, "artists", err)
		return
	}
	cards := make([]Card, 0, len(rows))
	for _, a := range rows {
		if ok, err := h.allowed(r.Context(), userOf(r), a.Item); err != nil {
			internalErr(w, r, "artists", err)
			return
		} else if ok {
			cards = append(cards, artistCard(r.Context(), a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cards, "total": total})
}

func (h *lib) artistDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindArtist)
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	row, err := h.Library.ArtistInfo(ctx, it)
	if err != nil {
		internalErr(w, r, "artist", err)
		return
	}
	albums, _, err := h.Library.Albums(ctx, library.AlbumQuery{ArtistID: it.ID, Sort: "year", MaxRating: ratingLimit(u)})
	if err != nil {
		internalErr(w, r, "artist albums", err)
		return
	}
	topIDs, err := h.Music.TopTracks(ctx, it.ID, topTracksN)
	if err != nil {
		internalErr(w, r, "artist top", err)
		return
	}
	tracks, err := h.Library.Tracks(ctx, topIDs)
	if err != nil {
		internalErr(w, r, "artist top", err)
		return
	}
	top, err := h.trackDTOs(ctx, u, tracks)
	if err != nil {
		internalErr(w, r, "artist top", err)
		return
	}
	appears, err := h.Library.AppearsOn(ctx, it.Title)
	if err != nil {
		internalErr(w, r, "artist appears", err)
		return
	}
	var visible []library.Album
	for _, a := range appears {
		if ok, err := h.allowed(ctx, u, a.Item); err != nil {
			internalErr(w, r, "artist appears", err)
			return
		} else if ok {
			visible = append(visible, a)
		}
	}
	writeJSON(w, http.StatusOK, ArtistDetail{Card: artistCard(ctx, row), Albums: albumCards(ctx, albums), TopTracks: top, AppearsOn: albumCards(ctx, visible)})
}

func (h *lib) songs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("starred") == "1" {
		h.starredSongs(w, r)
		return
	}
	limit, offset := pageParams(r, 100)
	tracks, total, err := h.Library.Songs(r.Context(), limit, offset)
	if err != nil {
		internalErr(w, r, "songs", err)
		return
	}
	dtos, err := h.trackDTOs(r.Context(), userOf(r), tracks)
	if err != nil {
		internalErr(w, r, "songs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": dtos, "total": total})
}

func (h *lib) lyrics(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindTrack)
	if !ok {
		return
	}
	tr, err := h.Library.TrackByID(r.Context(), it.ID)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	if err != nil {
		internalErr(w, r, "lyrics", err)
		return
	}
	// stored words first (the file's, then those fetched before): the
	// service is asked only when nothing synced is kept and its last
	// answer is due again, and what it says is kept
	stored, has, err := h.Library.TrackLyrics(r.Context(), tr.ID)
	if err != nil {
		internalErr(w, r, "lyrics", err)
		return
	}
	text, source := library.EffectiveLyrics(tr, stored, has)
	if !lyrics.Synced.MatchString(text) && h.Lyrics != nil && !(has && stored.Fresh(time.Now())) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		res, err := h.Lyrics.Find(ctx, lyrics.QueryOf(tr))
		cancel()
		if err != nil {
			log.Printf("lyrics: lrclib: %v", err)
			if text == "" && r.Context().Err() == nil {
				// not «Текста нет»: LRCLIB did not answer, the words may well be there
				writeErr(w, r, http.StatusServiceUnavailable, "Сервис текстов песен не отвечает. Попробуйте позже")
				return
			}
		} else {
			if !(res == nil && has && stored.Status == library.LyricsFound) {
				stored, has = lyrics.Stored(res, time.Now()), true
				if err := h.Library.SaveTrackLyrics(r.Context(), tr.ID, stored); err != nil {
					log.Printf("lyrics: track %d: save: %v", tr.ID, err)
				}
			}
			text, source = library.EffectiveLyrics(tr, stored, has)
		}
	}
	if text == "" {
		writeErr(w, r, http.StatusNotFound, "Текста нет")
		return
	}
	synced := lyrics.Synced.MatchString(text)
	// offsetMs: the track's correction, for the client to add to the line
	// times of lyrics (and lines, which do not include it); 0 when unset
	out := map[string]any{"lyrics": text, "synced": synced, "source": source, "offsetMs": stored.OffsetMs}
	if synced {
		out["lines"] = lyrics.Parse(text)
	}
	writeJSON(w, http.StatusOK, out)
}

// lyricsOffset sets the track's lyrics offset {"offsetMs": n} (added to
// the line times, positive: later; clamped to ±10 s) for everyone: it
// corrects the words, not a preference. Not for kid profiles. Answers the
// stored {"offsetMs"}.
func (h *lib) lyricsOffset(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindTrack)
	if !ok {
		return
	}
	var in struct {
		OffsetMs *float64 `json:"offsetMs"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.OffsetMs == nil || math.IsNaN(*in.OffsetMs) {
		writeErr(w, r, http.StatusBadRequest, "Не удалось разобрать запрос")
		return
	}
	v := math.Round(max(-library.MaxLyricsOffsetMs, min(library.MaxLyricsOffsetMs, *in.OffsetMs)))
	ms, err := h.Library.SetLyricsOffset(r.Context(), it.ID, int(v))
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	if err != nil {
		internalErr(w, r, "lyrics offset", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"offsetMs": ms})
}

// playsMaxAge: older timed plays (a long-forgotten offline queue) are
// dropped, like the progress queue's.
const playsMaxAge = 30 * 24 * time.Hour

func (h *lib) played(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TrackID int64 `json:"trackId"`
		// At is when the client played it (RFC 3339); "" = now.
		At string `json:"at"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.TrackID <= 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return
	}
	var at time.Time
	if in.At != "" {
		t, err := time.Parse(time.RFC3339, in.At)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, "Неверное время записи")
			return
		}
		at = t
	}
	it, ok := h.item(w, r, in.TrackID)
	if !ok {
		return
	}
	if it.Kind != library.KindTrack {
		writeErr(w, r, http.StatusBadRequest, "Это не песня")
		return
	}
	var err error
	if at.IsZero() {
		err = h.Music.Played(r.Context(), userOf(r).ID, it.ID, time.Time{})
	} else {
		// a future at is stored as now (PlayedAt)
		if h.Music.Now().Sub(at) > playsMaxAge {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		err = h.Music.PlayedAt(r.Context(), userOf(r).ID, it.ID, at)
	}
	if err != nil {
		internalErr(w, r, "played", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *lib) playlists(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Music.Playlists(r.Context(), userOf(r).ID)
	if err != nil {
		internalErr(w, r, "playlists", err)
		return
	}
	out := make([]PlaylistDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, playlistDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// checkTracks writes 400 unless every id is a track visible to the user.
func (h *lib) checkTracks(w http.ResponseWriter, r *http.Request, ids []int64) bool {
	ctx := r.Context()
	v, err := h.trackView(ctx, userOf(r))
	if err != nil {
		internalErr(w, r, "playlist tracks", err)
		return false
	}
	tracks, err := h.Library.Tracks(ctx, ids)
	if err != nil {
		internalErr(w, r, "playlist tracks", err)
		return false
	}
	if len(tracks) != len(ids) { // unknown, missing or not a track
		writeErr(w, r, http.StatusBadRequest, "Песня не найдена")
		return false
	}
	for _, tr := range tracks {
		al, err := v.album(ctx, tr.AlbumID)
		if err != nil {
			internalErr(w, r, "playlist tracks", err)
			return false
		}
		if al == nil {
			writeErr(w, r, http.StatusBadRequest, "Песня не найдена")
			return false
		}
	}
	return true
}

// PlaylistTrack is a playlist entry: Idx is its stored position, the one
// PATCH remove/order take (entries the profile cannot see are left out of
// the list but keep their positions).
type PlaylistTrack struct {
	TrackDTO
	Idx int `json:"idx"`
}

// playlistEntries renders the stored items visible to u, with their
// positions, and reports which positions are visible.
func (h *lib) playlistEntries(ctx context.Context, u auth.User, ids []int64) ([]PlaylistTrack, map[int]bool, error) {
	v, err := h.trackView(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	tracks, err := h.Library.Tracks(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	by := map[int64]library.Track{}
	for _, t := range tracks {
		by[t.ID] = t
	}
	out := []PlaylistTrack{}
	visible := map[int]bool{}
	for i, id := range ids {
		t, ok := by[id]
		if !ok {
			continue
		}
		d, err := v.dtos(ctx, []library.Track{t})
		if err != nil {
			return nil, nil, err
		}
		if len(d) == 1 {
			out = append(out, PlaylistTrack{TrackDTO: d[0], Idx: i})
			visible[i] = true
		}
	}
	return out, visible, nil
}

// errPlaylistPos: a remove/order position that is not a visible entry.
var errPlaylistPos = errors.New("bad playlist position")

// editPositions applies remove and order (stored positions of visible
// entries) and append to cur. Order lists the visible entries in their new
// order, filling the visible slots; visible entries it leaves out are
// dropped. Hidden entries always keep their slots.
func editPositions(cur []int64, visible map[int]bool, remove, order []int, appendIDs []int64) ([]int64, error) {
	valid := func(ps []int) error {
		seen := map[int]bool{}
		for _, p := range ps {
			if p < 0 || p >= len(cur) || !visible[p] || seen[p] {
				return errPlaylistPos
			}
			seen[p] = true
		}
		return nil
	}
	if err := valid(remove); err != nil {
		return nil, err
	}
	if err := valid(order); err != nil {
		return nil, err
	}
	pos := make([]int, 0, len(cur))
	queue := order
	for i := range cur {
		switch {
		case order == nil || !visible[i]:
			pos = append(pos, i)
		case len(queue) > 0:
			pos = append(pos, queue[0])
			queue = queue[1:]
		}
	}
	drop := map[int]bool{}
	for _, p := range remove {
		drop[p] = true
	}
	next := make([]int64, 0, len(pos)+len(appendIDs))
	for _, p := range pos {
		if !drop[p] {
			next = append(next, cur[p])
		}
	}
	return append(next, appendIDs...), nil
}

// maxPlaylist bounds a playlist (and one request's ids).
const maxPlaylist = 5000

func (h *lib) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		TrackIDs []int64 `json:"trackIds"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeErr(w, r, http.StatusBadRequest, "Введите название плейлиста")
		return
	}
	if len(in.TrackIDs) > maxPlaylist {
		writeErr(w, r, http.StatusBadRequest, "Слишком много песен")
		return
	}
	if !h.checkTracks(w, r, in.TrackIDs) {
		return
	}
	p, err := h.Music.CreatePlaylist(r.Context(), userOf(r).ID, name, in.TrackIDs)
	if err != nil {
		internalErr(w, r, "create playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDTO(p))
}

func playlistErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, music.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Плейлист не найден")
		return
	}
	internalErr(w, r, what, err)
}

func (h *lib) playlist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	p, ids, err := h.Music.Playlist(ctx, u.ID, id)
	if err != nil {
		playlistErr(w, r, "playlist", err)
		return
	}
	entries, _, err := h.playlistEntries(ctx, u, ids)
	if err != nil {
		internalErr(w, r, "playlist tracks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlist": playlistDTO(p), "tracks": entries})
}

func (h *lib) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Name   *string `json:"name"`
		Append []int64 `json:"append"`
		Remove []int   `json:"remove"` // stored positions (PlaylistTrack.Idx)
		Order  []int   `json:"order"`  // stored positions in the new order
	}
	if !decode(w, r, &in) {
		return
	}
	ctx, u := r.Context(), userOf(r)
	if _, _, err := h.Music.Playlist(ctx, u.ID, id); err != nil { // someone else's: 404 before anything else
		playlistErr(w, r, "update playlist", err)
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			writeErr(w, r, http.StatusBadRequest, "Введите название плейлиста")
			return
		}
		in.Name = &n
	}
	if len(in.Append) > maxPlaylist || len(in.Order) > maxPlaylist || len(in.Remove) > maxPlaylist {
		writeErr(w, r, http.StatusBadRequest, "Слишком много песен")
		return
	}
	if !h.checkTracks(w, r, in.Append) {
		return
	}
	var edit func([]int64) ([]int64, error)
	if len(in.Remove) > 0 || len(in.Append) > 0 || in.Order != nil {
		edit = func(cur []int64) ([]int64, error) {
			_, visible, err := h.playlistEntries(ctx, u, cur)
			if err != nil {
				return nil, err
			}
			next, err := editPositions(cur, visible, in.Remove, in.Order, in.Append)
			if err == nil && len(next) > maxPlaylist {
				err = errTooManySongs
			}
			return next, err
		}
	}
	err := h.Music.EditPlaylist(ctx, u.ID, id, in.Name, edit)
	switch {
	case errors.Is(err, errPlaylistPos):
		writeErr(w, r, http.StatusBadRequest, "Неверная позиция в плейлисте")
		return
	case errors.Is(err, errTooManySongs):
		writeErr(w, r, http.StatusBadRequest, "Слишком много песен")
		return
	case err != nil:
		playlistErr(w, r, "update playlist", err)
		return
	}
	p, _, err := h.Music.Playlist(ctx, u.ID, id)
	if err != nil {
		playlistErr(w, r, "update playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDTO(p))
}

var errTooManySongs = errors.New("playlist too long")

func (h *lib) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Music.DeletePlaylist(r.Context(), userOf(r).ID, id); err != nil {
		playlistErr(w, r, "delete playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *lib) star(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, ok := h.itemParam(w, r, "itemId")
		if !ok {
			return
		}
		switch it.Kind {
		case library.KindTrack, library.KindAlbum, library.KindArtist:
		default:
			writeErr(w, r, http.StatusBadRequest, "В избранное добавляются только песни, альбомы и артисты")
			return
		}
		var err error
		if on {
			err = h.Music.Star(r.Context(), userOf(r).ID, it.ID)
		} else {
			err = h.Music.Unstar(r.Context(), userOf(r).ID, it.ID)
		}
		if err != nil {
			internalErr(w, r, "star", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
