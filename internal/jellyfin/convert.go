package jellyfin

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// fieldSet is the fields= list of a request, lower case.
type fieldSet map[string]bool

func fieldsOf(r *http.Request) fieldSet {
	f := fieldSet{}
	for _, v := range qList(r, "fields") {
		f[strings.ToLower(v)] = true
	}
	return f
}

// fieldFull marks a full item (/Items/{id}): media sources, streams,
// path, provider ids and user data whatever fields says.
const fieldFull = "\x00full"

// errKind: the item is of a kind the Jellyfin API does not serve (music,
// books): callers skip it, or answer 404.
var errKind = errors.New("jellyfin: kind not served")

// jfTypes maps Vault kinds to Jellyfin item types; kinds maps them back.
var (
	jfTypes = map[string]string{library.KindMovie: "Movie", library.KindSeries: "Series",
		library.KindSeason: "Season", library.KindEpisode: "Episode"}
	kinds = map[string]string{"movie": library.KindMovie, "series": library.KindSeries,
		"season": library.KindSeason, "episode": library.KindEpisode}
)

// posterRatio is PrimaryImageAspectRatio of a poster, stillRatio of an
// episode still.
const (
	posterRatio = 0.6667
	stillRatio  = 1.7778
)

// imageTag is the cache key of an artwork reference in image URLs: it
// changes when the art does.
func imageTag(ref string) string {
	sum := sha1.Sum([]byte(ref))
	return hex.EncodeToString(sum[:])[:16]
}

// itemMeta is what Jellyfin shows of items.meta_json.
type itemMeta struct {
	Logo    string   `json:"logo"`
	Genres  []string `json:"genres"`
	Runtime int      `json:"runtime"` // minutes
	ImdbID  string   `json:"imdb_id"`
	TvdbID  any      `json:"tvdb_id"`
}

func metaOf(it library.Item) itemMeta {
	var m itemMeta
	if it.MetaJSON != "" {
		_ = json.Unmarshal([]byte(it.MetaJSON), &m) // degrade: no genres or logo
	}
	return m
}

// conv converts items for one request: it caches parents, visibility,
// progress and the watchlist across the items of a list.
type conv struct {
	s      *Server
	ctx    context.Context
	u      auth.User
	limit  string // u.RatingLimit()
	fields fieldSet

	items  map[int64]library.Item
	vis    map[int64]bool
	prog   map[int64]*progress.Entry
	fav    map[int64]bool
	counts map[int64]epCounts
}

func (s *Server) conv(ctx context.Context, u auth.User, fields fieldSet) *conv {
	return &conv{s: s, ctx: ctx, u: u, limit: u.RatingLimit(), fields: fields,
		items: map[int64]library.Item{}, vis: map[int64]bool{}, prog: map[int64]*progress.Entry{}, counts: map[int64]epCounts{}}
}

// dto converts one item (movie, series, season, episode; others: errKind).
func (s *Server) dto(ctx context.Context, u auth.User, it library.Item, fields fieldSet) (BaseItemDto, error) {
	return s.conv(ctx, u, fields).dto(it)
}

// item loads an item (cached); library.ErrNotFound when missing.
func (c *conv) item(id int64) (library.Item, error) {
	if it, ok := c.items[id]; ok {
		return it, nil
	}
	it, err := c.s.Library.Item(c.ctx, id)
	if err != nil {
		return it, err
	}
	c.items[id] = it
	return it, nil
}

// visible applies library.Repo.Visible (the rule of the web and
// Subsonic) with the profile's rating limit. A season or an episode has
// its root's rating, so its parent's answer is cached and reused.
func (c *conv) visible(it library.Item) (bool, error) {
	if c.limit == "" {
		return true, nil
	}
	if v, ok := c.vis[it.ID]; ok {
		return v, nil
	}
	var v bool
	var err error
	if library.HasRatedParent(it.Kind) && it.ParentID != 0 {
		p, perr := c.item(it.ParentID)
		if errors.Is(perr, library.ErrNotFound) {
			return false, nil
		}
		if perr != nil {
			return false, perr
		}
		v, err = c.visible(p)
	} else {
		v, err = c.s.Library.Visible(c.ctx, c.limit, it)
	}
	if err != nil {
		return false, err
	}
	c.vis[it.ID] = v
	return v, nil
}

// visibleItem loads an item the profile may see: ok false when it is
// missing, hidden or of a kind not served.
func (c *conv) visibleItem(id int64) (library.Item, bool, error) {
	it, err := c.item(id)
	if errors.Is(err, library.ErrNotFound) {
		return it, false, nil
	}
	if err != nil {
		return it, false, err
	}
	if jfTypes[it.Kind] == "" {
		return it, false, nil
	}
	ok, err := c.visible(it)
	return it, ok, err
}

// tag is the image tag of a reference that resolves to a file ("" for
// none or an http URL: never advertise an image that cannot be served).
func (c *conv) tag(ref string) string {
	if _, err := imgcache.Resolve(c.s.ImageDir, ref); err != nil {
		return ""
	}
	return imageTag(ref)
}

func intp(n int) *int { return &n }

// dtos converts items, skipping kinds not served.
func (c *conv) dtos(items []library.Item) ([]BaseItemDto, error) {
	out := make([]BaseItemDto, 0, len(items))
	for _, it := range items {
		d, err := c.dto(it)
		if errors.Is(err, errKind) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (c *conv) dto(it library.Item) (BaseItemDto, error) {
	typ := jfTypes[it.Kind]
	if typ == "" {
		return BaseItemDto{}, errKind
	}
	id := ItemID(it.ID)
	m := metaOf(it)
	// the name in the request language: «Серия %d», «Спецвыпуски»… are stored in Russian
	d := BaseItemDto{Name: i18n.Localize(c.ctx, it.Title), Id: id, ServerId: c.s.sid(), Type: typ, Overview: it.Overview,
		OfficialRating: it.Rating, ProductionYear: it.Year, LocationType: "FileSystem", ImageTags: map[string]string{}}
	if it.OriginalTitle != "" && it.OriginalTitle != it.Title {
		d.OriginalTitle = it.OriginalTitle
	}
	if it.Year > 0 {
		d.PremiereDate = jfTime(time.Date(it.Year, 1, 1, 0, 0, 0, 0, time.UTC))
	}
	if !it.AddedAt.IsZero() {
		d.DateCreated = jfTime(it.AddedAt)
	}
	d.ProviderIds = providerIDs(it, m)
	for _, g := range m.Genres {
		d.Genres = append(d.Genres, g)
		d.GenreItems = append(d.GenreItems, NameIDPair{Name: g, Id: genreID(g)})
	}
	if t := c.tag(m.Logo); t != "" {
		d.ImageTags["Logo"] = t
	}
	if t := c.tag(it.Poster); t != "" {
		d.ImageTags["Primary"] = t
		d.PrimaryImageAspectRatio = posterRatio
		if it.Kind == library.KindEpisode {
			d.PrimaryImageAspectRatio = stillRatio
		}
	}
	if t := c.tag(it.Backdrop); t != "" {
		d.BackdropImageTags = []string{t}
	}

	switch it.Kind {
	case library.KindSeries, library.KindSeason:
		d.IsFolder = true
		n, err := c.episodeCounts(it)
		if err != nil {
			return d, err
		}
		d.ChildCount = intp(n.children)
		d.RecursiveItemCount = intp(n.episodes)
		if it.Kind == library.KindSeason {
			d.IndexNumber = intp(it.SeasonNo)
			if err := c.seriesFields(&d, it.ParentID); err != nil {
				return d, err
			}
			d.ParentId = d.SeriesId
		}
	case library.KindMovie, library.KindEpisode:
		d.MediaType, d.VideoType = "Video", "VideoFile"
		if it.Kind == library.KindEpisode {
			d.IndexNumber, d.ParentIndexNumber = intp(it.EpisodeNo), intp(it.SeasonNo)
			season, err := c.item(it.ParentID)
			if err != nil && !errors.Is(err, library.ErrNotFound) {
				return d, err
			}
			if err == nil {
				d.SeasonId, d.SeasonName, d.ParentId = ItemID(season.ID), i18n.Localize(c.ctx, season.Title), ItemID(season.ID)
				if err := c.seriesFields(&d, season.ParentID); err != nil {
					return d, err
				}
			}
		}
		if err := c.files(&d, it, m); err != nil {
			return d, err
		}
	}
	ud, err := c.userData(it)
	if err != nil {
		return d, err
	}
	d.UserData = ud
	return d, nil
}

// seriesFields fills the series of a season or an episode: clients take
// its poster and backdrop when the child has none.
func (c *conv) seriesFields(d *BaseItemDto, seriesID int64) error {
	sr, err := c.item(seriesID)
	if errors.Is(err, library.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	d.SeriesId, d.SeriesName = ItemID(sr.ID), sr.Title
	d.SeriesPrimaryImageTag = c.tag(sr.Poster)
	if t := c.tag(sr.Backdrop); t != "" {
		d.ParentBackdropItemId, d.ParentBackdropImageTags = ItemID(sr.ID), []string{t}
	}
	return nil
}

// files fills the run time and, when asked (or for a full item), the
// media sources, streams and file name of a movie or an episode.
func (c *conv) files(d *BaseItemDto, it library.Item, m itemMeta) error {
	fs, err := c.s.Library.FilesOf(c.ctx, it.ID)
	if err != nil {
		return err
	}
	full := c.fields[fieldFull]
	var dur int64 // timeline, below
	for _, f := range fs {
		if d.Container == "" {
			d.Container = strings.TrimPrefix(strings.ToLower(filepath.Ext(f.Path)), ".")
		}
		if !full && !c.fields["mediasources"] && !c.fields["mediastreams"] {
			continue
		}
		p, _ := cachedProbe(f)
		// no external audio: it plays only through an HLS PlaybackInfo answer
		ms := mediaSource(c.ctx, f, p, nil, subtitles.FindExternal(f.Path, c.s.DataDir, f.ID)...)
		if full || c.fields["mediasources"] {
			d.MediaSources = append(d.MediaSources, ms)
		}
		if len(d.MediaStreams) == 0 && (full || c.fields["mediastreams"]) {
			d.MediaStreams = ms.MediaStreams
		}
		for _, s := range ms.MediaStreams {
			d.HasSubtitles = d.HasSubtitles || s.Type == "Subtitle"
		}
	}
	// the timeline progress is kept on: a multi-part movie's parts, else
	// the first version (alternative versions are not one after another)
	_, dur = timeline(fs, 0, 0)
	if dur == 0 && m.Runtime > 0 {
		dur = int64(m.Runtime) * 60_000
	}
	d.RunTimeTicks = ticks(dur)
	if len(fs) > 0 && (full || c.fields["path"]) {
		d.Path = filepath.Base(fs[0].Path)
	}
	return nil
}

func providerIDs(it library.Item, m itemMeta) map[string]string {
	ids := map[string]string{}
	if it.TmdbID > 0 {
		ids["Tmdb"] = strconv.FormatInt(it.TmdbID, 10)
	}
	if m.ImdbID != "" {
		ids["Imdb"] = m.ImdbID
	}
	switch v := m.TvdbID.(type) {
	case string:
		if v != "" {
			ids["Tvdb"] = v
		}
	case float64:
		if v > 0 {
			ids["Tvdb"] = strconv.FormatInt(int64(v), 10)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// genreID is a stable id of a genre name (not a Vault item).
func genreID(name string) string {
	sum := sha1.Sum([]byte("vault-genre:" + strings.ToLower(name)))
	return hex.EncodeToString(sum[:16])
}

func cachedProbe(f library.File) (media.ProbeResult, bool) {
	var p media.ProbeResult
	if f.ProbeJSON == "" {
		return p, false
	}
	return p, json.Unmarshal([]byte(f.ProbeJSON), &p) == nil
}

// MediaSourceInfo is a playable file of an item.
type MediaSourceInfo struct {
	Id                         string        `json:"Id"`
	ETag                       string        `json:"ETag"`
	Name                       string        `json:"Name"`
	Path                       string        `json:"Path"`
	Protocol                   string        `json:"Protocol"`
	Type                       string        `json:"Type"`
	Container                  string        `json:"Container"`
	Size                       int64         `json:"Size,omitempty"`
	Bitrate                    int64         `json:"Bitrate,omitempty"`
	RunTimeTicks               int64         `json:"RunTimeTicks,omitempty"`
	IsRemote                   bool          `json:"IsRemote"`
	SupportsDirectPlay         bool          `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool          `json:"SupportsDirectStream"`
	SupportsTranscoding        bool          `json:"SupportsTranscoding"`
	RequiresOpening            bool          `json:"RequiresOpening"`
	ReadAtNativeFramerate      bool          `json:"ReadAtNativeFramerate"`
	DefaultAudioStreamIndex    *int          `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex int           `json:"DefaultSubtitleStreamIndex"`
	MediaStreams               []MediaStream `json:"MediaStreams"`
	TranscodingUrl             string        `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol     string        `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer       string        `json:"TranscodingContainer,omitempty"`
}

// MediaStream is a video, audio or subtitle stream. Index is the ffprobe
// index (external subtitles: 1000+, as in /api/play), the number clients
// send back as AudioStreamIndex/SubtitleStreamIndex.
type MediaStream struct {
	Type                   string `json:"Type"`
	Index                  int    `json:"Index"`
	Codec                  string `json:"Codec,omitempty"`
	Language               string `json:"Language,omitempty"`
	Title                  string `json:"Title,omitempty"`
	DisplayTitle           string `json:"DisplayTitle,omitempty"`
	IsDefault              bool   `json:"IsDefault"`
	IsForced               bool   `json:"IsForced"`
	IsExternal             bool   `json:"IsExternal"`
	IsTextSubtitleStream   bool   `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool   `json:"SupportsExternalStream"`
	Width                  int    `json:"Width,omitempty"`
	Height                 int    `json:"Height,omitempty"`
	BitRate                int64  `json:"BitRate,omitempty"`
	BitDepth               int    `json:"BitDepth,omitempty"`
	Channels               int    `json:"Channels,omitempty"`
	ChannelLayout          string `json:"ChannelLayout,omitempty"`
	SampleRate             int    `json:"SampleRate,omitempty"`
	Profile                string `json:"Profile,omitempty"`
	PixelFormat            string `json:"PixelFormat,omitempty"`
	VideoRange             string `json:"VideoRange,omitempty"`
	VideoRangeType         string `json:"VideoRangeType,omitempty"`
	DeliveryMethod         string `json:"DeliveryMethod,omitempty"`
	DeliveryUrl            string `json:"DeliveryUrl,omitempty"`
}

// externalSubBase is the index of the first external subtitle file (the
// numbering of /api/play and /api/subs).
const externalSubBase = 1000

// externalAudioBase is the first audio index of the files beside the video
// (the web's /api/play numbers them the same).
const externalAudioBase = 2000

// textSubCodecs are the subtitle codecs served as text (WebVTT); the
// others (PGS, VobSub) are images and need burning in.
var textSubCodecs = map[string]bool{"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// mediaSource describes file f with its cached probe p and its external
// subtitle files ext (subtitles.FindExternal, the order of /api/play).
// ctx's language names a cleaned voice-over («LostFilm (очищено)»).
func mediaSource(ctx context.Context, f library.File, p media.ProbeResult, audio []media.ExternalAudio, ext ...subtitles.External) MediaSourceInfo {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%d", f.ID, f.Path, f.Size, f.Mtime)))
	base := filepath.Base(f.Path)
	ms := MediaSourceInfo{Id: FileID(f.ID), ETag: hex.EncodeToString(sum[:16]), Name: strings.TrimSuffix(base, filepath.Ext(base)),
		Path: base, Protocol: "File", Type: "Default", Container: p.Container, Size: f.Size,
		SupportsDirectPlay: true, SupportsDirectStream: true, SupportsTranscoding: true,
		DefaultSubtitleStreamIndex: -1, MediaStreams: []MediaStream{}}
	if ms.Container == "" {
		ms.Container = strings.TrimPrefix(strings.ToLower(filepath.Ext(f.Path)), ".")
	}
	dur := p.DurationMs
	if dur == 0 {
		dur = f.DurationMs
	}
	ms.RunTimeTicks = ticks(dur)
	if dur > 0 && f.Size > 0 {
		ms.Bitrate = f.Size * 8 * 1000 / dur
	}
	used := map[int]bool{}
	for _, a := range p.Audio {
		used[a.Index] = true
	}
	for _, s := range p.Subs {
		used[s.Index] = true
	}
	if v := p.Video; v != nil {
		idx := 0
		for used[idx] {
			idx++
		}
		rng, rngType := "SDR", "SDR"
		if v.HDR {
			rng, rngType = "HDR", "HDR10"
		}
		ms.MediaStreams = append(ms.MediaStreams, MediaStream{Type: "Video", Index: idx, Codec: v.Codec, Width: v.Width, Height: v.Height,
			BitRate: v.BitRate, BitDepth: v.BitDepth, Profile: v.Profile, PixelFormat: v.PixFmt, VideoRange: rng, VideoRangeType: rngType,
			IsDefault: true, DisplayTitle: videoTitle(v, rng)})
	}
	for _, a := range p.Audio {
		lang := iso6392(a.Lang)
		ms.MediaStreams = append(ms.MediaStreams, MediaStream{Type: "Audio", Index: a.Index, Codec: a.Codec, Language: lang,
			Title: a.Title, Channels: a.Channels, ChannelLayout: channelLayout(a.Channels), SampleRate: a.SampleRate, BitRate: a.BitRate,
			IsDefault: a.Default, DisplayTitle: streamTitle(a.Title, lang, a.Codec, channelLayout(a.Channels))})
		if ms.DefaultAudioStreamIndex == nil || a.Default && !defaultAudio(p, *ms.DefaultAudioStreamIndex) {
			ms.DefaultAudioStreamIndex = intp(a.Index)
		}
	}
	for i, e := range audio {
		a := e.Stream
		lang := iso6392(a.Lang)
		// a cleaned voice-over says so: these players show no badge
		title := e.Title(i18n.FromContext(ctx))
		ms.MediaStreams = append(ms.MediaStreams, MediaStream{Type: "Audio", Index: externalAudioBase + i, Codec: a.Codec, Language: lang,
			Title: title, Channels: a.Channels, ChannelLayout: channelLayout(a.Channels), SampleRate: a.SampleRate, BitRate: a.BitRate,
			IsExternal: true, DisplayTitle: streamTitle(title, lang, a.Codec, channelLayout(a.Channels))})
		if ms.DefaultAudioStreamIndex == nil { // a file without its own audio
			ms.DefaultAudioStreamIndex = intp(externalAudioBase + i)
		}
	}
	for _, s := range p.Subs {
		lang := iso6392(s.Lang)
		text := textSubCodecs[s.Codec]
		ms.MediaStreams = append(ms.MediaStreams, MediaStream{Type: "Subtitle", Index: s.Index, Codec: jfSubCodec(s.Codec), Language: lang,
			Title: s.Title, IsDefault: s.Default, IsForced: s.Forced, IsTextSubtitleStream: text, SupportsExternalStream: text,
			DisplayTitle: streamTitle(s.Title, lang, jfSubCodec(s.Codec), "")})
	}
	for i, s := range ext {
		lang := iso6392(s.Lang)
		ms.MediaStreams = append(ms.MediaStreams, MediaStream{Type: "Subtitle", Index: externalSubBase + i, Codec: jfSubCodec(s.Codec),
			Language: lang, Title: s.Title, IsForced: s.Forced, IsExternal: true, IsTextSubtitleStream: true,
			SupportsExternalStream: true, DisplayTitle: streamTitle(s.Title, lang, jfSubCodec(s.Codec), "")})
	}
	return ms
}

func defaultAudio(p media.ProbeResult, idx int) bool {
	for _, a := range p.Audio {
		if a.Index == idx {
			return a.Default
		}
	}
	return false
}

func videoTitle(v *media.VideoStream, rng string) string {
	res := ""
	switch {
	case v.Width >= 3200 || v.Height >= 1800:
		res = "4K"
	case v.Width >= 1800 || v.Height >= 1000:
		res = "1080p"
	case v.Width >= 1200 || v.Height >= 700:
		res = "720p"
	case v.Height > 0:
		res = strconv.Itoa(v.Height) + "p"
	}
	return strings.TrimSpace(res + " " + strings.ToUpper(v.Codec) + " " + rng)
}

// streamTitle is "Title - RUS - AC3 5.1" without the empty parts.
func streamTitle(title, lang, codec, layout string) string {
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if lang != "" {
		parts = append(parts, strings.ToUpper(lang))
	}
	if c := strings.TrimSpace(strings.ToUpper(codec) + " " + layout); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " - ")
}

func channelLayout(n int) string {
	switch n {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	case 0:
		return ""
	}
	return strconv.Itoa(n) + "ch"
}

// iso6391to2 maps ISO 639-1 codes to the ISO 639-2/B codes clients show.
var iso6391to2 = map[string]string{
	"ru": "rus", "en": "eng", "uk": "ukr", "be": "bel", "kk": "kaz", "de": "ger", "fr": "fre", "es": "spa", "it": "ita",
	"pt": "por", "pl": "pol", "cs": "cze", "sk": "slo", "nl": "dut", "sv": "swe", "no": "nor", "da": "dan", "fi": "fin",
	"hu": "hun", "ro": "rum", "bg": "bul", "sr": "srp", "hr": "hrv", "sl": "slv", "el": "gre", "tr": "tur", "he": "heb",
	"ar": "ara", "fa": "per", "hi": "hin", "ja": "jpn", "ko": "kor", "zh": "chi", "th": "tha", "vi": "vie", "id": "ind",
	"lt": "lit", "lv": "lav", "et": "est", "ka": "geo", "hy": "arm", "az": "aze", "uz": "uzb",
}

// iso6392 is a stream language as ISO 639-2 ("" for unknown/und).
func iso6392(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	switch {
	case l == "" || l == "und" || l == "unk":
		return ""
	case len(l) == 2:
		if v, ok := iso6391to2[l]; ok {
			return v
		}
		return ""
	case len(l) == 3:
		return l
	}
	return ""
}
