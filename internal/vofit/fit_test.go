package vofit

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// fixtureMs: the length of the test original (the windows shortened to fit).
const fixtureMs = 60_000

func needFFmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	return p
}

// shortWindows shortens the measured windows for 60 s fixtures.
func shortWindows(t *testing.T) {
	w, s := windowMs, searchMs
	windowMs, searchMs = 8_000, 4_000
	t.Cleanup(func() { windowMs, searchMs = w, s })
}

// writeWAV writes mono 16-bit PCM at rate.
func writeWAV(t *testing.T, p string, rate int, samples []float64) {
	t.Helper()
	b := make([]byte, 44+2*len(samples))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(samples)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(samples)))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(int16(math.Max(-1, math.Min(1, v))*32000)))
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// soundtrack is fixtureMs of noise bursts of random lengths and levels
// (a soundtrack's envelope: never periodic).
func soundtrack(rate int) []float64 {
	r := rand.New(rand.NewPCG(7, 11))
	out := make([]float64, fixtureMs*rate/1000)
	for i := 0; i < len(out); {
		n := (10 + r.IntN(290)) * rate / 1000
		level := r.Float64()
		if r.IntN(4) == 0 {
			level = 0
		}
		for k := 0; k < n && i < len(out); k++ {
			out[i] = level * (2*r.Float64() - 1) * 0.5
			i++
		}
	}
	return out
}

func run(t *testing.T, ffmpeg string, args ...string) {
	t.Helper()
	out, err := exec.Command(ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}

// fixture writes the original as a video file's only audio stream
// (Matroska, FLAC) and a track made of it by the filter (an .mka).
func fixture(t *testing.T, filter string) (video, track string) {
	t.Helper()
	ff := needFFmpeg(t)
	dir := t.TempDir()
	wav := filepath.Join(dir, "orig.wav")
	writeWAV(t, wav, 48000, soundtrack(48000))
	video = filepath.Join(dir, "Film.mkv")
	run(t, ff, "-i", wav, "-c:a", "flac", video)
	track = filepath.Join(dir, "Film.ru.LostFilm.mka")
	args := []string{"-i", wav}
	if filter != "" {
		args = append(args, "-af", filter)
	}
	run(t, ff, append(args, "-c:a", "flac", track)...)
	return video, track
}

func fit(t *testing.T, video, track string) Result {
	t.Helper()
	r, err := Fit(context.Background(), needFFmpeg(t), Input{Video: video, OrigIndex: 0, DurationMs: fixtureMs, Track: track})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A track of the same cut shifted on the timeline: one offset, the sign
// how the player shifts it (positive: later).
func TestFitOffset(t *testing.T) {
	shortWindows(t)
	for _, c := range []struct {
		name   string
		filter string
		want   float64
	}{
		{"in sync", "", 0},
		{"delayed by 1.2 s", "adelay=1200:all=1", -1200},
		{"1.2 s cut from its start", "atrim=start=1.2,asetpts=PTS-STARTPTS", 1200},
	} {
		t.Run(c.name, func(t *testing.T) {
			video, track := fixture(t, c.filter)
			r := fit(t, video, track)
			if r.State != StateOK || r.Tempo != 1 || math.Abs(r.OffsetMs-c.want) > 5 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

// A PAL track (25 fps: 25/23.976 faster, the pitch up with it, as a PAL
// master plays) is slowed by the ratio; the offset after the correction
// is the start's.
func TestFitPALSpeedUp(t *testing.T) {
	shortWindows(t)
	video, track := fixture(t, "asetrate="+strconv.FormatFloat(48000*25/ntsc, 'f', 3, 64)+",aresample=48000")
	r := fit(t, video, track)
	// the offset is measured through atempo as the players play it (its
	// window shifts the output by a few tens of ms, the same way there)
	if r.State != StateOK || math.Abs(r.Tempo-ntsc/25) > 1e-6 || math.Abs(r.OffsetMs) > 40 {
		t.Fatalf("%+v", r)
	}
	// played as the players play it (atempo, shifted by the offset: a
	// file of its own keeps no -itsoffset, so adelay shifts it here), the
	// track is in sync: fitted again, no tempo and no offset (±10 ms)
	if r.OffsetMs < 0 {
		t.Fatalf("offset %v: the check below delays only", r.OffsetMs)
	}
	played := filepath.Join(t.TempDir(), "played.mka")
	run(t, needFFmpeg(t), "-i", track, "-af", "atempo="+strconv.FormatFloat(r.Tempo, 'f', -1, 64)+
		",adelay="+strconv.FormatFloat(r.OffsetMs, 'f', 1, 64)+":all=1", "-c:a", "flac", played)
	again := fit(t, video, played)
	if again.State != StateOK || again.Tempo != 1 || math.Abs(again.OffsetMs) > 10 {
		t.Fatalf("played: %+v", again)
	}
}

// Another cut (3 s inserted mid-way): no single offset or ratio fits.
func TestFitOtherCutIsMisfit(t *testing.T) {
	shortWindows(t)
	video, track := fixture(t, "asplit[a][b];[a]atrim=end=30,asetpts=PTS-STARTPTS[x];[b]atrim=start=30,asetpts=PTS-STARTPTS,adelay=3000:all=1[y];[x][y]concat=n=2:v=0:a=1")
	r := fit(t, video, track)
	if r.State != StateMisfit || r.Why == "" {
		t.Fatalf("%+v", r)
	}
}

// A silent original says nothing: unchecked, not a misfit.
func TestFitSilentUnchecked(t *testing.T) {
	shortWindows(t)
	video, track := fixture(t, "volume=0")
	// the silent one is the original here
	r, err := Fit(context.Background(), needFFmpeg(t), Input{Video: track, DurationMs: fixtureMs, Track: video})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateUnmeasured {
		t.Fatalf("%+v", r)
	}
}
