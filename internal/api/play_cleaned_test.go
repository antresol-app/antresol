package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/media"
)

// cleanedEnv: «Брат», a 60 s movie with an English track (1, default) and
// LostFilm (2), and the cleaned LostFilm (voclean) in DataDir/audio. The
// file's timeline starts at startMs; the cleaned track was made when it
// started at 21 ms.
type cleanedEnv struct {
	*libEnv
	item, file int64
	cleaned    string
	dataDir    string
}

const cleanedProbe = `{"Container":"mp4","DurationMs":60000,%START%"Video":{"Codec":"h264","Height":720,"BitDepth":8},
	"Audio":[{"Index":1,"Codec":"aac","Lang":"eng","Channels":2,"Default":true},
	{"Index":2,"Codec":"aac","Lang":"rus","Title":"LostFilm","Channels":2}]}`

func newCleanedEnv(t *testing.T, start string) *cleanedEnv {
	x := &cleanedEnv{}
	x.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		x.dataDir = d.DataDir
		d.Probe = func(_ context.Context, p string) (media.ProbeResult, error) {
			if !strings.HasSuffix(p, ".mka") {
				t.Fatalf("probed %s", p)
			}
			return media.ProbeResult{Container: "matroska", DurationMs: 60_000,
				Audio: []media.AudioStream{{Index: 0, Codec: "eac3", Lang: "rus", Title: "LostFilm (очищено)", Channels: 6}}}, nil
		}
	})
	x.item, x.file = x.movie("Брат", "16+", "0123456789")
	x.probeJSON(x.file, strings.Replace(cleanedProbe, "%START%", start, 1))
	x.cleaned = filepath.Join(x.dataDir, "audio", itoa(x.file)+"-2.mka")
	if err := os.MkdirAll(filepath.Dir(x.cleaned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.cleaned, []byte("mka"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO cleaned_audio(file_id, vo_index, orig_index, studio, lang, title, state, path, file_size, file_mtime, start_ms)
		SELECT id, 2, 1, 'LostFilm', 'rus', 'LostFilm', 'done', ?, size, mtime, 21 FROM files WHERE id=?`, x.cleaned, x.file); err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *cleanedEnv) play(body string) (extPlayOut, int, string) {
	w := x.do("POST", "/api/play/"+itoa(x.file), body, x.admin)
	var out extPlayOut
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			x.t.Fatal(err)
		}
	}
	return out, w.Code, w.Body.String()
}

func (x *cleanedEnv) preferCleaned(on bool) {
	v := map[bool]string{true: "true", false: "false"}[on]
	if w := x.do("PUT", "/api/me/playback-prefs/prefer-cleaned", `{"on":`+v+`}`, x.admin); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"preferCleaned":`+v) {
		x.t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// prefs is GET /api/me/playback-prefs as cookie c sees it.
func (x *cleanedEnv) prefs(c *http.Cookie) playbackPrefs {
	w := x.do("GET", "/api/me/playback-prefs", "", c)
	var p playbackPrefs
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
		x.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return p
}

// The cleaned voice-over is listed after the file's tracks as an external
// track: kind "cleaned", its studio, method and the stream it was made
// from; the title is the studio track's (the players add the badge).
func TestPlayListsCleanedTrack(t *testing.T) {
	x := newCleanedEnv(t, `"StartMs":21,`)
	out, code, body := x.play(`{` + extDirectCaps + `}`)
	if code != 200 || out.Mode != "direct" || out.AudioIndex != 1 || len(out.Audio) != 3 {
		t.Fatalf("%d %s", code, body)
	}
	a := out.Audio[2]
	if a.Index != externalAudioBase || !a.External || a.Kind != "cleaned" || a.Method != "subtraction" || a.Studio != "LostFilm" ||
		a.Title != "LostFilm" || a.Lang != "rus" || a.CleanedFrom == nil || *a.CleanedFrom != 2 || a.Src != extaudio.ID(x.cleaned) ||
		a.Codec != "eac3" || a.Channels != 6 || a.Default {
		t.Fatalf("%+v", a)
	}
	if out.Audio[1].Kind != "" || out.Audio[1].CleanedFrom != nil {
		t.Fatalf("studio track: %+v", out.Audio[1])
	}
	// the item page lists it the same way
	w := x.do("GET", "/api/items/"+itoa(x.item), "", x.admin)
	var d struct {
		Files []FileInfo `json:"files"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Files) != 1 || len(d.Files[0].Audio) != 3 ||
		d.Files[0].Audio[2].Kind != "cleaned" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Chosen, it plays through the second-input path; on a timeline that
// began where it did, at offset 0.
func TestPlayCleanedTrackSecondInput(t *testing.T) {
	x := newCleanedEnv(t, `"StartMs":21,`)
	out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2000,"audioSrc":"` + extaudio.ID(x.cleaned) + `","startSec":30}`)
	if code != 200 || out.Mode != "remux" || out.AudioIndex != externalAudioBase || len(x.hls.started) != 1 {
		t.Fatalf("%d %s", code, body)
	}
	d := x.hls.started[0]
	if d.AudioInput != x.cleaned || d.AudioInputCodec != "eac3" || d.AudioInputOffsetMs != 0 {
		t.Fatalf("%+v", d)
	}
}

// cleaned_audio.start_ms is applied: the file's timeline now begins 21 ms
// before where the cleaned track was made to start (its probe says 0), so
// the track's input is shifted by +21 ms.
func TestPlayCleanedTrackOffsetApplied(t *testing.T) {
	x := newCleanedEnv(t, `"StartMs":0,`)
	if _, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2000,"audioSrc":"` + extaudio.ID(x.cleaned) + `"}`); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if d := x.hls.started[0]; d.AudioInput != x.cleaned || d.AudioInputOffsetMs != 21 {
		t.Fatalf("%+v", d)
	}
	// a probe cached before the start was kept: what the track was made on (0)
	x2 := newCleanedEnv(t, ``)
	x2.play(`{` + extDirectCaps + `,"audioIndex":2000}`)
	if d := x2.hls.started[0]; d.AudioInput != x2.cleaned || d.AudioInputOffsetMs != 0 {
		t.Fatalf("%+v", d)
	}
}

// A file replaced since (another size): its old cleaned track is not offered.
func TestPlayCleanedTrackOfReplacedFile(t *testing.T) {
	x := newCleanedEnv(t, `"StartMs":21,`)
	if _, err := x.db.Exec(`UPDATE files SET size=size+1 WHERE id=?`, x.file); err != nil {
		t.Fatal(err)
	}
	if out, code, body := x.play(`{` + extDirectCaps + `}`); code != 200 || len(out.Audio) != 2 {
		t.Fatalf("%d %s", code, body)
	}
}

// «Выбирать очищенную озвучку»: the server's own choice (no track asked
// for) takes the cleaned version of the track it would have played.
func TestPlayPreferCleanedDefaultTrack(t *testing.T) {
	x := newCleanedEnv(t, `"StartMs":21,`)
	// LostFilm is the file's default
	x.probeJSON(x.file, `{"Container":"mp4","DurationMs":60000,"StartMs":21,"Video":{"Codec":"h264","Height":720,"BitDepth":8},
		"Audio":[{"Index":1,"Codec":"aac","Lang":"eng","Channels":2},
		{"Index":2,"Codec":"aac","Lang":"rus","Title":"LostFilm","Channels":2,"Default":true}]}`)
	if out, code, body := x.play(`{` + extDirectCaps + `}`); code != 200 || out.Mode != "direct" || out.AudioIndex != 2 {
		t.Fatalf("off: %d %s", code, body)
	}
	x.preferCleaned(true)
	out, code, body := x.play(`{` + extDirectCaps + `}`)
	if code != 200 || out.Mode != "remux" || out.AudioIndex != externalAudioBase || len(x.hls.started) != 1 || x.hls.started[0].AudioInput != x.cleaned {
		t.Fatalf("on: %d %s", code, body)
	}
	// a track asked for is played as asked (the players swap it themselves)
	if out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2}`); code != 200 || out.AudioIndex != 2 || out.Mode != "direct" {
		t.Fatalf("asked: %d %s", code, body)
	}
	// the default English has no cleaned version: it plays
	x.probeJSON(x.file, strings.Replace(cleanedProbe, "%START%", `"StartMs":21,`, 1))
	if out, code, body := x.play(`{` + extDirectCaps + `}`); code != 200 || out.AudioIndex != 1 {
		t.Fatalf("english: %d %s", code, body)
	}
}

// The preference is a profile's, off by default, stored apart from its
// languages: switching it keeps a profile on the household's «Языки».
func TestPlaybackPrefsPreferCleaned(t *testing.T) {
	x := newCleanedEnv(t, ``)
	if p := x.prefs(x.admin); p.PreferCleaned || p.Custom {
		t.Fatalf("%+v", p)
	}
	x.preferCleaned(true)
	if p := x.prefs(x.admin); !p.PreferCleaned || p.Custom {
		t.Fatalf("switching it made the prefs the profile's own: %+v", p)
	}
	w := x.do("GET", "/api/items/"+itoa(x.item)+"/track-prefs", "", x.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"preferCleaned":true`) || !strings.Contains(w.Body.String(), `"custom":false`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	x.preferCleaned(false)
	if p := x.prefs(x.admin); p.PreferCleaned {
		t.Fatalf("%+v", p)
	}
}

// The full PUT never changes it (a client that does not know it sends
// none), «Как у семьи» (DELETE) keeps it.
func TestPlaybackPrefsPreferCleanedSurvives(t *testing.T) {
	x := newCleanedEnv(t, ``)
	x.preferCleaned(true)
	for _, b := range []string{
		`{"audioLangs":["en"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"translucent"}`,
		`{"audioLangs":["en"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"translucent","preferCleaned":false}`,
	} {
		w := x.do("PUT", "/api/me/playback-prefs", b, x.admin)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"preferCleaned":true`) || !strings.Contains(w.Body.String(), `"custom":true`) {
			t.Fatalf("%s: %d %s", b, w.Code, w.Body)
		}
	}
	w := x.do("DELETE", "/api/me/playback-prefs", "", x.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"preferCleaned":true`) || !strings.Contains(w.Body.String(), `"custom":false`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A kid's is the administrator's, like the rest of «Звук и субтитры».
func TestPreferCleanedKidAndAdmin(t *testing.T) {
	x := newCleanedEnv(t, ``)
	kid := x.kid("")
	if w := x.do("PUT", "/api/me/playback-prefs/prefer-cleaned", `{"on":true}`, kid); w.Code != 403 {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	var me struct {
		ID int64 `json:"id"`
	}
	if w := x.do("GET", "/api/me", "", kid); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := x.do("PUT", "/api/users/"+itoa(me.ID)+"/playback-prefs/prefer-cleaned", `{"on":true}`, x.admin); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"preferCleaned":true`) {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	if p := x.prefs(kid); !p.PreferCleaned || p.Custom {
		t.Fatalf("%+v", p)
	}
	if w := x.do("PUT", "/api/users/"+itoa(me.ID)+"/playback-prefs/prefer-cleaned", `{"on":true}`, kid); w.Code != 403 {
		t.Fatalf("kid on the admin route: %d", w.Code)
	}
	if w := x.do("PUT", "/api/me/playback-prefs/prefer-cleaned", `{}`, x.admin); w.Code != 400 {
		t.Fatalf("no on: %d", w.Code)
	}
}

// A hand-picked cleaned track is remembered as such for the series.
func TestSeriesMemoryKeepsCleanedKind(t *testing.T) {
	x := newCleanedEnv(t, ``)
	_, eps := x.series("Миллиарды", "16+", 1)
	ep := eps[0]
	for _, k := range []string{"cleaned", ""} {
		body := `{"audio":{"lang":"rus","title":"LostFilm","kind":"` + k + `"}}`
		if w := x.do("PUT", "/api/items/"+itoa(ep)+"/track-prefs", body, x.admin); w.Code != 204 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		w := x.do("GET", "/api/items/"+itoa(ep)+"/track-prefs", "", x.admin)
		want := `"audio":{"lang":"rus","title":"LostFilm"}`
		if k != "" {
			want = `"audio":{"lang":"rus","title":"LostFilm","kind":"cleaned"}`
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%q: %d %s", k, w.Code, w.Body)
		}
	}
	if w := x.do("PUT", "/api/items/"+itoa(ep)+"/track-prefs", `{"audio":{"lang":"rus","title":"LostFilm","kind":"dub"}}`, x.admin); w.Code != 400 {
		t.Fatalf("unknown kind: %d", w.Code)
	}
}

// The device copy of the cleaned track: its input shifted by the offset,
// its title saying it is cleaned (the phone's player has no badge); with
// the preference, a copy asked without a track takes it.
func TestDeviceCopyCleanedTrack(t *testing.T) {
	var dataDir string
	c := newCopyEnv(t, func(d *Deps, db *sql.DB) {
		d.DB, dataDir = db, d.DataDir
		d.Probe = func(_ context.Context, p string) (media.ProbeResult, error) {
			return media.ProbeResult{DurationMs: 7_200_000, Audio: []media.AudioStream{{Codec: "eac3", Channels: 6}}}, nil
		}
	})
	_, f := c.movie("Брат", "16+", "0123456789")
	c.probeJSON(f, `{"Container":"mkv","DurationMs":7200000,"StartMs":0,"Video":{"Codec":"h264","Width":1920,"Height":1080,"BitDepth":8,"BitRate":30000000},
		"Audio":[{"Index":1,"Codec":"aac","Lang":"rus","Title":"LostFilm","Channels":2,"Default":true}]}`)
	cleaned := filepath.Join(dataDir, "audio", itoa(f)+"-1.mka")
	os.MkdirAll(filepath.Dir(cleaned), 0o755)
	if err := os.WriteFile(cleaned, []byte("mka"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO cleaned_audio(file_id, vo_index, orig_index, studio, lang, title, state, path, file_size, file_mtime, start_ms)
		SELECT id, 1, 1, 'LostFilm', 'rus', 'LostFilm', 'done', ?, size, mtime, 21 FROM files WHERE id=?`, cleaned, f); err != nil {
		t.Fatal(err)
	}
	w := c.do("POST", "/api/device-copies", `{"fileId":`+itoa(f)+`,"quality":"original","audioIndex":2000,"audioSrc":"`+extaudio.ID(cleaned)+`"}`, c.admin)
	var st media.CopyStatus
	if json.Unmarshal(w.Body.Bytes(), &st) != nil || w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	c.waitReady(st.ID, c.admin)
	j := strings.Join(c.lastArgs(), " ")
	if !strings.Contains(j, "-itsoffset 0.021 -protocol_whitelist file -format_whitelist "+media.AudioFormats+" -i file:"+cleaned) ||
		!strings.Contains(j, "title=LostFilm (очищено)") {
		t.Fatalf("%s", j)
	}
	// preferred: the copy without a track is the cleaned one's
	if w := c.do("PUT", "/api/me/playback-prefs/prefer-cleaned", `{"on":true}`, c.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = c.do("POST", "/api/device-copies", `{"fileId":`+itoa(f)+`,"quality":"original"}`, c.admin)
	var st2 media.CopyStatus
	if json.Unmarshal(w.Body.Bytes(), &st2) != nil || st2.ID != st.ID {
		t.Fatalf("%d %s (want %s)", w.Code, w.Body, st.ID)
	}
}
