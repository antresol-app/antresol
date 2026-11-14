package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

type prefsOut struct {
	AudioLangs    []string `json:"audioLangs"`
	SubMode       string   `json:"subMode"`
	SubLangs      []string `json:"subLangs"`
	SubSize       string   `json:"subSize"`
	SubBackground string   `json:"subBackground"`
	Custom        bool     `json:"custom"`
}

type trackChoiceOut struct {
	Lang  string `json:"lang"`
	Title string `json:"title"`
}

type trackPrefsOut struct {
	Prefs  prefsOut `json:"prefs"`
	Series *struct {
		Audio        *trackChoiceOut `json:"audio"`
		Subtitle     *trackChoiceOut `json:"subtitle"`
		SubtitlesOff bool            `json:"subtitlesOff"`
	} `json:"series"`
	Offsets map[string]int `json:"offsets"`
}

func (e *libEnv) prefsOf(path string, c *http.Cookie) prefsOut {
	e.t.Helper()
	w := e.do("GET", path, "", c)
	if w.Code != 200 {
		e.t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	return decodeJSON[prefsOut](e.t, w.Body.Bytes())
}

func (e *libEnv) trackPrefsOf(itemID int64, c *http.Cookie) trackPrefsOut {
	e.t.Helper()
	w := e.do("GET", "/api/items/"+itoa(itemID)+"/track-prefs", "", c)
	if w.Code != 200 {
		e.t.Fatalf("track-prefs %d: %d %s", itemID, w.Code, w.Body)
	}
	return decodeJSON[trackPrefsOut](e.t, w.Body.Bytes())
}

// A profile's defaults: the household's languages until it saves its own;
// its own are checked, kept per profile and dropped by DELETE.
func TestPlaybackPrefsPerProfile(t *testing.T) {
	e := newLibEnv(t)
	_, mom := e.profile(`{"name":"Мама"}`)
	p := e.prefsOf("/api/me/playback-prefs", mom)
	if p.Custom || strings.Join(p.AudioLangs, ",") != "ru,en" || p.SubMode != "off" || p.SubSize != "m" || p.SubBackground != "translucent" {
		t.Fatalf("defaults %+v", p)
	}
	body := `{"audioLangs":["en","ru"],"subMode":"foreign","subLangs":["ru"],"subSize":"xl","subBackground":"solid"}`
	w := e.do("PUT", "/api/me/playback-prefs", body, mom)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"custom":true`) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	p = e.prefsOf("/api/me/playback-prefs", mom)
	if !p.Custom || strings.Join(p.AudioLangs, ",") != "en,ru" || p.SubMode != "foreign" || strings.Join(p.SubLangs, ",") != "ru" ||
		p.SubSize != "xl" || p.SubBackground != "solid" {
		t.Fatalf("stored %+v", p)
	}
	// another profile keeps the defaults
	if p := e.prefsOf("/api/me/playback-prefs", e.admin); p.Custom {
		t.Fatalf("admin got mom's: %+v", p)
	}
	for _, bad := range []string{
		`{"audioLangs":[],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"none"}`,
		`{"audioLangs":["xx"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"none"}`,
		`{"audioLangs":["ru","ru"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"none"}`,
		`{"audioLangs":["ru"],"subMode":"sometimes","subLangs":[],"subSize":"m","subBackground":"none"}`,
		`{"audioLangs":["ru"],"subMode":"off","subLangs":["ru"],"subSize":"xxl","subBackground":"none"}`,
		`{"audioLangs":["ru"],"subMode":"off","subLangs":["ru"],"subSize":"m","subBackground":"glass"}`,
		`nope`,
	} {
		if w := e.do("PUT", "/api/me/playback-prefs", bad, mom); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	if w := e.do("DELETE", "/api/me/playback-prefs", "", mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"custom":false`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if p := e.prefsOf("/api/me/playback-prefs", mom); p.Custom {
		t.Fatalf("after delete %+v", p)
	}
}

// A kid's defaults are the parents' choice: the kid reads them, only an
// administrator changes them (PUT /api/users/{id}/playback-prefs).
func TestPlaybackPrefsKidAdminOnly(t *testing.T) {
	e := newLibEnv(t)
	kidID, kid := e.profile(`{"name":"Вася","isKid":true}`)
	body := `{"audioLangs":["ru"],"subMode":"always","subLangs":["en"],"subSize":"l","subBackground":"none"}`
	if w := e.do("PUT", "/api/me/playback-prefs", body, kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid put: %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/me/playback-prefs", "", kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid delete: %d %s", w.Code, w.Body)
	}
	path := "/api/users/" + itoa(kidID) + "/playback-prefs"
	if w := e.do("PUT", path, body, kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid via users: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", path, body, e.admin); w.Code != 200 {
		t.Fatalf("admin put: %d %s", w.Code, w.Body)
	}
	if p := e.prefsOf("/api/me/playback-prefs", kid); !p.Custom || p.SubMode != "always" || p.SubSize != "l" {
		t.Fatalf("kid sees %+v", p)
	}
	if p := e.prefsOf(path, e.admin); !p.Custom || strings.Join(p.SubLangs, ",") != "en" {
		t.Fatalf("admin sees %+v", p)
	}
	if w := e.do("GET", "/api/users/99999/playback-prefs", "", e.admin); w.Code != http.StatusNotFound {
		t.Fatalf("missing profile: %d %s", w.Code, w.Body)
	}
}

// The tracks chosen in an episode are remembered for the whole series and
// that profile; a movie has no series to remember them for.
func TestSeriesTracksRemembered(t *testing.T) {
	e := newLibEnv(t)
	_, eps := e.series("Смешарики", "6+", 3)
	_, mom := e.profile(`{"name":"Мама"}`)
	if tp := e.trackPrefsOf(eps[2], mom); tp.Series != nil || tp.Prefs.Custom || tp.Offsets == nil {
		t.Fatalf("fresh %+v", tp)
	}
	e.put("/api/items/"+itoa(eps[0])+"/track-prefs", `{"audio":{"lang":"rus","title":"Кубик в кубе"}}`, mom)
	e.put("/api/items/"+itoa(eps[0])+"/track-prefs", `{"subtitle":{"lang":"eng","title":"Full"}}`, mom)
	tp := e.trackPrefsOf(eps[2], mom)
	if tp.Series == nil || tp.Series.Audio == nil || tp.Series.Audio.Title != "Кубик в кубе" || tp.Series.Audio.Lang != "rus" ||
		tp.Series.Subtitle == nil || tp.Series.Subtitle.Lang != "eng" || tp.Series.SubtitlesOff {
		t.Fatalf("remembered %+v", tp.Series)
	}
	// turned off later: the audio stays
	e.put("/api/items/"+itoa(eps[1])+"/track-prefs", `{"subtitleOff":true}`, mom)
	tp = e.trackPrefsOf(eps[0], mom)
	if tp.Series == nil || tp.Series.Audio == nil || tp.Series.Subtitle != nil || !tp.Series.SubtitlesOff {
		t.Fatalf("off %+v", tp.Series)
	}
	// per profile
	if tp := e.trackPrefsOf(eps[2], e.admin); tp.Series != nil {
		t.Fatalf("admin got mom's series %+v", tp.Series)
	}
	// a kid remembers its own too
	_, kid := e.profile(`{"name":"Вася","isKid":true}`)
	e.put("/api/items/"+itoa(eps[0])+"/track-prefs", `{"audio":{"lang":"rus","title":"TVShows"}}`, kid)
	if tp := e.trackPrefsOf(eps[1], kid); tp.Series == nil || tp.Series.Audio.Title != "TVShows" {
		t.Fatalf("kid %+v", tp.Series)
	}
	// a movie: accepted, nothing to remember
	movie, _ := e.movie("Брат", "16+", "0123456789")
	e.put("/api/items/"+itoa(movie)+"/track-prefs", `{"audio":{"lang":"rus","title":""}}`, mom)
	if tp := e.trackPrefsOf(movie, mom); tp.Series != nil {
		t.Fatalf("movie %+v", tp.Series)
	}
	for _, bad := range []string{`{}`, `{"audio":{"lang":"rus"},"subtitleOff":true}`, `{"audio":{"lang":"` + strings.Repeat("x", 40) + `","title":""}}`,
		`{"audio":{"lang":"ru","title":"` + strings.Repeat("я", 300) + `"}}`, `nope`} {
		if w := e.do("PUT", "/api/items/"+itoa(eps[0])+"/track-prefs", bad, mom); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/items/99999/track-prefs", "", mom); w.Code != http.StatusNotFound {
		t.Fatalf("missing item: %d", w.Code)
	}
}

// «Субтитры раньше / позже» is kept per file and per profile, within ±60 s
// and on the 50 ms grid; 0 clears it.
func TestSubtitleOffsetPerFile(t *testing.T) {
	e := newLibEnv(t)
	movie, file := e.movie("Брат", "6+", "0123456789")
	_, mom := e.profile(`{"name":"Мама"}`)
	path := "/api/files/" + itoa(file) + "/subtitle-offset"
	w := e.do("PUT", path, `{"offsetMs":-750}`, mom)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"offsetMs":-750`) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if tp := e.trackPrefsOf(movie, mom); tp.Offsets[itoa(file)] != -750 {
		t.Fatalf("offsets %+v", tp.Offsets)
	}
	if tp := e.trackPrefsOf(movie, e.admin); len(tp.Offsets) != 0 {
		t.Fatalf("admin got mom's offset %+v", tp.Offsets)
	}
	if w := e.do("PUT", path, `{"offsetMs":99999}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"offsetMs":60000`) {
		t.Fatalf("clamp: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", path, `{"offsetMs":1234}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"offsetMs":1250`) {
		t.Fatalf("grid: %d %s", w.Code, w.Body)
	}
	_, kid := e.profile(`{"name":"Вася","isKid":true}`)
	if w := e.do("PUT", path, `{"offsetMs":500}`, kid); w.Code != 200 {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	e.do("PUT", path, `{"offsetMs":0}`, mom)
	if tp := e.trackPrefsOf(movie, mom); len(tp.Offsets) != 0 {
		t.Fatalf("cleared %+v", tp.Offsets)
	}
	for _, bad := range []string{`{}`, `{"offsetMs":"x"}`, `nope`} {
		if w := e.do("PUT", path, bad, mom); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	if w := e.do("PUT", "/api/files/99999/subtitle-offset", `{"offsetMs":250}`, mom); w.Code != http.StatusNotFound {
		t.Fatalf("missing file: %d", w.Code)
	}
}

// A kid cannot reach the tracks of a title above its age limit.
func TestTrackPrefsHiddenItem(t *testing.T) {
	e := newLibEnv(t)
	movie, file := e.movie("Брат", "18+", "0123456789")
	kid := e.kid("12+")
	if w := e.do("GET", "/api/items/"+itoa(movie)+"/track-prefs", "", kid); w.Code != http.StatusNotFound {
		t.Fatalf("get: %d", w.Code)
	}
	if w := e.do("PUT", "/api/files/"+itoa(file)+"/subtitle-offset", `{"offsetMs":250}`, kid); w.Code != http.StatusNotFound {
		t.Fatalf("offset: %d", w.Code)
	}
}

// The household's defaults put the profile's interface language first.
func TestPlaybackPrefsFollowInterfaceLanguage(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	// english's defaults put the profile's interface language first
	if w := e.do("PUT", "/api/me/ui-language", `{"language":"en"}`, e.admin); w.Code >= 300 {
		t.Fatalf("ui language: %d %s", w.Code, w.Body)
	}
	if p := e.prefsOf("/api/me/playback-prefs", e.admin); p.Custom || strings.Join(p.AudioLangs, ",") != "en,ru" || strings.Join(p.SubLangs, ",") != "en,ru" {
		t.Fatalf("english interface: %+v", p)
	}
	// a language the household lacks is added first
	if w := e.do("PUT", "/api/me/ui-language", `{"language":"ru"}`, e.admin); w.Code >= 300 {
		t.Fatal(w.Body)
	}
	if p := e.prefsOf("/api/me/playback-prefs", e.admin); strings.Join(p.AudioLangs, ",") != "ru,en" {
		t.Fatalf("russian interface: %+v", p)
	}
}
