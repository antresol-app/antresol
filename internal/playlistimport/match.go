package playlistimport

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
	"github.com/antresol-app/antresol/internal/release"
)

// Review reasons (shown as is).
const (
	ReasonNoExact   = "Нет точного совпадения"
	ReasonAmbiguous = "Несколько исполнителей с таким именем"
	ReasonNotInMB   = "Не найдено в MusicBrainz"
)

// durationSlack is how far a recording's length may be from the
// service's (when both are known).
const durationSlack = 10_000 // ms

// Recordings is the part of *musicbrainz.Client matching uses (its
// limiter keeps MusicBrainz at 1 request/s).
type Recordings interface {
	MatchCandidates(ctx context.Context, q musicbrainz.TrackQuery) ([]musicbrainz.TrackCandidate, error)
	// ArtistsNamed finds the artists a romanized name stands for (an
	// alias, a transliteration: «Bi-2» is «Би-2»).
	ArtistsNamed(ctx context.Context, name string) ([]musicbrainz.Artist, error)
}

// NewMatcher matches songs against MusicBrainz. When unsure it sends the
// song to review: a wrong album, a live take, a cover, a karaoke version
// or a same-named artist is worse than a question. A song asked twice in
// one job (liked and in a playlist) costs one request.
func NewMatcher(mb Recordings) Matcher { return &mbMatcher{mb: mb} }

type mbMatcher struct {
	mb Recordings

	mu    sync.Mutex
	job   int64
	cache map[string]Decision // by norm artist|title|album, of job
}

// Match decides one song. A MusicBrainz error that is no answer
// (offline, 5xx, 429) is returned: the song stays pending.
func (m *mbMatcher) Match(ctx context.Context, t Track) (Decision, error) {
	key := musicbrainz.Norm(t.Artist) + "|" + musicbrainz.Norm(t.Title) + "|" + musicbrainz.Norm(t.Album)
	m.mu.Lock()
	if m.job != t.JobID {
		m.job, m.cache = t.JobID, map[string]Decision{}
	}
	d, ok := m.cache[key]
	m.mu.Unlock()
	if ok {
		return d, nil
	}
	d, err := m.decide(ctx, t)
	if err != nil {
		if ctx.Err() != nil || metadata.Classify(err) != metadata.Miss {
			return Decision{}, err
		}
		d = review(ReasonNotInMB) // a definite «no» (404, another 4xx)
	}
	m.mu.Lock()
	if m.job == t.JobID {
		m.cache[key] = d
	}
	m.mu.Unlock()
	return d, nil
}

func review(reason string) Decision { return Decision{State: TrackReview, Reason: reason} }

func (m *mbMatcher) decide(ctx context.Context, t Track) (Decision, error) {
	// a version the service names («(Live at Wembley)», « - Acoustic») is
	// never matched to another take: only to that version on the album
	// the service names
	if musicbrainz.VersionTagged(t.Title) {
		return m.versionMatch(ctx, t)
	}
	if t.ISRC != "" {
		cs, err := m.mb.MatchCandidates(ctx, musicbrainz.TrackQuery{ISRC: t.ISRC})
		if err != nil {
			return Decision{}, err
		}
		// the ISRC names the recording, not whether it is the studio take:
		// only clean candidates count (no version, the length close, not
		// on a live album). When the ISRC's recordings are all versions —
		// a live take on live albums only, even with no «(Live)» in the
		// titles — the song is that version: review, not the studio album
		// by the text search.
		var ok []musicbrainz.TrackCandidate
		found := false
		for _, c := range cs {
			id, match := artistMatches(t.Artist, c)
			if !match {
				continue
			}
			found = true
			if sameVersion(t, c) && closeLength(t, c) && !c.Live {
				c.ArtistID = id
				ok = append(ok, c)
			}
		}
		if found && len(ok) == 0 {
			return review(ReasonNoExact), nil
		}
		if d, ok := choose(t, ok); ok {
			return d, nil
		}
		// a clean recording on no album of the rules (compilations only):
		// the text search may find the studio album
	}
	d, seen, err := m.byText(ctx, t, lookup{Artist: t.Artist, Title: t.Title, Album: t.Album}, nil)
	if err != nil || d.State == TrackMatched || d.Reason == ReasonAmbiguous {
		return d, err
	}
	// Nothing matched as the service names the song: the same rules
	// again with its title and album cleaned — «(feat. …)», «(Radio
	// Edit)», « - Single» dropped (a cut tag only with the lengths close)
	// — and its first artist alone…
	l := lookupOf(t)
	if l != (lookup{Artist: t.Artist, Title: t.Title, Album: t.Album}) {
		d2, seen2, err := m.byText(ctx, t, l, nil)
		if err != nil || d2.State == TrackMatched || d2.Reason == ReasonAmbiguous {
			return d2, err
		}
		if seen2 && !seen {
			d = d2
		}
		seen = seen || seen2
	}
	if seen {
		return d, nil // the artist is there; the song is not
	}
	// …then under the name MusicBrainz gives the artist: a service's
	// romanization (Bi-2, Alisa, Igor Talkov) is an alias of «Би-2»…
	as, err := m.mb.ArtistsNamed(ctx, primaryArtist(t.Artist))
	if err != nil {
		return Decision{}, err
	}
	var other []musicbrainz.Artist // spelled another way (the others were searched)
	for _, a := range as {
		if musicbrainz.Norm(a.Name) != musicbrainz.Norm(l.Artist) {
			other = append(other, a)
		}
	}
	switch {
	case len(other) == 0:
		return d, nil
	case len(other) > 1:
		return review(ReasonAmbiguous), nil // the name stands for several artists
	}
	al := l
	al.Artist = other[0].Name
	d3, seen3, err := m.byText(ctx, t, al, map[string]bool{other[0].ID: true})
	if err != nil || d3.State == TrackMatched || d3.Reason == ReasonAmbiguous || seen3 {
		return d3, err
	}
	return d, nil
}

// versionMatch matches a song whose title names a version («Eminence
// Front (Live)» on «It's Hard», «Hey Jude (Live)» on «Back in the U.S.:
// Live 2002») only to a recording of the same version words (its title,
// its disambiguation, a live album) on the album the service names, of
// the artist, of a length known and close; else review.
func (m *mbMatcher) versionMatch(ctx context.Context, t Track) (Decision, error) {
	base, words := versionBase(t.Title)
	album := musicbrainz.Norm(musicbrainz.StripEditions(cleanAlbum(t.Album)))
	if base == "" || album == "" || t.DurationMs <= 0 {
		return review(ReasonNoExact), nil
	}
	cs, err := m.mb.MatchCandidates(ctx, musicbrainz.TrackQuery{Artist: primaryArtist(t.Artist), Title: base, Album: t.Album,
		DurationMs: t.DurationMs})
	if err != nil {
		return Decision{}, err
	}
	var ok []musicbrainz.TrackCandidate
	for _, c := range cs {
		id, match := artistMatches(t.Artist, c)
		if !match || c.DurationMs <= 0 || !closeLength(t, c) || !ownGroupOf(id, c) {
			continue
		}
		cb, cw := versionBase(c.Title)
		have := append(cw, musicbrainz.VersionWords(c.Disambiguation)...)
		if c.Live {
			have = append(have, "live")
		}
		if musicbrainz.Norm(cb) != musicbrainz.Norm(base) || !sameWords(words, have) ||
			musicbrainz.Norm(musicbrainz.StripEditions(c.Group.Title)) != album {
			continue
		}
		c.ArtistID = id
		ok = append(ok, c)
	}
	if len(ok) == 0 || artists(ok) > 1 {
		return review(ReasonNoExact), nil
	}
	return decision(t, ok[0].Group, ok), nil
}

// ownGroupOf: the release is credited to the artist id (or to nobody known).
func ownGroupOf(id string, c musicbrainz.TrackCandidate) bool {
	return c.Group.ArtistID == "" || c.Group.ArtistID == id
}

// versionBase splits a title into its name and the version words of its
// suffixes («Sad But True (Live at Wembley) [2021 Remaster]» → «Sad But
// True», [live]); a version word of the name itself («Live Forever») is
// not one of them.
func versionBase(title string) (string, []string) {
	s := strings.TrimSpace(title)
	var words []string
	for {
		head, tail, ok := release.LastSuffix(s)
		if !ok || head == "" || head == s {
			return s, words
		}
		if vw := musicbrainz.VersionWords(tail); len(vw) > 0 {
			words = append(words, vw...)
		} else if !featTag(tail) && !cutTag(tail) && musicbrainz.StripEditions(s) == s {
			return s, words
		}
		s = head
	}
}

// sameWords: a and b hold the same words (any order, any repeats).
func sameWords(a, b []string) bool {
	for _, w := range a {
		if !slices.Contains(b, w) {
			return false
		}
	}
	for _, w := range b {
		if !slices.Contains(a, w) {
			return false
		}
	}
	return len(a) > 0
}

// lookup is how a song is searched: its artist, title and album as the
// service names them, or cleaned (lookupOf).
type lookup struct{ Artist, Title, Album string }

// lookupOf is the song's cleaned lookup: the first artist, the title
// without «feat.» credits and cut tags, the album without them and its
// « - Single» / « - EP» mark.
func lookupOf(t Track) lookup {
	title, _ := cleanTitle(t.Title)
	return lookup{Artist: strings.TrimSpace(primaryArtist(t.Artist)), Title: title, Album: cleanAlbum(t.Album)}
}

// byText searches the song by l and applies the rules: the candidates of
// the service's artist (or of an artist ids names: the one l spells
// another way) and the same song. With ids (the alias search) both
// lengths must be known, and a song two artists have goes to review.
// seen: a candidate of the artist was among them (the artist is in
// MusicBrainz).
func (m *mbMatcher) byText(ctx context.Context, t Track, l lookup, ids map[string]bool) (Decision, bool, error) {
	cs, err := m.mb.MatchCandidates(ctx, musicbrainz.TrackQuery{Artist: l.Artist, Title: l.Title, Album: l.Album, DurationMs: t.DurationMs})
	if err != nil {
		return Decision{}, false, err
	}
	if len(cs) == 0 {
		return review(ReasonNotInMB), false, nil
	}
	plain := l.Title == t.Title
	var ok []musicbrainz.TrackCandidate
	seen := false
	for _, c := range cs {
		id, match := artistMatches(t.Artist, c)
		if !match {
			id, match = artistByID(ids, c)
		}
		if !match {
			continue
		}
		seen = true
		if ids != nil && (t.DurationMs <= 0 || c.DurationMs <= 0) {
			continue // another spelling of the artist: only with both lengths known
		}
		if plain && sameSong(t, c) || !plain && sameCleanSong(t, l, c) {
			c.ArtistID = id
			ok = append(ok, c)
		}
	}
	if len(ok) == 0 {
		return review(ReasonNoExact), seen, nil
	}
	if ids != nil && artists(ok) > 1 {
		// the alias's artist and one spelled as the service spells it
		// both have the song
		return review(ReasonAmbiguous), true, nil
	}
	if d, ok := choose(t, ok); ok {
		return d, true, nil
	}
	if artists(ok) > 1 && artists(byServiceAlbum(t, ok)) != 1 {
		return review(ReasonAmbiguous), true, nil
	}
	return review(ReasonNoExact), true, nil
}

// artistByID: a credited artist of the recording is one of ids.
func artistByID(ids map[string]bool, c musicbrainz.TrackCandidate) (string, bool) {
	if ids[c.ArtistID] {
		return c.ArtistID, true
	}
	for _, a := range c.Credits {
		if ids[a.ID] {
			return a.ID, true
		}
	}
	return "", false
}

// sameCleanSong is sameSong for a cleaned lookup: both titles cleaned the
// same way agree; a cut tag dropped from either («(Radio Edit)», «(Long
// Version)») is the same recording only with both lengths known and
// close.
func sameCleanSong(t Track, l lookup, c musicbrainz.TrackCandidate) bool {
	ct, cut := cleanTitle(c.Title)
	_, ownCut := cleanTitle(t.Title)
	if musicbrainz.Norm(musicbrainz.StripEditions(ct)) != musicbrainz.Norm(musicbrainz.StripEditions(l.Title)) {
		return false
	}
	if (cut || ownCut) && (t.DurationMs <= 0 || c.DurationMs <= 0) {
		return false
	}
	return sameVersion(t, c) && closeLength(t, c)
}

// cutWords are the words of a title suffix that names a cut or a pressing
// of the recording, not another version: «(Radio Edit)», «(LP Version)»,
// «(Long Edit)», «[Single Version]», «(7" Mix)». Lengths tell whether
// it is the recording MusicBrainz has.
var cutWords = []string{"radio", "edit", "edited", "version", "single", "album", "lp", "long", "short", "extended", "original",
	"mix", "full", "length", "inch", "mono", "stereo", "reissue", "bonus", "track"}

// cleanTitle drops a title's trailing «feat.» credits and cut tags (and
// edition suffixes between them); cut: a cut tag was dropped.
func cleanTitle(s string) (string, bool) {
	s = strings.TrimSpace(s)
	cut := false
	for {
		head, tail, ok := release.LastSuffix(s)
		switch {
		case !ok || head == "":
		case featTag(tail):
			s = head
			continue
		case cutTag(tail):
			s, cut = head, true
			continue
		case musicbrainz.StripEditions(s) != s:
			s = musicbrainz.StripEditions(s)
			continue
		}
		break
	}
	// «Song feat. Artist» with no brackets
	low := strings.ToLower(s)
	for _, sep := range []string{" feat. ", " feat ", " ft. ", " featuring "} {
		if i := strings.Index(low, sep); i > 0 {
			s, low = strings.TrimSpace(s[:i]), low[:i]
		}
	}
	return s, cut
}

// cleanAlbum is cleanTitle of an album, its « - Single» / « - EP» mark
// dropped too.
func cleanAlbum(s string) string {
	s, _ = cleanTitle(s)
	for {
		head, tail, ok := release.LastSuffix(s)
		if !ok || head == "" || !slices.Contains([]string{"single", "ep"}, musicbrainz.Norm(tail)) {
			return s
		}
		s, _ = cleanTitle(head)
	}
}

// featTag: a suffix crediting another artist («feat. Vijee», «ft. X»).
func featTag(tail string) bool {
	low := strings.ToLower(strings.TrimSpace(tail))
	for _, p := range []string{"feat.", "feat ", "ft.", "ft ", "featuring "} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// cutTag: a suffix of cut words only (and numbers: «7"», «2018»), no
// version word.
func cutTag(tail string) bool {
	words := strings.Fields(musicbrainz.Norm(tail))
	named := false
	for _, w := range words {
		switch {
		case strings.Trim(w, "0123456789") == "":
		case slices.Contains(cutWords, w):
			named = true
		default:
			return false
		}
	}
	return named
}

// sameSong: the title matches without edition suffixes, the recording is
// no other version and, when both are known, the lengths are within
// durationSlack.
func sameSong(t Track, c musicbrainz.TrackCandidate) bool {
	return musicbrainz.Norm(musicbrainz.StripEditions(c.Title)) == musicbrainz.Norm(musicbrainz.StripEditions(t.Title)) &&
		sameVersion(t, c) && closeLength(t, c)
}

// sameVersion: the recording's title has no version word the service
// title lacks («Live Forever» has «live»), and its disambiguation none at
// all («live, 1994-…» is a live take even of «Live Forever»; a service
// title naming a version went to review before).
func sameVersion(t Track, c musicbrainz.TrackCandidate) bool {
	if len(musicbrainz.VersionWords(c.Disambiguation)) > 0 {
		return false
	}
	own := musicbrainz.VersionWords(t.Title)
	for _, w := range musicbrainz.VersionWords(c.Title) {
		if !slices.Contains(own, w) {
			return false
		}
	}
	return true
}

// closeLength: the lengths are within durationSlack, or one is unknown.
func closeLength(t Track, c musicbrainz.TrackCandidate) bool {
	if t.DurationMs <= 0 || c.DurationMs <= 0 {
		return true
	}
	d := t.DurationMs - c.DurationMs
	return d <= durationSlack && d >= -durationSlack
}

// artistMatches: the recording's whole credit is the service's artist,
// or one credited artist is the service's primary artist (the part before
// «feat.», «,», «&»…). id is the MBID of the artist that matched (the
// first credited one for a whole credit): the candidates' ArtistID from
// then on, so that a recording of several artists counts as the
// service's artist and only same-named artists are told apart.
func artistMatches(service string, c musicbrainz.TrackCandidate) (id string, ok bool) {
	ns := musicbrainz.Norm(service)
	if ns == "" {
		return "", false
	}
	if ns == musicbrainz.Norm(c.Artist) {
		return c.ArtistID, true
	}
	p := musicbrainz.Norm(primaryArtist(service))
	for _, a := range c.Credits {
		if musicbrainz.Norm(a.Name) == p {
			return a.ID, true
		}
	}
	return "", false
}

// ruAnd joins artists in Russian names («Артист и Другой»).
const ruAnd = "и"

func primaryArtist(s string) string {
	low := strings.ToLower(s)
	cut := len(s)
	for _, sep := range []string{",", ";", " feat.", " feat ", " ft.", " ft ", " featuring ", " & ", " x ", " with ", " " + ruAnd + " "} {
		if i := strings.Index(low, sep); i > 0 && i < cut {
			cut = i
		}
	}
	return s[:cut]
}

// artists counts the different artists (by MBID) of the candidates.
func artists(cs []musicbrainz.TrackCandidate) int {
	seen := map[string]bool{}
	for _, c := range cs {
		seen[c.ArtistID] = true
	}
	return len(seen)
}

// choose picks the album among candidates that passed the rules: one
// artist (several are told apart only by the service's album), then the
// album choice; ok false when nothing fits.
func choose(t Track, cs []musicbrainz.TrackCandidate) (Decision, bool) {
	if len(cs) == 0 {
		return Decision{}, false
	}
	if artists(cs) > 1 {
		by := byServiceAlbum(t, cs)
		if artists(by) != 1 {
			return Decision{}, false
		}
		id := by[0].ArtistID
		cs = slices.DeleteFunc(slices.Clone(cs), func(c musicbrainz.TrackCandidate) bool { return c.ArtistID != id })
	}
	g, ok := chooseAlbum(t, cs)
	if !ok {
		return Decision{}, false
	}
	return decision(t, g, cs), true
}

// byServiceAlbum: the candidates on the album the service names (album
// choice rule 1).
func byServiceAlbum(t Track, cs []musicbrainz.TrackCandidate) []musicbrainz.TrackCandidate {
	album := musicbrainz.Norm(cleanAlbum(t.Album)) // « - Single», «(feat. …)» dropped
	if album == "" {
		return nil
	}
	var out []musicbrainz.TrackCandidate
	for _, c := range cs {
		g := c.Group
		if musicbrainz.Norm(musicbrainz.StripEditions(g.Title)) == album && ownGroup(c) &&
			slices.Contains([]string{"Album", "EP", "Single"}, g.PrimaryType) &&
			!slices.ContainsFunc(g.SecondaryTypes, func(s string) bool { return s == "Live" || s == "Compilation" || s == "Karaoke" }) {
			out = append(out, c)
		}
	}
	return out
}

// ownGroup: the release is credited to the service's artist (not a
// various-artists or another artist's album).
func ownGroup(c musicbrainz.TrackCandidate) bool {
	return c.Group.ArtistID == "" || c.Group.ArtistID == c.ArtistID
}

// chooseAlbum is the album choice for one artist's candidates (in release
// date order): (1) the service's album when it is a studio Album, EP or
// Single; (2) else the earliest official Album with no secondary type;
// (3) else the earliest EP or Single; (4) else the artist's own compilation
// the service names. Any other compilation never wins.
func chooseAlbum(t Track, cs []musicbrainz.TrackCandidate) (musicbrainz.ReleaseGroup, bool) {
	if by := byServiceAlbum(t, cs); len(by) > 0 {
		return by[0].Group, true
	}
	for _, types := range [][]string{{"Album"}, {"EP", "Single"}} {
		for _, c := range cs {
			if ownGroup(c) && slices.Contains(types, c.Group.PrimaryType) && len(c.Group.SecondaryTypes) == 0 {
				return c.Group, true
			}
		}
	}
	// (4) the recording is on no studio release: the artist's own
	// compilation the service names (Silver Convention's «Fly Robin
	// Fly»), never a various-artists one
	album := musicbrainz.Norm(cleanAlbum(t.Album))
	for _, c := range cs {
		g := c.Group
		if album != "" && musicbrainz.Norm(musicbrainz.StripEditions(g.Title)) == album && ownGroup(c) && g.ArtistID != "" &&
			slices.Contains(g.SecondaryTypes, "Compilation") &&
			!slices.ContainsFunc(g.SecondaryTypes, func(s string) bool { return s == "Live" || s == "Karaoke" || s == "DJ-mix" }) {
			return g, true
		}
	}
	return musicbrainz.ReleaseGroup{}, false
}

// decision is the match on group g: the recording on it closest in
// length (then the most released), the group's earliest year.
func decision(t Track, g musicbrainz.ReleaseGroup, cs []musicbrainz.TrackCandidate) Decision {
	var best *musicbrainz.TrackCandidate
	year := 0
	off := func(c *musicbrainz.TrackCandidate) int64 {
		if t.DurationMs <= 0 || c.DurationMs <= 0 {
			return durationSlack + 1
		}
		return max(t.DurationMs-c.DurationMs, c.DurationMs-t.DurationMs)
	}
	for i := range cs {
		c := &cs[i]
		if c.Group.ID != g.ID {
			continue
		}
		if c.Group.Year > 0 && (year == 0 || c.Group.Year < year) {
			year = c.Group.Year
		}
		if best == nil || off(c) < off(best) || off(c) == off(best) && c.Releases > best.Releases {
			best = c
		}
	}
	return Decision{State: TrackMatched, RecordingID: best.RecordingID, ReleaseGroup: g.ID, Title: best.Title,
		Artist: g.Artist, Album: g.Title, Year: year}
}
