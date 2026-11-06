package subsonic

import (
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/screentime"
)

// streamTrack loads the visible track named by id and its file; a non-nil
// response is the error.
func (c *call) streamTrack() (library.Track, library.File, *response) {
	v := c.p.Get("id")
	if v == "" {
		return library.Track{}, library.File{}, c.missing("id")
	}
	id, ok := parseID(v, "")
	if !ok {
		return library.Track{}, library.File{}, c.fail(codeNotFound, msgSongMissing)
	}
	tr, err := c.track(id)
	if err != nil {
		return library.Track{}, library.File{}, c.internal("stream", err)
	}
	if tr == nil {
		return library.Track{}, library.File{}, c.fail(codeNotFound, msgSongMissing)
	}
	f, err := c.s.Library.FileByID(c.ctx, tr.FileID)
	if err != nil {
		return *tr, f, c.fail(codeNotFound, "Файл не найден")
	}
	return *tr, f, nil
}

// stream sends a track: the original at home, «Музыка вне дома» outside
// it unless maxBitRate > 0 says otherwise (0 means "no preference" in
// Subsonic clients, so it does not lift the remote cap). A lossy
// transcode is MP3 unless format asks for another; timeOffset (seconds)
// moves the start of a transcode; a CUE track is cut from its image.
func (c *call) stream() *response {
	tr, f, fail := c.streamTrack()
	if fail != nil {
		return fail
	}
	if fail := c.screenTime(); fail != nil {
		return fail
	}
	req := audioserve.TrackRequest(tr, f)
	req.Lang = i18n.FromContext(c.ctx)
	if s := c.p.Get("maxBitRate"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return c.fail(codeGeneric, "Неверный maxBitRate")
		}
		if n > 0 {
			req.MaxKbps, req.MaxKbpsSet = n, true
		}
	}
	switch f := strings.ToLower(c.p.Get("format")); f {
	case "raw", "mp3", "aac", "opus":
		req.Format = f
	} // anything else (flac, ogg…): the server decides
	var off int64
	if s := c.p.Get("timeOffset"); s != "" {
		t, err := strconv.ParseFloat(s, 64)
		if err != nil || t < 0 || math.IsNaN(t) || math.IsInf(t, 0) || t > 1e7 {
			return c.fail(codeGeneric, "Неверный timeOffset")
		}
		off = int64(math.Round(t * 1000))
	}
	if off > 0 && req.Format == "" {
		req.Format = "mp3" // only a transcode can start mid-track (transcodeOffset)
	}
	d := c.s.Audio
	if !req.MaxKbpsSet && d.IsRemote != nil && d.RemoteKbps != nil && d.IsRemote(c.r) {
		req.MaxKbps, req.MaxKbpsSet = d.RemoteKbps(c.ctx), true
	}
	plan := media.AudioDecide(req)
	if req.Format == "" && !plan.Direct && plan.Codec == "aac" {
		req.Format = "mp3" // what every Subsonic client plays
		plan = media.AudioDecide(req)
	}
	if c.r.Method == http.MethodHead && !plan.Direct {
		// a transcode would run to the end only for its headers
		h := c.w.Header()
		h.Set("Content-Type", plan.ContentType)
		h.Set("Accept-Ranges", "none")
		h.Set("Cache-Control", "no-store")
		c.w.WriteHeader(http.StatusOK)
		return nil
	}
	if !plan.Direct {
		if !c.s.acquireStream(c.u.ID) {
			c.w.Header().Set("Retry-After", "5")
			writeStatus(c.w, http.StatusServiceUnavailable, c.json, c.fail(codeGeneric, "Слишком много потоков одновременно, подождите"))
			return nil
		}
		defer c.s.releaseStream(c.u.ID)
	}
	// a kid's music counts by what is sent (the queue's stream) and stops at the limit
	d.Meter = c.meter(tr.ID)
	audioserve.Serve(c.w, c.r, d, f, tr.StartMs+off, tr.EndMs, req)
	return nil
}

// meter is audioserve's Meter of a kid's music (nil: none).
func (c *call) meter(trackID int64) func(http.ResponseWriter, float64) (http.ResponseWriter, func()) {
	if c.s.ScreenTime == nil || !c.u.IsKid {
		return nil
	}
	return func(w http.ResponseWriter, bps float64) (http.ResponseWriter, func()) {
		m := c.s.ScreenTime.Meter(c.ctx, w, c.u, screentime.Signal{Category: screentime.Audio, Key: screentime.MusicStream, ItemID: trackID}, bps)
		if m == nil {
			return nil, nil
		}
		return m, m.Done
	}
}

// screenTime is the refusal of a kid profile whose music time is over
// (error 50, the kid's message), else nil.
func (c *call) screenTime() *response {
	if ref := c.s.ScreenTime.Allow(c.ctx, c.u, screentime.Audio); ref != nil {
		return c.fail(codeForbidden, ref.Message)
	}
	return nil
}

// downloadOff: a kid whose music is limited keeps no files (a client's
// offline cache plays out of the clock's reach); error 50.
func (c *call) downloadOff() *response {
	if c.s.ScreenTime.Restricted(c.ctx, c.u, screentime.Audio) {
		return c.fail(codeForbidden, "Скачивание выключено родителем")
	}
	return nil
}

// downloadRequest is what download sends: the original file, or for a CUE
// track (a segment of an image) the track cut from it: FLAC when the image
// is lossless, else MP3. A client caching through download must not keep
// the whole album for every track (and iOS cannot play an .ape image).
// Never capped by «Музыка вне дома»: a download is the original.
func downloadRequest(tr library.Track, f library.File) media.AudioRequest {
	req := audioserve.TrackRequest(tr, f)
	req.MaxKbps, req.MaxKbpsSet = 0, true
	switch {
	case !req.Segment:
		req.Format = "raw"
	case !req.Lossless:
		req.Format = "mp3"
	}
	return req
}

// download sends the original file; a CUE track is cut from its image
// (downloadRequest).
func (c *call) download() *response {
	tr, f, fail := c.streamTrack()
	if fail != nil {
		return fail
	}
	if fail := c.downloadOff(); fail != nil {
		return fail
	}
	req := downloadRequest(tr, f)
	if plan := media.AudioDecide(req); !plan.Direct {
		if c.r.Method == http.MethodHead {
			h := c.w.Header()
			h.Set("Content-Type", plan.ContentType)
			h.Set("Accept-Ranges", "none")
			h.Set("Cache-Control", "no-store")
			c.w.WriteHeader(http.StatusOK)
			return nil
		}
		if !c.s.acquireStream(c.u.ID) {
			c.w.Header().Set("Retry-After", "5")
			writeStatus(c.w, http.StatusServiceUnavailable, c.json, c.fail(codeGeneric, "Слишком много потоков одновременно, подождите"))
			return nil
		}
		defer c.s.releaseStream(c.u.ID)
	}
	d := c.s.Audio
	d.Meter = c.meter(tr.ID)
	audioserve.Serve(c.w, c.r, d, f, tr.StartMs, tr.EndMs, req)
	return nil
}

// coverItem resolves a cover id: al-<album>, ar-<artist>, pl-<playlist>,
// or a bare id of a track (its album), an album or an artist. The item
// returned holds the poster to serve.
func (c *call) coverItem(v string) (*library.Item, error) {
	switch {
	case strings.HasPrefix(v, "al-"):
		id, ok := parseID(v, "al-")
		if !ok {
			return nil, nil
		}
		return c.album(id)
	case strings.HasPrefix(v, "ar-"):
		id, ok := parseID(v, "ar-")
		if !ok {
			return nil, nil
		}
		return c.artistCover(id)
	case strings.HasPrefix(v, "pl-"):
		id, ok := parseID(v, "pl-")
		if !ok {
			return nil, nil
		}
		p, _, err := c.s.Music.Playlist(c.ctx, c.u.ID, id)
		if errors.Is(err, music.ErrNotFound) {
			return nil, nil // someone else's or gone: no cover
		}
		if err != nil {
			return nil, err
		}
		for _, al := range p.CoverAlbums {
			a, err := c.album(al)
			if err != nil {
				return nil, err
			}
			if a != nil {
				return a, nil
			}
		}
		return nil, nil
	}
	id, ok := parseID(v, "")
	if !ok {
		return nil, nil
	}
	it, err := c.item(id, library.KindTrack, library.KindAlbum, library.KindArtist)
	if err != nil || it == nil {
		return nil, err
	}
	switch it.Kind {
	case library.KindTrack:
		return c.album(it.ParentID)
	case library.KindArtist:
		return c.artistCover(id)
	}
	return it, nil
}

// artistCover is the artist's own picture, else its newest album's cover.
func (c *call) artistCover(id int64) (*library.Item, error) {
	it, err := c.item(id, library.KindArtist)
	if err != nil || it == nil {
		return nil, err
	}
	if it.Poster != "" {
		return it, nil
	}
	as, _, err := c.s.Library.Albums(c.ctx, library.AlbumQuery{ArtistID: id, Sort: "year", MaxRating: c.limit()})
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		if a.Poster == "" {
			continue
		}
		if al, err := c.album(a.ID); err != nil || al != nil {
			return al, err
		}
	}
	return nil, nil
}

// getCoverArt serves a cover: the original, or with size=N a copy that
// fits in N×N (imgcache.Resized).
func (c *call) getCoverArt() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	it, err := c.coverItem(v)
	if err != nil {
		return c.internal("getCoverArt", err)
	}
	if it == nil {
		return c.fail(codeNotFound, "Обложка не найдена")
	}
	path, err := imgcache.Resolve(c.s.ImageDir, it.Poster)
	if err != nil {
		return c.fail(codeNotFound, "Обложка не найдена")
	}
	if n, err := strconv.Atoi(c.p.Get("size")); err == nil && n > 0 {
		if path, err = imgcache.Resized(c.s.ImageDir, path, n, n); err != nil {
			return c.fail(codeNotFound, "Обложка не найдена")
		}
	}
	fh, err := os.Open(path)
	if err != nil {
		return c.fail(codeNotFound, "Обложка не найдена")
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return c.fail(codeNotFound, "Обложка не найдена")
	}
	h := c.w.Header()
	h.Set("Cache-Control", "private, max-age=604800")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(c.w, c.r, filepath.Base(path), st.ModTime(), fh)
	return nil
}

// lrcTag is one [mm:ss(.xx)] time tag of an LRC line.
var lrcTag = regexp.MustCompile(`^\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)

// parseLyrics splits lyrics into lines; synced when any line has an LRC
// time tag (then untagged lines and headers are dropped, and a line with
// several tags repeats at each time).
func parseLyrics(text string) (lines []lyricLine, synced bool) {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, l := range raw {
		if lrcTag.MatchString(strings.TrimSpace(l)) {
			synced = true
			break
		}
	}
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if !synced {
			lines = append(lines, lyricLine{Value: l})
			continue
		}
		var starts []int64
		for {
			m := lrcTag.FindStringSubmatch(l)
			if m == nil {
				break
			}
			mm, _ := strconv.ParseInt(m[1], 10, 64)
			ss, _ := strconv.ParseInt(m[2], 10, 64)
			var frac int64
			if m[3] != "" {
				frac, _ = strconv.ParseInt(m[3], 10, 64)
				for i := len(m[3]); i < 3; i++ {
					frac *= 10
				}
			}
			starts = append(starts, (mm*60+ss)*1000+frac)
			l = l[len(m[0]):]
		}
		if len(starts) == 0 {
			continue // header or untagged line
		}
		for _, s := range starts {
			lines = append(lines, lyricLine{Start: &s, Value: strings.TrimSpace(l)})
		}
	}
	if synced {
		sortLines(lines)
	}
	return lines, synced
}

func sortLines(ls []lyricLine) {
	for i := 1; i < len(ls); i++ { // insertion sort: stable, nearly sorted input
		for j := i; j > 0 && *ls[j].Start < *ls[j-1].Start; j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// plainLyrics drops LRC tags and headers.
func plainLyrics(text string) string {
	lines, synced := parseLyrics(text)
	if !synced {
		return strings.TrimSpace(text)
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Value)
	}
	return strings.Join(out, "\n")
}

// words are a track's lyrics: the file's, else those kept from LRCLIB
// (library.EffectiveLyrics).
func (c *call) words(t library.Track) (string, error) {
	st, has, err := c.s.Library.TrackLyrics(c.ctx, t.ID)
	if err != nil {
		return "", err
	}
	text, _ := library.EffectiveLyrics(t, st, has)
	return text, nil
}

// getLyrics finds a visible track by artist and title (any case) with lyrics.
func (c *call) getLyrics() *response {
	artist, title := strings.TrimSpace(c.p.Get("artist")), strings.TrimSpace(c.p.Get("title"))
	resp := c.ok()
	resp.Lyrics = &lyrics{Artist: artist, Title: title}
	if title == "" {
		return resp
	}
	items, err := c.s.Library.SearchKinds(c.ctx, title, []string{library.KindTrack}, 50, c.limit())
	if err != nil {
		return c.internal("getLyrics", err)
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	tracks, err := c.s.Library.Tracks(c.ctx, ids)
	if err != nil {
		return c.internal("getLyrics", err)
	}
	for _, t := range tracks {
		if !strings.EqualFold(strings.TrimSpace(t.Title), title) {
			continue
		}
		words, err := c.words(t)
		if err != nil {
			return c.internal("getLyrics", err)
		}
		if words == "" {
			continue
		}
		if artist != "" && !strings.EqualFold(strings.TrimSpace(t.Artist), artist) && !strings.EqualFold(c.artistName(t.ArtistID), artist) {
			continue
		}
		if al, err := c.album(t.AlbumID); err != nil {
			return c.internal("getLyrics", err)
		} else if al == nil {
			continue
		}
		resp.Lyrics = &lyrics{Artist: t.Artist, Title: t.Title, Value: plainLyrics(words)}
		return resp
	}
	return resp
}

// getLyricsBySongID is OpenSubsonic songLyrics: structured lines, synced
// from LRC.
func (c *call) getLyricsBySongID() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "")
	if !ok {
		return c.fail(codeNotFound, msgSongMissing)
	}
	tr, err := c.track(id)
	if err != nil {
		return c.internal("getLyricsBySongId", err)
	}
	if tr == nil {
		return c.fail(codeNotFound, msgSongMissing)
	}
	words, err := c.words(*tr)
	if err != nil {
		return c.internal("getLyricsBySongId", err)
	}
	out := &lyricsList{StructuredLyrics: []structuredLyrics{}}
	if words != "" {
		lines, synced := parseLyrics(words)
		if lines == nil {
			lines = []lyricLine{}
		}
		out.StructuredLyrics = append(out.StructuredLyrics, structuredLyrics{DisplayArtist: tr.Artist,
			DisplayTitle: tr.Title, Lang: "und", Synced: synced, Line: lines})
	}
	resp := c.ok()
	resp.LyricsList = out
	return resp
}
