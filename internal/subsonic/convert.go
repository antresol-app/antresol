package subsonic

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// musicFolderID is the only music folder («Музыка»).
const musicFolderID = 1

// limit is the profile's maximum rating ("" = none).
func (c *call) limit() string { return c.u.RatingLimit() }

// allowed is the web's visibility rule (library.Repo.Visible).
func (c *call) allowed(it library.Item) (bool, error) {
	return c.s.Library.Visible(c.ctx, c.limit(), it)
}

// album returns the album item if it exists and is visible (nil otherwise),
// cached per call.
func (c *call) album(id int64) (*library.Item, error) {
	if a, ok := c.albums[id]; ok {
		return a, nil
	}
	it, err := c.s.Library.Item(c.ctx, id)
	if errors.Is(err, library.ErrNotFound) || (err == nil && it.Kind != library.KindAlbum) {
		c.albums[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ok, err := c.allowed(it)
	if err != nil {
		return nil, err
	}
	if !ok {
		c.albums[id] = nil
		return nil, nil
	}
	c.albums[id] = &it
	return &it, nil
}

// starredAt caches the profile's stars for the call.
func (c *call) starredAt() (map[int64]time.Time, error) {
	if c.stars != nil {
		return c.stars, nil
	}
	m, err := c.s.Music.StarredAt(c.ctx, c.u.ID)
	if err != nil {
		return nil, err
	}
	c.stars = m
	return m, nil
}

// starredStr is when id was starred ("" = not); an error is kept for the
// response (see call.fail).
func (c *call) starredStr(id int64) string {
	m, err := c.starredAt()
	if err != nil {
		c.keep(err)
		return ""
	}
	if t, ok := m[id]; ok {
		return stamp(t)
	}
	return ""
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func sid(id int64) string { return strconv.FormatInt(id, 10) }

func albumCoverID(id int64) string { return "al-" + sid(id) }

// albumCover is the album's cover id, "" when it has no cover (clients
// would ask for nothing).
func albumCover(al library.Item) string {
	if al.Poster == "" {
		return ""
	}
	return albumCoverID(al.ID)
}
func artistCoverID(id int64) string { return "ar-" + sid(id) }

// genres of an item (meta_json "genres").
func genresOf(it library.Item) []string {
	var m struct {
		Genres []string `json:"genres"`
	}
	if it.MetaJSON != "" {
		json.Unmarshal([]byte(it.MetaJSON), &m)
	}
	return m.Genres
}

func firstGenre(it library.Item) string {
	if g := genresOf(it); len(g) > 0 {
		return g[0]
	}
	return ""
}

func secs(ms int64) int { return int((ms + 500) / 1000) }

// bitrateKbps: the probe's audio bitrate, else size over duration.
func bitrateKbps(f library.File, durMs int64) int {
	var p media.ProbeResult
	if f.ProbeJSON != "" && json.Unmarshal([]byte(f.ProbeJSON), &p) == nil && len(p.Audio) > 0 && p.Audio[0].BitRate > 0 {
		return int(p.Audio[0].BitRate / 1000)
	}
	if d := f.DurationMs; f.Size > 0 && d > 0 {
		return int(f.Size * 8 / d)
	}
	if f.Size > 0 && durMs > 0 {
		return int(f.Size * 8 / durMs)
	}
	return 0
}

// pathName makes a path component safe ("/" would split it).
func pathName(s string) string {
	s = strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(s))
	if s == "" {
		return "_"
	}
	return s
}

// artistName is an artist's name: its parent item's title.
func (c *call) artistName(id int64) string {
	if id == 0 {
		return ""
	}
	if n, ok := c.artistNames[id]; ok {
		return n
	}
	it, err := c.s.Library.Item(c.ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		c.artistNames[id] = ""
		return ""
	}
	if err != nil {
		c.keep(err)
		return ""
	}
	c.artistNames[id] = it.Title
	return it.Title
}

// songs renders tracks visible to the profile, in order.
func (c *call) songs(tracks []library.Track) ([]child, error) {
	fileIDs := make([]int64, 0, len(tracks))
	for _, t := range tracks {
		fileIDs = append(fileIDs, t.FileID)
	}
	files, err := c.s.Library.FilesByID(c.ctx, fileIDs)
	if err != nil {
		return nil, err
	}
	out := make([]child, 0, len(tracks))
	for _, t := range tracks {
		al, err := c.album(t.AlbumID)
		if err != nil {
			return nil, err
		}
		if al == nil {
			continue
		}
		out = append(out, c.song(t, *al, files[t.FileID]))
	}
	return out, nil
}

// song is a track as a Subsonic child.
func (c *call) song(t library.Track, al library.Item, f library.File) child {
	ext := strings.ToLower(filepath.Ext(f.Path))
	ct := audioserve.AudioTypes[ext]
	if ct == "" && f.Path != "" {
		ct = "application/octet-stream"
	}
	year := t.Year
	if year == 0 {
		year = al.Year
	}
	artist := t.Artist
	albumArtist := c.artistName(t.ArtistID)
	if artist == "" {
		artist = albumArtist
	}
	path := ""
	if f.Path != "" {
		path = pathName(albumArtist) + "/" + pathName(al.Title) + "/" + filepath.Base(f.Path)
	}
	size, suffix, kbps := f.Size, strings.TrimPrefix(ext, "."), bitrateKbps(f, t.DurationMs)
	if req := downloadRequest(t, f); req.Segment {
		// a CUE track: describe the cut that download sends, not the image
		plan := media.AudioDecide(req)
		ct, suffix = plan.ContentType, plan.Codec
		if plan.Kbps > 0 {
			kbps = plan.Kbps
			size = int64(plan.Kbps) * t.DurationMs / 8
		} else if f.DurationMs > 0 {
			size = f.Size * t.DurationMs / f.DurationMs // FLAC from a lossless image: its share
		}
		if path != "" {
			path = strings.TrimSuffix(path, ext) + fmt.Sprintf(" - %02d.%s", t.No, suffix)
		}
	}
	return child{
		ID: sid(t.ID), Parent: sid(al.ID), IsDir: false, Title: c.loc(t.Title), Album: c.loc(al.Title), Artist: c.loc(artist),
		Track: t.No, DiscNumber: t.Disc, Year: year, Genre: firstGenre(al), CoverArt: albumCover(al),
		Size: size, ContentType: ct, Suffix: suffix, Duration: secs(t.DurationMs),
		BitRate: kbps, Path: path, Created: stamp(t.AddedAt), Starred: c.starredStr(t.ID),
		AlbumID: sid(al.ID), ArtistID: idOrEmpty(t.ArtistID), Type: "music",
		ReplayGain: songReplayGain(t.Gain),
	}
}

// loc renders a label the scanner stored in Russian («Трек %d», «Без
// альбома», «Разные исполнители»…) in the request language; other text
// is unchanged.
func (c *call) loc(s string) string { return i18n.Localize(c.ctx, s) }

func idOrEmpty(id int64) string {
	if id == 0 {
		return ""
	}
	return sid(id)
}

// albumID3 renders an album.
func (c *call) albumID3(a library.Album) albumID3 {
	return albumID3{ID: sid(a.ID), Name: c.loc(a.Title), Artist: c.loc(a.Artist), ArtistID: idOrEmpty(a.ArtistID),
		CoverArt: albumCover(a.Item), SongCount: a.TrackCount, Duration: secs(a.DurationMs), Created: stamp(a.AddedAt),
		Year: a.Year, Genre: firstGenre(a.Item), Starred: c.starredStr(a.ID)}
}

// albumDir renders an album as a directory child (getAlbumList, search2).
func (c *call) albumDir(a library.Album) child {
	return child{ID: sid(a.ID), Parent: idOrEmpty(a.ArtistID), IsDir: true, Title: c.loc(a.Title), Album: c.loc(a.Title),
		Artist: c.loc(a.Artist), Year: a.Year, Genre: firstGenre(a.Item), CoverArt: albumCover(a.Item),
		Duration: secs(a.DurationMs), Created: stamp(a.AddedAt), Starred: c.starredStr(a.ID),
		AlbumID: sid(a.ID), ArtistID: idOrEmpty(a.ArtistID)}
}

func (c *call) artistID3(a library.ArtistRow) artistID3 {
	out := artistID3{ID: sid(a.ID), Name: c.loc(a.Title), AlbumCount: a.AlbumCount, Starred: c.starredStr(a.ID)}
	if a.Poster != "" || a.Cover != "" {
		out.CoverArt = artistCoverID(a.ID)
	}
	return out
}

// visibleAlbums keeps the albums the profile may see.
func (c *call) visibleAlbums(as []library.Album) ([]library.Album, error) {
	out := make([]library.Album, 0, len(as))
	for _, a := range as {
		ok, err := c.allowed(a.Item)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// item loads a visible item of one of kinds; nil when absent or hidden.
func (c *call) item(id int64, kinds ...string) (*library.Item, error) {
	it, err := c.s.Library.Item(c.ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	match := len(kinds) == 0
	for _, k := range kinds {
		match = match || it.Kind == k
	}
	if !match {
		return nil, nil
	}
	ok, err := c.allowed(it)
	if err != nil || !ok {
		return nil, err
	}
	return &it, nil
}

// track loads a visible track; nil when absent or hidden.
func (c *call) track(id int64) (*library.Track, error) {
	tr, err := c.s.Library.TrackByID(c.ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	al, err := c.album(tr.AlbumID)
	if err != nil || al == nil {
		return nil, err
	}
	return &tr, nil
}

// songReplayGain is a song's OpenSubsonic replayGain; nil when nothing is
// known (the element is left out).
func songReplayGain(g library.Loudness) *replayGain {
	if g.TrackGain == nil && g.AlbumGain == nil {
		return nil
	}
	return &replayGain{TrackGain: g.TrackGain, AlbumGain: g.AlbumGain, TrackPeak: g.TrackPeak, AlbumPeak: g.AlbumPeak}
}
