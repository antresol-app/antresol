package channels

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// Program is what a channel plays at some time: its schedule entry and the
// file to read.
type Program struct {
	Entry
	Path  string
	Probe media.ProbeResult
	// Tracks fill the stream's audio and subtitle slots (nil: Plan of the
	// probe alone).
	Tracks *Tracks
	// URL and Headers: a title outside the library plays this stream
	// (a loopback URL, media.LiveInput.Check) instead of Path; Release
	// (when set) is called once it is over.
	URL     string
	Headers map[string]string
	Release func()
	// Skip: nothing plays in this programme (a title outside the library
	// that would not play); the stream goes on with the next one.
	Skip bool
}

// external: a title from outside the library.
func (p Program) external() bool { return p.URL != "" || p.ExtraID != 0 }

// Source finds the programme of a channel at t (on air at t, else the
// next one); ErrNothing when there is none.
type Source interface {
	Program(ctx context.Context, channel int64, t time.Time) (Program, error)
}

var (
	// ErrBusy: Max channels are encoding already.
	ErrBusy = errors.New("channels: too many live channels")
	// ErrNotReady: the channel's first segments are not there yet.
	ErrNotReady = errors.New("channels: live stream not ready")
	// ErrNoSegment: no such segment (never made, or deleted).
	ErrNoSegment = errors.New("channels: no such segment")
	// ErrClosed: the server is shutting down.
	ErrClosed = errors.New("channels: closed")
)

// liveDir is the name of a channel run's directory (<channel>-<run>).
var liveDir = regexp.MustCompile(`^\d+-\d+$`)

var (
	segName   = regexp.MustCompile(`^seg(\d{1,12})\.ts$`)
	audioName = regexp.MustCompile(`^a(\d)-(\d{1,12})\.ts$`)
	runSeg    = regexp.MustCompile(`^(?:seg|a\d-)(\d{1,12})\.ts$`)
	vttName   = regexp.MustCompile(`^s-([a-z0-9]+)-(\d{1,12})\.vtt$`)
)

// Live runs one HLS encoder per watched channel, shared by all its
// viewers. A channel starts on its first playlist request, at the
// programme on air and its offset, and stops IdleTimeout after the last
// request. Each programme is one ffmpeg (media.LiveArgs) writing into the
// channel's directory with one numbering and one timeline; Live writes
// the live playlist itself (a sliding window, a discontinuity at each
// programme boundary).
//
// Zero values pick defaults: Max 3, IdleTimeout 60 s, ZapIdle 10 s, Window 10 segments,
// Keep 30 segments, Lead 20 s, MaxLag 30 s, ReadyWait 20 s, ReadySegments
// 2, ReadRate 1 (a negative ReadRate reads at full speed: tests), Width ×
// Height 1920×1080.
type Live struct {
	FFmpeg string
	HW     string
	Dir    string
	Source Source

	Max         int
	IdleTimeout time.Duration
	// ZapIdle: a channel nobody asked for this long gives its place to a
	// new one when Max are running (a viewer switched channels).
	ZapIdle       time.Duration
	Window, Keep  int
	Lead, MaxLag  time.Duration
	ReadyWait     time.Duration
	ReadySegments int
	ReadRate      float64
	Width, Height int
	Bitrate       int64
	Now           func() time.Time

	once  sync.Once
	root  context.Context
	stop  context.CancelFunc
	mu    sync.Mutex
	chans map[int64]*liveChan
	runs  int64
	wg    sync.WaitGroup
	shut  bool
}

type liveSeg struct {
	seq  int64
	dur  float64
	disc bool // the first segment of a programme after another
	// prog: the programme it belongs to; adur the length of each of its
	// audio outputs' segment of this number; pts its first video PTS and
	// at where it starts in the programme (seconds, from the PTS).
	prog *liveProg
	adur []float64
	pts  int64
	at   float64
}

// liveProg is one programme's encode: its audio outputs (slots[i] is the
// output slot i plays) and its subtitle tracks.
type liveProg struct {
	outs  int
	slots [4]int
	subs  [2]*liveSubs
	p0    int64 // the PTS of its first segment
	hasP0 bool
}

// liveSubs are the cues of a programme's subtitle output read so far.
type liveSubs struct {
	path  string
	size  int64
	cues  []cue
	final bool
}

type liveChan struct {
	id     int64
	dir    string
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	segs     []liveSeg // newest last
	discGone int       // discontinuities of segments already dropped
	changed  chan struct{}
	last     time.Time // last request
	err      error     // why the encoder stopped (nil: still running)
	program  int64     // item on air (for the status)
}

func (l *Live) init() {
	l.once.Do(func() {
		if l.Max == 0 {
			l.Max = 3
		}
		if l.IdleTimeout == 0 {
			l.IdleTimeout = 60 * time.Second
		}
		if l.ZapIdle == 0 {
			l.ZapIdle = 10 * time.Second
		}
		if l.Window == 0 {
			l.Window = 10
		}
		if l.Keep == 0 {
			l.Keep = 30
		}
		if l.Keep < l.Window {
			l.Keep = l.Window
		}
		if l.Lead == 0 {
			l.Lead = 20 * time.Second
		}
		if l.MaxLag == 0 {
			l.MaxLag = 30 * time.Second
		}
		if l.ReadyWait == 0 {
			l.ReadyWait = 20 * time.Second
		}
		if l.ReadySegments == 0 {
			l.ReadySegments = 2
		}
		if l.ReadRate == 0 {
			l.ReadRate = 1
		}
		if l.Now == nil {
			l.Now = time.Now
		}
		if l.FFmpeg == "" {
			l.FFmpeg = "ffmpeg"
		}
		l.chans = map[int64]*liveChan{}
		l.root, l.stop = context.WithCancel(context.Background())
		// directories of an earlier process (a crash) go
		if es, err := os.ReadDir(l.Dir); err == nil {
			for _, e := range es {
				if liveDir.MatchString(e.Name()) && e.IsDir() {
					os.RemoveAll(filepath.Join(l.Dir, e.Name()))
				}
			}
		}
	})
}

// Running returns the channels encoding now.
func (l *Live) Running() []int64 {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []int64
	for id := range l.chans {
		out = append(out, id)
	}
	return out
}

// channel returns the running channel id, starting it when start is set.
func (l *Live) channel(id int64, start bool) (*liveChan, error) {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shut {
		return nil, ErrClosed
	}
	if c := l.chans[id]; c != nil {
		c.touch(l.Now())
		return c, nil
	}
	if !start {
		return nil, ErrNoSegment
	}
	if len(l.chans) >= l.Max {
		// a viewer who switched channels left one behind: its player asks
		// nothing any more (a watching one reloads the playlist every
		// segment), so it makes room at once instead of after IdleTimeout
		var stale *liveChan
		for _, c := range l.chans {
			if c.idle(l.Now(), l.ZapIdle) && (stale == nil || c.lastSeen().Before(stale.lastSeen())) {
				stale = c
			}
		}
		if stale == nil {
			return nil, ErrBusy
		}
		stale.cancel()
		delete(l.chans, stale.id) // its goroutine sees it gone and only cleans up
	}
	// a directory per run: a stopped run of the same channel may still be
	// removing its own
	l.runs++
	dir := filepath.Join(l.Dir, strconv.FormatInt(id, 10)+"-"+strconv.FormatInt(l.runs, 10))
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(l.root)
	c := &liveChan{id: id, dir: dir, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}), last: l.Now()}
	l.chans[id] = c
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		err := l.run(ctx, c)
		l.mu.Lock()
		if l.chans[id] == c {
			delete(l.chans, id)
		}
		l.mu.Unlock()
		c.mu.Lock()
		c.err = err
		close(c.changed)
		c.changed = make(chan struct{})
		c.mu.Unlock()
		os.RemoveAll(dir)
		close(c.done)
	}()
	return c, nil
}

func (c *liveChan) touch(now time.Time) {
	c.mu.Lock()
	c.last = now
	c.mu.Unlock()
}

// Playlist returns a playlist of channel id: "index.m3u8" the master,
// "video.m3u8", "audio-<slot>.m3u8" and "subs-<slot>.m3u8" the media
// playlists. The channel starts when it is not running; the answer waits
// (up to ReadyWait) for its first ReadySegments segments, video and every
// audio output.
func (l *Live) Playlist(ctx context.Context, id int64, name string) ([]byte, error) {
	render, ok := l.renderer(name)
	if !ok {
		return nil, ErrNoSegment
	}
	c, err := l.channel(id, true)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(l.ReadyWait)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		n, ch, cerr := len(c.segs), c.changed, c.err
		c.mu.Unlock()
		select {
		case <-c.done:
			if cerr == nil {
				cerr = ErrNotReady
			}
			return nil, cerr
		default:
		}
		if n >= l.ReadySegments {
			return render(c), nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			if n > 0 {
				return render(c), nil
			}
			return nil, ErrNotReady
		}
	}
}

// renderer is the playlist of name.
func (l *Live) renderer(name string) (func(*liveChan) []byte, bool) {
	media := func(k mediaKind) func(*liveChan) []byte {
		return func(c *liveChan) []byte {
			c.mu.Lock()
			defer c.mu.Unlock()
			return renderMedia(c.segs, c.discGone, l.Window, k)
		}
	}
	switch name {
	case "index.m3u8":
		return func(*liveChan) []byte { return l.master() }, true
	case "video.m3u8":
		return media(mediaKind{}), true
	}
	if slot, ok := strings.CutPrefix(name, "audio-"); ok {
		if i := slotIndex(AudioSlots, strings.TrimSuffix(slot, ".m3u8")); i >= 0 && strings.HasSuffix(slot, ".m3u8") {
			return media(mediaKind{audio: i + 1}), true
		}
	}
	if slot, ok := strings.CutPrefix(name, "subs-"); ok {
		if i := slotIndex(SubSlots, strings.TrimSuffix(slot, ".m3u8")); i >= 0 && strings.HasSuffix(slot, ".m3u8") {
			return media(mediaKind{sub: i + 1}), true
		}
	}
	return nil, false
}

func slotIndex(slots []Slot, id string) int {
	for i, s := range slots {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// master is the channel's master playlist (the same for every channel and
// all the time: the slots are fixed).
func (l *Live) master() []byte {
	br := l.Bitrate
	if br <= 0 {
		br = 6_000_000
	}
	w, h := l.Width, l.Height
	if w <= 0 || h <= 0 {
		w, h = 1920, 1080
	}
	return renderMaster(br*4/3+audioBitrate, br+audioBitrate, w, h)
}

// Segment returns the path and length of a segment of a running channel
// (seg<N>.ts the video, a<k>-<N>.ts an audio output's).
func (l *Live) Segment(id int64, name string) (string, float64, error) {
	var seq int64
	out := -1
	if m := segName.FindStringSubmatch(name); m != nil {
		seq, _ = strconv.ParseInt(m[1], 10, 64)
	} else if m := audioName.FindStringSubmatch(name); m != nil {
		out, _ = strconv.Atoi(m[1])
		seq, _ = strconv.ParseInt(m[2], 10, 64)
	} else {
		return "", 0, ErrNoSegment
	}
	c, err := l.channel(id, false)
	if err != nil {
		return "", 0, ErrNoSegment
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.segs {
		if s.seq != seq {
			continue
		}
		if out < 0 {
			return filepath.Join(c.dir, name), s.dur, nil
		}
		if out < len(s.adur) {
			return filepath.Join(c.dir, name), s.adur[out], nil
		}
	}
	return "", 0, ErrNoSegment
}

// VTT returns a subtitle segment of a running channel (s-<slot>-<N>.vtt):
// the cues of the programme's track in that slot within video segment N,
// empty when the programme has none.
func (l *Live) VTT(id int64, name string) ([]byte, error) {
	m := vttName.FindStringSubmatch(name)
	if m == nil {
		return nil, ErrNoSegment
	}
	slot := slotIndex(SubSlots, m[1])
	seq, _ := strconv.ParseInt(m[2], 10, 64)
	if slot < 0 {
		return nil, ErrNoSegment
	}
	c, err := l.channel(id, false)
	if err != nil {
		return nil, ErrNoSegment
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.segs {
		if s.seq == seq {
			var cues []cue
			if sub := s.prog.subs[slot]; sub != nil {
				cues = sub.cues
			}
			return vttSegment(cues, s.at, s.dur, s.pts), nil
		}
	}
	return nil, ErrNoSegment
}

// Stop ends channel id's encoder (an edited or deleted channel).
func (l *Live) Stop(id int64) {
	l.init()
	l.mu.Lock()
	c := l.chans[id]
	l.mu.Unlock()
	if c != nil {
		c.cancel()
		<-c.done
	}
}

// Close stops every channel and waits for their ffmpeg.
func (l *Live) Close() {
	l.init()
	l.mu.Lock()
	l.shut = true
	l.mu.Unlock()
	l.stop()
	l.wg.Wait()
}

// add appends new segments, drops the ones past keep (and their files) and
// wakes the waiters.
func (c *liveChan) add(segs []liveSeg, keep int) {
	if len(segs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.segs = append(c.segs, segs...)
	if n := len(c.segs) - keep; n > 0 {
		for _, s := range c.segs[:n] {
			if s.disc {
				c.discGone++
			}
			os.Remove(filepath.Join(c.dir, "seg"+strconv.FormatInt(s.seq, 10)+".ts"))
			for k := range s.adur {
				os.Remove(filepath.Join(c.dir, media.LiveAudioSegment(k, s.seq)))
			}
		}
		c.segs = append([]liveSeg(nil), c.segs[n:]...)
	}
	close(c.changed)
	c.changed = make(chan struct{})
}

// runPlaylist reads the segments ffmpeg's own playlist lists (complete
// #EXTINF + name pairs only: the file may be half written).
func runPlaylist(b []byte) []liveSeg {
	var out []liveSeg
	sc := bufio.NewScanner(bytes.NewReader(b))
	dur := -1.0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			v, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			d, err := strconv.ParseFloat(v, 64)
			if err != nil {
				dur = -1
				continue
			}
			dur = d
		case line != "" && !strings.HasPrefix(line, "#") && dur >= 0:
			if m := runSeg.FindStringSubmatch(line); m != nil {
				seq, _ := strconv.ParseInt(m[1], 10, 64)
				out = append(out, liveSeg{seq: seq, dur: dur})
			}
			dur = -1
		}
	}
	return out
}

func (c *liveChan) lastSeen() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *liveChan) idle(now time.Time, d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return now.Sub(c.last) > d
}

// run is a channel's encoder loop: programme after programme until the
// channel idles out or ctx ends.
func (l *Live) run(ctx context.Context, c *liveChan) error {
	now := l.Now()
	pos := now                                          // the schedule time encoded so far
	seq := now.Unix() / int64(media.LiveSegmentSeconds) // never a number a player saw before
	var tsOffset float64
	first := true
	fails := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		now = l.Now()
		if c.idle(now, l.IdleTimeout) {
			return nil
		}
		if pos.Before(now.Add(-l.MaxLag)) {
			// the encoder fell behind the schedule: catch up (skip)
			pos = now
		}
		p, err := l.Source.Program(ctx, c.id, pos)
		if err != nil {
			if errors.Is(err, ErrNothing) {
				return fmt.Errorf("channels: channel %d has nothing to play", c.id)
			}
			return err
		}
		if p.Start.After(pos) {
			pos = p.Start // a gap in the schedule
		}
		if p.Skip {
			pos = p.End
			continue
		}
		rem := p.End.Sub(pos)
		if rem < 500*time.Millisecond {
			pos = p.End
			continue
		}
		// burst ahead until the stream leads the clock by Lead
		burst := (l.Lead - pos.Sub(now)).Seconds()
		burst = math.Max(0, math.Min(burst, l.Lead.Seconds()))
		c.mu.Lock()
		c.program = p.ItemID
		c.mu.Unlock()
		segs, clean, err := l.encode(ctx, c, p, pos.Sub(p.Start).Seconds(), rem.Seconds(), seq, tsOffset, burst, !first)
		if p.Release != nil {
			p.Release()
		}
		if ctx.Err() != nil {
			return nil
		}
		var made float64
		for _, s := range segs {
			made += s.dur
		}
		if len(segs) == 0 && p.external() {
			// a stream from outside the library that would not play is
			// skipped and does not count: the library's programmes decide
			// whether the channel works
			log.Printf("channels: channel %d: the title outside the library (entry %d) gave nothing: %v", c.id, p.Seq, err)
			pos = p.End
			continue
		}
		if len(segs) == 0 {
			fails++
			log.Printf("channels: channel %d: %s at %.0f s gave nothing: %v", c.id, filepath.Base(p.Path), pos.Sub(p.Start).Seconds(), err)
			if fails >= 5 {
				return fmt.Errorf("channels: channel %d: %d programmes in a row failed: %w", c.id, fails, err)
			}
			pos = p.End // skip it
			continue
		}
		fails = 0
		first = false
		seq = segs[len(segs)-1].seq + 1
		// the next programme's timestamps start a little after this one's
		// end: its audio may run a few frames past the video
		tsOffset += made + tsGap
		if clean {
			pos = p.End
		} else {
			pos = pos.Add(time.Duration(made * float64(time.Second)))
		}
	}
}

// encode runs one programme's ffmpeg, adding its segments to c as they
// appear. clean: ffmpeg finished the stretch.
func (l *Live) encode(ctx context.Context, c *liveChan, p Program, startSec, durSec float64, seq int64, tsOffset, burst float64, disc bool) ([]liveSeg, bool, error) {
	tr := Plan(p.Probe, nil, nil, PlanOptions{})
	if p.Tracks != nil {
		tr = *p.Tracks
	}
	in := media.LiveInput{
		Input: p.Path, URL: p.URL, Headers: p.Headers, StartSec: startSec, DurationSec: durSec,
		Width: l.Width, Height: l.Height, Bitrate: l.Bitrate,
		TsOffset: tsOffset, StartNumber: seq, OutDir: c.dir, Playlist: "run.m3u8",
	}
	if v := p.Probe.Video; v != nil {
		in.VideoCodec, in.PixFmt, in.SrcWidth, in.SrcHeight = v.Codec, v.PixFmt, v.Width, v.Height
	}
	if l.ReadRate > 0 {
		in.Throttle = media.Throttle{Rate: l.ReadRate, Burst: burst}
	}
	if p.URL != "" {
		if err := in.Check(); err != nil {
			return nil, false, err
		}
	} else if p.ExtraID != 0 {
		return nil, false, errors.New("channels: a title outside the library without a stream")
	}
	segs, clean, err := l.encodeTracks(ctx, c, in, tr, seq, disc, l.HW)
	if len(segs) == 0 && l.HW == "qsv" && ctx.Err() == nil {
		// the iGPU refused (a codec it does not take): the CPU then
		log.Printf("channels: channel %d: qsv failed (%v), retrying in software", c.id, err)
		segs, clean, err = l.encodeTracks(ctx, c, in, tr, seq, disc, "none")
	}
	if len(segs) == 0 && ctx.Err() == nil {
		if own, changed := tr.ownOnly(p.Probe); changed {
			// an external voice-over or subtitle file ffmpeg could not read:
			// the file's own tracks then
			log.Printf("channels: channel %d: %s with its external tracks failed (%v), retrying without them", c.id, filepath.Base(p.Path), err)
			hw := l.HW
			if hw == "qsv" {
				hw = "none"
			}
			segs, clean, err = l.encodeTracks(ctx, c, in, own, seq, disc, hw)
		}
	}
	return segs, clean, err
}

// ownOnly is t without external audio and subtitle files (changed: it
// had some).
func (t Tracks) ownOnly(p media.ProbeResult) (Tracks, bool) {
	changed := false
	for i, a := range t.Audio {
		if a != nil && a.Source.External != "" {
			t.Audio[i], changed = nil, true
		}
	}
	for i, s := range t.Subs {
		if s != nil && s.External != "" {
			t.Subs[i], changed = nil, true
		}
	}
	if t.Fallback.External != "" {
		changed = true
		t.Fallback = silence
		if a := t.Audio[slotRU]; a != nil {
			t.Fallback = a.Source
		} else if len(p.Audio) > 0 {
			t.Fallback = AudioSource{Index: p.Audio[0].Index}
		}
	}
	return t, changed
}

// encodeTracks runs the programme's ffmpeg with the tracks tr: one audio
// output per distinct source of the slots.
func (l *Live) encodeTracks(ctx context.Context, c *liveChan, in media.LiveInput, tr Tracks, seq int64, disc bool, hw string) ([]liveSeg, bool, error) {
	prog := &liveProg{}
	var outs []AudioSource
	for i := range AudioSlots {
		src := tr.source(i)
		k := slices.Index(outs, src)
		if k < 0 {
			k = len(outs)
			outs = append(outs, src)
		}
		prog.slots[i] = k
	}
	prog.outs = len(outs)
	in.Audio = nil
	for _, o := range outs {
		in.Audio = append(in.Audio, media.LiveAudio{Input: o.External, Index: o.Index, OffsetMs: o.OffsetMs, Tempo: o.Tempo})
	}
	if in.URL != "" {
		// a stream nobody probed: its first audio stream instead of silence
		for k := range in.Audio {
			if in.Audio[k].Input == "" && in.Audio[k].Index < 0 {
				in.Audio[k].Index = media.LiveFirstAudio
			}
		}
	}
	in.Subs = nil
	for j, sp := range tr.Subs {
		if sp == nil {
			continue
		}
		out := "sub" + strconv.FormatInt(seq, 10) + "-" + SubSlots[j].ID + ".vtt"
		ls := media.LiveSub{Index: sp.Index, Codec: sp.Codec, Out: out}
		if sp.External != "" {
			// read here and handed to ffmpeg as UTF-8 (old .srt files are
			// Windows-1251, some UTF-16)
			b, err := os.ReadFile(sp.External)
			if err != nil || len(b) > maxSubFile {
				log.Printf("channels: channel %d: subtitles %s left out: %v", c.id, filepath.Base(sp.External), err)
				continue
			}
			src := filepath.Join(c.dir, "subsrc"+strconv.FormatInt(seq, 10)+"-"+SubSlots[j].ID+filepath.Ext(sp.External))
			if err := os.WriteFile(src, []byte(subtitles.Decode(b)), 0o644); err != nil {
				continue
			}
			defer os.Remove(src)
			ls.Input = src
		}
		in.Subs = append(in.Subs, ls)
		prog.subs[j] = &liveSubs{path: filepath.Join(c.dir, out)}
	}
	// a run's own playlists and leftovers of an earlier try
	os.Remove(filepath.Join(c.dir, in.Playlist))
	for k := range outs {
		os.Remove(filepath.Join(c.dir, media.LiveAudioPlaylist(k)))
	}
	for _, sub := range prog.subs {
		if sub != nil {
			os.Remove(sub.path)
		}
	}
	return l.runFFmpeg(ctx, c, media.LiveArgs(hw, in), in.Playlist, prog, disc)
}

// maxSubFile bounds a subtitle file read for a programme.
const maxSubFile = 8 << 20

// audioBitrate is each audio output's bit rate (media.LiveArgs).
const audioBitrate = 192_000

func (l *Live) runFFmpeg(ctx context.Context, c *liveChan, args []string, playlist string, prog *liveProg, disc bool) ([]liveSeg, bool, error) {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(rctx, l.FFmpeg, args...)
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &limited{b: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var got []liveSeg // published
	audio := make([]map[int64]float64, prog.outs)
	var video []liveSeg // as ffmpeg listed them
	collect := func(final bool) {
		if b, err := os.ReadFile(filepath.Join(c.dir, playlist)); err == nil {
			video = runPlaylist(b)
		}
		for k := range audio {
			if b, err := os.ReadFile(filepath.Join(c.dir, media.LiveAudioPlaylist(k))); err == nil {
				m := map[int64]float64{}
				for _, s := range runPlaylist(b) {
					m[s.seq] = s.dur
				}
				audio[k] = m
			}
		}
		// a segment goes out once every audio output has its own of it
		var fresh []liveSeg
		for _, s := range video[min(len(got), len(video)):] {
			s.adur = make([]float64, prog.outs)
			ok := true
			for k := range audio {
				d, has := audio[k][s.seq]
				ok = ok && has
				s.adur[k] = d
			}
			if !ok {
				break
			}
			pts, err := firstPTS(filepath.Join(c.dir, "seg"+strconv.FormatInt(s.seq, 10)+".ts"))
			if err != nil {
				log.Printf("channels: channel %d: segment %d: %v", c.id, s.seq, err)
				break
			}
			if !prog.hasP0 {
				prog.p0, prog.hasP0 = pts, true
			}
			s.prog, s.pts, s.at = prog, pts, float64(ptsSince(prog.p0, pts))/90000
			if len(got)+len(fresh) == 0 && disc {
				s.disc = true
			}
			fresh = append(fresh, s)
		}
		got = append(got, fresh...)
		c.mu.Lock()
		subsChanged := prog.readSubs(final)
		c.mu.Unlock()
		if len(fresh) > 0 {
			c.add(fresh, l.Keep)
		} else if subsChanged {
			c.wake()
		}
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			collect(true)
			// segments past the last published one (an audio output's
			// extra second, a video segment whose audio never came) go
			last := int64(-1)
			if len(got) > 0 {
				last = got[len(got)-1].seq
			}
			for _, s := range video {
				if s.seq > last {
					os.Remove(filepath.Join(c.dir, "seg"+strconv.FormatInt(s.seq, 10)+".ts"))
				}
			}
			for k, m := range audio {
				for n := range m {
					if n > last {
						os.Remove(filepath.Join(c.dir, media.LiveAudioSegment(k, n)))
					}
				}
			}
			for _, sub := range prog.subs {
				if sub != nil {
					os.Remove(sub.path) // its cues are read
				}
			}
			if err != nil && ctx.Err() == nil {
				err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return got, err == nil, err
		case <-tick.C:
			collect(false)
			if c.idle(l.Now(), l.IdleTimeout) {
				cancel() // nobody watches: done picks it up
			}
		}
	}
}

// readSubs reads the programme's subtitle outputs again where they grew
// (c.mu held); true when there are new cues.
func (p *liveProg) readSubs(final bool) bool {
	changed := false
	for _, s := range p.subs {
		if s == nil || s.final {
			continue
		}
		st, err := os.Stat(s.path)
		if !final && (err != nil || st.Size() == s.size) {
			continue
		}
		b, err := os.ReadFile(s.path)
		if err != nil {
			s.final = final
			continue
		}
		s.size, s.final = int64(len(b)), final
		cues := parseVTT(b, final)
		changed = changed || len(cues) != len(s.cues)
		s.cues = cues
	}
	return changed
}

// wake tells the waiters something changed.
func (c *liveChan) wake() {
	c.mu.Lock()
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

// tsGap separates the timelines of two programmes (seconds).
const tsGap = 0.25

// limited keeps the first n bytes written to it.
type limited struct {
	b *bytes.Buffer
	n int
}

func (w *limited) Write(p []byte) (int, error) {
	if room := w.n - w.b.Len(); room > 0 {
		w.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
