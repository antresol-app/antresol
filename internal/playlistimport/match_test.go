package playlistimport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
)

const metallicaID = "65f4f0c5-ef9e-490c-aee3-909e7ae6b2ab"

// recording is a made-up MusicBrainz recording added to a recorded answer
// (the cases the real data of a song lacks: a karaoke take, a tribute
// band, a second artist of the same name).
func recording(id, title, disamb string, length int, artist, artistID, group, groupTitle, groupType, date string, secondary ...string) map[string]any {
	rg := map[string]any{"id": group, "title": groupTitle, "primary-type": groupType}
	if len(secondary) > 0 {
		rg["secondary-types"] = secondary
	}
	credit := []any{map[string]any{"name": artist, "artist": map[string]any{"id": artistID, "name": artist}}}
	return map[string]any{"id": id, "score": 100, "title": title, "disambiguation": disamb, "length": length,
		"artist-credit": credit,
		"releases": []any{map[string]any{"id": "rel-" + id, "title": groupTitle, "status": "Official", "date": date,
			"artist-credit": credit, "release-group": rg}}}
}

// mbServer answers the recording search with a recorded file of the
// musicbrainz package (answer(query): its name, "" = nothing found) plus
// extra recordings, the artist search with no artist; it counts the
// requests.
func mbServer(t *testing.T, answer func(q string) string, extra ...map[string]any) (*musicbrainz.Client, *atomic.Int32) {
	t.Helper()
	return mbServerFull(t, func(q string) []any {
		var recs []any
		if f := answer(q); f != "" {
			raw := map[string]any{}
			b, err := os.ReadFile(filepath.Join("..", "metadata", "musicbrainz", "testdata", f))
			if err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal(b, &raw); err != nil {
				t.Error(err)
			}
			recs = raw["recordings"].([]any)
		}
		for _, x := range extra {
			recs = append(recs, x)
		}
		return recs
	}, nil)
}

// mbServerFull answers the recording search with recs(query) and the
// artist search with artists(query) (nil: none); it counts the requests.
func mbServerFull(t *testing.T, recs, artists func(q string) []any) (*musicbrainz.Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		q := r.URL.Query().Get("query")
		switch r.URL.Path {
		case "/ws/2/recording":
			out := recs(q)
			if out == nil {
				out = []any{}
			}
			json.NewEncoder(w).Encode(map[string]any{"recordings": out})
		case "/ws/2/artist":
			out := []any{}
			if artists != nil {
				out = append(out, artists(q)...)
			}
			json.NewEncoder(w).Encode(map[string]any{"artists": out})
		default:
			t.Errorf("request %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return &musicbrainz.Client{BaseURL: srv.URL + "/ws/2", Limiter: &metadata.Limiter{}}, &n
}

func file(name string) func(string) string { return func(string) string { return name } }

// The other «Sad But True»s: a karaoke take, a tribute band, Metallica's
// karaoke version, all on groups titled «Metallica» released earlier.
var sadButTrueOthers = []map[string]any{
	recording("karaoke-1", "Sad But True", "", 324000, "Karaoke Hits", "karaoke-hits", "rg-karaoke", "Metallica", "Album", "1990"),
	recording("tribute-1", "Sad But True", "", 324666, "Metallica (tribute)", "tribute-band", "rg-tribute", "Metallica", "Album", "1990"),
	recording("karaoke-2", "Sad But True", "karaoke", 324666, "Metallica", metallicaID, "rg-karaoke-2", "Metallica", "Album", "1989"),
	recording("wembley-1", "Sad But True (Live at Wembley)", "", 334000, "Metallica", metallicaID, "rg-wembley", "Live at Wembley", "Album", "1992", "Live"),
}

func match(t *testing.T, m Matcher, tr Track) Decision {
	t.Helper()
	if tr.JobID == 0 {
		tr.JobID = 1
	}
	d, err := m.Match(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMatchByISRCChoosesStudioAlbum(t *testing.T) {
	mb, n := mbServer(t, file("recordings-isrc-USEE10001993.json"))
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", ISRC: "USEE10001993"})
	if d.State != TrackMatched || d.Album != "Metallica" || d.Year != 1991 || d.Artist != "Metallica" ||
		d.RecordingID != "a105dba3-a35b-464f-8f46-b18c21c13809" || d.ReleaseGroup == "" || n.Load() != 1 {
		t.Fatalf("%+v (%d requests)", d, n.Load())
	}
}

// The ISRC names a recording, but not whether it is a version: a live
// take on the studio album's group (a deluxe edition's bonus disc), or
// a length far from the service's, goes to review.
func TestMatchISRCRejectsLiveTake(t *testing.T) {
	live := recording("live-1", "Sad But True", "live, 1993-03-01: Mexico City", 324666, "Metallica", metallicaID,
		"rg-studio", "Metallica", "Album", "1991")
	mb, n := mbServer(t, file(""), live)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", Album: "Live Shit: Binge & Purge",
		ISRC: "USEE19900001", DurationMs: 324666 + 40000})
	if d.State != TrackReview || d.Reason != "Нет точного совпадения" || n.Load() != 1 {
		t.Fatalf("live ISRC: %+v (%d requests)", d, n.Load())
	}
	// a studio recording, but 25 s longer than the service's song
	studio := recording("studio-1", "Sad But True", "", 324666, "Metallica", metallicaID, "rg-studio", "Metallica", "Album", "1991")
	mb, _ = mbServer(t, file(""), studio)
	d = match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", ISRC: "USEE19900001", DurationMs: 324666 + 25000})
	if d.State != TrackReview || d.Reason != "Нет точного совпадения" {
		t.Fatalf("ISRC 25 s off: %+v", d)
	}
	d = match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", ISRC: "USEE19900001", DurationMs: 324666 + 3000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-studio" {
		t.Fatalf("ISRC 3 s off: %+v", d)
	}
}

// An ISRC recording found only on live albums is a live take even with
// no «(Live)» in either title: review, never the studio album by the
// text search.
func TestMatchISRCOnlyOnLiveAlbums(t *testing.T) {
	live := recording("live-2", "Sad But True", "", 324666, "Metallica", metallicaID,
		"rg-live-shit", "Live Shit: Binge & Purge", "Album", "1993", "Live")
	mb, n := mbServer(t, file(""), live)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", Album: "Live Shit: Binge & Purge",
		ISRC: "USEE19930001", DurationMs: 324666})
	if d.State != TrackReview || d.Reason != "Нет точного совпадения" || n.Load() != 1 {
		t.Fatalf("%+v (%d requests)", d, n.Load())
	}
}

// Among the ISRC's recordings a clean studio one wins over a live one.
func TestMatchISRCPrefersStudioCandidate(t *testing.T) {
	live := recording("live-1", "Sad But True", "live, 1993-03-01: Mexico City", 330000, "Metallica", metallicaID,
		"rg-live-shit", "Live Shit: Binge & Purge", "Album", "1993", "Live")
	studio := recording("studio-1", "Sad But True", "", 324666, "Metallica", metallicaID, "rg-studio", "Metallica", "Album", "1991")
	mb, n := mbServer(t, file(""), live, studio)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Metallica", Title: "Sad But True", ISRC: "USEE19910001", DurationMs: 325000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-studio" || d.RecordingID != "studio-1" || n.Load() != 1 {
		t.Fatalf("%+v (%d requests)", d, n.Load())
	}
}

// A version word in the song's own name («Live Forever») does not excuse
// a live take (its disambiguation) as the recording.
func TestMatchOwnVersionWordKeepsStudioTake(t *testing.T) {
	studio := recording("studio-1", "Live Forever", "", 276000, "Oasis", "oasis", "rg-dm", "Definitely Maybe", "Album", "1994")
	live := recording("live-1", "Live Forever", "live, 1994-08-13: Glastonbury", 276500, "Oasis", "oasis", "rg-dm", "Definitely Maybe", "Album", "1994")
	mb, _ := mbServer(t, file(""), studio, live)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Oasis", Title: "Live Forever", Album: "Definitely Maybe", DurationMs: 276400})
	if d.State != TrackMatched || d.RecordingID != "studio-1" {
		t.Fatalf("%+v", d)
	}
}

func TestMatchRejectsLiveKaraokeAndOtherArtist(t *testing.T) {
	mb, _ := mbServer(t, file("recordings-sad-but-true-match.json"), sadButTrueOthers...)
	m := NewMatcher(mb)
	d := match(t, m, Track{Key: "a", Artist: "Metallica", Title: "Sad But True", Album: "Metallica", DurationMs: 324000})
	if d.State != TrackMatched || d.Album != "Metallica" || d.Year != 1991 || d.Artist != "Metallica" ||
		d.RecordingID != "a105dba3-a35b-464f-8f46-b18c21c13809" {
		t.Fatalf("studio song: %+v", d)
	}
	for _, rg := range []string{"rg-karaoke", "rg-tribute", "rg-karaoke-2", "rg-wembley"} {
		if d.ReleaseGroup == rg {
			t.Fatalf("chose %s", rg)
		}
	}
	// a live take is never matched on its own, even when one has its title
	d = match(t, m, Track{Key: "b", Artist: "Metallica", Title: "Sad But True (Live at Wembley)", Album: "Live at Wembley"})
	if d.State != TrackReview || d.Reason != "Нет точного совпадения" {
		t.Fatalf("live: %+v", d)
	}
	// a version marker sharing its suffix with an edition word stays a version
	for i, title := range []string{"Sad But True - Live", "Sad But True - Live / Remastered 2021",
		"Sad But True (Live - 2021 Remaster)", "Sad But True - Acoustic / Remastered", "Sad But True (Remastered Live Version)"} {
		d = match(t, m, Track{Key: "c" + strconv.Itoa(i), Artist: "Metallica", Title: title, Album: "Metallica"})
		if d.State != TrackReview || d.Reason != "Нет точного совпадения" {
			t.Fatalf("%q: %+v", title, d)
		}
	}
	// without the album: still the studio album, never the tribute's
	d = match(t, m, Track{Key: "d", Artist: "Metallica", Title: "Sad But True (Remastered 2021)"})
	if d.State != TrackMatched || d.Album != "Metallica" || d.Year != 1991 {
		t.Fatalf("no album: %+v", d)
	}
}

func TestMatchPrefersServiceAlbum(t *testing.T) {
	mb, _ := mbServer(t, file("recordings-kino-gruppa-krovi.json"))
	m := NewMatcher(mb)
	d := match(t, m, Track{Key: "a", Artist: "Кино", Title: "Группа крови", Album: "Группа крови"})
	if d.State != TrackMatched || d.Album != "Группа крови" || d.Year != 1988 || d.Artist != "Кино" {
		t.Fatalf("service album: %+v", d)
	}
	studio := d.ReleaseGroup
	// a compilation named by the service never wins over the studio album
	d = match(t, m, Track{Key: "b", Artist: "Кино", Title: "Группа крови", Album: "Легенды русского рока"})
	if d.State != TrackMatched || d.ReleaseGroup != studio || d.Album != "Группа крови" {
		t.Fatalf("compilation: %+v", d)
	}
	// «Последний герой» is a studio album holding another recording of the song
	d = match(t, m, Track{Key: "c", Artist: "Кино", Title: "Группа крови", Album: "Последний герой"})
	if d.State != TrackMatched || d.Album != "Последний герой" || d.Year != 1989 {
		t.Fatalf("other studio album: %+v", d)
	}
}

func TestMatchDurationGuard(t *testing.T) {
	mb, _ := mbServer(t, file("recordings-sad-but-true-match.json"), sadButTrueOthers...)
	m := NewMatcher(mb)
	d := match(t, m, Track{Key: "a", Artist: "Metallica", Title: "Sad But True", Album: "Metallica", DurationMs: 324666 + 25000})
	if d.State != TrackReview || d.Reason != "Нет точного совпадения" {
		t.Fatalf("25 s off: %+v", d)
	}
	d = match(t, m, Track{Key: "b", Artist: "Metallica", Title: "Sad But True", Album: "Metallica", DurationMs: 324666 + 3000, JobID: 2})
	if d.State != TrackMatched || d.Album != "Metallica" {
		t.Fatalf("3 s off: %+v", d)
	}
}

func TestMatchAmbiguousArtistGoesToReview(t *testing.T) {
	other := recording("kino-other-1", "Группа крови", "", 280000, "Кино", "aaaaaaaa-0000-4000-8000-000000000001",
		"rg-other-kino", "Кино", "Album", "1995")
	mb, _ := mbServer(t, file("recordings-kino-gruppa-krovi.json"), other)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Кино", Title: "Группа крови"})
	if d.State != TrackReview || d.Reason != "Несколько исполнителей с таким именем" {
		t.Fatalf("%+v", d)
	}
}

func TestMatchNotInMusicBrainz(t *testing.T) {
	mb, _ := mbServer(t, file(""))
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Неизвестные", Title: "Песня", Album: "Альбом"})
	if d.State != TrackReview || d.Reason != "Не найдено в MusicBrainz" {
		t.Fatalf("%+v", d)
	}
}

func TestMatchCachesPerJob(t *testing.T) {
	mb, n := mbServer(t, file("recordings-sad-but-true-match.json"))
	m := NewMatcher(mb)
	a := match(t, m, Track{JobID: 7, Key: "liked:1", Artist: "Metallica", Title: "Sad But True", Album: "Metallica"})
	b := match(t, m, Track{JobID: 7, Key: "playlist:9", Artist: "metallica", Title: "Sad but True", Album: "METALLICA"})
	if n.Load() != 1 || a.ReleaseGroup != b.ReleaseGroup || b.State != TrackMatched {
		t.Fatalf("%d requests, %+v / %+v", n.Load(), a, b)
	}
	match(t, m, Track{JobID: 8, Key: "liked:1", Artist: "Metallica", Title: "Sad But True", Album: "Metallica"})
	if n.Load() != 2 {
		t.Fatalf("another job reused the cache: %d requests", n.Load())
	}
}

// MusicBrainz offline: the song stays pending and matching pauses.
func TestMatchOfflinePauses(t *testing.T) {
	mb, _ := mbServer(t, file(""))
	srvDown := httptest.NewServer(http.NotFoundHandler())
	srvDown.Close()
	mb.BaseURL = srvDown.URL + "/ws/2"
	if _, err := NewMatcher(mb).Match(context.Background(), Track{JobID: 1, Key: "a", Artist: "Кино", Title: "Кукушка"}); err == nil {
		t.Fatal("no error while offline")
	}

	ctx := context.Background()
	st, uid := newTestStore(t)
	id, err := st.CreateJob(ctx, uid, Snapshot{Service: "file", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}, {Key: "b", Artist: "Кино", Title: "Звезда", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'matching' WHERE id = ?`, id)
	var calls atomic.Int32
	svc := &Service{Store: st, Tick: 5 * time.Millisecond, MatchPause: time.Hour,
		Matcher: matcherFunc(func(context.Context, Track) (Decision, error) {
			calls.Add(1)
			return Decision{}, errOffline
		})}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(rctx) }()
	time.Sleep(100 * time.Millisecond) // ~20 ticks
	cancel()
	<-done
	if calls.Load() != 1 {
		t.Fatalf("%d match calls during the pause", calls.Load())
	}
	if n, err := st.Pending(ctx, id); err != nil || n != 2 {
		t.Fatalf("pending %d %v", n, err)
	}
}

// MusicBrainz answering badly for one song (5xx after retries, a broken
// body) sends it to review after a few tries instead of stalling the job.
func TestMatchKeepsFailingGoesToReview(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	id, err := st.CreateJob(ctx, uid, Snapshot{Service: "file", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'matching' WHERE id = ?`, id)
	var calls atomic.Int32
	svc := &Service{Store: st, Tick: 2 * time.Millisecond, MatchPause: time.Millisecond,
		Matcher: matcherFunc(func(context.Context, Track) (Decision, error) {
			calls.Add(1)
			return Decision{}, &metadata.HTTPError{Service: "musicbrainz", Path: "/recording", Status: 500}
		})}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitUntil(t, "the song in review", func() bool {
		p, err := st.Progress(ctx, id)
		return err == nil && p.Review == 1
	})
	if c := calls.Load(); c != maxMatchFails {
		t.Fatalf("%d tries (want %d)", c, maxMatchFails)
	}
	tr, _ := st.NextPending(ctx, id, 1)
	if len(tr) != 0 {
		t.Fatal("still pending")
	}
}

// A long run of 429/503 pauses matching with a growing backoff and never
// sends the song to review: nothing is wrong with the song.
func TestMatchOverloadBacksOffWithoutReview(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	id, err := st.CreateJob(ctx, uid, Snapshot{Service: "file", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'matching' WHERE id = ?`, id)
	var calls atomic.Int32
	status := []int{503, 429}
	svc := &Service{Store: st, Tick: time.Millisecond, MatchPause: time.Millisecond,
		Matcher: matcherFunc(func(context.Context, Track) (Decision, error) {
			n := calls.Add(1)
			return Decision{}, &metadata.HTTPError{Service: "musicbrainz", Path: "/recording", Status: status[n%2]}
		})}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(rctx) }()
	waitUntil(t, "a few tries", func() bool { return calls.Load() > 2*maxMatchFails })
	cancel()
	<-done
	if p, err := st.Progress(ctx, id); err != nil || p.Review != 0 {
		t.Fatalf("progress %+v %v", p, err)
	}
	if n, _ := st.Pending(ctx, id); n != 1 {
		t.Fatalf("pending %d", n)
	}
	// the pause doubles with every failure in a row, up to maxBackoff
	if b := (&Service{MatchPause: time.Minute}).backoff(1); b != time.Minute {
		t.Fatalf("first pause %s", b)
	}
	if b := (&Service{MatchPause: time.Minute}).backoff(3); b != 4*time.Minute {
		t.Fatalf("third pause %s", b)
	}
	if b := (&Service{MatchPause: time.Minute}).backoff(40); b != maxBackoff*time.Minute {
		t.Fatalf("capped pause %s", b)
	}
}

type matcherFunc func(context.Context, Track) (Decision, error)

func (f matcherFunc) Match(ctx context.Context, t Track) (Decision, error) { return f(ctx, t) }

var errOffline = &netError{}

type netError struct{}

func (*netError) Error() string { return "musicbrainz /recording: dial tcp: connection refused" }

// YouTube guesses («Artist - Title» from a video title, else the channel
// as the artist; no ISRC, no album) are accepted only on a MusicBrainz
// match of both the artist and the title (connect.songName's shapes).
func TestMatchYouTubeGuesses(t *testing.T) {
	mb, _ := mbServer(t, file("recordings-sad-but-true-match.json"), sadButTrueOthers...)
	m := NewMatcher(mb)
	// «Metallica - Sad But True (Official Music Video)», 5:25
	d := match(t, m, Track{Key: "youtube:a", Artist: "Metallica", Title: "Sad But True", DurationMs: 325000})
	if d.State != TrackMatched || d.Album != "Metallica" || d.Year != 1991 {
		t.Fatalf("artist and title: %+v", d)
	}
	// «Metallica - Sad But True (Live at Wembley)»: a version, review
	d = match(t, m, Track{Key: "youtube:b", Artist: "Metallica", Title: "Sad But True (Live at Wembley)"})
	if d.State != TrackReview {
		t.Fatalf("live: %+v", d)
	}
	// «Sad But True» on a cover channel: the title alone never matches
	for i, artist := range []string{"Guitar Covers Daily", "Lofi Beats"} {
		d = match(t, m, Track{Key: "youtube:c" + strconv.Itoa(i), Artist: artist, Title: "Sad But True"})
		if d.State != TrackReview {
			t.Fatalf("%s: %+v", artist, d)
		}
	}
	// a tail the noise filter keeps («(Guitar Lesson)»): no exact title, review
	d = match(t, m, Track{Key: "youtube:d", Artist: "Metallica", Title: "Sad But True (Guitar Lesson)"})
	if d.State != TrackReview {
		t.Fatalf("lesson: %+v", d)
	}
}

// artist is an artist search hit.
func artist(id, name string, score int, aliases ...string) map[string]any {
	als := []any{}
	for _, a := range aliases {
		als = append(als, map[string]any{"name": a})
	}
	return map[string]any{"id": id, "name": name, "sort-name": name, "score": score, "aliases": als}
}

// onQuery answers the recordings only to a query holding every part.
func onQuery(recs []map[string]any, parts ...string) func(q string) []any {
	return func(q string) []any {
		for _, p := range parts {
			if !strings.Contains(q, p) {
				return nil
			}
		}
		out := make([]any, len(recs))
		for i, r := range recs {
			out[i] = r
		}
		return out
	}
}

// Apple Music romanizes Russian artists («Bi-2») while MusicBrainz names
// them in Cyrillic with the romanization as an alias: the artist search
// finds «Би-2», its studio recording on the service's album wins (not
// the live takes or the compilation).
func TestMatchRomanizedArtistByAlias(t *testing.T) {
	recs := []map[string]any{
		recording("bi2-live", "Полковнику никто не пишет", "", 313000, "Би-2", "bi2", "rg-live", "Горизонт событий @ ВТБ Арена (Live)", "Album", "2018", "Live"),
		recording("bi2-best", "Полковнику никто не пишет", "", 292000, "Би-2", "bi2", "rg-best", "лучшие песни", "Album", "2006", "Compilation"),
		recording("bi2-studio", "Полковнику никто не пишет", "", 294000, "Би-2", "bi2", "rg-bi2", "Би-2", "Album", "2000"),
	}
	mb, n := mbServerFull(t, onQuery(recs, `artist:"Би-2"`),
		func(string) []any {
			return []any{artist("bi2", "Би-2", 100, "Bi-2", "Би 2"), artist("shura", "Шура Би-2", 72)}
		})
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Bi-2", Title: "Полковнику никто не пишет", Album: "Би-2", DurationMs: 292000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-bi2" || d.RecordingID != "bi2-studio" || d.Artist != "Би-2" || d.Year != 2000 {
		t.Fatalf("%+v (%d requests)", d, n.Load())
	}
	// the alias's consonants: «Chernyi kofe» is Apple's «Cherny Kofe»
	kofe := []map[string]any{recording("kofe-1", "Владимирская Русь", "", 318000, "Чёрный кофе", "kofe", "rg-porog", "Переступи порог",
		"Album", "1987")}
	mb, _ = mbServerFull(t, onQuery(kofe, `artist:"Чёрный кофе"`),
		func(string) []any {
			return []any{artist("kofe", "Чёрный кофе", 100, "Chernyi kofe", "Black Coffee"), artist("k2", "Kofe", 96)}
		})
	d = match(t, NewMatcher(mb), Track{Key: "b", Artist: "Cherny Kofe", Title: "Владимирская Русь", Album: "Переступи порог", DurationMs: 318500})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-porog" {
		t.Fatalf("by consonants: %+v", d)
	}
}

// An alias found is no licence: the song must still be that artist's,
// of that title and length; a genuinely absent song stays in review.
func TestMatchAliasKeepsTheRules(t *testing.T) {
	recs := []map[string]any{recording("bi2-other", "Другая песня", "", 294000, "Би-2", "bi2", "rg-bi2", "Би-2", "Album", "2000"),
		recording("bi2-long", "Полковнику никто не пишет", "", 330000, "Би-2", "bi2", "rg-orch", "Би-2 и оркестр", "Album", "2010")}
	mb, _ := mbServerFull(t, onQuery(recs, `artist:"Би-2"`), func(string) []any { return []any{artist("bi2", "Би-2", 100, "Bi-2")} })
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Bi-2", Title: "Полковнику никто не пишет", Album: "Би-2", DurationMs: 292000})
	if d.State != TrackReview || d.Reason != ReasonNoExact {
		t.Fatalf("not the song: %+v", d)
	}
	// streaming-only artists: nothing anywhere, a few requests
	mb, n := mbServerFull(t, func(string) []any { return nil }, nil)
	d = match(t, NewMatcher(mb), Track{Key: "b", Artist: "promptgenix", Title: "Ghosts in the Airlock", Album: "Digital Delta: volume two"})
	if d.State != TrackReview || d.Reason != ReasonNotInMB || n.Load() > 3 {
		t.Fatalf("absent: %+v (%d requests)", d, n.Load())
	}
	// an artist of a low score or another name is not asked
	mb, n = mbServerFull(t, onQuery(recs, `artist:"Би-2"`), func(string) []any { return []any{artist("bi2", "Би-2", 80, "Bi-2"), artist("x", "Bi-3", 100)} })
	d = match(t, NewMatcher(mb), Track{Key: "c", Artist: "Bi-2", Title: "Другая песня", Album: "Би-2", DurationMs: 294000})
	if d.State != TrackReview || n.Load() != 3 {
		t.Fatalf("low score: %+v (%d requests)", d, n.Load())
	}
}

// Diacritics and odd hyphens: Apple's «JAŸ-Z» is MusicBrainz's «JAY‐Z».
func TestMatchFoldsDiacritics(t *testing.T) {
	recs := []map[string]any{recording("oj", "The Story of O.J.", "explicit", 231760, "JAY‐Z", "jayz", "rg-444", "4:44", "Album", "2017")}
	mb, n := mbServerFull(t, onQuery(recs), nil)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "JAŸ-Z", Title: "The Story of O.J.", Album: "4:44", DurationMs: 231000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-444" || n.Load() != 1 {
		t.Fatalf("%+v (%d requests)", d, n.Load())
	}
}

// «(feat. …)», cut tags and « - Single» are searched without: MusicBrainz
// credits the guest and titles the song plainly. A cut tag («(Radio
// Version)») is the recording only with the lengths close and known.
func TestMatchCleanedTitle(t *testing.T) {
	thicke := []map[string]any{recording("bl", "Blurred Lines", "", 263000, "Robin Thicke", "thicke", "rg-bl", "Blurred Lines", "Album", "2013")}
	mb, _ := mbServerFull(t, func(q string) []any {
		if strings.Contains(q, "feat") {
			return nil
		}
		return onQuery(thicke)(q)
	}, nil)
	d := match(t, NewMatcher(mb), Track{Key: "a", Artist: "Robin Thicke", Title: "Blurred Lines (feat. T.I. & Pharrell Williams)",
		Album: "Feel-Good Jams", DurationMs: 263500})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-bl" {
		t.Fatalf("feat.: %+v", d)
	}
	skin := []map[string]any{
		recording("skin-promo", "Under My Skin", "", 212000, "Blue System", "bs", "rg-heat", "Body Heat", "Album", "1988"),
		recording("skin", "Under My Skin", "", 212000, "Blue System", "bs", "rg-skin", "Under My Skin", "Single", "1988"),
	}
	skin[0]["releases"].([]any)[0].(map[string]any)["status"] = "Promotion"
	mb, _ = mbServerFull(t, func(q string) []any {
		if strings.Contains(q, "Radio") {
			return nil
		}
		return onQuery(skin)(q)
	}, nil)
	m := NewMatcher(mb)
	d = match(t, m, Track{Key: "b", Artist: "Blue System", Title: "Under My Skin (Radio Version)", Album: "Body Heat", DurationMs: 213000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-skin" {
		t.Fatalf("radio version: %+v", d)
	}
	for i, ms := range []int64{0, 260000} { // unknown or far: not that cut
		d = match(t, NewMatcher(mb), Track{Key: "c" + strconv.Itoa(i), Artist: "Blue System", Title: "Under My Skin (Radio Version)", Album: "Body Heat",
			DurationMs: ms})
		if d.State != TrackReview {
			t.Fatalf("radio version of %d ms: %+v", ms, d)
		}
	}
	// « - Single» and the first of two artists
	space := []map[string]any{recording("so3", "Space Opera Part 3", "", 232000, "Space", "space", "rg-so", "Space Opera", "Album", "1987")}
	mb, _ = mbServerFull(t, onQuery(space, `artist:"Space"`, `"Space Opera Part 3"`), nil)
	d = match(t, NewMatcher(mb), Track{Key: "d", Artist: "Space & Didier Marouani", Title: "Space Opera Part 3", Album: "Space Opera - Single",
		DurationMs: 232000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-so" {
		t.Fatalf("first artist: %+v", d)
	}
}

// A recording on no studio release: the artist's own compilation the
// service names is taken; a various-artists one never.
func TestMatchOwnCompilationOfTheService(t *testing.T) {
	recs := []map[string]any{
		recording("frf", "Fly Robin Fly", "", 230920, "Silver Convention", "sc", "rg-frf", "Fly Robin Fly", "Album", "", "Compilation"),
		recording("frf-single", "Fly, Robin, Fly", "", 335000, "Silver Convention", "sc", "rg-single", "Fly, Robin, Fly / Lady Bump", "Single", "1976"),
		recording("frf-va", "Fly Robin Fly", "", 230920, "Silver Convention", "sc", "rg-va", "Disco Hits", "Album", "1995", "Compilation"),
	}
	recs[2]["releases"].([]any)[0].(map[string]any)["artist-credit"] = []any{map[string]any{"name": "Various Artists",
		"artist": map[string]any{"id": "89ad4ac3-39f7-470e-963a-56509c546377", "name": "Various Artists"}}}
	mb, _ := mbServerFull(t, onQuery(recs), nil)
	m := NewMatcher(mb)
	d := match(t, m, Track{Key: "a", Artist: "Silver Convention", Title: "Fly Robin Fly", Album: "Fly Robin Fly", DurationMs: 230000})
	if d.State != TrackMatched || d.ReleaseGroup != "rg-frf" {
		t.Fatalf("own compilation: %+v", d)
	}
	d = match(t, m, Track{Key: "b", Artist: "Silver Convention", Title: "Fly Robin Fly", Album: "Disco Hits", DurationMs: 230000})
	if d.State != TrackReview {
		t.Fatalf("various artists: %+v", d)
	}
}

// A version the service names is matched only to that version on the
// album it names: «Eminence Front (Live)», a bonus track of «It's Hard»,
// is the live recording there — never the studio take; a live album's
// plainly titled track is live by the album.
func TestMatchNamedVersionOnServiceAlbum(t *testing.T) {
	recs := []map[string]any{
		recording("ef-studio", "Eminence Front", "", 340000, "The Who", "who", "rg-hard", "It's Hard", "Album", "1982"),
		recording("ef-live", "Eminence Front (live)", "", 354000, "The Who", "who", "rg-hard", "It's Hard", "Album", "1997"),
		recording("ef-live-other", "Eminence Front", "live, 1989", 354500, "The Who", "who", "rg-join", "Join Together", "Album", "1990", "Live"),
		recording("hj", "Hey Jude", "", 421000, "Paul McCartney", "macca", "rg-us", "Back in the U.S.: Live 2002", "Album", "2002", "Live"),
		recording("hj-studio", "Hey Jude", "", 421500, "Paul McCartney", "macca", "rg-hj", "Hey Jude", "Single", "1968"),
	}
	mb, _ := mbServerFull(t, onQuery(recs), nil)
	m := NewMatcher(mb)
	d := match(t, m, Track{Key: "a", Artist: "The Who", Title: "Eminence Front (Live)", Album: "It's Hard", DurationMs: 354000})
	if d.State != TrackMatched || d.RecordingID != "ef-live" || d.ReleaseGroup != "rg-hard" {
		t.Fatalf("bonus live track: %+v", d)
	}
	d = match(t, m, Track{Key: "b", Artist: "Paul McCartney", Title: "Hey Jude (Live)", Album: "Back In the U.S. (Live 2002)", DurationMs: 421000})
	if d.State != TrackMatched || d.RecordingID != "hj" || d.ReleaseGroup != "rg-us" {
		t.Fatalf("live album: %+v", d)
	}
	for i, tr := range []Track{
		{Artist: "The Who", Title: "Eminence Front (Live)", Album: "Greatest Hits Live", DurationMs: 354000}, // another album
		{Artist: "The Who", Title: "Eminence Front (Live)", Album: "It's Hard"},                              // no length
		{Artist: "The Who", Title: "Eminence Front (Live)", Album: "It's Hard", DurationMs: 300000},          // another length
		{Artist: "The Who", Title: "Eminence Front (Acoustic)", Album: "It's Hard", DurationMs: 340000},      // another version
	} {
		tr.Key = "c" + strconv.Itoa(i)
		if d := match(t, NewMatcher(mb), tr); d.State != TrackReview {
			t.Fatalf("%+v: %+v", tr, d)
		}
	}
}

// The alias search is the least certain path: the lengths must be known,
// a name standing for two artists of other spellings, or a song both the
// alias's artist and one spelled as the service spells it have, goes to
// review.
func TestMatchAliasNeedsCertainty(t *testing.T) {
	plach := recording("plach", "Плач", "", 345733, "Алиса", "alisa-ru", "rg-luna", "Для тех, кто свалился с Луны", "Album", "1993")
	latin := recording("plach-latin", "Плач", "", 345000, "Alisa", "alisa-lat", "rg-other", "Для тех, кто свалился с Луны", "Album",
		"2001")
	tr := Track{Artist: "Alisa", Title: "Плач", Album: "Для тех, кто свалился с Луны", DurationMs: 345000}
	one := func(string) []any {
		return []any{artist("alisa-ru", "Алиса", 100, "Alisa"), artist("alisa-lat", "Alisa", 91)}
	}
	cases := []struct {
		name    string
		recs    func(string) []any
		artists func(string) []any
		tr      Track
		state   string
		reason  string
	}{
		{"found", onQuery([]map[string]any{plach}, `artist:"Алиса"`), one, tr, TrackMatched, ""},
		{"no length", onQuery([]map[string]any{plach}, `artist:"Алиса"`), one,
			Track{Artist: tr.Artist, Title: tr.Title, Album: tr.Album}, TrackReview, ReasonNoExact},
		{"two alias artists", onQuery([]map[string]any{plach}, `artist:"Алиса"`),
			func(string) []any {
				return []any{artist("alisa-ru", "Алиса", 100, "Alisa"), artist("alisa-ua", "Аліса", 95, "Alisa")}
			},
			tr, TrackReview, ReasonAmbiguous},
		{"both spellings have it", onQuery([]map[string]any{plach, latin}, `artist:"Алиса"`), one, tr, TrackReview, ReasonAmbiguous},
	}
	for _, c := range cases {
		mb, _ := mbServerFull(t, c.recs, c.artists)
		c.tr.Key = c.name
		d := match(t, NewMatcher(mb), c.tr)
		if d.State != c.state || d.Reason != c.reason || c.state == TrackMatched && d.ReleaseGroup != "rg-luna" {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
}
