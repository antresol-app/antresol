package api

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// Badges are the short facts a card, row or detail page shows under a title,
// chosen per media type: music — format and bitrate or bit depth/sample rate
// («FLAC 24/96», «MP3 320»); audiobooks — language and size; movies and
// series — quality, HDR, audio and subtitle languages; books — language and
// format. They come from the cached probe and the item's meta; a missing fact
// is simply left out.

var langCodes = map[string]string{
	"rus": "RU", "ru": "RU", "eng": "EN", "en": "EN", "ukr": "UK", "uk": "UK", "deu": "DE", "ger": "DE", "de": "DE",
	"fra": "FR", "fre": "FR", "fr": "FR", "spa": "ES", "es": "ES", "ita": "IT", "it": "IT", "jpn": "JA", "ja": "JA",
	"kor": "KO", "ko": "KO", "zho": "ZH", "chi": "ZH", "zh": "ZH", "pol": "PL", "pl": "PL", "por": "PT", "pt": "PT",
}

// langBadge is a two-letter upper-case language code ("" for unknown/undetermined).
func langBadge(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	if c, ok := langCodes[l]; ok {
		return c
	}
	if len(l) == 2 {
		return strings.ToUpper(l)
	}
	return ""
}

// langList: the badge codes of ls, at most max, only those of mine when
// mine is set (a release may carry ten tracks; only the household's
// languages are worth a badge).
func langList(ls []string, max int, mine []string) string {
	want := map[string]bool{}
	for _, l := range mine {
		if c := langBadge(l); c != "" {
			want[c] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range ls {
		if c := langBadge(l); c != "" && !seen[c] && (len(want) == 0 || want[c]) {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return strings.Join(out, " ")
}

// sizeBadge is a compact size in the language: «842 МБ», «1,4 ГБ» /
// "842 MB", "1.4 GB".
func sizeBadge(lang string, n int64) string {
	if n <= 0 {
		return ""
	}
	const mb, gb = 1 << 20, 1 << 30
	if n >= gb {
		return i18n.TL(lang, "%s ГБ", decimal(lang, strconv.FormatFloat(float64(n)/gb, 'f', 1, 64)))
	}
	return i18n.TL(lang, "%d МБ", max(1, int((n+mb/2)/mb)))
}

// decimal writes a number formatted with a point in the language's
// convention: «1,4» in Russian, "1.4" in English.
func decimal(lang, num string) string {
	if lang == i18n.RU {
		return strings.Replace(num, ".", ",", 1)
	}
	return num
}

// videoFacts are what a video's badges say, before the wording: the quality
// tier, "Dolby Vision"/"HDR" and the badge codes of its audio and subtitle
// languages (deduplicated, in track order, all of them: «Мои языки» filter
// at the wording).
type videoFacts struct {
	res, hdr    string
	audio, subs []string
}

// factsOf: the facts of the first file with a cached video probe.
func factsOf(files []library.File) (videoFacts, bool) {
	for _, f := range files {
		p, ok := cachedProbe(f)
		if !ok || p.Video == nil {
			continue
		}
		v := videoFacts{res: resolution(p.Video.Width, p.Video.Height)}
		switch tag := strings.ToLower(p.Video.CodecTag); {
		case tag == "dvh1" || tag == "dvhe" || tag == "dav1":
			v.hdr = "Dolby Vision"
		case p.Video.HDR:
			v.hdr = "HDR"
		}
		for _, a := range p.Audio {
			v.audio = appendCode(v.audio, a.Lang)
		}
		for _, s := range p.Subs {
			v.subs = appendCode(v.subs, s.Lang)
		}
		return v, true
	}
	return videoFacts{}, false
}

func appendCode(codes []string, lang string) []string {
	c := langBadge(lang)
	if c == "" || slices.Contains(codes, c) {
		return codes
	}
	return append(codes, c)
}

// badges words the facts: quality, HDR, then the audio and subtitle
// languages (of mine, «Мои языки», when given; at most three each).
func (v videoFacts) badges(lang string, mine []string) []string {
	out := nonEmpty(v.res, v.hdr)
	if s := langList(v.audio, 3, mine); s != "" {
		out = append(out, i18n.TL(lang, "Звук %s", s))
	}
	if s := langList(v.subs, 3, mine); s != "" {
		out = append(out, i18n.TL(lang, "Субтитры %s", s))
	}
	return out
}

// videoBadges: quality (4K/1080p…), HDR / Dolby Vision, audio and subtitle
// languages (of mine, «Мои языки», when given).
func videoBadges(lang string, files []library.File, mine []string) []string {
	v, ok := factsOf(files)
	if !ok {
		return nil
	}
	return v.badges(lang, mine)
}

// resRank orders the quality tiers of resolution, lowest first.
var resRank = []string{"SD", "576p", "720p", "1080p", "1440p", "4K"}

// showFacts sums up the episodes of a series or a season (the files grouped
// by episode, each episode said by its first probed file, like its own
// card) so the show's badges promise what the show as a whole delivers. A
// fact is advertised when a strict majority of the probed episodes carry
// it: a language that only a stray episode or a lone season has is not
// presented as the show's (Russian subtitles on 3 of 40 episodes are not
// «Субтитры RU»), while a language most episodes have still is, even when a
// few lack it. The quality is the highest tier a majority reaches or
// exceeds (mostly 4K with a 1080p pilot is 4K; half 4K, half 1080p is
// 1080p); Dolby Vision needs a majority of its own, else HDR counts Dolby
// Vision episodes too. Languages keep the order they first appear in.
// false when no episode is probed yet.
func showFacts(files []library.File) (videoFacts, bool) {
	var order []int64
	byEp := map[int64][]library.File{}
	for _, f := range files {
		if _, ok := byEp[f.ItemID]; !ok {
			order = append(order, f.ItemID)
		}
		byEp[f.ItemID] = append(byEp[f.ItemID], f)
	}
	var eps []videoFacts
	for _, id := range order {
		if v, ok := factsOf(byEp[id]); ok {
			eps = append(eps, v)
		}
	}
	n := len(eps)
	if n == 0 {
		return videoFacts{}, false
	}
	most := func(k int) bool { return 2*k > n }
	var out videoFacts
	for i := len(resRank) - 1; i >= 0 && out.res == ""; i-- {
		k := 0
		for _, e := range eps {
			if slices.Index(resRank, e.res) >= i {
				k++
			}
		}
		if most(k) {
			out.res = resRank[i]
		}
	}
	dv, hdr := 0, 0
	for _, e := range eps {
		if e.hdr == "Dolby Vision" {
			dv++
		}
		if e.hdr != "" {
			hdr++
		}
	}
	switch {
	case most(dv):
		out.hdr = "Dolby Vision"
	case most(hdr):
		out.hdr = "HDR"
	}
	majority := func(codes func(videoFacts) []string) []string {
		count := map[string]int{}
		var seen, kept []string
		for _, e := range eps {
			for _, c := range codes(e) {
				if count[c] == 0 {
					seen = append(seen, c)
				}
				count[c]++
			}
		}
		for _, c := range seen {
			if most(count[c]) {
				kept = append(kept, c)
			}
		}
		return kept
	}
	out.audio = majority(func(v videoFacts) []string { return v.audio })
	out.subs = majority(func(v videoFacts) []string { return v.subs })
	return out, true
}

// showCache keeps the facts of each series and season until its files
// change (library.Repo.ShowFilesKey): a grid of series costs one small
// aggregate query per card, not every episode's probe.
type showCache struct {
	mu sync.Mutex
	m  map[int64]showEntry
}

type showEntry struct {
	key   string
	facts videoFacts
	ok    bool
}

// showCacheMax bounds showCache: past it the cache starts over (like
// probeCache), so a long-running server does not keep every series and
// season it ever drew.
const showCacheMax = 2048

func (c *showCache) get(id int64) (showEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	return e, ok
}

func (c *showCache) put(id int64, e showEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[id]; !ok && (c.m == nil || len(c.m) >= showCacheMax) {
		c.m = map[int64]showEntry{}
	}
	c.m[id] = e
}

// showBadges are the badges of a series or a season, from its episodes.
func (h *lib) showBadges(ctx context.Context, id int64) []string {
	key, err := h.Library.ShowFilesKey(ctx, id)
	if err != nil {
		log.Printf("api: badges %d: %v", id, err)
		return nil
	}
	e, hit := h.shows.get(id)
	if !hit || e.key != key {
		files, err := h.Library.ShowFiles(ctx, id)
		if err != nil {
			log.Printf("api: badges %d: %v", id, err)
			return nil
		}
		e = showEntry{key: key}
		e.facts, e.ok = showFacts(files)
		h.shows.put(id, e)
	}
	if !e.ok {
		return nil
	}
	return e.facts.badges(i18n.FromContext(ctx), h.myLanguages(ctx))
}

// langsTTL: how long the badges keep «Мои языки» before reading them again.
const langsTTL = 30 * time.Second

type langsCache struct {
	mu sync.Mutex
	at time.Time
	v  []string
}

// myLanguages is «Мои языки» of the settings (nil when unreadable: no filter).
func (h *lib) myLanguages(ctx context.Context) []string {
	h.langs.mu.Lock()
	defer h.langs.mu.Unlock()
	if !h.langs.at.IsZero() && time.Since(h.langs.at) < langsTTL {
		return h.langs.v
	}
	if h.DB == nil {
		return nil
	}
	s, err := LoadAppSettings(ctx, h.DB, h.SettingsDefaults)
	if err != nil {
		log.Printf("api: badges: settings: %v", err)
		return nil
	}
	h.langs.at, h.langs.v = time.Now(), s.LanguagesOrDefault()
	return h.langs.v
}

// musicBadges: format plus bit depth/sample rate (lossless) or bitrate (lossy).
func musicBadges(f library.File) []string {
	p, ok := cachedProbe(f)
	if !ok || len(p.Audio) == 0 {
		return nil
	}
	a := p.Audio[0]
	codec := strings.ToUpper(a.Codec)
	switch strings.ToLower(a.Codec) {
	case "flac", "mp3", "aac", "alac", "opus", "vorbis", "wavpack", "ape":
		if strings.EqualFold(a.Codec, "vorbis") {
			codec = "OGG"
		}
	case "":
		return nil
	default:
		if strings.HasPrefix(strings.ToLower(a.Codec), "pcm_") {
			codec = "WAV"
		}
	}
	lossless := a.BitDepth > 0 || codec == "FLAC" || codec == "ALAC" || codec == "WAV" || codec == "WAVPACK" || codec == "APE"
	if lossless && a.BitDepth > 0 && a.SampleRate > 0 {
		khz := strconv.FormatFloat(float64(a.SampleRate)/1000, 'f', -1, 64)
		return []string{fmt.Sprintf("%s %d/%s", codec, a.BitDepth, khz)}
	}
	if !lossless && a.BitRate > 0 {
		return []string{fmt.Sprintf("%s %d", codec, int((a.BitRate+500)/1000))}
	}
	return []string{codec}
}

func filesSize(files []library.File) (n int64) {
	for _, f := range files {
		n += f.Size
	}
	return n
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// bookBadges: language and format. audiobookBadges: language and size.
func bookBadges(m bookMeta, files []library.File) []string {
	f := ""
	if len(files) > 0 {
		f = bookFormat(files[0].Path)
	}
	return nonEmpty(langBadge(m.Lang), f)
}

func audiobookBadges(lang string, m bookMeta, files []library.File) []string {
	return nonEmpty(langBadge(m.Lang), sizeBadge(lang, filesSize(files)))
}

// badges computes the badges of an item for its card; nil when none are known.
func (h *lib) badges(ctx context.Context, it library.Item) []string {
	switch it.Kind {
	case library.KindMovie, library.KindEpisode:
		files, err := h.filesOf(ctx, it.ID)
		if err != nil {
			log.Printf("api: badges %d: %v", it.ID, err)
			return nil
		}
		return videoBadges(i18n.FromContext(ctx), files, h.myLanguages(ctx))
	case library.KindSeries, library.KindSeason:
		return h.showBadges(ctx, it.ID)
	case library.KindAlbum:
		tracks, err := h.Library.AlbumTracks(ctx, it.ID)
		if err != nil || len(tracks) == 0 {
			return nil
		}
		fm, err := h.Library.FilesByID(ctx, []int64{tracks[0].FileID})
		if err != nil {
			return nil
		}
		return musicBadges(fm[tracks[0].FileID])
	case library.KindBook, library.KindAudiobook:
		files, err := h.filesOf(ctx, it.ID)
		if err != nil {
			log.Printf("api: badges %d: %v", it.ID, err)
			return nil
		}
		if it.Kind == library.KindBook {
			return bookBadges(bookMetaOf(it), files)
		}
		return audiobookBadges(i18n.FromContext(ctx), bookMetaOf(it), files)
	}
	return nil
}
