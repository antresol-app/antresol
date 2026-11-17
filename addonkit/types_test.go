package addonkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func keys(t *testing.T, v any, out *[]string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var walk func(x any)
	walk = func(x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, c := range y {
				*out = append(*out, k)
				walk(c)
			}
		case []any:
			for _, c := range y {
				walk(c)
			}
		}
	}
	walk(m)
}

func TestDTO_CamelCase(t *testing.T) {
	end := int64(5)
	raw := json.RawMessage(`{}`)
	vals := []any{
		Item{Meta: raw}, File{Probe: raw}, Track{EndMs: &end}, User{}, Text{},
		Hold{Files: []HoldFile{{}}}, RefPath{}, RefFile{},
		Bound{Missing: []string{"a"}, Taken: []string{"b"}},
		ImportResult{Files: []ImportedFile{{}}, Missing: []string{}, Taken: []string{}},
		SeriesAudio{Files: []EpisodeAudio{{Titles: []string{"x"}}}, Profiles: []ProfileVoice{{}}},
		Info{},
		InputRequest{Title: &InputTitle{}, Slow: true, ItemID: 1, Meta: raw,
			Sidecars: InputSidecars{Audio: []AudioSidecar{{}}, Subtitles: []SubtitleSidecar{{}}}},
		InputCreated{}, PlayProgress{},
		PendingCard{}, PendingQuery{}, PendingAnswer{Cards: []PendingCard{{}}},
		TrackingTitle{}, TrackingEpisode{}, TrackingSource{Labels: []string{"x"}},
		ImportJob{}, ImportGroup{Aliases: []string{}}, ImportTrack{}, ImportSnapshot{Tracks: []ImportSnapTrack{{}},
			Playlists: []ImportSnapPlaylist{{}}, Albums: []ImportSnapAlbum{{}}},
	}
	var all []string
	for _, v := range vals {
		keys(t, v, &all)
	}
	have := map[string]bool{}
	for _, k := range all {
		have[k] = true
		if strings.Contains(k, "_") {
			t.Errorf("key %q has an underscore", k)
		}
	}
	for _, want := range []string{"tmdbId", "itemId", "durationMs", "publicUrl", "libraryRoots", "ffmpeg", "isAdmin", "endMs", "audioTitle", "userId", "expiresAt", "positionSec", "durationSec", "inputId", "ended", "sidecars", "subtitles", "audio", "season", "episode", "kind", "slow", "title", "meta", "lang", "card", "pending", "subtitle", "tmdbKind", "status", "done", "size", "scope", "cards", "imdbId", "cert", "titleId", "number", "airDate", "ref", "provider", "quality", "labels", "firstSeen", "lastSeen", "skipKey", "albumItemId", "runSec", "quietSec", "savedAlbumsMissing", "mbReleaseGroup", "groupId", "albumArtist", "entries"} {
		if !have[want] {
			t.Errorf("missing key %q", want)
		}
	}
}

func TestDTO_InputOmitted(t *testing.T) {
	b, err := json.Marshal(InputRequest{Path: "/p", Name: "a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"itemId", "title", "slow", "meta"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q not omitted: %s", k, b)
		}
	}
	if _, ok := m["sidecars"].(map[string]any); !ok {
		t.Errorf("sidecars missing: %s", b)
	}
}

func TestModuleIsStdlibOnly(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Fields(string(out)) {
		if p != "github.com/antresol-app/antresol/addonkit" {
			t.Errorf("non-stdlib dependency %s", p)
		}
	}
}

// PendingCard is nested ({card, pending}); the core flattens it into its own
// answer of GET /api/library/pending.
func TestDTO_PendingCardShape(t *testing.T) {
	b, err := json.Marshal(PendingCard{Card: PendingCardInfo{Kind: "movie", Title: "T"}, Pending: PendingState{Key: "k", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"card":{"id":0,"kind":"movie","title":"T","subtitle":"","year":0,"poster":"","tmdbKind":"","tmdbId":0},` +
		`"pending":{"key":"k","status":"active","done":0,"size":0}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

// The requests.* hooks' bodies and answers are camelCase JSON.
func TestRequestTypesJSON(t *testing.T) {
	b, _ := json.Marshal(RequestCandidate{Kind: "music", ExtID: "mb-1", Title: "A", Subtitle: "B", Year: 1988, Explicit: true, Aliases: []string{"x"}})
	if string(b) != `{"kind":"music","tmdbId":0,"extId":"mb-1","title":"A","subtitle":"B","year":1988,"poster":"","explicit":true,"aliases":["x"]}` {
		t.Fatalf("%s", b)
	}
	var q RequestSearchQuery
	if err := json.Unmarshal([]byte(`{"kind":"book","q":"сказки"}`), &q); err != nil || q.Kind != "book" || q.Q != "сказки" {
		t.Fatalf("%+v %v", q, err)
	}
	var tq RequestTitleQuery
	if err := json.Unmarshal([]byte(`{"kind":"music","extId":"mb-1"}`), &tq); err != nil || tq.ExtID != "mb-1" {
		t.Fatalf("%+v %v", tq, err)
	}
	b, _ = json.Marshal(RequestTitleAnswer{Title: "A", Subtitle: "B", Year: 1, Poster: "p", Explicit: true})
	if string(b) != `{"title":"A","subtitle":"B","year":1,"poster":"p","explicit":true}` {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(RequestsOpenAnswer{Open: true})
	if string(b) != `{"open":true}` {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(RequestSearchAnswer{Candidates: []RequestCandidate{}})
	if string(b) != `{"candidates":[]}` {
		t.Fatalf("%s", b)
	}
}

// The tracking.*, notify.interest and voiceover.origLang hooks' bodies and
// answers round-trip as the contract writes them.
func TestTrackingHookTypesJSON(t *testing.T) {
	cases := []struct {
		name string
		v    any
		json string
	}{
		{"ScanRequest", &ScanRequest{Title: TrackingTitle{ID: 7, Kind: "series", TMDBID: 42, Name: "N", Status: "airing"},
			Episodes: []TrackingEpisode{{TitleID: 7, Season: 1, Number: 2, Name: "E", AirDate: "2026-01-02"}}},
			`{"title":{"id":7,"kind":"series","tmdbId":42,"imdbId":"","name":"N","poster":"","status":"airing","cert":""},` +
				`"episodes":[{"titleId":7,"season":1,"number":2,"name":"E","airDate":"2026-01-02"}]}`},
		{"ScanAnswer", &ScanAnswer{Sources: []TrackingSource{{Season: 1, Episode: 2, Kind: "alpha", Ref: "r", Provider: "p", Quality: "1080p",
			Labels: []string{"x"}, Meta: TrackingSourceMeta{SourceID: "s", Lang: "ru"}}}},
			`{"sources":[{"titleId":0,"season":1,"episode":2,"kind":"alpha","ref":"r","provider":"p","quality":"1080p","labels":["x"],` +
				`"meta":{"sourceId":"s","lang":"ru"},"firstSeen":"0001-01-01T00:00:00Z","lastSeen":"0001-01-01T00:00:00Z"}]}`},
		{"ScanAnswerUnchanged", &ScanAnswer{Unchanged: true}, `{"unchanged":true}`},
		{"TitlesAnswer", &TitlesAnswer{Titles: []ExtraTitle{{Kind: "movie", TMDBID: 603, Name: "M", Poster: "/p.jpg"}}},
			`{"titles":[{"kind":"movie","tmdbId":603,"name":"M","poster":"/p.jpg"}]}`},
		{"WatchedRequest", &WatchedRequest{UserID: 3, Kind: "series", TMDBID: 42}, `{"userId":3,"kind":"series","tmdbId":42}`},
		{"WatchedAnswer", &WatchedAnswer{Episodes: []WatchedEpisode{{Season: 1, Number: 2, Watched: true, PositionSec: 90}}},
			`{"episodes":[{"season":1,"number":2,"watched":true,"positionSec":90}]}`},
		{"InterestRequest", &InterestRequest{Kind: "series", TMDBID: 42}, `{"kind":"series","tmdbId":42}`},
		{"InterestAnswer", &InterestAnswer{UserIDs: []int64{1, 2}}, `{"userIds":[1,2]}`},
		{"OrigLangRequest", &OrigLangRequest{FileID: 9}, `{"fileId":9}`},
		{"OrigLangAnswer", &OrigLangAnswer{Lang: "ja"}, `{"lang":"ja"}`},
		{"ImportServicesAnswer", &ImportServicesAnswer{Services: []ImportService{{ID: "apple", Configured: true, Connect: "redirect",
			Setup: []string{"s"}, Files: []string{"xml"}}}},
			`{"services":[{"id":"apple","configured":true,"connect":"redirect","setup":["s"],"files":["xml"]}]}`},
		{"ImportStopRequest", &ImportStopRequest{JobID: 7}, `{"jobId":7}`},
		{"LeftoversRequest", &LeftoversRequest{Lang: "en"}, `{"lang":"en"}`},
		{"LeftoversAnswer", &LeftoversAnswer{Lines: []LeftoverLine{{Source: "librarr", Name: "Librarr", Status: "warning", Reason: "r"}}},
			`{"lines":[{"source":"librarr","name":"Librarr","status":"warning","reason":"r"}]}`},
		{"ImportResolveRequest", &ImportResolveRequest{IDs: []string{"v1"}}, `{"ids":["v1"]}`},
		{"ImportResolveAnswer", &ImportResolveAnswer{Tracks: []ImportSnapTrack{{Key: "v1", Artist: "A", Title: "T"}}},
			`{"tracks":[{"key":"v1","artist":"A","title":"T","album":"","albumArtist":"","isrc":"","durationMs":0,"addedAt":null,"liked":false}]}`},
		{"ImportSearchRequest", &ImportSearchRequest{Kind: "music", Q: "q"}, `{"kind":"music","q":"q"}`},
		{"ImportSearchAnswer", &ImportSearchAnswer{Candidates: []ImportCandidate{{Kind: "music", ExtID: "rg", Title: "T", Year: 1988,
			Aliases: []string{"a"}, MatchedTrack: "m", ByArtist: "b"}}},
			`{"candidates":[{"tmdbId":0,"extId":"rg","kind":"music","title":"T","subtitle":"","year":1988,"poster":"","overview":"",` +
				`"inLibrary":false,"libraryItemId":null,"aliases":["a"],"matchedTrack":"m","byArtist":"b"}]}`},
		{"TrailerFetch", &TrailerFetch{Key: "abcdefghijk", URL: "https://example.org/v/abcdefghijk", MaxHeight: 720},
			`{"key":"abcdefghijk","url":"https://example.org/v/abcdefghijk","maxHeight":720}`},
		{"TrailerFetchNoHeight", &TrailerFetch{Key: "abcdefghijk", URL: "u"}, `{"key":"abcdefghijk","url":"u"}`},
		{"TrailerFile", &TrailerFile{Path: "/media/abcdefghijk.mp4", Size: 10, DurationMs: 2000, ContentType: "video/mp4"},
			`{"path":"/media/abcdefghijk.mp4","size":10,"durationMs":2000,"contentType":"video/mp4"}`},
		{"TrailerPending", &TrailerPending{RetryAfterMs: 500}, `{"retryAfterMs":500}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.json {
			t.Errorf("%s: marshal %s %v\nwant %s", c.name, b, err, c.json)
			continue
		}
		// decode into a fresh value of the same type and marshal again
		fresh := reflect.New(reflect.TypeOf(c.v).Elem()).Interface()
		if err := json.Unmarshal([]byte(c.json), fresh); err != nil {
			t.Errorf("%s: unmarshal %v", c.name, err)
			continue
		}
		if b2, _ := json.Marshal(fresh); string(b2) != c.json {
			t.Errorf("%s: round trip %s", c.name, b2)
		}
	}
}
