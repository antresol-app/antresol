package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/subtitles"
)

func TestStreamRange(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789abcdefghij")
	w := e.do("GET", "/api/stream/"+itoa(f), "", e.admin, "Range", "bytes=0-9")
	if w.Code != 206 || w.Body.String() != "0123456789" || w.Header().Get("Content-Range") != "bytes 0-9/20" {
		t.Fatalf("%d %q %v", w.Code, w.Body, w.Header())
	}
	if w := e.do("GET", "/api/stream/"+itoa(f), "", nil); w.Code != 401 {
		t.Fatalf("anon %d", w.Code)
	}
}

func TestDownloadAttachment(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	w := e.do("GET", "/api/download/"+itoa(f), "", e.admin)
	cd := w.Header().Get("Content-Disposition")
	if w.Code != 200 || !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "%D0%91") || w.Body.Len() != 10 {
		t.Fatalf("%d %q", w.Code, cd)
	}
}

const hevcProbe = `{"Container":"mkv","DurationMs":5000,"Video":{"Codec":"hevc","Width":1920,"Height":1080,"BitDepth":10},
 "Audio":[{"Index":1,"Codec":"aac","Lang":"rus","Channels":2,"Default":true},{"Index":2,"Codec":"ac3","Lang":"eng","Channels":6}],
 "Subs":[{"Index":3,"Codec":"subrip","Lang":"rus"},{"Index":4,"Codec":"hdmv_pgs_subtitle","Lang":"eng"}]}`

func TestPlayReturnsHLSForUnsupported(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	if _, err := e.db.Exec(`UPDATE files SET probe_json=? WHERE id=?`, hevcProbe, f); err != nil {
		t.Fatal(err)
	}
	fl, err := e.repo.FileByID(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(fl.Path, ".mkv")+".en.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"startSec":12}`
	w := e.do("POST", "/api/play/"+itoa(f), body, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Mode      string  `json:"mode"`
		URL       string  `json:"url"`
		StartSec  float64 `json:"startSec"`
		Subtitles []struct {
			Index  int    `json:"index"`
			Lang   string `json:"lang"`
			URL    string `json:"url"`
			BurnIn bool   `json:"burnIn"`
		} `json:"subtitles"`
		Audio []struct {
			Index int `json:"index"`
		} `json:"audio"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "transcode" || out.URL != "/api/hls/sess1/index.m3u8" || out.StartSec != 12 || len(e.hls.started) != 1 {
		t.Fatalf("%s", w.Body)
	}
	if len(out.Audio) != 2 || len(out.Subtitles) != 3 {
		t.Fatalf("tracks: %s", w.Body)
	}
	s := out.Subtitles
	if s[0].Index != 3 || s[0].URL != "/api/subs/"+itoa(f)+"/3.vtt" || s[0].BurnIn ||
		!s[1].BurnIn || s[2].Index != 1000 || s[2].Lang != "en" {
		t.Fatalf("subs: %s", w.Body)
	}
	pl := e.do("GET", "/api/hls/sess1/index.m3u8", "", e.admin)
	if pl.Code != 200 || !strings.HasPrefix(pl.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist %d", pl.Code)
	}
	if w := e.do("GET", "/api/hls/nope/index.m3u8", "", e.admin); w.Code != 404 {
		t.Fatalf("missing session %d", w.Code)
	}
}

func TestPlayDirectForSupported(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`, e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"direct"`) || !strings.Contains(w.Body.String(), `"/api/stream/`+itoa(f)+`"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func (e *libEnv) probeJSON(fileID int64, js string) {
	if _, err := e.db.Exec(`UPDATE files SET probe_json=? WHERE id=?`, js, fileID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *libEnv) writeNextTo(fileID int64, name string, b []byte) {
	fl, err := e.repo.FileByID(context.Background(), fileID)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fl.Path), name), b, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestExternalSubtitleToVTT(t *testing.T) {
	e := newLibEnv(t)
	if _, err := lookFFmpeg(); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	_, f := e.movie("Брат - cd1", "16+", "0123456789")
	e.probeJSON(f, `{"Container":"mkv"}`)
	e.writeNextTo(f, "Брат - cd1.ru.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nПривет\n"))
	// UTF-16LE with BOM must not be decoded as CP1251.
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "1\n00:00:01,000 --> 00:00:02,000\nПока\n" {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	e.writeNextTo(f, "Брат - cd1.uk.srt", u16)
	cp := []byte("1\n00:00:01,000 --> 00:00:02,000\n\xcf\xf0\xe8\xe2\xe5\xf2\n") // "Привет" in CP1251
	e.writeNextTo(f, "Брат - cd1.rus.forced.srt", cp)
	e.writeNextTo(f, "Брат - cd1.Extended.ru.srt", []byte("x")) // another cut: not ours

	// sorted: .ru.srt, .rus.forced.srt, .uk.srt
	for i, want := range []string{"Привет", "Привет", "Пока"} {
		w := e.do("GET", "/api/subs/"+itoa(f)+"/"+itoa(int64(1000+i))+".vtt", "", e.admin)
		b, _ := io.ReadAll(w.Body)
		if w.Code != 200 || !strings.HasPrefix(string(b), "WEBVTT") || !strings.Contains(string(b), want) {
			t.Fatalf("%d: %d %s", 1000+i, w.Code, b)
		}
	}
	if w := e.do("GET", "/api/subs/"+itoa(f)+"/1003.vtt", "", e.admin); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestParseSubName(t *testing.T) {
	for _, c := range []struct {
		name, lang string
		ok         bool
	}{
		{"Movie.srt", "", true},
		{"Movie.en.srt", "en", true},
		{"Movie.rus.forced.srt", "ru", true},
		{"Movie.Russian.ass", "ru", true},
		{"Movie.Extended.ru.srt", "", false},
		{"Movie 2.ru.srt", "", false},
		{"Movie.en.mkv", "", false},
		{"Movie.en.commentary.srt", "en", true},
	} {
		lang, _, _, ok := subtitles.ParseName(c.name, "Movie")
		if ok != c.ok || lang != c.lang {
			t.Errorf("%s: %q %v", c.name, lang, ok)
		}
	}
}

// playSubs returns the subtitles of POST /api/play.
func (e *libEnv) playSubs(fileID int64) []SubTrack {
	e.t.Helper()
	w := e.do("POST", "/api/play/"+itoa(fileID), `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`, e.admin)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct{ Subtitles []SubTrack }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out.Subtitles
}

func TestSubtitleCommentaryName(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Movie", "16+", "x")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	e.writeNextTo(f, "Movie.en.commentary.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nx\n"))
	subs := e.playSubs(f)
	if len(subs) != 1 || subs[0].Lang != "en" || subs[0].Title != "commentary" || !subs[0].External || subs[0].Index != 1000 {
		t.Fatalf("%+v", subs)
	}
}

func TestExternalSubsKeepISOCode(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Movie", "16+", "x")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	e.writeNextTo(f, "Movie.Ukrainian.srt", []byte("x"))
	e.writeNextTo(f, "Movie.tr.srt", []byte("x"))
	subs := e.playSubs(f)
	if len(subs) != 2 || subs[0].Lang != "uk" || subs[0].Title != "" || subs[1].Lang != "tr" {
		t.Fatalf("%+v", subs) // the web's languageName names them
	}
}

func TestExternalSubsInSubsFolder(t *testing.T) {
	e := newLibEnv(t)
	if _, err := lookFFmpeg(); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	_, f := e.movie("Movie", "16+", "x")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	if err := os.MkdirAll(filepath.Join(e.media, "Subs", "Movie"), 0o755); err != nil {
		t.Fatal(err)
	}
	cp := []byte("1\n00:00:01,000 --> 00:00:02,000\n\xcf\xf0\xe8\xe2\xe5\xf2\n") // "Привет" in CP1251
	e.writeNextTo(f, "Subs/2_Russian.srt", cp)
	e.writeNextTo(f, "Subs/Movie/3_English.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	subs := e.playSubs(f)
	if len(subs) != 2 || subs[0].Lang != "ru" || subs[1].Lang != "en" || subs[0].Title != "" {
		t.Fatalf("%+v", subs)
	}
	for i, want := range []string{"Привет", "Hello"} {
		w := e.do("GET", subs[i].URL, "", e.admin)
		if b := w.Body.String(); w.Code != 200 || !strings.HasPrefix(b, "WEBVTT") || !strings.Contains(b, want) {
			t.Fatalf("%s: %d %s", subs[i].URL, w.Code, b)
		}
	}
}

func TestExternalSubsFromDataDir(t *testing.T) {
	dataDir := t.TempDir()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.DataDir = dataDir })
	_, f := e.movie("Movie", "16+", "x")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	if err := os.MkdirAll(filepath.Join(dataDir, "subs", "ext"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dataDir, "subs", "ext", itoa(f)+".ru.srt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dataDir, "subs", "ext", itoa(f+1)+".ru.srt"), []byte("x"), 0o644)
	subs := e.playSubs(f)
	if len(subs) != 1 || subs[0].Lang != "ru" || !subs[0].External {
		t.Fatalf("%+v", subs)
	}
}

func TestKidCannotFetchSubsOrArtOfAdultItem(t *testing.T) {
	e := newLibEnv(t)
	id, f := e.movie("Брат", "18+", "x")
	e.probeJSON(f, `{"Container":"mkv","Subs":[{"Index":2,"Codec":"subrip"}]}`)
	e.writeNextTo(f, "Брат.ru.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nx\n"))
	if err := os.WriteFile(filepath.Join(e.media, "fanart.jpg"), []byte("JPEG"), 0o644); err != nil {
		t.Fatal(err)
	}
	kid := e.kid("12+")
	for _, p := range []string{"/api/subs/" + itoa(f) + "/2.vtt", "/api/subs/" + itoa(f) + "/1000.vtt",
		"/api/images/local/" + itoa(id) + "/backdrop"} {
		if w := e.do("GET", p, "", kid); w.Code != 404 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestHLSSessionBoundToUser(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	if w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"]}}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	other := e.login(`{"name":"Мама"}`)
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", other); w.Code != 404 {
		t.Fatalf("other GET %d", w.Code)
	}
	if w := e.do("DELETE", "/api/hls/sess1", "", other); w.Code != 404 {
		t.Fatalf("other DELETE %d", w.Code)
	}
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", e.admin); w.Code != 200 {
		t.Fatalf("owner GET %d", w.Code)
	}
	if w := e.do("DELETE", "/api/hls/sess1", "", e.admin); w.Code != 204 {
		t.Fatalf("owner DELETE %d", w.Code)
	}
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", e.admin); w.Code != 404 {
		t.Fatalf("after stop %d", w.Code)
	}
}

func TestImagesRejectTraversal(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.movie("Брат", "16+", "x")
	if err := os.WriteFile(filepath.Join(e.media, "fanart.jpg"), []byte("JPEG"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/images/..%2F..%2Fetc%2Fpasswd", "/api/images/abc.jpg",
		"/api/images/local/" + itoa(id) + "/..%2F..%2Fetc%2Fpasswd", "/api/images/local/" + itoa(id) + "/fanart.jpg"} {
		if w := e.do("GET", p, "", e.admin); w.Code != 404 || !strings.Contains(w.Body.String(), "Картинка не найдена") {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body) // rejected by the handler, not by routing
		}
	}
	if w := e.do("GET", "/api/images/local/"+itoa(id)+"/backdrop", "", e.admin); w.Code != 200 || w.Body.String() != "JPEG" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func lookFFmpeg() (string, error) { return exec.LookPath("ffmpeg") }

// Subtitle inputs come from outside: ffmpeg may only read the file itself,
// through the subtitle demuxer of its extension.
func TestSubtitleInputsAreFileOnly(t *testing.T) {
	s := extSub{path: "/m/a.ru.srt", codec: "subrip"}
	cases := []struct {
		data []byte
		want string
	}{
		{[]byte("1\n00:00:01,000 --> 00:00:02,000\nПривет\n"), "-protocol_whitelist file -f srt -i file:/m/a.ru.srt"},
		{[]byte("\xcf\xf0\xe8"), "-sub_charenc CP1251 -protocol_whitelist file -f srt -i file:/m/a.ru.srt"},
		{[]byte{0xFF, 0xFE, '1', 0}, "-protocol_whitelist pipe -f srt -i pipe:0"},
	}
	for _, c := range cases {
		args, _ := subInput(s, c.data)
		if got := strings.Join(args, " "); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	if got := strings.Join(embeddedSubArgs("/m/a.mkv", 3), " "); got != "-protocol_whitelist file -i file:/m/a.mkv -map 0:3" {
		t.Errorf("embedded: %q", got)
	}
}

// F15: Auto writes <stem>.en.srt next to the video while it is watched; it
// sorts ahead of Subs/Rus.srt. A player that loaded /api/play before still
// gets the Russian file from the URL it holds.
func TestExternalSubtitleURLSurvivesNewFile(t *testing.T) {
	e := newLibEnv(t)
	if _, err := lookFFmpeg(); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	fl, err := e.repo.FileByID(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(filepath.Dir(fl.Path), "Subs")
	if err := os.MkdirAll(subs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subs, "Rus.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nПривет\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`, e.admin)
	var out struct{ Subtitles []SubTrack }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || len(out.Subtitles) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	url := out.Subtitles[0].URL
	e.writeNextTo(f, "Брат.en.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	w = e.do("GET", url, "", e.admin)
	if b, _ := io.ReadAll(w.Body); w.Code != 200 || !strings.Contains(string(b), "Привет") {
		t.Fatalf("%s after a new file: %d %s", url, w.Code, b)
	}
	// the file is gone: 404, not whatever took its place
	os.Remove(filepath.Join(subs, "Rus.srt"))
	if w := e.do("GET", url, "", e.admin); w.Code != 404 {
		t.Fatalf("%s removed: %d", url, w.Code)
	}
}

func TestPlayBurnsImageSubtitle(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}],
 "Subs":[{"Index":2,"Codec":"subrip","Lang":"rus"},{"Index":3,"Codec":"hdmv_pgs_subtitle","Lang":"eng"}]}`)
	caps := `"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}`
	// without a burn request this file plays direct
	if w := e.do("POST", "/api/play/"+itoa(f), `{`+caps+`}`, e.admin); !strings.Contains(w.Body.String(), `"mode":"direct"`) {
		t.Fatalf("%s", w.Body)
	}
	// a text track cannot be burnt; the image one forces a transcode
	if w := e.do("POST", "/api/play/"+itoa(f), `{`+caps+`,"burnSubtitle":2}`, e.admin); !strings.Contains(w.Body.String(), `"mode":"direct"`) {
		t.Fatalf("text track burnt: %s", w.Body)
	}
	w := e.do("POST", "/api/play/"+itoa(f), `{`+caps+`,"burnSubtitle":3}`, e.admin)
	if !strings.Contains(w.Body.String(), `"mode":"transcode"`) || !strings.Contains(w.Body.String(), `"burnedSubtitle":3`) {
		t.Fatalf("%s", w.Body)
	}
	if got := e.hls.started[len(e.hls.started)-1]; got.BurnSub != 3 || got.VideoCopy {
		t.Fatalf("decision %+v", got)
	}
}

// A pass still being written: only its complete cues count; the quick
// pass's cues join after the last of them.
func TestPartialSubJoinsThePasses(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "7-10.vtt")
	full := "WEBVTT\n\n00:01.000 --> 00:02.000\nfirst\n\n00:03.000 --> 00:04.000\nsecond\n\n00:05.000 --> 00:06"
	quick := "WEBVTT\n\n00:03.000 --> 00:04.000\nsecond\n\n01:00:00.000 --> 01:00:02.500\nlater\nline two\n\n"
	if partialSub(out) != nil {
		t.Fatal("nothing written yet: want nil")
	}
	os.WriteFile(out+".tmp", []byte(full), 0o644)
	os.WriteFile(out+subFromSuffix+".tmp", []byte(quick), 0o644)
	got := string(partialSub(out))
	want := "WEBVTT\n\n00:01.000 --> 00:02.000\nfirst\n\n00:03.000 --> 00:04.000\nsecond\n\n01:00:00.000 --> 01:00:02.500\nlater\nline two\n\n"
	if got != want {
		t.Fatalf("partial:\n%q\nwant\n%q", got, want)
	}
}
