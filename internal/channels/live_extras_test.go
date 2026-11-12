package channels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

func TestLiveArgsReadsALoopbackURLWithItsHeaders(t *testing.T) {
	in := media.LiveInput{URL: "http://127.0.0.1:5/s/x.m3u8", Headers: map[string]string{"X-B": "2", "X-A": "1"}, Audio: []media.LiveAudio{{Index: media.LiveFirstAudio}},
		OutDir: "/o", Playlist: "run.m3u8"}
	if err := in.Check(); err != nil {
		t.Fatal(err)
	}
	a := strings.Join(media.LiveArgs("none", in), " ")
	for _, want := range []string{"-protocol_whitelist http,tcp,crypto", ",hls", "-headers X-A: 1\r\nX-B: 2\r\n", "-i http://127.0.0.1:5/s/x.m3u8", "-map 0:a:0"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	if strings.Contains(a, "file:") || strings.Contains(a, "anullsrc") {
		t.Errorf("file input or silence: %s", a)
	}
	for _, bad := range []media.LiveInput{
		{URL: "http://192.168.1.10/a.mp4"}, {URL: "https://cdn.example/a.m3u8"}, {URL: "ftp://127.0.0.1/a.mp4"},
		{URL: "file:///etc/passwd"}, {URL: "http://127.0.0.1/a", Headers: map[string]string{"X": "a\r\nHost: evil"}},
	} {
		if bad.Check() == nil {
			t.Errorf("Check passed %+v", bad)
		}
	}
}

// seqSource plays progs one after another from base, then nothing.
type seqSource struct {
	base  time.Time
	progs []Program
	mu    sync.Mutex
	asked map[int64]int
}

func (s *seqSource) Program(_ context.Context, ch int64, t time.Time) (Program, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := s.base
	for i, p := range s.progs {
		d := p.End.Sub(p.Start)
		if t.Before(start.Add(d)) {
			p.Start, p.End, p.Seq, p.Channel = start, start.Add(d), int64(i+1), ch
			if s.asked == nil {
				s.asked = map[int64]int{}
			}
			s.asked[p.Seq]++
			return p, nil
		}
		start = start.Add(d)
	}
	return Program{}, ErrNothing
}

// TestLiveSkipsAnExtraThatFailsAndGoesOn: real ffmpeg. A library
// programme, a title outside the library whose stream is dead, five more
// such in a row reported as skipped, one that plays over a loopback URL,
// then the library again: the channel never stops, the dead one leaves no
// segments, the good one does, and its stream is released.
func TestLiveSkipsAnExtraThatFailsAndGoesOn(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	mk := func(name string, args ...string) string {
		p := filepath.Join(dir, name)
		if out, err := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, args...), p)...).CombinedOutput(); err != nil {
			t.Fatalf("make %s: %v %s", name, err, out)
		}
		return p
	}
	lib := mk("a.mkv", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=4", "-f", "lavfi", "-i", "sine=f=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest")
	ext := mk("x.mp4", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=4", "-f", "lavfi", "-i", "sine=f=220:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", "-movflags", "+faststart")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Relay-Key") != "k" {
			http.Error(w, "no key", 403)
			return
		}
		http.ServeFile(w, r, ext)
	}))
	defer srv.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	var released atomic.Int32
	base := time.Now()
	d := 4 * time.Second
	libProbe := media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 320, Height: 240}, Audio: []media.AudioStream{{Index: 1, Codec: "aac"}}}
	progs := []Program{
		{Entry: Entry{ItemID: 1, Start: base, End: base.Add(d)}, Path: lib, Probe: libProbe},
		{Entry: Entry{ItemID: -1, ExtraID: 1, Start: base, End: base.Add(d)}, URL: dead.URL + "/s.mp4", Release: func() { released.Add(1) }},
	}
	for i := 0; i < 5; i++ {
		progs = append(progs, Program{Entry: Entry{ItemID: -2, ExtraID: 2, Start: base, End: base.Add(time.Second)}, Skip: true})
	}
	progs = append(progs,
		Program{Entry: Entry{ItemID: -3, ExtraID: 3, Start: base, End: base.Add(d)}, URL: srv.URL + "/x.mp4", Headers: map[string]string{"X-Relay-Key": "k"},
			Release: func() { released.Add(1) }},
		Program{Entry: Entry{ItemID: 2, Start: base, End: base.Add(d)}, Path: lib, Probe: libProbe},
		Program{Entry: Entry{ItemID: 3, Start: base, End: base.Add(time.Hour)}, Path: lib, Probe: libProbe},
	)
	src := &seqSource{base: base, progs: progs}
	live := &Live{FFmpeg: "ffmpeg", HW: "none", Dir: filepath.Join(dir, "live"), Source: src,
		ReadRate: -1, Width: 320, Height: 180, Bitrate: 300_000, Keep: 200, Window: 200, ReadySegments: 1}
	defer live.Close()
	ctx := context.Background()
	deadline := time.Now().Add(90 * time.Second)
	var b []byte
	for {
		var err error
		if b, err = live.Playlist(ctx, 7, "video.m3u8"); err != nil {
			t.Fatalf("the channel stopped: %v", err)
		}
		// library, the extra that plays, the library: two boundaries
		if strings.Count(string(b), "#EXT-X-DISCONTINUITY\n") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			src.mu.Lock()
			t.Fatalf("never got past the extras (asked %v):\n%s", src.asked, b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	src.mu.Lock()
	if src.asked[8] == 0 {
		t.Fatalf("the library after the extras was not reached: %v", src.asked)
	}
	src.mu.Unlock()
	if r := live.Running(); len(r) != 1 {
		t.Fatalf("running %v", r)
	}
	if released.Load() < 2 {
		t.Fatalf("released %d streams", released.Load())
	}
}
