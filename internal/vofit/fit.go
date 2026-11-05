// Package vofit fits an external audio track (a voice-over found by
// «Найти озвучку…», stage B of the voice-over design, or any track beside
// a video) to its video: the offset that puts it on
// the video's timeline and, for a track of a PAL master (25 fps against
// 23.976), the tempo that undoes the speed-up. A track that no single
// offset and tempo fits is of another cut («дорожка от другой версии»):
// it is not offered. The players apply the fit (-itsoffset, atempo).
package vofit

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/dsp"
	"github.com/antresol-app/antresol/internal/media"
)

// The states of a fit (external_audio_fit.state).
const (
	// StatePending: placed by «Найти озвучку…», not fitted yet (not offered).
	StatePending = "pending"
	// StateOK: fitted; OffsetMs and Tempo apply.
	StateOK = "ok"
	// StateMisfit: another cut; never offered.
	StateMisfit = "misfit"
	// StateUnmeasured: nothing to measure against (no track in the original
	// language, a silent original, too short): not offered — its sync is
	// unknown.
	StateUnmeasured = "unmeasured"
	// StateFailed: the measurement failed (retried later, given up after
	// maxFails); not offered.
	StateFailed = "failed"
)

// ntsc is 23.976 fps exactly (24000/1001).
const ntsc = 24000.0 / 1001

// Tempos are the tempos tried: none, then a PAL track (sped up 25/23.976,
// slowed by its inverse) and the other way round.
var Tempos = []float64{1, ntsc / 25, 25 / ntsc}

const (
	// envRate: envelope frames per second (1 ms each); pcmRate: the
	// decode rate it is made from.
	envRate = 1000
	pcmRate = 8000
	// agreeMs: the windows' offsets agree within this (else they drift).
	agreeMs = 20
	// minCorr: the normalised envelope correlation a window must reach to
	// count as found.
	minCorr = 0.35
	// minLevel: an envelope's spread below this (dB) is silence.
	minLevel = 1
	// hpMs: the envelope's slow level (ducking, mixing) removed over this.
	hpMs = 200
	// decodeLimit: one window's ffmpeg running time (not held).
	decodeLimit = 5 * time.Minute
	// videoFormats: the containers a library video can be.
	videoFormats = "matroska,webm,mov,mp4,m4a,3gp,3g2,mj2,avi,mpegts"
)

// at: where the windows are, as shares of the video's duration.
var at = [3]float64{0.15, 0.45, 0.75}

// windowMs: each window measured; searchMs: how far the offset is
// searched either way. (Variables: tests shorten them.)
var (
	windowMs int64 = 60_000
	searchMs int64 = 30_000
)

// Input is a track to fit and the video stream it is fitted to.
type Input struct {
	// Video is the library file, OrigIndex its original-language track
	// (ffprobe stream index), DurationMs its length.
	Video      string
	OrigIndex  int
	DurationMs int64
	// Track is the external audio file (its first audio stream).
	Track string
	// Busy (nil: never) holds ffmpeg stopped while playback needs the disk.
	Busy func() bool
}

// Window is one measured window: where it starts on the video (ms), the
// offset found and how well the envelopes correlate there.
type Window struct {
	AtMs     int64   `json:"atMs"`
	OffsetMs float64 `json:"offsetMs"`
	Corr     float64 `json:"corr"`
}

// Attempt is the windows measured at a tempo.
type Attempt struct {
	Tempo   float64  `json:"tempo"`
	Windows []Window `json:"windows"`
}

// Result is a fit: State, and for StateOK the offset (ms, positive: the
// track plays later on the video's timeline) and the tempo (atempo factor;
// 1: none) — the track's sample at time a plays at video time a/Tempo +
// OffsetMs. Why says what failed; Attempts is the record.
type Result struct {
	State    string    `json:"state"`
	OffsetMs float64   `json:"offsetMs"`
	Tempo    float64   `json:"tempo"`
	Why      string    `json:"why,omitempty"`
	Attempts []Attempt `json:"attempts,omitempty"`
}

// Fit measures the track against the video's original track on three
// windows (15 %, 45 %, 75 % of the video): the envelopes' cross-correlation
// gives each window's offset. They agree within agreeMs: fitted, the
// median. Otherwise the track is tried slowed and sped up by the PAL
// ratio (offsets growing linearly at that slope agree once it is undone):
// the first tempo whose windows agree. None: another cut. An error is a
// failure to read (ffmpeg).
func Fit(ctx context.Context, ffmpeg string, in Input) (Result, error) {
	if in.DurationMs < 2000 {
		return Result{State: StateUnmeasured, Tempo: 1, Why: "too short to measure"}, nil
	}
	win := min(windowMs, in.DurationMs)
	starts := make([]int64, len(at))
	videos := make([][]float64, len(at))
	silent := 0
	for i, a := range at {
		starts[i] = max(min(int64(a*float64(in.DurationMs)), in.DurationMs-win), 0)
		pcm, err := decode(ctx, ffmpeg, videoArgs(in.Video, in.OrigIndex, starts[i], win), in.Busy)
		if err != nil {
			return Result{}, err
		}
		videos[i] = envelope(pcm)
		if spread(videos[i]) < minLevel {
			silent++
		}
	}
	if silent == len(at) {
		return Result{State: StateUnmeasured, Tempo: 1, Why: "the original is silent"}, nil
	}
	res := Result{State: StateMisfit, Tempo: 1}
	for _, tempo := range Tempos {
		att := Attempt{Tempo: tempo}
		ok := true
		for i := range at {
			if spread(videos[i]) < minLevel {
				continue
			}
			w, err := measure(ctx, ffmpeg, in, videos[i], starts[i], tempo)
			if err != nil {
				return Result{}, err
			}
			att.Windows = append(att.Windows, w)
			if w.Corr < minCorr {
				ok = false
				break // this tempo does not fit: the next windows are not read
			}
		}
		res.Attempts = append(res.Attempts, att)
		if !ok {
			continue
		}
		offs := make([]float64, len(att.Windows))
		for i, w := range att.Windows {
			offs[i] = w.OffsetMs
		}
		if slices.Max(offs)-slices.Min(offs) <= agreeMs {
			res.State, res.Tempo, res.OffsetMs = StateOK, tempo, median(offs)
			return res, nil
		}
	}
	res.Why = why(res.Attempts)
	return res, nil
}

// why says what the first attempt (no tempo) found.
func why(as []Attempt) string {
	if len(as) == 0 {
		return "nothing measured"
	}
	var parts []string
	for _, w := range as[0].Windows {
		if w.Corr < minCorr {
			parts = append(parts, fmt.Sprintf("at %ds: not found (corr %.2f)", w.AtMs/1000, w.Corr))
		} else {
			parts = append(parts, fmt.Sprintf("at %ds: %+.0f ms", w.AtMs/1000, w.OffsetMs))
		}
	}
	return "another version: " + strings.Join(parts, ", ")
}

// measure finds the offset of one window: the track read around it at
// the tempo (atempo) and its envelope correlated with the video's.
func measure(ctx context.Context, ffmpeg string, in Input, video []float64, startMs int64, tempo float64) (Window, error) {
	w := Window{AtMs: startMs}
	lo := max(startMs-searchMs, 0)                        // video time the track read starts at
	length := startMs - lo + int64(len(video)) + searchMs // ms of output
	pcm, err := decode(ctx, ffmpeg, trackArgs(in.Track, float64(lo)*tempo, float64(length)*tempo, tempo), in.Busy)
	if err != nil {
		return w, err
	}
	track := envelope(pcm)
	if len(track) < len(video) || spread(track) < minLevel {
		return w, nil // the track ends or is silent there: not found
	}
	k, corr := bestLag(video, track)
	// output frame k+t (track time (lo+k+t)·tempo) matches video frame t
	// (video time startMs+t): offset = video − track/tempo
	w.OffsetMs, w.Corr = float64(startMs-lo)-k, corr
	return w, nil
}

// videoArgs decode the stream idx of a video from startMs for durMs as
// mono PCM on the file's timeline (a stream starting late padded).
func videoArgs(path string, idx int, startMs, durMs int64) []string {
	return []string{"-protocol_whitelist", "file", "-format_whitelist", videoFormats,
		"-ss", msArg(float64(startMs)), "-t", msArg(float64(durMs)), "-i", "file:" + path,
		"-map", "0:" + strconv.Itoa(idx), "-vn", "-sn", "-dn",
		"-af", "aresample=min_comp=0.001:min_hard_comp=0.005:first_pts=0"}
}

// trackArgs decode the first audio stream of an external track from
// startMs for durMs (both on the track's own time), played at tempo.
func trackArgs(path string, startMs, durMs, tempo float64) []string {
	af := "aresample=min_comp=0.001:min_hard_comp=0.005:first_pts=0"
	if tempo != 1 {
		// atempo alone, as the players' ffmpeg runs it: a resampler ahead of
		// it (the timeline's padding) skews its output by ~10 ms
		af = "atempo=" + strconv.FormatFloat(tempo, 'f', -1, 64)
	}
	return []string{"-protocol_whitelist", "file", "-format_whitelist", media.AudioFormats,
		"-ss", msArg(startMs), "-t", msArg(durMs), "-i", "file:" + strings.TrimPrefix(path, "file:"),
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-af", af}
}

func msArg(ms float64) string { return strconv.FormatFloat(ms/1000, 'f', 3, 64) }

// decode runs ffmpeg with the input args and returns mono PCM at pcmRate,
// at the lowest priority, held while busy.
func decode(ctx context.Context, ffmpeg string, input []string, busy func() bool) ([]float32, error) {
	args := append([]string{"-hide_banner", "-nostats", "-nostdin", "-loglevel", "error"}, input...)
	args = append(args, "-ac", "1", "-ar", strconv.Itoa(pcmRate), "-f", "f32le", "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	media.LowPriority(cmd)
	if busy == nil {
		busy = func() bool { return false }
	}
	if err := media.RunYieldingLimit(cmd, busy, 250*time.Millisecond, decodeLimit); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	b := out.Bytes()
	pcm := make([]float32, len(b)/4)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return pcm, nil
}

// envelope is the PCM's log energy per millisecond with its slow level
// (over hpMs) taken away: the onsets of sounds, which a voice-over keeps
// from its original (music and effects under the voices).
func envelope(pcm []float32) []float64 {
	step := pcmRate / envRate
	n := len(pcm) / step
	e := make([]float64, n)
	for i := range e {
		var s float64
		for _, v := range pcm[i*step : (i+1)*step] {
			s += float64(v) * float64(v)
		}
		e[i] = 10 * math.Log10(s/float64(step)+1e-9)
	}
	// subtract the centred moving average (prefix sums)
	pre := make([]float64, n+1)
	for i, v := range e {
		pre[i+1] = pre[i] + v
	}
	h := hpMs / 2
	out := make([]float64, n)
	for i := range e {
		a, b := max(i-h, 0), min(i+h+1, n)
		out[i] = e[i] - (pre[b]-pre[a])/float64(b-a)
	}
	return out
}

// spread is the standard deviation of an envelope (dB): ~0 for silence.
func spread(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s, ss float64
	for _, v := range x {
		s += v
		ss += v * v
	}
	m := s / float64(len(x))
	return math.Sqrt(max(ss/float64(len(x))-m*m, 0))
}

// bestLag finds k maximising the normalised correlation of v with
// e[k : k+len(v)] (FFT), refined below a frame by a parabola through the
// peak, and that correlation.
func bestLag(v, e []float64) (float64, float64) {
	p := 1
	for p < len(e)+len(v) {
		p <<= 1
	}
	zv, ze := make([]complex128, p), make([]complex128, p)
	for i, x := range v {
		zv[i] = complex(x, 0)
	}
	for i, x := range e {
		ze[i] = complex(x, 0)
	}
	f := dsp.NewFFT(p)
	f.Transform(zv, false)
	f.Transform(ze, false)
	for i := range ze {
		c := zv[i]
		ze[i] *= complex(real(c), -imag(c))
	}
	f.Transform(ze, true) // ze[k] = p · Σ e[t+k]·v[t]
	var vv float64
	for _, x := range v {
		vv += x * x
	}
	pre := make([]float64, len(e)+1)
	for i, x := range e {
		pre[i+1] = pre[i] + x*x
	}
	last := len(e) - len(v)
	corr := make([]float64, last+1)
	best := 0
	for k := 0; k <= last; k++ {
		ee := pre[k+len(v)] - pre[k]
		if ee <= 0 || vv <= 0 {
			continue
		}
		corr[k] = real(ze[k]) / float64(p) / math.Sqrt(vv*ee)
		if corr[k] > corr[best] {
			best = k
		}
	}
	k := float64(best)
	if best > 0 && best < last {
		a, b, c := corr[best-1], corr[best], corr[best+1]
		if d := a - 2*b + c; d < 0 {
			k += 0.5 * (a - c) / d
		}
	}
	return k, corr[best]
}

func median(x []float64) float64 {
	s := slices.Clone(x)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
