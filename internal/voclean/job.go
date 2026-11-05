package voclean

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"

	"github.com/antresol-app/antresol/internal/media"
)

// TitleSuffix marks a cleaned track's title (for clients without badges).
const TitleSuffix = " (очищено)"

// centre: the voice channel of 5.1 and 7.1 (FL FR FC …).
const centre = 2

// encodeCodec: E-AC3 448 kb/s for 5.1; 7.1 (beyond ffmpeg's E-AC3
// encoder) as AAC 768 kb/s.
func encodeCodec(ch int) []string {
	if ch == 8 {
		return []string{"-c:a", "aac", "-b:a", "768k"}
	}
	return []string{"-c:a", "eac3", "-b:a", "448k"}
}

// pcmCodec: lossless float output (tests compare samples).
func pcmCodec(int) []string { return []string{"-c:a", "pcm_f32le"} }

// Job is a voice-over to clean: the pair, the measured lag
// (Measure.LagMs) and the output file.
type Job struct {
	Pair
	LagMs float64
	// GainCap bounds the centre's fitted gain (GainCap of Measure.FrontGain;
	// 0: gainMax)
	GainCap float64
	Out     string
}

// Clean writes the cleaned version of each job's voice-over (Matroska
// audio): its original, LagMs earlier on the file's timeline, subtracted
// from the centre; the other channels are the voice-over's own samples
// (clipped to full scale). One ffmpeg reads the file once for all the
// jobs; the DSP runs in this goroutine; one encoder per job writes
// Out+".tmp", renamed to Out when complete. It returns each job's error:
// a job that fails (its encoder died) leaves no output and does not stop
// the others; a failed read (ErrBadlyInterleaved among others) fails them
// all. The cleaned tracks start where the file's timeline does
// (StartOffsetMs). The ffmpeg children run at the lowest CPU and I/O
// priority; in.Busy holds the read while playback needs the disk.
func Clean(ctx context.Context, ffmpeg string, in Input, jobs []Job) []error {
	return clean(ctx, ffmpeg, in, jobs, encodeCodec)
}

type cleanJob struct {
	Job
	vk, ok  int // the streams' positions in the read
	ch, och int
	or      []float32 // the original's centre, queued for the lag
	dropOr  int
	sub     *Subtractor
	cv, co  []float32
	pending []float32
	enc     *encoder
	ended   bool
	tmp     string
	err     error
}

// fail ends a job: its encoder killed, its temp file removed.
func (c *cleanJob) fail(err error) {
	if c.err != nil {
		return
	}
	c.err, c.ended = err, true
	if c.enc != nil {
		c.enc.kill()
	}
	_ = os.Remove(c.tmp)
}

func clean(ctx context.Context, ffmpeg string, in Input, jobs []Job, codec func(int) []string) []error {
	errs := make([]error, len(jobs))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var cjs []*cleanJob // the jobs that can run, by position in jobs
	var at []int
	for i, j := range jobs {
		if why := layoutWhy(j.Pair); why != "" {
			errs[i] = fmt.Errorf("voclean: %s", why)
			continue
		}
		c := &cleanJob{Job: j, ch: j.Vo.Channels, och: j.Orig.Channels,
			sub: NewSubtractor(rate), cv: make([]float32, readFrames), co: make([]float32, readFrames), tmp: j.Out + ".tmp"}
		c.sub.SetGainCap(j.GainCap)
		// vo[t] pairs with orig[t − lag]: a late voice-over gets zeros of
		// the original first, an early one drops its head
		if lag := int(math.Round(j.LagMs * rate / 1000)); lag > 0 {
			c.or = make([]float32, lag)
		} else {
			c.dropOr = -lag
		}
		title := j.Vo.Title + TitleSuffix
		if j.Vo.Title == "" {
			title = "Очищено"
		}
		var err error
		if c.enc, err = startEncoder(ctx, ffmpeg, c.ch, codec(c.ch), j.Vo.Lang, title, c.tmp); err != nil {
			c.fail(err)
		}
		cjs, at = append(cjs, c), append(at, i)
	}
	defer func() {
		for k, c := range cjs {
			errs[at[k]] = c.err
		}
	}()
	live := func() bool {
		for _, c := range cjs {
			if c.err == nil {
				return true
			}
		}
		return false
	}
	failAll := func(err error) {
		for _, c := range cjs {
			c.fail(err)
		}
	}
	if !live() {
		return errs
	}
	pairs := make([]Pair, len(cjs))
	idx := make([]int, len(cjs))
	for k, c := range cjs {
		pairs[k], idx[k] = c.Pair, k
	}
	srcs, pos := streamsOf(pairs, idx)
	for k, c := range cjs {
		c.vk, c.ok = pos[k][0], pos[k][1]
	}
	dec, err := startDecoder(ctx, ffmpeg, in.Path, in.External, srcs, 0, 0, in.Busy, in.Limit)
	if err != nil {
		failAll(err)
		return errs
	}
	bufs := make([][]float32, len(srcs))
	ns := make([]int, len(srcs))
	for k, s := range srcs {
		bufs[k] = make([]float32, readFrames*s.ch)
	}
	for live() {
		any := false
		for k := range srcs {
			n, err := dec.read(k, bufs[k])
			if err != nil {
				dec.abort()
				failAll(err)
				return errs
			}
			ns[k] = n
			any = any || n > 0
		}
		if !any {
			break
		}
		for _, c := range cjs {
			if err := c.step(bufs[c.vk][:ns[c.vk]*c.ch], bufs[c.ok][:ns[c.ok]*c.och]); err != nil {
				c.fail(err)
			}
		}
	}
	if !live() { // every job failed on its own: the read is not needed
		dec.abort()
		return errs
	}
	for _, c := range cjs {
		if err := c.finish(); err != nil {
			c.fail(err)
		}
	}
	if err := dec.wait(); err != nil {
		failAll(err)
		return errs
	}
	for _, c := range cjs {
		if c.err != nil {
			continue
		}
		if err := c.enc.close(); err != nil {
			c.fail(err)
			continue
		}
		if err := os.Rename(c.tmp, c.Out); err != nil {
			c.fail(err)
		}
	}
	return errs
}

// step takes the next blocks of the voice-over and its original.
func (c *cleanJob) step(vb, ob []float32) error {
	if c.ended {
		return nil
	}
	nv := len(vb) / c.ch
	no := len(ob) / c.och
	skip := min(c.dropOr, no)
	c.dropOr -= skip
	for i := skip; i < no; i++ {
		c.or = append(c.or, ob[i*c.och+centre])
	}
	if nv == 0 {
		return c.finish()
	}
	for i := range nv {
		c.cv[i] = vb[i*c.ch+centre]
		if i < len(c.or) {
			c.co[i] = c.or[i]
		} else {
			c.co[i] = 0 // the original ended first
		}
	}
	c.or = c.or[:copy(c.or, c.or[min(nv, len(c.or)):])]
	c.pending = append(c.pending, vb...)
	return c.emit(c.sub.Process(c.cv[:nv], c.co[:nv]))
}

// finish flushes the voice-over at its end (once).
func (c *cleanJob) finish() error {
	if c.ended {
		return nil
	}
	c.ended = true
	if err := c.emit(c.sub.Flush()); err != nil {
		return err
	}
	if len(c.pending) != 0 {
		return errors.New("voclean: output shorter than the voice-over")
	}
	return nil
}

// emit writes the frames whose cleaned centre is ready, clipped to full
// scale.
func (c *cleanJob) emit(out []float32) error {
	m := len(out)
	p := c.pending[:m*c.ch]
	for i := range m {
		p[i*c.ch+centre] = out[i]
	}
	for i, v := range p {
		p[i] = min(max(v, -1), 1)
	}
	if err := c.enc.write(p); err != nil {
		return err
	}
	c.pending = c.pending[:copy(c.pending, c.pending[m*c.ch:])]
	return nil
}

// encoder is an ffmpeg encoding interleaved f32le PCM from its stdin.
type encoder struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	w      *bufio.Writer
	stderr *tailBuffer
	raw    []byte
	waited bool
}

func startEncoder(ctx context.Context, ffmpeg string, ch int, codec []string, lang, title, out string) (*encoder, error) {
	layout := "5.1(side)"
	if ch == 8 {
		layout = "7.1"
	}
	args := []string{"-hide_banner", "-nostats", "-loglevel", "error", "-protocol_whitelist", "pipe,file",
		"-f", "f32le", "-ar", strconv.Itoa(rate), "-ch_layout", layout, "-i", "pipe:0", "-map", "0:a"}
	args = append(args, codec...)
	if lang != "" {
		args = append(args, "-metadata:s:a:0", "language="+lang)
	}
	args = append(args, "-metadata:s:a:0", "title="+title, "-f", "matroska", "-y", "file:"+out)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	e := &encoder{cmd: cmd, stderr: &tailBuffer{max: 8 << 10}}
	cmd.Stderr = e.stderr
	media.LowPriority(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e.in, e.w = in, bufio.NewWriterSize(in, 256<<10)
	return e, nil
}

func (e *encoder) write(samples []float32) error {
	if cap(e.raw) < len(samples)*4 {
		e.raw = make([]byte, len(samples)*4)
	}
	raw := e.raw[:len(samples)*4]
	for i, v := range samples {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	if _, err := e.w.Write(raw); err != nil {
		return e.fail(err)
	}
	return nil
}

func (e *encoder) close() error {
	if err := e.w.Flush(); err != nil {
		return e.fail(err)
	}
	if err := e.in.Close(); err != nil {
		return e.fail(err)
	}
	e.waited = true
	if err := e.cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg encode: %w: %s", err, e.stderr.last())
	}
	return nil
}

// fail: a write to the encoder failed (it exited: a full disk, an
// unwritable folder): its own error says why.
func (e *encoder) fail(err error) error {
	_ = e.in.Close()
	e.waited = true
	if werr := e.cmd.Wait(); werr != nil {
		return fmt.Errorf("ffmpeg encode: %w: %s", werr, e.stderr.last())
	}
	return fmt.Errorf("ffmpeg encode: %w", err)
}

// kill ends an encoder whose job failed elsewhere.
func (e *encoder) kill() {
	if e.waited {
		return
	}
	e.waited = true
	_ = e.in.Close()
	_ = e.cmd.Process.Kill()
	_ = e.cmd.Wait()
}
