package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInputArgs(t *testing.T) {
	opts, op, err := InputArgs("/m/a.mkv")
	if err != nil || strings.Join(opts, " ") != "-protocol_whitelist file" || op != "file:/m/a.mkv" {
		t.Fatalf("local: %v %q %v", opts, op, err)
	}
	if _, op, _ := InputArgs("/m/concat:x://y.mkv"); op != "file:/m/concat:x://y.mkv" {
		t.Fatalf("absolute path with :// is a file: %q", op)
	}
	opts, op, err = InputArgs("http://127.0.0.1:4100/stream/abc/0")
	if err != nil || op != "http://127.0.0.1:4100/stream/abc/0" ||
		!strings.Contains(strings.Join(opts, " "), "-protocol_whitelist http,tcp -format_whitelist") {
		t.Fatalf("loopback: %v %q %v", opts, op, err)
	}
	if strings.Contains(strings.Join(opts, " "), "hls") || strings.Contains(strings.Join(opts, " "), "concat") {
		t.Fatalf("playlist demuxers allowed: %v", opts)
	}
	for _, u := range []string{"http://192.168.1.5/x", "https://127.0.0.1/x", "http://localhost/x",
		"http://u@127.0.0.1/x", "rtp://127.0.0.1:5000", "ftp://127.0.0.1/x"} {
		if _, _, err := InputArgs(u); !errors.Is(err, ErrUnsafeInput) {
			t.Errorf("%s: %v", u, err)
		}
	}
	if !IsLoopbackURL("http://[::1]:80/x") {
		t.Error("::1 is loopback")
	}
}

func TestBuildArgsWhitelistsProtocols(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1}
	local := strings.Join(BuildArgs("none", "/m/in.mkv", d, 0, "/w"), " ")
	if !strings.Contains(local, "-protocol_whitelist file -i file:/m/in.mkv") {
		t.Fatalf("local: %s", local)
	}
	remote := strings.Join(BuildArgs("none", "http://127.0.0.1:1/s", d, 0, "/w"), " ")
	if !strings.Contains(remote, "-protocol_whitelist http,tcp -format_whitelist ") || !strings.Contains(remote, "-i http://127.0.0.1:1/s") {
		t.Fatalf("loopback: %s", remote)
	}
	lan := strings.Join(BuildArgs("none", "http://10.0.0.1/s", d, 0, "/w"), " ")
	if strings.Contains(lan, "10.0.0.1") {
		t.Fatalf("LAN url passed to ffmpeg: %s", lan)
	}
}

func TestHLSStartRejectsLANURL(t *testing.T) {
	m := &HLSManager{FFmpeg: "/nonexistent/ffmpeg", WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	if _, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "http://192.168.1.10/x.mkv", Decision: Decision{AudioIndex: -1}, AudioIndex: -1}); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("got %v", err)
	}
}

// fakeTool writes a script that records its arguments (one per line) and
// prints an empty ffprobe JSON.
func fakeTool(t *testing.T) (bin, argsFile string) {
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "tool"), filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > " + argsFile + "\necho '{}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestProbeWhitelistsFileProtocol(t *testing.T) {
	bin, argsFile := fakeTool(t)
	if _, err := Probe(context.Background(), bin, "/m/a.mkv"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(b), "-protocol_whitelist\nfile\n") || !strings.HasSuffix(string(b), "file:/m/a.mkv\n") {
		t.Fatalf("args:\n%s", b)
	}
	if _, err := Probe(context.Background(), bin, "http://10.1.1.1/x"); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("LAN probe: %v", err)
	}
}

// hostilePlaylists writes downloaded-looking files whose content is a playlist
// pointing at srv. Older ffmpeg builds detect HLS by content regardless of
// the extension; newer ones by extension, hence both names.
func hostilePlaylists(t *testing.T, srv string) []string {
	dir := t.TempDir()
	hls := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\n" + srv + "/seg.ts\n#EXT-X-ENDLIST\n"
	files := map[string]string{
		"x.mkv":    hls,
		"x.m3u8":   hls,
		"cat.mkv":  "ffconcat version 1.0\nfile " + srv + "/concat.mkv\n",
		"dash.mkv": `<?xml version="1.0"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT6S"><Period><AdaptationSet><Representation id="1" bandwidth="1" mimeType="video/mp4"><BaseURL>` + srv + `/dash.mp4</BaseURL></Representation></AdaptationSet></Period></MPD>`,
	}
	var out []string
	for n, c := range files {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestProbeHostilePlaylistDoesNotFetch(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for _, p := range hostilePlaylists(t, srv.URL) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		Probe(ctx, ffprobe, p)
		cancel()
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("ffprobe fetched %d URLs from hostile files", n)
	}
}

func TestStreamFormatWhitelistAudio(t *testing.T) {
	opts, _, err := InputArgs("http://127.0.0.1:4100/stream/abc/3")
	if err != nil {
		t.Fatal(err)
	}
	var wl string
	for i, o := range opts {
		if o == "-format_whitelist" && i+1 < len(opts) {
			wl = opts[i+1]
		}
	}
	set := map[string]bool{}
	for _, f := range strings.Split(wl, ",") {
		set[f] = true
	}
	for _, f := range []string{"flac", "mp3", "wav", "aac", "ac3", "eac3", "dts", "ape", "wv", "aiff", "ogg", "mov"} {
		if !set[f] {
			t.Errorf("%s missing from %q", f, wl)
		}
	}
	for _, f := range []string{"hls", "dash", "concat", "sdp"} {
		if set[f] {
			t.Errorf("%s allowed in %q", f, wl)
		}
	}
}
