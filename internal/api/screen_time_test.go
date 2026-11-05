package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/screentime"
)

type stClock struct{ t time.Time }

func (c *stClock) now() time.Time { return c.t }

// newScreenEnv is a library env with screen time on a test clock (Monday
// 2026-11-02 12:00 UTC) and a kid profile.
func newScreenEnv(t *testing.T) (*libEnv, *stClock, int64, *http.Cookie) {
	t.Helper()
	c := &stClock{time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.ScreenTime = &screentime.Service{DB: db, Now: c.now, Loc: time.UTC}
	})
	id, kid := e.profile(`{"name":"Вася","isKid":true}`)
	return e, c, id, kid
}

func (e *libEnv) limits(kid int64, body string) {
	e.t.Helper()
	if w := e.do("PUT", "/api/users/"+itoa(kid)+"/screen-time", body, e.admin); w.Code != 200 {
		e.t.Fatalf("limits: %d %s", w.Code, w.Body)
	}
}

func refusedScreen(t *testing.T, w interface {
	Result() *http.Response
}, reason string) {
	t.Helper()
	res := w.Result()
	b, _ := io.ReadAll(res.Body)
	var out struct{ Error, Code, Reason string }
	json.Unmarshal(b, &out)
	if res.StatusCode != 403 || out.Code != ScreenTimeCode || out.Reason != reason || out.Error == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

const stPlayBody = `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`
const stDirectProbe = `{"Container":"mp4","DurationMs":600000,"Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`

func TestScreenTimePlayAndStreamRefused(t *testing.T) {
	e, _, kid, kc := newScreenEnv(t)
	_, f := e.movie("Смешарики", "0+", "0123456789")
	e.probeJSON(f, stDirectProbe)
	if w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc); w.Code != 200 {
		t.Fatalf("no limits: %d %s", w.Code, w.Body)
	}
	e.limits(kid, `{"videoMin":0}`)
	refusedScreen(t, e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc), "limit")
	refusedScreen(t, e.do("GET", "/api/stream/"+itoa(f), "", kc), "limit")
	refusedScreen(t, e.do("GET", "/api/download/"+itoa(f), "", kc), "limit")
	w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc, "Accept-Language", "en")
	if !strings.Contains(w.Body.String(), "That’s all for today") {
		t.Fatalf("%s", w.Body)
	}
	// the admin is not limited; books never are
	if w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, e.admin); w.Code != 200 {
		t.Fatalf("admin %d", w.Code)
	}
	book := e.book("Мастер", ".epub")
	files, _ := e.repo.FilesOf(t.Context(), book)
	if w := e.do("GET", "/api/stream/"+itoa(files[0].ID), "", kc); w.Code != 200 {
		t.Fatalf("book %d", w.Code)
	}
}

func TestScreenTimeHours(t *testing.T) {
	e, c, kid, kc := newScreenEnv(t)
	_, f := e.movie("Смешарики", "0+", "0123456789")
	e.probeJSON(f, stDirectProbe)
	e.limits(kid, `{"weekday":{"from":"08:00","to":"20:30"}}`)
	if w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	c.t = time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)
	w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc)
	if !strings.Contains(w.Body.String(), "Сейчас нельзя смотреть: с 20:30 до 08:00") {
		t.Fatalf("%s", w.Body)
	}
	refusedScreen(t, e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc), "hours")
}

// An HLS session started before the limit stops at it: its segments and
// playlist are refused and the session ends.
func TestScreenTimeStopsRunningHLS(t *testing.T) {
	e, c, kid, kc := newScreenEnv(t)
	_, f := e.movie("Смешарики", "0+", "0123456789")
	e.probeJSON(f, hevcProbe)
	e.limits(kid, `{"videoMin":1}`)
	if w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc); w.Code != 200 || !strings.Contains(w.Body.String(), "/api/hls/sess1/") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// segment reads (a client that does not report) count the time
	for i := 0; i < 11; i++ { // 50 s
		if w := e.do("GET", "/api/hls/sess1/seg"+itoa(int64(i))+".ts", "", kc); w.Code != 404 { // the fake has no segments
			t.Fatalf("%d: %d %s", i, w.Code, w.Body)
		}
		c.t = c.t.Add(5 * time.Second)
	}
	c.t = c.t.Add(15 * time.Second) // 50 s and the last read's 15
	refusedScreen(t, e.do("GET", "/api/hls/sess1/seg99.ts", "", kc), "limit")
	if len(e.hls.stopped) != 1 || e.hls.stopped[0] != "sess1" {
		t.Fatalf("stopped %v", e.hls.stopped)
	}
	// the session is gone for the profile
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", kc); w.Code != 404 {
		t.Fatalf("after %d", w.Code)
	}
}

// Progress saves count the wall time; GET /api/me/screen-time reports it.
func TestScreenTimeProgressCounts(t *testing.T) {
	e, c, kid, kc := newScreenEnv(t)
	id, f := e.movie("Смешарики", "0+", "0123456789")
	e.probeJSON(f, stDirectProbe)
	e.limits(kid, `{"videoMin":30}`)
	// saves of a title the server never played (an offline copy's queue,
	// whatever the device's clock says) are not playback
	other, _ := e.movie("Тачки", "0+", "0123456789")
	for i := 0; i < 7; i++ {
		e.put("/api/progress/"+itoa(other), `{"positionMs":`+itoa(int64(i+1)*10000)+`,"durationMs":600000}`, kc)
		c.t = c.t.Add(10 * time.Second)
	}
	if w := e.do("POST", "/api/play/"+itoa(f), stPlayBody, kc); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for i := 0; i < 7; i++ { // 60 s
		e.put("/api/progress/"+itoa(id), `{"positionMs":`+itoa(int64(i+1)*10000)+`,"durationMs":600000}`, kc)
		c.t = c.t.Add(10 * time.Second)
	}
	w := e.do("GET", "/api/me/screen-time", "", kc)
	var st screentime.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !st.Limited || st.Video.UsedSec < 60 || st.Video.UsedSec > 75 || st.Video.RemainingSec == nil || *st.Video.RemainingSec > 1800-60 {
		t.Fatalf("%s", w.Body)
	}
	// the admin's own status: not limited
	w = e.do("GET", "/api/me/screen-time", "", e.admin)
	if !strings.Contains(w.Body.String(), `"limited":false`) {
		t.Fatalf("%s", w.Body)
	}
}

func TestScreenTimeAdminAPI(t *testing.T) {
	e, c, kid, kc := newScreenEnv(t)
	e.series("Смешарики", "0+", 2)
	_, epIDs := e.series("Фиксики", "0+", 2)
	// two episodes and a movie: the series' time adds up before it is sorted
	_, movieFile := e.movie("Тачки", "0+", "0123456789")
	e.probeJSON(movieFile, stDirectProbe)
	play := func(fileID int64) {
		if w := e.do("POST", "/api/play/"+itoa(fileID), stPlayBody, kc); w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	for _, ep := range epIDs {
		fs, _ := e.repo.FilesOf(t.Context(), ep)
		e.probeJSON(fs[0].ID, stDirectProbe)
		play(fs[0].ID)
		for i := 0; i < 4; i++ {
			e.put("/api/progress/"+itoa(ep), `{"positionMs":`+itoa(int64(i+1)*10000)+`,"durationMs":600000}`, kc)
			c.t = c.t.Add(10 * time.Second)
		}
	}
	movie, _ := e.repo.FileByID(t.Context(), movieFile)
	play(movieFile)
	for i := 0; i < 6; i++ { // more than one episode, less than both
		e.put("/api/progress/"+itoa(movie.ItemID), `{"positionMs":`+itoa(int64(i+1)*10000)+`,"durationMs":600000}`, kc)
		c.t = c.t.Add(10 * time.Second)
	}
	c.t = c.t.Add(time.Minute)
	e.limits(kid, `{"videoMin":45,"audioMin":null,"weekday":{"from":"08:00","to":"20:30"},"weekend":null}`)
	w := e.do("POST", "/api/users/"+itoa(kid)+"/screen-time/bonus", `{"minutes":30}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var rep struct {
		Limits screentime.Limits
		Status screentime.Status
		Titles []struct {
			ItemID  int64  `json:"itemId"`
			Title   string `json:"title"`
			Kind    string `json:"kind"`
			Seconds int64  `json:"seconds"`
		}
		Days []screentime.DayUsage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if *rep.Limits.VideoMin != 45 || rep.Status.Video.BonusMin != 30 || len(rep.Days) != 7 || rep.Days[6].Day != "2026-11-02" {
		t.Fatalf("%s", w.Body)
	}
	// an episode's time is its series'
	if len(rep.Titles) != 2 || rep.Titles[0].Title != "Фиксики" || rep.Titles[0].Kind != "series" || rep.Titles[0].Seconds < 70 ||
		rep.Titles[1].Title != "Тачки" || rep.Titles[1].Seconds < 50 {
		t.Fatalf("%s", w.Body)
	}
	if w := e.do("GET", "/api/users/"+itoa(kid)+"/screen-time", "", kc); w.Code != 403 {
		t.Fatalf("kid reads the admin's view: %d", w.Code)
	}
	if w := e.do("GET", "/api/users/1/screen-time", "", e.admin); w.Code != 400 {
		t.Fatalf("adult: %d", w.Code)
	}
	if w := e.do("PUT", "/api/users/"+itoa(kid)+"/screen-time", `{"videoMin":-5}`, e.admin); w.Code != 400 {
		t.Fatalf("bad limits: %d", w.Code)
	}
	if w := e.do("POST", "/api/users/"+itoa(kid)+"/screen-time/bonus", `{"minutes":0}`, e.admin); w.Code != 400 {
		t.Fatalf("bad bonus: %d", w.Code)
	}
}

func TestScreenTimeAudio(t *testing.T) {
	e, _, kid, kc := newScreenEnv(t)
	_, tracks := e.simpleAlbum("Кино", "Звезда", 1)
	url := "/api/tracks/" + itoa(tracks[0]) + "/stream?format=raw"
	_, abFiles := e.audiobook("Война и мир", 1)
	e.limits(kid, `{"videoMin":0}`)
	// video only: music and audiobooks play on
	if w := e.do("GET", url, "", kc); w.Code != 200 {
		t.Fatalf("music %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/audio/"+itoa(abFiles[0])+"?format=raw", "", kc); w.Code != 200 {
		t.Fatalf("audiobook %d %s", w.Code, w.Body)
	}
	e.limits(kid, `{"audioMin":0}`)
	refusedScreen(t, e.do("GET", url, "", kc), "limit")
	refusedScreen(t, e.do("GET", "/api/audio/"+itoa(abFiles[0])+"?format=raw", "", kc), "limit")
}

// A direct stream read as one long request ends once the time is over.
func TestScreenReaderStops(t *testing.T) {
	n := 0
	r := &screenReader{ReadSeeker: bytes.NewReader(make([]byte, 1000)), check: func() bool { n++; return n < 3 }}
	buf := make([]byte, 100)
	got := 0
	var err error
	for err == nil {
		var k int
		k, err = r.Read(buf)
		got += k
	}
	if err != errScreenTime || got != 200 {
		t.Fatalf("%v %d", err, got)
	}
}

// A kid downloads nothing of a limited category (a media key neither):
// with only audio limited a film downloads (counted), music does not;
// with video limited the film does not. Books are never limited; the
// title page's toDevice follows the same rule.
func TestScreenTimeDownloadOff(t *testing.T) {
	e, _, kid, kc := newScreenEnv(t)
	id, f := e.movie("Смешарики", "0+", "0123456789")
	_, tracks := e.simpleAlbum("Кино", "Звезда", 1)
	tr, _ := e.repo.TrackByID(t.Context(), tracks[0])
	toDevice := func() bool {
		var d struct{ Actions Actions }
		json.Unmarshal(e.do("GET", "/api/items/"+itoa(id), "", kc).Body.Bytes(), &d)
		return d.Actions.ToDevice
	}
	if w := e.do("GET", "/api/download/"+itoa(f), "", kc); w.Code != 200 || !toDevice() {
		t.Fatalf("no rules: %d %s", w.Code, w.Body)
	}
	e.limits(kid, `{"audioMin":120}`)
	if w := e.do("GET", "/api/download/"+itoa(f), "", kc); w.Code != 200 || !toDevice() {
		t.Fatalf("audio limited, a film: %d", w.Code)
	}
	refusedScreen(t, e.do("GET", "/api/download/"+itoa(tr.FileID), "", kc), "download")
	e.limits(kid, `{"videoMin":120}`)
	if toDevice() {
		t.Fatal("toDevice with video limited")
	}
	w := e.do("GET", "/api/download/"+itoa(f), "", kc)
	refusedScreen(t, w, "download")
	if !strings.Contains(e.do("GET", "/api/download/"+itoa(f), "", kc).Body.String(), "Скачивание выключено родителем") {
		t.Fatal("message")
	}
	// streaming plays on (time is left)
	if w := e.do("GET", "/api/stream/"+itoa(f), "", kc); w.Code != 200 {
		t.Fatalf("stream %d", w.Code)
	}
	book := e.book("Мастер", ".epub")
	files, _ := e.repo.FilesOf(t.Context(), book)
	if w := e.do("GET", "/api/download/"+itoa(files[0].ID), "", kc); w.Code != 200 {
		t.Fatalf("book %d", w.Code)
	}
	if w := e.do("GET", "/api/download/"+itoa(f), "", e.admin); w.Code != 200 {
		t.Fatalf("admin %d", w.Code)
	}
}

// An audiobook streamed to a kid ends mid-file at the limit, counted by
// what was sent.
func TestScreenTimeAudiobookCutMidFile(t *testing.T) {
	screentime.MeterEvery = 0
	t.Cleanup(func() { screentime.MeterEvery = 5 * time.Second })
	e, c, kid, kc := newScreenEnv(t)
	_, files := e.audiobook("Война и мир", 1)
	// 600 kB of a 600 s part: 1000 B/s
	fl, _ := e.repo.FileByID(t.Context(), files[0])
	if err := os.WriteFile(fl.Path, make([]byte, 600_000), 0o644); err != nil {
		t.Fatal(err)
	}
	e.db.Exec(`UPDATE files SET size=600000, duration_ms=600000 WHERE id=?`, files[0])
	e.limits(kid, `{"audioMin":1}`)
	// the player reads 70 s worth, then 10 s more each time (as it plays)
	url := "/api/audio/" + itoa(files[0]) + "?format=raw"
	read := func(from, to int) *httptest.ResponseRecorder {
		return e.do("GET", url, "", kc, "Range", "bytes="+itoa(int64(from))+"-"+itoa(int64(to)))
	}
	if w := read(0, 69_999); w.Code != 206 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	c.t = c.t.Add(70 * time.Second)
	refusedScreen(t, read(70_000, 79_999), "limit")
	w := e.do("GET", "/api/me/screen-time", "", kc)
	var st screentime.Status
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Audio == nil || st.Audio.UsedSec < 60 || st.Audio.UsedSec > 75 {
		t.Fatalf("%s", w.Body)
	}
}

// The sheet and «Профили» warn about profiles a kid could switch to
// without a password (adults, kids without rules); admin-only.
func TestScreenTimePasswordless(t *testing.T) {
	e, _, kid, kc := newScreenEnv(t)
	e.profile(`{"name":"Мама"}`)                                              // an adult without a password
	e.do("POST", "/api/users", `{"name":"Гость","password":"1234"}`, e.admin) // with one
	other, _ := e.profile(`{"name":"Петя","isKid":true}`)
	e.limits(kid, `{"videoMin":60}`)
	var rep struct{ Passwordless []string }
	json.Unmarshal(e.do("GET", "/api/users/"+itoa(kid)+"/screen-time", "", e.admin).Body.Bytes(), &rep)
	if strings.Join(rep.Passwordless, ",") != "Мама,Петя" {
		t.Fatalf("%v", rep.Passwordless)
	}
	e.limits(other, `{"videoMin":30}`) // a kid with rules is no way around them
	json.Unmarshal(e.do("GET", "/api/users/"+itoa(kid)+"/screen-time", "", e.admin).Body.Bytes(), &rep)
	if strings.Join(rep.Passwordless, ",") != "Мама" {
		t.Fatalf("%v", rep.Passwordless)
	}
	w := e.do("GET", "/api/screen-time/limited", "", e.admin)
	var lim struct {
		UserIDs      []int64 `json:"userIds"`
		Passwordless []struct{ Name string }
	}
	json.Unmarshal(w.Body.Bytes(), &lim)
	if w.Code != 200 || len(lim.UserIDs) != 2 || len(lim.Passwordless) != 1 || lim.Passwordless[0].Name != "Мама" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/screen-time/limited", "", kc); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
	// the kid's own status says what is limited
	var st screentime.Status
	json.Unmarshal(e.do("GET", "/api/me/screen-time", "", kc).Body.Bytes(), &st)
	if !st.VideoLimited || st.AudioLimited {
		t.Fatalf("%+v", st)
	}
}
