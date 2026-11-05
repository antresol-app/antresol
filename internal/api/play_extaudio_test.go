package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/media"
)

// extAudioEnv: a direct-playable 60 s movie «Брат» with «Брат.LostFilm.mka»
// (AC-3 5.1) beside it. Only the external file is probed (the movie's probe
// is cached); probes counts the calls.
type extAudioEnv struct {
	*libEnv
	item, file int64
	ext        string
	probes     atomic.Int32
	probeErr   error
}

func newExtAudioEnv(t *testing.T) *extAudioEnv {
	x := &extAudioEnv{}
	x.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db // the fits of the tracks (vofit)
		d.Probe = func(_ context.Context, p string) (media.ProbeResult, error) {
			if !strings.HasSuffix(p, ".mka") {
				t.Fatalf("probed %s", p)
			}
			x.probes.Add(1)
			if x.probeErr != nil {
				return media.ProbeResult{}, x.probeErr
			}
			return media.ProbeResult{Container: "matroska", DurationMs: 61_000,
				Audio: []media.AudioStream{{Index: 0, Codec: "ac3", Channels: 6, Default: true}}}, nil
		}
	})
	x.item, x.file = x.movie("Брат", "16+", "0123456789")
	x.probeJSON(x.file, `{"Container":"mp4","DurationMs":60000,"Video":{"Codec":"h264","Height":720,"BitDepth":8},
		"Audio":[{"Index":1,"Codec":"aac","Lang":"eng","Channels":2,"Default":true}]}`)
	x.writeNextTo(x.file, "Брат.LostFilm.mka", []byte("mka"))
	x.ext = filepath.Join(x.media, "Брат.LostFilm.mka")
	return x
}

type extPlayOut struct {
	Mode       string       `json:"mode"`
	AudioIndex int          `json:"audioIndex"`
	Audio      []AudioTrack `json:"audio"`
	URL        string       `json:"url"`
}

func (x *extAudioEnv) play(body string) (extPlayOut, int, string) {
	w := x.do("POST", "/api/play/"+itoa(x.file), body, x.admin)
	var out extPlayOut
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			x.t.Fatal(err)
		}
	}
	return out, w.Code, w.Body.String()
}

const extDirectCaps = `"caps":{"videoCodecs":["h264"],"audioCodecs":["aac","ac3"],"containers":["mp4"]}`

func TestPlayListsExternalAudio(t *testing.T) {
	x := newExtAudioEnv(t)
	out, code, body := x.play(`{` + extDirectCaps + `}`)
	if code != 200 || out.Mode != "direct" || out.AudioIndex != 1 {
		t.Fatalf("%d %s", code, body)
	}
	if len(out.Audio) != 2 || out.Audio[0].Index != 1 || out.Audio[0].External {
		t.Fatalf("%s", body)
	}
	a := out.Audio[1]
	if a.Index != externalAudioBase || !a.External || a.Src != extaudio.ID(x.ext) || a.Studio != "LostFilm" || a.Title != "LostFilm" ||
		a.Lang != "ru" || a.Codec != "ac3" || a.Channels != 6 || a.Default {
		t.Fatalf("%+v", a)
	}
	// probed once: the description is cached by path, size and mtime
	x.play(`{` + extDirectCaps + `}`)
	if n := x.probes.Load(); n != 1 {
		t.Fatalf("probes %d", n)
	}
}

// Review focus 4: choosing the external track plays HLS, with the file as
// the second input, even for a file that plays direct.
func TestPlayExternalAudioForcesHLS(t *testing.T) {
	x := newExtAudioEnv(t)
	out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2000,"audioSrc":"` + extaudio.ID(x.ext) + `","startSec":2400}`)
	if code != 200 || out.Mode != "remux" || out.AudioIndex != externalAudioBase || !strings.HasPrefix(out.URL, "/api/hls/") {
		t.Fatalf("%d %s", code, body)
	}
	if len(x.hls.started) != 1 {
		t.Fatalf("started %d", len(x.hls.started))
	}
	d := x.hls.started[0]
	if d.AudioInput != x.ext || d.AudioIndex != 0 || !d.AudioCopy || d.AudioInputCodec != "ac3" {
		t.Fatalf("%+v", d)
	}
	// by position alone (no src): the same track
	if out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2000}`); code != 200 || out.Mode != "remux" || out.AudioIndex != 2000 {
		t.Fatalf("%d %s", code, body)
	}
}

// A track gone meanwhile (a stale id, a position past the list): the
// file's default track plays, as for an unknown embedded index.
func TestPlayExternalAudioStaleSrc(t *testing.T) {
	x := newExtAudioEnv(t)
	out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2000,"audioSrc":"deadbeef"}`)
	if code != 200 || out.Mode != "direct" || out.AudioIndex != 1 {
		t.Fatalf("%d %s", code, body)
	}
	if out, code, body := x.play(`{` + extDirectCaps + `,"audioIndex":2001}`); code != 200 || out.AudioIndex != 1 {
		t.Fatalf("past the list: %d %s", code, body)
	}
	if len(x.hls.started) != 0 {
		t.Fatalf("started %+v", x.hls.started)
	}
}

// The item page and the player describe the track against the same
// (probed) duration: one probe, whatever the scan stored.
func TestExternalAudioOneDurationSource(t *testing.T) {
	x := newExtAudioEnv(t)
	if _, err := x.db.Exec(`UPDATE files SET duration_ms=59000 WHERE id=?`, x.file); err != nil {
		t.Fatal(err)
	}
	if w := x.do("GET", "/api/items/"+itoa(x.item), "", x.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	x.play(`{` + extDirectCaps + `}`)
	if n := x.probes.Load(); n != 1 {
		t.Fatalf("probes %d", n)
	}
}

// Review focus 3: an unreadable track is left out; the file plays as before.
func TestPlayExternalAudioUnreadable(t *testing.T) {
	x := newExtAudioEnv(t)
	x.probeErr = errors.New("ffprobe: invalid data")
	out, code, body := x.play(`{` + extDirectCaps + `}`)
	if code != 200 || out.Mode != "direct" || len(out.Audio) != 1 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestItemFilesListExternalAudio(t *testing.T) {
	x := newExtAudioEnv(t)
	w := x.do("GET", "/api/items/"+itoa(x.item), "", x.admin)
	var d struct {
		Files []FileInfo `json:"files"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Files) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if a := d.Files[0].Audio; len(a) != 2 || a[1].Index != 2000 || !a[1].External || a[1].Src != extaudio.ID(x.ext) || a[1].Studio != "LostFilm" {
		t.Fatalf("%+v", a)
	}
}

// Review focus 5: the external track is the copy's second input, and
// another track (or none) is another copy.
func TestDeviceCopyExternalAudio(t *testing.T) {
	var probes atomic.Int32
	c := newCopyEnv(t, func(d *Deps, _ *sql.DB) {
		d.Probe = func(_ context.Context, p string) (media.ProbeResult, error) {
			probes.Add(1)
			return media.ProbeResult{DurationMs: 7_200_000, Audio: []media.AudioStream{{Codec: "ac3", Channels: 6}}}, nil
		}
	})
	_, f := c.movie("Брат", "16+", "0123456789")
	c.probeJSON(f, remuxProbe)
	c.writeNextTo(f, "Брат.LostFilm.mka", []byte("a"))
	c.writeNextTo(f, "Брат.AlexFilm.mka", []byte("b"))
	post := func(body string) (int, media.CopyStatus, string) {
		w := c.do("POST", "/api/device-copies", body, c.admin)
		var st media.CopyStatus
		json.Unmarshal(w.Body.Bytes(), &st)
		return w.Code, st, w.Body.String()
	}
	_, plain, _ := c.post(f, "original", c.admin)
	c.waitReady(plain.ID, c.admin)
	lost := extaudio.ID(filepath.Join(c.media, "Брат.LostFilm.mka"))
	alex := extaudio.ID(filepath.Join(c.media, "Брат.AlexFilm.mka"))
	code, a, body := post(`{"fileId":` + itoa(f) + `,"quality":"original","audioIndex":2001,"audioSrc":"` + lost + `"}`)
	if code != 202 || a.ID == plain.ID {
		t.Fatalf("%d %s", code, body)
	}
	c.waitReady(a.ID, c.admin)
	j := strings.Join(c.lastArgs(), " ")
	if !strings.Contains(j, "-format_whitelist "+media.AudioFormats+" -i file:"+filepath.Join(c.media, "Брат.LostFilm.mka")) || !strings.Contains(j, "-map 1:a:0") ||
		!strings.Contains(j, "title=LostFilm") {
		t.Fatalf("%s", j)
	}
	_, b, _ := post(`{"fileId":` + itoa(f) + `,"quality":"original","audioIndex":2000,"audioSrc":"` + alex + `"}`)
	if b.ID == a.ID || b.ID == plain.ID {
		t.Fatal(plain.ID, a.ID, b.ID)
	}
	// a stale id: the copy of the default track
	if code, st, body := post(`{"fileId":` + itoa(f) + `,"quality":"original","audioIndex":2000,"audioSrc":"00000000"}`); code != 200 || st.ID != plain.ID {
		t.Fatalf("%d %s", code, body)
	}
}

// The first App Store build (1.0) knows no external audio: its play and
// item answers leave the files beside the video out, and it is never
// switched to one (a profile's «Очищено» included). A newer app says it
// understands them (X-Vault-Caps), and a browser always gets them.
func TestExternalAudioOnlyForClientsThatKnowIt(t *testing.T) {
	x := newExtAudioEnv(t)
	tok := x.deviceLogin(1, "secret", "phone-0001")
	tracks := func(hdr ...string) int {
		t.Helper()
		w := x.do("POST", "/api/play/"+itoa(x.file), `{`+extDirectCaps+`}`, nil, append(bearer(tok), hdr...)...)
		var out extPlayOut
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if out.Mode != "direct" || out.AudioIndex != 1 {
			t.Fatalf("%s", w.Body)
		}
		return len(out.Audio)
	}
	if n := tracks(); n != 1 {
		t.Fatalf("old app: %d tracks", n)
	}
	if n := tracks("X-Vault-Caps", "external-audio"); n != 2 {
		t.Fatalf("new app: %d tracks", n)
	}
	if w := x.do("GET", "/api/items/"+itoa(x.item), "", nil, bearer(tok)...); w.Code != 200 || strings.Contains(w.Body.String(), "LostFilm") {
		t.Fatalf("old app's item: %d %s", w.Code, w.Body)
	}
	if w := x.do("GET", "/api/items/"+itoa(x.item), "", nil, append(bearer(tok), "X-Vault-Caps", "external-audio")...); !strings.Contains(w.Body.String(), "LostFilm") {
		t.Fatalf("new app's item: %d %s", w.Code, w.Body)
	}
	if out, _, body := x.play(`{` + extDirectCaps + `}`); len(out.Audio) != 2 { // the browser
		t.Fatalf("web: %s", body)
	}
}
