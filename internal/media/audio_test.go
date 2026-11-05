package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAudioDecideFLACHomeDirect(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Accept: []string{"flac"}})
	if !p.Direct {
		t.Fatalf("%+v", p)
	}
}

func TestAudioDecideMaxBitRate128Transcodes(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, BitrateKbps: 900, MaxKbps: 128})
	if p.Direct || p.Codec != "aac" || p.Kbps != 128 || p.ContentType != "audio/aac" || p.Reason == "" {
		t.Fatalf("%+v", p)
	}
}

func TestAudioDecideMP3UnderLimitDirect(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "mp3", BitrateKbps: 128, MaxKbps: 192})
	if !p.Direct {
		t.Fatalf("%+v", p)
	}
	if p := AudioDecide(AudioRequest{Codec: "mp3", BitrateKbps: 320, MaxKbps: 192}); p.Direct || p.Kbps != 192 {
		t.Fatalf("over limit: %+v", p)
	}
}

func TestAudioDecideOpusFormat(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Format: "opus"})
	if p.Direct || p.Codec != "opus" || p.ContentType != "audio/ogg; codecs=opus" {
		t.Fatalf("%+v", p)
	}
	if p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Format: "raw", MaxKbps: 128}); !p.Direct {
		t.Fatalf("raw: %+v", p)
	}
	if p := AudioDecide(AudioRequest{Codec: "mp3", Format: "mp3", MaxKbps: 512, BitrateKbps: 900}); p.Codec != "mp3" || p.Kbps != 320 || p.ContentType != "audio/mpeg" {
		t.Fatalf("mp3 clamp: %+v", p)
	}
}

func TestAudioDecideCueSegment(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Segment: true})
	if p.Direct || p.Codec != "flac" || p.ContentType != "audio/flac" {
		t.Fatalf("lossless: %+v", p)
	}
	p = AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Segment: true, MaxKbps: 128})
	if p.Direct || p.Codec != "aac" || p.Kbps != 128 {
		t.Fatalf("limit: %+v", p)
	}
	if p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Segment: true, Format: "raw"}); p.Direct {
		t.Fatalf("raw segment must not be direct: %+v", p)
	}
}

func TestAudioDecideUnsupportedCodec(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "ape", Lossless: true, Accept: []string{"flac", "mp3", "aac"}})
	if p.Direct || p.Codec != "aac" || p.Kbps != 256 {
		t.Fatalf("%+v", p)
	}
}

func TestAudioDecideEmptyAcceptDefaults(t *testing.T) {
	if p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true}); !p.Direct {
		t.Fatalf("%+v", p)
	}
}

func TestAudioDecideOpusClamp(t *testing.T) {
	p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, Format: "opus", MaxKbps: 512})
	if p.Codec != "opus" || p.Kbps != 256 {
		t.Fatalf("%+v", p)
	}
	if p := AudioDecide(AudioRequest{Codec: "flac", Lossless: true, MaxKbps: 32}); p.Kbps != 64 {
		t.Fatalf("low clamp: %+v", p)
	}
}

func TestAudioArgsWhitelistAndDuration(t *testing.T) {
	end := int64(2000)
	args, err := AudioArgs("/m/a.flac", AudioPlan{Codec: "aac", Kbps: 128}, 1000, &end)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(args, " ")
	for _, want := range []string{"-ss 1.000 -protocol_whitelist file -i file:/m/a.flac -t 1.000",
		"-vn -map 0:a:0 -ac 2 -c:a aac -b:a 128k -f adts pipe:1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "-to") {
		t.Fatalf("-to used: %s", s)
	}
	args, _ = AudioArgs("/m/a.flac", AudioPlan{Codec: "flac"}, 0, nil)
	if s := strings.Join(args, " "); strings.Contains(s, "-b:a") || strings.Contains(s, "-ss") || !strings.Contains(s, "-c:a flac -f flac pipe:1") {
		t.Fatalf("flac: %s", s)
	}
	if _, err := AudioArgs("http://10.0.0.1/x", AudioPlan{Codec: "aac", Kbps: 128}, 0, nil); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("lan: %v", err)
	}
}

// sineFLAC writes a stereo sine FLAC of the given length.
func sineFLAC(t *testing.T, seconds int) (ffmpeg, ffprobe, path string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	path = filepath.Join(t.TempDir(), "in.flac")
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=44100:duration="+strconv.Itoa(seconds), "-ac", "2", "-c:a", "flac", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	return ff, fp, path
}

// probeStream writes b to a file and probes it.
func probeStream(t *testing.T, ffprobe string, b []byte, ext string) ProbeResult {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out"+ext)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Probe(context.Background(), ffprobe, p)
	if err != nil {
		t.Fatal(err)
	}
	// The real duration: the end of the last packet (a piped ADTS or FLAC
	// stream carries no reliable header duration).
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries",
		"packet=pts_time,duration_time", "-of", "csv=p=0", p).Output()
	if err != nil {
		t.Fatal(err)
	}
	r.DurationMs = 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 2 {
			continue
		}
		pts, e1 := strconv.ParseFloat(f[0], 64)
		dur, e2 := strconv.ParseFloat(f[1], 64)
		if e1 == nil && e2 == nil {
			r.DurationMs = int64((pts + dur) * 1000)
		}
	}
	return r
}

func TestTranscodeProducesAudio(t *testing.T) {
	ff, fp, in := sineFLAC(t, 3)
	tr := &AudioTranscoder{FFmpeg: ff, Limit: 4}
	var buf bytes.Buffer
	if err := tr.Stream(context.Background(), &buf, in, AudioPlan{Codec: "aac", Kbps: 128}, 0, nil); err != nil {
		t.Fatal(err)
	}
	r := probeStream(t, fp, buf.Bytes(), ".aac")
	if len(r.Audio) != 1 || r.Audio[0].Codec != "aac" || r.DurationMs < 2700 || r.DurationMs > 3300 {
		t.Fatalf("%+v", r)
	}
}

func TestTranscodeMP3AndOpus(t *testing.T) {
	ff, fp, in := sineFLAC(t, 2)
	tr := &AudioTranscoder{FFmpeg: ff, Limit: 4}
	for _, c := range []struct{ codec, ext, probed string }{{"mp3", ".mp3", "mp3"}, {"opus", ".ogg", "opus"}} {
		t.Run(c.codec, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tr.Stream(context.Background(), &buf, in, AudioPlan{Codec: c.codec, Kbps: 128}, 0, nil); err != nil {
				t.Fatal(err)
			}
			r := probeStream(t, fp, buf.Bytes(), c.ext)
			if len(r.Audio) != 1 || r.Audio[0].Codec != c.probed || r.DurationMs < 1700 || r.DurationMs > 2300 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestTranscodeSegment(t *testing.T) {
	ff, fp, in := sineFLAC(t, 3)
	tr := &AudioTranscoder{FFmpeg: ff, Limit: 4}
	var buf bytes.Buffer
	end := int64(2000)
	if err := tr.Stream(context.Background(), &buf, in, AudioPlan{Codec: "flac"}, 1000, &end); err != nil {
		t.Fatal(err)
	}
	r := probeStream(t, fp, buf.Bytes(), ".flac")
	if len(r.Audio) != 1 || r.Audio[0].Codec != "flac" || r.DurationMs < 800 || r.DurationMs > 1200 {
		t.Fatalf("want ~1s: %+v", r)
	}
}

// blockingWriter holds the first Write until released.
type blockingWriter struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return len(p), nil
}

func TestTranscoderLimit(t *testing.T) {
	ff, _, in := sineFLAC(t, 3)
	tr := &AudioTranscoder{FFmpeg: ff, Limit: 1}
	bw := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- tr.Stream(context.Background(), bw, in, AudioPlan{Codec: "aac", Kbps: 128}, 0, nil) }()
	select {
	case <-bw.started:
	case <-time.After(10 * time.Second):
		t.Fatal("first stream did not start")
	}
	err := tr.Stream(context.Background(), io.Discard, in, AudioPlan{Codec: "aac", Kbps: 128}, 0, nil)
	if !errors.Is(err, ErrAudioBusy) {
		t.Fatalf("second: %v", err)
	}
	close(bw.release)
	if err := <-done; err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := tr.Stream(context.Background(), io.Discard, in, AudioPlan{Codec: "aac", Kbps: 128}, 0, nil); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
}

func TestProbeAudioFields(t *testing.T) {
	js := `{"format":{"format_name":"flac","duration":"3.0","bit_rate":"900000"},"streams":[
	 {"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"96000","bits_per_raw_sample":"24"},
	 {"index":1,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`
	r, err := parseProbe([]byte(js), "/m/a.flac")
	if err != nil {
		t.Fatal(err)
	}
	a := r.Audio[0]
	if a.SampleRate != 96000 || a.BitDepth != 24 || a.BitRate != 900000 {
		t.Fatalf("%+v", a)
	}
	js = `{"format":{"format_name":"mp3","bit_rate":"330000"},"streams":[
	 {"index":0,"codec_type":"audio","codec_name":"mp3","sample_rate":"44100","bit_rate":"320000"}]}`
	if r, _ := parseProbe([]byte(js), "/m/a.mp3"); r.Audio[0].BitRate != 320000 || r.Audio[0].BitDepth != 0 {
		t.Fatalf("%+v", r.Audio[0])
	}
}
