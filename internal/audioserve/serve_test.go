package audioserve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

func sineFLAC(t *testing.T, seconds string) (ffmpeg, path string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	path = filepath.Join(t.TempDir(), "image.flac")
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=22050:duration="+seconds, "-c:a", "flac", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	return ff, path
}

func serve(d Deps, f library.File, start int64, end *int64, req media.AudioRequest, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x?"+query, nil)
	Serve(w, r, d, f, start, end, req)
	return w
}

func TestServeTranscodeDurationHeader(t *testing.T) {
	ff, p := sineFLAC(t, "5")
	d := Deps{Transcoder: &media.AudioTranscoder{FFmpeg: ff, Limit: 4}}
	seg := media.AudioRequest{Codec: "flac", Lossless: true, Segment: true}
	end := int64(4000)
	w := serve(d, library.File{ID: 1, Path: p, DurationMs: 5000}, 2000, &end, seg, "")
	h := w.Header()
	if w.Code != 200 || h.Get("X-Content-Duration") != "2" || h.Get("Content-Type") != "audio/flac" ||
		h.Get("Accept-Ranges") != "none" || h.Get("Cache-Control") != "no-store" || h.Get("X-Vault-Transcode") != "1" || w.Body.Len() == 0 {
		t.Fatalf("%d %v %d", w.Code, h, w.Body.Len())
	}
	// last track of an image: no end_ms, file not probed yet
	w = serve(d, library.File{ID: 1, Path: p}, 2000, nil, seg, "")
	if w.Code != 200 || w.Header().Get("X-Content-Duration") != "" || w.Body.Len() == 0 {
		t.Fatalf("no duration: %d %v", w.Code, w.Header())
	}
	// a start past the end of a plain probed file
	if w := serve(d, library.File{ID: 1, Path: p, DurationMs: 5000}, 5000, nil, seg, ""); w.Code != 416 {
		t.Fatalf("past end: %d", w.Code)
	}
	// last track with a probed file: the file's duration ends it
	w = serve(d, library.File{ID: 1, Path: p, DurationMs: 5000}, 2500, nil, seg, "")
	if w.Header().Get("X-Content-Duration") != "2.5" {
		t.Fatalf("file duration: %v", w.Header())
	}
}

func TestServeDirectRange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.mp3")
	if err := os.WriteFile(p, []byte("0123456789abcdefghij"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Deps{Transcoder: &media.AudioTranscoder{FFmpeg: "/nonexistent"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Range", "bytes=2-5")
	Serve(w, r, d, library.File{Path: p}, 0, nil, media.AudioRequest{Codec: "mp3", BitrateKbps: 128})
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Type") != "audio/mpeg" ||
		w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%d %q %v", w.Code, w.Body, w.Header())
	}
	w = serve(d, library.File{Path: filepath.Join(t.TempDir(), "gone.mp3")}, 0, nil, media.AudioRequest{Codec: "mp3"}, "")
	if w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
}

func TestServeRemoteUsesSetting(t *testing.T) {
	ff, p := sineFLAC(t, "1")
	var asked int
	d := Deps{Transcoder: &media.AudioTranscoder{FFmpeg: ff},
		IsRemote:   func(*http.Request) bool { return true },
		RemoteKbps: func(context.Context) int { asked++; return 128 }}
	flac := media.AudioRequest{Codec: "flac", Lossless: true}
	w := serve(d, library.File{Path: p}, 0, nil, flac, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/aac" || asked != 1 {
		t.Fatalf("remote: %d %v", w.Code, w.Header())
	}
	// an explicit limit in the request wins, even 0 (no limit)
	explicit := flac
	explicit.MaxKbpsSet = true
	w = serve(d, library.File{Path: p}, 0, nil, explicit, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/flac" || w.Header().Get("X-Vault-Transcode") != "" {
		t.Fatalf("explicit: %d %v", w.Code, w.Header())
	}
	// the raw query is not consulted: only req carries the client's limit
	if w := serve(d, library.File{Path: p}, 0, nil, flac, "maxBitRate=0"); w.Header().Get("Content-Type") != "audio/aac" {
		t.Fatalf("query must not bypass the remote cap: %v", w.Header())
	}
	// lossless setting: original outside too
	d.RemoteKbps = func(context.Context) int { return 0 }
	if w := serve(d, library.File{Path: p}, 0, nil, flac, ""); w.Header().Get("X-Vault-Transcode") != "" {
		t.Fatalf("lossless remote: %v", w.Header())
	}
}

func TestServeBusy(t *testing.T) {
	ff, p := sineFLAC(t, "3")
	tr := &media.AudioTranscoder{FFmpeg: ff, Limit: 1}
	d := Deps{Transcoder: tr}
	// hold the only slot with a stream nobody reads
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- tr.Stream(context.Background(), pw, p, media.AudioPlan{Codec: "aac", Kbps: 128}, 0, nil)
	}()
	buf := make([]byte, 1)
	if _, err := pr.Read(buf); err != nil {
		t.Fatal(err)
	}
	w := serve(d, library.File{Path: p}, 0, nil, media.AudioRequest{Codec: "flac", Lossless: true, MaxKbps: 128}, "")
	var body struct{ Error string }
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" || body.Error != "Сервер занят, попробуйте через пару секунд" ||
		w.Header().Get("X-Vault-Transcode") != "" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
	go io.Copy(io.Discard, pr)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestParseQueryAndRequests(t *testing.T) {
	var req media.AudioRequest
	if err := ParseQuery(url.Values{"accept": {"FLAC, mp3"}, "maxBitRate": {"192"}, "format": {"opus"}}, &req); err != nil {
		t.Fatal(err)
	}
	if strings.Join(req.Accept, ",") != "flac,mp3" || req.MaxKbps != 192 || !req.MaxKbpsSet || req.Format != "opus" {
		t.Fatalf("%+v", req)
	}
	if err := ParseQuery(url.Values{"maxBitRate": {"0"}}, &req); err != nil || req.MaxKbps != 0 || !req.MaxKbpsSet {
		t.Fatalf("explicit 0: %+v %v", req, err)
	}
	if r := (media.AudioRequest{}); ParseQuery(url.Values{}, &r) != nil || r.MaxKbpsSet {
		t.Fatalf("absent: %+v", r)
	}
	for _, q := range []url.Values{{"maxBitRate": {"-1"}}, {"maxBitRate": {"x"}}, {"format": {"wma"}}} {
		if err := ParseQuery(q, &media.AudioRequest{}); err == nil {
			t.Errorf("%v accepted", q)
		}
	}
	for ext, codec := range map[string]string{".m4b": "aac", ".M4A": "aac", ".mp3": "mp3", ".flac": "flac", ".ogg": "vorbis", ".oga": "vorbis", ".opus": "opus"} {
		if r := FileRequest(library.File{Path: "/b/x" + ext}); r.Codec != codec || r.Lossless != (codec == "flac") {
			t.Errorf("%s: %+v", ext, r)
		}
	}
	for _, ext := range []string{".wav", ".aiff", ".aif"} {
		if r := FileRequest(library.File{Path: "/b/x" + ext}); !r.Lossless || r.Codec == "" {
			t.Errorf("%s: %+v", ext, r)
		}
	}
	if r := FileRequest(library.File{Path: "/b/x.caf", ProbeJSON: `{"Audio":[{"Codec":"pcm_s24le"}]}`}); !r.Lossless {
		t.Errorf("pcm: %+v", r)
	}
	probe := `{"Audio":[{"Codec":"mp3","BitRate":192000}]}`
	if r := FileRequest(library.File{Path: "/b/x.mp3", ProbeJSON: probe}); r.BitrateKbps != 192 {
		t.Errorf("probe bitrate: %+v", r)
	}
	if r := FileRequest(library.File{Path: "/b/x.mp3", Size: 1_600_000, DurationMs: 100_000}); r.BitrateKbps != 128 {
		t.Errorf("size bitrate: %+v", r)
	}
	end := int64(5000)
	tr := library.Track{Codec: "flac", Lossless: true, StartMs: 0, EndMs: &end}
	if r := TrackRequest(tr, library.File{Path: "/m/image.flac"}); !r.Segment || r.Codec != "flac" || !r.Lossless {
		t.Errorf("track: %+v", r)
	}
	if r := TrackRequest(library.Track{Codec: "mp3"}, library.File{Path: "/m/a.mp3"}); r.Segment {
		t.Errorf("plain track is not a segment: %+v", r)
	}
}
