package voclean

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

const (
	// rate: every track is decoded and processed at 48 kHz.
	rate = 48000
	// formatWhitelist: the containers a library video can be (a file named
	// .mkv could be anything else).
	formatWhitelist = "matroska,webm,mov,mp4,m4a,3gp,3g2,mj2,avi,mpegts"
	// timeline puts every decoded stream on the file's timeline from 0:
	// a stream that starts late (a container delay) is padded with silence,
	// a gap in its timestamps filled, so sample i of every stream is the
	// same instant — the same in the measurement (input -ss trims on that
	// timeline) and the cleaning (from the start). Only hard compensation
	// (silence in, samples out past 5 ms); no stretching.
	timeline = "aresample=min_comp=0.001:min_hard_comp=0.005:first_pts=0"
	// readFrames: frames per block handed from a pipe to the DSP.
	readFrames = 4096
	// pipeBlocks: blocks buffered per stream, so the streams' interleaving
	// in the file (well under a second) never stalls ffmpeg while the DSP
	// waits on another stream.
	pipeBlocks = 48
	yieldEvery = 250 * time.Millisecond
)

// stallAfter: a read that hands the DSP no block for this long of running
// (not held) time is ended: ErrBadlyInterleaved when the lockstep read
// waits on an empty stream while another's queue is full (the file's
// streams are interleaved too coarsely for the queues: a verdict on the
// file), else ErrStalled (the disk starved at idle I/O priority, a slow
// encoder, a hung ffmpeg: a failure, retried).
var stallAfter = 60 * time.Second

var (
	// ErrBadlyInterleaved: the file's streams could not be read in step.
	ErrBadlyInterleaved = errors.New("badly interleaved: the streams cannot be read in step")
	// ErrStalled: the read made no progress, for no reason of the file's.
	ErrStalled = errors.New("read stalled: no progress")
)

// source is a stream to decode: its input (0: the file, k: its external
// track k-1, Input.External), its index in the file and channel count.
type source struct{ input, idx, ch int }

// decoder is one ffmpeg reading the file once and decoding several audio
// streams, each as interleaved f32le PCM on its own pipe (fd 3, 4, …).
type decoder struct {
	cmd     *exec.Cmd
	outs    []*pcmStream
	readers []*os.File
	stderr  *tailBuffer
	quit    chan struct{}
	exited  chan struct{}
	werr    error
	once    sync.Once
	// taken counts the blocks handed to the DSP (the stall watchdog's
	// sign of progress); stall closes when it gives up
	taken     atomic.Int64
	stall     chan struct{}
	stallOnce sync.Once
	// waiting: the stream the DSP waits on for a block (-1: none);
	// interleaved: the stall was the file's interleaving
	waiting     atomic.Int32
	interleaved atomic.Bool
}

// pcmStream is one decoded stream, read by its own goroutine into a
// bounded queue of blocks.
type pcmStream struct {
	ch     int
	blocks chan []float32
	cur    []float32
	err    error // set before blocks closes
}

// startDecoder decodes srcs of path from startMs for durMs (0: to the
// end), at the lowest priority, reading only a local file of a video
// container. busy (nil: never) holds ffmpeg stopped while playback needs
// the disk; it is killed once it has run longer than limit (0: none).
func startDecoder(ctx context.Context, ffmpeg, path string, ext []ExternalTrack, srcs []source, startMs, durMs int64, busy func() bool, limit time.Duration) (*decoder, error) {
	args := []string{"-hide_banner", "-nostats", "-nostdin", "-loglevel", "error",
		"-protocol_whitelist", "file", "-format_whitelist", formatWhitelist}
	window := func() {
		if startMs > 0 {
			args = append(args, "-ss", msArg(startMs))
		}
		if durMs > 0 {
			args = append(args, "-t", msArg(durMs))
		}
	}
	window()
	args = append(args, "-i", "file:"+path)
	// an external track: shifted onto the file's timeline by its fit (the
	// players' -itsoffset), read with the audio demuxers only
	for _, e := range ext {
		if math.Abs(e.OffsetMs) >= 0.5 {
			args = append(args, "-itsoffset", strconv.FormatFloat(math.Round(e.OffsetMs)/1000, 'f', -1, 64))
		}
		window()
		args = append(args, "-protocol_whitelist", "file", "-format_whitelist", media.AudioFormats, "-i", "file:"+e.Path)
	}
	var readers, writers []*os.File
	closeAll := func() {
		for _, f := range append(readers, writers...) {
			f.Close()
		}
	}
	for k, s := range srcs {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll()
			return nil, err
		}
		readers, writers = append(readers, r), append(writers, w)
		m := "0:" + strconv.Itoa(s.idx)
		if s.input > 0 {
			m = strconv.Itoa(s.input) + ":a:0"
		}
		args = append(args, "-map", m, "-vn", "-sn", "-dn", "-af", timeline,
			"-f", "f32le", "-ac", strconv.Itoa(s.ch), "-ar", strconv.Itoa(rate), "pipe:"+strconv.Itoa(3+k))
	}
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	d := &decoder{cmd: cmd, stderr: &tailBuffer{max: 8 << 10}, quit: make(chan struct{}), exited: make(chan struct{}),
		stall: make(chan struct{}), readers: readers}
	d.waiting.Store(-1)
	cmd.Stderr = d.stderr
	cmd.ExtraFiles = writers
	media.LowPriority(cmd)
	if err := cmd.Start(); err != nil {
		closeAll()
		return nil, err
	}
	for _, w := range writers {
		w.Close() // the child holds them
	}
	go func() { d.werr = cmd.Wait(); close(d.exited) }()
	for k, s := range srcs {
		st := &pcmStream{ch: s.ch, blocks: make(chan []float32, pipeBlocks)}
		d.outs = append(d.outs, st)
		go st.pump(readers[k], d.quit)
	}
	go d.yield(busy, limit)
	return d, nil
}

// pump reads the pipe into blocks until its end (or quit).
func (s *pcmStream) pump(r *os.File, quit <-chan struct{}) {
	defer r.Close()
	defer close(s.blocks)
	br := bufio.NewReaderSize(r, 64<<10)
	raw := make([]byte, readFrames*s.ch*4)
	for {
		n, err := io.ReadFull(br, raw)
		frames := n / 4 / s.ch
		if frames > 0 {
			b := make([]float32, frames*s.ch)
			for i := range b {
				b[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			}
			select {
			case s.blocks <- b:
			case <-quit:
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				s.err = err
			}
			return
		}
	}
}

// read fills buf (interleaved, whole frames) from stream k and returns the
// frames read: fewer only at the stream's end.
func (d *decoder) read(k int, buf []float32) (int, error) {
	s := d.outs[k]
	want := len(buf) / s.ch * s.ch
	got := 0
	for got < want {
		if len(s.cur) == 0 {
			var b []float32
			ok := false
			d.waiting.Store(int32(k))
			select {
			case b, ok = <-s.blocks:
			case <-d.stall:
			}
			d.waiting.Store(-1)
			if d.stalled() {
				return got / s.ch, d.stallErr()
			}
			if !ok {
				return got / s.ch, s.err
			}
			d.taken.Add(1)
			s.cur = b
		}
		n := copy(buf[got:want], s.cur)
		s.cur = s.cur[n:]
		got += n
	}
	return got / s.ch, nil
}

// stallErr says why the read was ended.
func (d *decoder) stallErr() error {
	if d.interleaved.Load() {
		return ErrBadlyInterleaved
	}
	return ErrStalled
}

// lockstepStall: the DSP waits on an empty stream while another stream's
// queue is full — only the file's interleaving does that.
func (d *decoder) lockstepStall() bool {
	w := d.waiting.Load()
	if w < 0 || len(d.outs[w].blocks) > 0 {
		return false
	}
	for k, s := range d.outs {
		if k != int(w) && len(s.blocks) == cap(s.blocks) {
			return true
		}
	}
	return false
}

func (d *decoder) stalled() bool {
	select {
	case <-d.stall:
		return true
	default:
		return false
	}
}

// yield holds ffmpeg stopped while busy, kills it past limit of running
// (not stopped) time, and ends a read that makes no progress for
// stallAfter of running time.
func (d *decoder) yield(busy func() bool, limit time.Duration) {
	tick := time.NewTicker(min(yieldEvery, stallAfter/4))
	defer tick.Stop()
	paused := false
	var idle time.Duration // running time since the DSP last got a block
	lastTaken := d.taken.Load()
	if busy != nil && busy() { // at once: a short read must not slip through
		_ = media.PauseProcess(d.cmd.Process)
		paused = true
	}
	var ran time.Duration
	last := time.Now()
	for {
		select {
		case <-d.exited:
			return
		case <-tick.C:
		}
		now := time.Now()
		if !paused {
			ran += now.Sub(last)
			idle += now.Sub(last)
		}
		last = now
		if t := d.taken.Load(); t != lastTaken {
			lastTaken, idle = t, 0
		}
		if idle > stallAfter {
			d.interleaved.Store(d.lockstepStall())
			d.stallOnce.Do(func() { close(d.stall) })
			_ = media.ResumeProcess(d.cmd.Process)
			_ = d.cmd.Process.Kill()
			return
		}
		if limit > 0 && ran > limit {
			_ = media.ResumeProcess(d.cmd.Process)
			_ = d.cmd.Process.Kill()
			return
		}
		if busy == nil {
			continue
		}
		if b := busy(); b != paused {
			if b {
				_ = media.PauseProcess(d.cmd.Process)
			} else {
				_ = media.ResumeProcess(d.cmd.Process)
			}
			paused = b
		}
	}
}

// wait ends the read: everything left is dropped and ffmpeg waited for,
// its error with the last line it printed.
func (d *decoder) wait() error {
	d.stop()
	<-d.exited
	if d.stalled() {
		return d.stallErr()
	}
	if d.werr != nil {
		return fmt.Errorf("ffmpeg decode: %w: %s", d.werr, d.stderr.last())
	}
	return nil
}

// abort kills ffmpeg and waits for it.
func (d *decoder) abort() {
	_ = media.ResumeProcess(d.cmd.Process)
	_ = d.cmd.Process.Kill()
	d.stop()
	<-d.exited
}

// stop ends the pumps: those waiting to hand a block see quit, those
// reading see their pipe closed (never waits on a pipe something still
// holds open).
func (d *decoder) stop() {
	d.once.Do(func() {
		close(d.quit)
		for _, r := range d.readers {
			_ = r.Close()
		}
	})
}

func msArg(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

// tailBuffer keeps the last max bytes written (an ffmpeg's stderr).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(bytes.ToValidUTF8(t.buf, nil)))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// StartOffsetMs returns where the file's timeline begins (ffprobe's
// format start_time, ms): the cleaned tracks start there, as ffmpeg
// presents every stream of the file from 0 (it subtracts this). 0 when
// unknown.
func StartOffsetMs(ctx context.Context, ffprobe, path string) (float64, error) {
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "file",
		"-show_entries", "format=start_time", "-of", "csv=p=0", "file:"+path).Output()
	if err != nil {
		return 0, err
	}
	v := strings.TrimSpace(string(out))
	if v == "" || v == "N/A" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, err
	}
	return math.Round(f*1e6) / 1e3, nil
}
