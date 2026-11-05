package audioserve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// A transcode that fails after it has sent bytes must break the response,
// not end it cleanly: a client saving the stream (the native app's offline
// copy) has no length to check and would keep a truncated file as complete.
func TestServeTranscodeFailingMidwayAborts(t *testing.T) {
	dir := t.TempDir()
	ff := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nprintf 'partial-audio-bytes'\necho 'corrupt frame' >&2\nexit 1\n"
	if err := os.WriteFile(ff, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(dir, "part.flac")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Deps{Transcoder: &media.AudioTranscoder{FFmpeg: ff, Limit: 1}, WriteIdle: -1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, d, library.File{ID: 7, Path: in, DurationMs: 5000}, 0, nil,
			media.AudioRequest{Codec: "flac", Lossless: true, MaxKbps: 128})
	}))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		return // aborted before the headers: also a broken response
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err == nil {
		t.Fatalf("a failed transcode ended cleanly: %d %q", res.StatusCode, body)
	}
}
