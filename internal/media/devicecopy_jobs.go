package media

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound: no device copy with that id (never made, evicted or failed
	// before a restart).
	ErrNotFound = errors.New("media: device copy not found")
	// ErrTooMany: the profile already has PerUserLimit copies queued or running.
	ErrTooMany = errors.New("media: too many device copies queued for this profile")
)

// Device copy states.
const (
	CopyQueued  = "queued"
	CopyRunning = "running"
	CopyReady   = "ready"
	CopyFailed  = "failed"
)

// Copy error texts shown by the app (CopyStatus.Error).
const (
	copyErrFailed  = "Не удалось подготовить копию"
	copyErrNoSpace = "На сервере не хватает места для копии"
)

var (
	copyIDRe   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	comicZipRe = regexp.MustCompile(`^(\d+)-\d+-\d+\.zip$`)
)

// IsCopyID reports whether id looks like a device copy id.
func IsCopyID(id string) bool { return copyIDRe.MatchString(id) }

// QualityInfo is the quality a copy was asked for and the cap in effect
// (the player's «Качество» menu, as /api/play reports it).
type QualityInfo struct {
	Selected string `json:"selected"`
	Applied  string `json:"applied"`
	Remote   bool   `json:"remote"`
}

// CopyStatus is what the app learns about a device copy.
type CopyStatus struct {
	ID       string      `json:"id"`
	State    string      `json:"state"` // queued | running | ready | failed
	Progress float64     `json:"progress"`
	Size     int64       `json:"size"`
	FileID   int64       `json:"fileId"`
	Error    string      `json:"error,omitempty"`
	Quality  QualityInfo `json:"quality"`
}

// DeviceCopies makes device copies (MP4 files AVPlayer plays offline) one
// at a time in Dir and keeps the finished ones, with the comic page zips,
// within MaxBytes: after each new copy or zip the least recently fetched
// ones are deleted while the total is above it, except those fetched
// within KeepRecent (unless the disk has less than MinFreeBytes free). A
// copy does not start unless the disk keeps MinFreeBytes free after it.
// The same key is one job, whoever asks; a profile may have PerUserLimit
// copies queued or running.
//
// Zero values pick defaults (MaxBytes 50 GiB, MinFreeBytes 2 GiB,
// PerUserLimit 3, KeepRecent 48h, FFmpeg "ffmpeg", Now time.Now); a
// negative value turns that limit off. Call Close when done.
type DeviceCopies struct {
	Dir          string
	MaxBytes     int64
	MinFreeBytes int64
	PerUserLimit int
	KeepRecent   time.Duration
	FFmpeg, HW   string
	Now          func() time.Time
	// Run runs ffmpeg with args, reporting out_time_us; nil: FFmpeg (tests).
	Run func(ctx context.Context, args []string, progress func(outTimeUs int64)) error
	// FreeBytes is the free space of dir; nil: the filesystem's (tests).
	FreeBytes func(dir string) (int64, error)

	once  sync.Once
	ctx   context.Context
	stop  context.CancelFunc
	done  chan struct{}
	wake  chan struct{}
	mu    sync.Mutex
	jobs  map[string]*copyJob     // by copy id
	queue []*copyJob              // FIFO of queued jobs
	files map[string]*storedFile  // finished copies and comic zips, by path
	zips  map[string]*zipBuilding // comic zips being built, by path
}

type copyJob struct {
	id       string
	uid      int64
	key      DeviceCopyKey
	input    string
	plan     DeviceCopyPlan
	quality  QualityInfo
	state    string
	progress float64
	size     int64
	err      string
}

func (j *copyJob) status() CopyStatus {
	return CopyStatus{ID: j.id, State: j.state, Progress: j.progress, Size: j.size, FileID: j.key.FileID, Error: j.err, Quality: j.quality}
}

// storedFile is a finished copy (id set) or a comic zip.
type storedFile struct {
	path     string
	id       string
	size     int64
	accessed time.Time
}

type zipBuilding struct {
	done chan struct{}
	err  error
}

// copyMeta is <id>.json next to <id>.mp4.
type copyMeta struct {
	FileID     int64       `json:"fileId"`
	Size       int64       `json:"size"`
	CreatedAt  time.Time   `json:"createdAt"`
	AccessedAt time.Time   `json:"accessedAt"`
	Quality    QualityInfo `json:"quality"`
}

func (c *DeviceCopies) init() {
	c.once.Do(func() {
		if c.MaxBytes == 0 {
			c.MaxBytes = 50 << 30
		}
		if c.MinFreeBytes == 0 {
			c.MinFreeBytes = 2 << 30
		}
		if c.PerUserLimit == 0 {
			c.PerUserLimit = 3
		}
		if c.KeepRecent == 0 {
			c.KeepRecent = 48 * time.Hour
		}
		if c.FFmpeg == "" {
			c.FFmpeg = "ffmpeg"
		}
		if c.Now == nil {
			c.Now = time.Now
		}
		c.jobs = map[string]*copyJob{}
		c.files = map[string]*storedFile{}
		c.zips = map[string]*zipBuilding{}
		c.wake = make(chan struct{}, 1)
		c.done = make(chan struct{})
		c.ctx, c.stop = context.WithCancel(context.Background())
		c.load()
		go c.loop()
	})
}

func (c *DeviceCopies) comicsDir() string { return filepath.Join(c.Dir, "comics") }
func (c *DeviceCopies) subsDir() string   { return filepath.Join(c.Dir, "subs") }

// SubsDir is where the API puts decoded (UTF-16 → UTF-8) external
// subtitles of a copy, named <id>-<k><ext>; a job deletes its own when it
// ends.
func (c *DeviceCopies) SubsDir() string {
	c.init() // the start-up clean-up runs before anything is put there
	return c.subsDir()
}

// load picks up the copies and zips of an earlier process and deletes what
// an interrupted one left (partial copies, temp zips, decoded subtitles).
// Only names this type makes are touched.
func (c *DeviceCopies) load() {
	for _, d := range []string{c.Dir, c.comicsDir(), c.subsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Printf("media: device copies: %v", err)
		}
	}
	es := c.readDir(c.Dir)
	for _, e := range es {
		n := e.Name()
		switch {
		case e.IsDir():
		case strings.HasSuffix(n, ".part.mp4") && IsCopyID(strings.TrimSuffix(n, ".part.mp4")):
			os.Remove(filepath.Join(c.Dir, n))
		case strings.HasSuffix(n, ".json") && IsCopyID(strings.TrimSuffix(n, ".json")):
			id := strings.TrimSuffix(n, ".json")
			mp4 := filepath.Join(c.Dir, id+".mp4")
			var m copyMeta
			b, err := os.ReadFile(filepath.Join(c.Dir, n))
			st, serr := os.Stat(mp4)
			if err != nil || json.Unmarshal(b, &m) != nil || serr != nil {
				os.Remove(filepath.Join(c.Dir, n))
				os.Remove(mp4)
				continue
			}
			c.jobs[id] = &copyJob{id: id, key: DeviceCopyKey{FileID: m.FileID}, quality: m.Quality, state: CopyReady, progress: 1, size: st.Size()}
			c.files[mp4] = &storedFile{path: mp4, id: id, size: st.Size(), accessed: m.AccessedAt}
		}
	}
	for _, e := range es { // a copy without its json is not known
		n := e.Name()
		if id := strings.TrimSuffix(n, ".mp4"); !e.IsDir() && n != id && IsCopyID(id) && c.jobs[id] == nil {
			os.Remove(filepath.Join(c.Dir, n))
		}
	}
	ss := c.readDir(c.subsDir())
	for _, e := range ss {
		if !e.IsDir() && len(e.Name()) > 17 && IsCopyID(e.Name()[:16]) && e.Name()[16] == '-' {
			os.Remove(filepath.Join(c.subsDir(), e.Name()))
		}
	}
	zs := c.readDir(c.comicsDir())
	for _, e := range zs {
		n, p := e.Name(), filepath.Join(c.comicsDir(), e.Name())
		if e.IsDir() {
			continue
		}
		if strings.Contains(n, ".zip.tmp") {
			os.Remove(p)
			continue
		}
		if z, ok := strings.CutSuffix(n, atimeSuffix); ok && comicZipRe.MatchString(z) {
			if _, err := os.Stat(filepath.Join(c.comicsDir(), z)); err != nil {
				os.Remove(p) // its zip is gone
			}
			continue
		}
		if comicZipRe.MatchString(n) {
			if st, err := e.Info(); err == nil {
				f := &storedFile{path: p, size: st.Size(), accessed: st.ModTime()}
				if at, err := os.Stat(p + atimeSuffix); err == nil {
					f.accessed = at.ModTime()
				}
				c.files[p] = f
			}
		}
	}
}

// readDir lists dir for load, logging (not hiding) a failure.
func (c *DeviceCopies) readDir(dir string) []os.DirEntry {
	es, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("media: device copies: %v", err)
	}
	return es
}

// atimeSuffix names the empty file beside a comic zip whose mtime is the
// zip's last access: the zip's own mtime stays its build time, the
// Last-Modified that Range resumes (If-Range) check.
const atimeSuffix = ".atime"

// recordZipAccess keeps a zip's access time across restarts.
func recordZipAccess(p string, t time.Time) {
	a := p + atimeSuffix
	err := os.Chtimes(a, t, t)
	if errors.Is(err, os.ErrNotExist) {
		var f *os.File
		if f, err = os.Create(a); err == nil {
			f.Close()
			err = os.Chtimes(a, t, t)
		}
	}
	if err != nil {
		log.Printf("media: comic zip access time: %v", err)
	}
}

func (c *DeviceCopies) free() (int64, error) {
	if c.FreeBytes != nil {
		return c.FreeBytes(c.Dir)
	}
	return freeBytes(c.Dir)
}

// lowDisk: the disk has less than MinFreeBytes + need free.
func (c *DeviceCopies) lowDisk(need int64) bool {
	if c.MinFreeBytes < 0 {
		return false
	}
	free, err := c.free()
	return err == nil && free < c.MinFreeBytes+need
}

// estimate is the expected size of a copy: the source's for a stream copy
// or an uncapped transcode, else the bitrate cap (plus the AAC audio) over
// the duration.
func estimate(k DeviceCopyKey, pl DeviceCopyPlan) int64 {
	if pl.VideoCopy || pl.MaxBitrate <= 0 {
		return k.Size
	}
	return (pl.MaxBitrate + 192_000) * pl.DurationMs / 8000
}

// keyQuality is the quality a key stands for (the API reports the
// caller's own choice over it).
func keyQuality(k DeviceCopyKey) QualityInfo {
	q := QualityInfo{Selected: "original", Applied: "original"}
	if k.MaxHeight > 0 {
		q.Applied = strconv.Itoa(k.MaxHeight)
		q.Selected = q.Applied
	}
	return q
}

// Request returns the copy of key k, queueing a job that makes it from
// input with pl when there is none (or the last one failed). userID is the
// profile counted against PerUserLimit.
func (c *DeviceCopies) Request(ctx context.Context, userID int64, k DeviceCopyKey, input string, pl DeviceCopyPlan) (CopyStatus, error) {
	c.init()
	if err := ctx.Err(); err != nil {
		return CopyStatus{}, err
	}
	if _, _, err := InputArgs(input); err != nil {
		return CopyStatus{}, err
	}
	id := k.ID()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return CopyStatus{}, errors.New("media: device copies closed")
	}
	if j := c.jobs[id]; j != nil && j.state != CopyFailed {
		if f := c.files[filepath.Join(c.Dir, id+".mp4")]; f != nil && j.state == CopyReady {
			f.accessed = c.Now() // the app downloads it next: keep it from eviction
			c.writeMetaLocked(j, f, time.Time{})
		}
		return j.status(), nil
	}
	if c.PerUserLimit > 0 {
		mine := 0
		for _, j := range c.jobs {
			if j.uid == userID && (j.state == CopyQueued || j.state == CopyRunning) {
				mine++
			}
		}
		if mine >= c.PerUserLimit {
			return CopyStatus{}, ErrTooMany
		}
	}
	if c.lowDisk(estimate(k, pl)) {
		return CopyStatus{}, ErrNoSpace
	}
	j := &copyJob{id: id, uid: userID, key: k, input: input, plan: pl, quality: keyQuality(k), state: CopyQueued}
	c.jobs[id] = j
	c.queue = append(c.queue, j)
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return j.status(), nil
}

// Status reports the copy id.
func (c *DeviceCopies) Status(id string) (CopyStatus, error) {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	j := c.jobs[id]
	if j == nil {
		return CopyStatus{}, ErrNotFound
	}
	return j.status(), nil
}

// accessRecordEvery bounds how often a fetch rewrites a copy's access time
// (a player's Range requests come in bursts).
const accessRecordEvery = time.Minute

// Path returns the file of a ready copy and the library file it was made
// from, and records the access (eviction takes the least recently fetched
// first). A copy still in work is ErrNotReady; a failed one ErrNotFound.
func (c *DeviceCopies) Path(id string) (string, int64, error) {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	j := c.jobs[id]
	if j == nil || j.state == CopyFailed {
		return "", 0, ErrNotFound
	}
	if j.state != CopyReady {
		return "", j.key.FileID, ErrNotReady
	}
	p := filepath.Join(c.Dir, id+".mp4")
	if f := c.files[p]; f != nil {
		if now := c.Now(); now.Sub(f.accessed) >= accessRecordEvery || now.Before(f.accessed) {
			f.accessed = now
			c.writeMetaLocked(j, f, time.Time{})
		}
	}
	return p, j.key.FileID, nil
}

// writeMetaLocked rewrites <id>.json (created zero: keep the stored one).
func (c *DeviceCopies) writeMetaLocked(j *copyJob, f *storedFile, created time.Time) {
	mp := filepath.Join(c.Dir, j.id+".json")
	if created.IsZero() {
		var old copyMeta
		if b, err := os.ReadFile(mp); err == nil && json.Unmarshal(b, &old) == nil {
			created = old.CreatedAt
		}
	}
	b, _ := json.Marshal(copyMeta{FileID: j.key.FileID, Size: f.size, CreatedAt: created.UTC(), AccessedAt: f.accessed.UTC(), Quality: j.quality})
	if err := writeFileAtomic(mp, b); err != nil {
		log.Printf("media: device copy %s: %v", j.id, err)
	}
}

func writeFileAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *DeviceCopies) loop() {
	defer close(c.done)
	for {
		c.mu.Lock()
		for len(c.queue) == 0 {
			c.mu.Unlock()
			select {
			case <-c.wake:
			case <-c.ctx.Done():
				return
			}
			c.mu.Lock()
		}
		j := c.queue[0]
		c.queue = c.queue[1:]
		j.state = CopyRunning
		c.mu.Unlock()
		if c.ctx.Err() != nil {
			return
		}
		c.runJob(j)
	}
}

func (c *DeviceCopies) fail(j *copyJob, msg string) {
	removeJobSubs(c.subsDir(), j.id) // before a new request may re-queue the id
	c.mu.Lock()
	j.state, j.err, j.progress = CopyFailed, msg, 0
	c.mu.Unlock()
}

func (c *DeviceCopies) runJob(j *copyJob) {
	if c.lowDisk(estimate(j.key, j.plan)) {
		c.fail(j, copyErrNoSpace)
		return
	}
	part := filepath.Join(c.Dir, j.id+".part.mp4")
	err := c.ffmpeg(j, c.HW, part)
	if err != nil && c.ctx.Err() == nil && !j.plan.VideoCopy && (c.HW == "qsv" || c.HW == "videotoolbox") {
		log.Printf("media: device copy %s: %s ffmpeg failed, retrying in software: %v", j.id, c.HW, err)
		err = c.ffmpeg(j, "none", part)
	}
	if err != nil {
		os.Remove(part)
		if c.ctx.Err() == nil {
			log.Printf("media: device copy %s of file %d: %v", j.id, j.key.FileID, err)
		}
		c.fail(j, copyErrFailed)
		return
	}
	final := filepath.Join(c.Dir, j.id+".mp4")
	st, err := os.Stat(part)
	if err == nil {
		err = os.Rename(part, final)
	}
	if err != nil {
		os.Remove(part)
		log.Printf("media: device copy %s: %v", j.id, err)
		c.fail(j, copyErrFailed)
		return
	}
	removeJobSubs(c.subsDir(), j.id)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	f := &storedFile{path: final, id: j.id, size: st.Size(), accessed: now}
	c.files[final] = f
	j.state, j.progress, j.size = CopyReady, 1, st.Size()
	c.writeMetaLocked(j, f, now)
	c.evictLocked(final)
}

// removeJobSubs deletes the decoded subtitles of job id.
func removeJobSubs(dir, id string) {
	ms, _ := filepath.Glob(filepath.Join(dir, id+"-*"))
	for _, m := range ms {
		os.Remove(m)
	}
}

func (c *DeviceCopies) ffmpeg(j *copyJob, hw, part string) error {
	args := DeviceCopyArgs(hw, j.input, j.plan, part)
	progress := func(us int64) {
		if j.plan.DurationMs <= 0 || us < 0 {
			return
		}
		c.mu.Lock()
		j.progress = min(float64(us)/1000/float64(j.plan.DurationMs), 0.99)
		c.mu.Unlock()
	}
	if c.Run != nil {
		return c.Run(c.ctx, args, progress)
	}
	return runFFmpegProgress(c.ctx, c.FFmpeg, args, progress)
}

// runFFmpegProgress runs ffmpeg, feeding the out_time_us lines of its
// -progress pipe:1 output to progress.
func runFFmpegProgress(ctx context.Context, bin string, args []string, progress func(int64)) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 2 * time.Second
	tail := &tailBuf{max: 4096}
	cmd.Stderr = tail
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "out_time_us="); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				progress(n)
			}
		}
	}
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail.String())
	}
	return nil
}

// evict deletes the least recently fetched copies and zips while they
// hold more than MaxBytes (see evictLocked).
func (c *DeviceCopies) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictLocked("")
}

// evictLocked deletes stored files, least recently fetched first, while
// they total more than MaxBytes. except (the file just made) stays, and so
// does any fetched within KeepRecent unless the disk is low.
func (c *DeviceCopies) evictLocked(except string) {
	if c.MaxBytes < 0 {
		return
	}
	var total int64
	for _, f := range c.files {
		total += f.size
	}
	if total <= c.MaxBytes {
		return
	}
	low := c.lowDisk(0)
	now := c.Now()
	for total > c.MaxBytes {
		var victim *storedFile
		for _, f := range c.files {
			if f.path == except || (!low && c.KeepRecent > 0 && now.Sub(f.accessed) < c.KeepRecent) {
				continue
			}
			if victim == nil || f.accessed.Before(victim.accessed) {
				victim = f
			}
		}
		if victim == nil {
			return
		}
		c.removeLocked(victim)
		total -= victim.size
	}
}

func (c *DeviceCopies) removeLocked(f *storedFile) {
	delete(c.files, f.path)
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("media: device copies: %v", err)
	}
	if f.id != "" {
		delete(c.jobs, f.id)
		os.Remove(filepath.Join(c.Dir, f.id+".json"))
	} else {
		os.Remove(f.path + atimeSuffix)
	}
}

// ComicZip returns the page zip of a comic volume (file fileID of size and
// mod), building it with build the first time: concurrent callers wait for
// one build, which runs in the first caller's goroutine. A zip of an older
// version of the volume is deleted. Zips count in MaxBytes.
func (c *DeviceCopies) ComicZip(ctx context.Context, fileID, size int64, mod time.Time, build func(w io.Writer) error) (string, error) {
	c.init()
	p := filepath.Join(c.comicsDir(), fmt.Sprintf("%d-%d-%d.zip", fileID, size, mod.Unix()))
	for {
		c.mu.Lock()
		if f := c.files[p]; f != nil {
			if now := c.Now(); now.Sub(f.accessed) >= accessRecordEvery || now.Before(f.accessed) {
				f.accessed = now
				recordZipAccess(p, now) // not the zip's mtime: that is its validator
			}
			c.mu.Unlock()
			return p, nil
		}
		if b := c.zips[p]; b != nil {
			c.mu.Unlock()
			select {
			case <-b.done:
				if b.err != nil && !errors.Is(b.err, context.Canceled) && !errors.Is(b.err, context.DeadlineExceeded) {
					return "", b.err
				}
				continue // built, or its caller went away: build it here
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if c.lowDisk(size) {
			c.mu.Unlock()
			return "", ErrNoSpace
		}
		b := &zipBuilding{done: make(chan struct{})}
		c.zips[p] = b
		c.mu.Unlock()

		err := c.buildZip(p, fileID, build)
		c.mu.Lock()
		delete(c.zips, p)
		b.err = err
		close(b.done)
		c.mu.Unlock()
		if err != nil {
			return "", err
		}
	}
}

func (c *DeviceCopies) buildZip(p string, fileID int64, build func(w io.Writer) error) error {
	tmp, err := os.CreateTemp(c.comicsDir(), filepath.Base(p)+".tmp*")
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(tmp, 256<<10)
	err = build(bw)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := strconv.FormatInt(fileID, 10) + "-"
	for q, f := range c.files { // older versions of the volume
		if f.id == "" && q != p && strings.HasPrefix(filepath.Base(q), prefix) {
			c.removeLocked(f)
		}
	}
	c.files[p] = &storedFile{path: p, size: st.Size(), accessed: c.Now()}
	c.evictLocked(p)
	return nil
}

// Start loads the copies of an earlier process and deletes what it left
// unfinished; any other method does it on first use too.
func (c *DeviceCopies) Start() { c.init() }

// Close stops the worker and kills a running ffmpeg (its partial copy is
// deleted now or at the next start).
func (c *DeviceCopies) Close() {
	c.init()
	c.stop()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
	}
}
