// Package ratings shows what critics and viewers think of a movie or a
// series, by its IMDb id and without any API key: the IMDb rating from
// IMDb's own ratings dataset (title.ratings.tsv.gz, for personal and
// non-commercial use), the Rotten Tomatoes scores from Wikidata (CC0).
package ratings

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The dataset on disk (Dataset.Dir): imdb-ratings.bin is the ratings sorted
// by title number, imdb-ratings.json what the last download said.
//
//	header  8 bytes "AIMDBR01", 4 bytes record count (big endian), 4 reserved
//	record  4 bytes title number (tt0111161 = 111161), 1 byte rating × 10,
//	        4 bytes votes
//
// 1.6 million titles take 15 MB; a lookup is a binary search with ReadAt
// (the page cache keeps the hot part), so nothing of it stays in memory.
const (
	binName   = "imdb-ratings.bin"
	stateName = "imdb-ratings.json"
	magic     = "AIMDBR01"
	headerLen = 16
	recLen    = 9
)

const (
	// DefaultCheckEvery: IMDb rebuilds the dataset daily; it is asked
	// whether it changed at most this often (a conditional GET).
	DefaultCheckEvery = 24 * time.Hour
	// failRetry: after a failed download the next try waits this long.
	failRetry      = time.Hour
	refreshTimeout = 15 * time.Minute
	maxDownload    = 256 << 20 // the gzip is 9 MB today
)

// IMDbRating is a title's rating on IMDb.
type IMDbRating struct {
	Rating float64 `json:"rating"` // 1.0..10.0
	Votes  int     `json:"votes"`
}

// Dataset is IMDb's ratings dataset kept on disk and refreshed in the
// background when a lookup finds it older than CheckEvery.
type Dataset struct {
	URL        string // https://datasets.imdbws.com/title.ratings.tsv.gz
	Dir        string // DataDir
	UserAgent  string
	HTTP       *http.Client  // nil: http.DefaultClient
	CheckEvery time.Duration // 0: DefaultCheckEvery
	Log        *slog.Logger
	now        func() time.Time

	mu     sync.RWMutex
	opened bool
	f      *os.File // nil: no dataset yet
	n      int
	state  state

	refresh   sync.Mutex // one download at a time
	bgMu      sync.Mutex
	bgRunning bool
	failedAt  time.Time
	bgDone    chan struct{} // tests: closed when a background refresh ends
}

type state struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"lastModified,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
	Titles       int       `json:"titles"`
}

func (d *Dataset) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

func (d *Dataset) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

func (d *Dataset) checkEvery() time.Duration {
	if d.CheckEvery > 0 {
		return d.CheckEvery
	}
	return DefaultCheckEvery
}

// open opens the file on disk once (d.mu held for writing).
func (d *Dataset) open() {
	if d.opened {
		return
	}
	d.opened = true
	if b, err := os.ReadFile(filepath.Join(d.Dir, stateName)); err == nil {
		_ = json.Unmarshal(b, &d.state)
	}
	f, n, err := openBin(filepath.Join(d.Dir, binName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.log().Warn("imdb ratings: unreadable dataset, downloading it again", "err", err)
		}
		d.state = state{} // no conditional GET without the file
		return
	}
	d.f, d.n = f, n
}

func openBin(p string) (*os.File, int, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	var h [headerLen]byte
	st, err := f.Stat()
	if err == nil {
		_, err = f.ReadAt(h[:], 0)
	}
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	n := int(binary.BigEndian.Uint32(h[8:12]))
	if string(h[:8]) != magic || st.Size() != int64(headerLen+n*recLen) {
		f.Close()
		return nil, 0, fmt.Errorf("%s: not a ratings file", p)
	}
	return f, n, nil
}

// titleNumber is the number of an IMDb title id ("tt0111161" → 111161).
func titleNumber(id string) (uint32, bool) {
	if !strings.HasPrefix(id, "tt") || len(id) < 3 || len(id) > 12 {
		return 0, false
	}
	n, err := strconv.ParseUint(id[2:], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// Lookup is the rating of an IMDb title id ("tt0111161"); false when the
// dataset has none (or is not downloaded yet). A dataset not checked for a
// day starts a refresh in the background.
func (d *Dataset) Lookup(id string) (IMDbRating, bool) {
	d.mu.Lock()
	d.open()
	stale := d.clock().Sub(d.state.CheckedAt) >= d.checkEvery()
	d.mu.Unlock()
	if stale {
		d.refreshInBackground()
	}
	num, ok := titleNumber(id)
	if !ok {
		return IMDbRating{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.f == nil {
		return IMDbRating{}, false
	}
	var rec [recLen]byte
	var readErr error
	i, found := sortSearch(d.n, func(i int) int {
		if readErr != nil {
			return 0
		}
		if _, err := d.f.ReadAt(rec[:4], int64(headerLen+i*recLen)); err != nil {
			readErr = err
			return 0
		}
		return cmpU32(binary.BigEndian.Uint32(rec[:4]), num)
	})
	if readErr != nil || !found {
		return IMDbRating{}, false
	}
	if _, err := d.f.ReadAt(rec[:], int64(headerLen+i*recLen)); err != nil {
		return IMDbRating{}, false
	}
	return IMDbRating{Rating: float64(rec[4]) / 10, Votes: int(binary.BigEndian.Uint32(rec[5:]))}, true
}

func cmpU32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// sortSearch is a binary search over n records (cmp: the record's key
// against the wanted one).
func sortSearch(n int, cmp func(int) int) (int, bool) {
	lo, hi := 0, n
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch c := cmp(m); {
		case c == 0:
			return m, true
		case c < 0:
			lo = m + 1
		default:
			hi = m
		}
	}
	return lo, false
}

func (d *Dataset) refreshInBackground() {
	d.bgMu.Lock()
	if d.bgRunning || (!d.failedAt.IsZero() && d.clock().Sub(d.failedAt) < failRetry) {
		d.bgMu.Unlock()
		return
	}
	d.bgRunning = true
	done := d.bgDone
	d.bgMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		err := d.Refresh(ctx)
		d.bgMu.Lock()
		d.bgRunning = false
		if err != nil {
			d.failedAt = d.clock()
		} else {
			d.failedAt = time.Time{}
		}
		d.bgMu.Unlock()
		if err != nil {
			d.log().Warn("imdb ratings: refresh failed", "err", err)
		}
		if done != nil {
			done <- struct{}{}
		}
	}()
}

// Refresh downloads the dataset when it changed since the last download
// (If-None-Match / If-Modified-Since) and replaces the file on disk.
func (d *Dataset) Refresh(ctx context.Context) error {
	d.refresh.Lock()
	defer d.refresh.Unlock()
	d.mu.Lock()
	d.open()
	st := d.state
	d.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return err
	}
	if d.UserAgent != "" {
		req.Header.Set("User-Agent", d.UserAgent)
	}
	if st.ETag != "" {
		req.Header.Set("If-None-Match", st.ETag)
	}
	if st.LastModified != "" {
		req.Header.Set("If-Modified-Since", st.LastModified)
	}
	hc := d.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	now := d.clock().UTC()
	switch resp.StatusCode {
	case http.StatusNotModified:
		st.CheckedAt = now
		return d.saveState(st, nil)
	case http.StatusOK:
	default:
		return fmt.Errorf("imdb ratings: %s: HTTP %d", d.URL, resp.StatusCode)
	}
	recs, err := parse(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return errors.New("imdb ratings: the dataset has no titles")
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := writeBin(d.Dir, recs)
	if err != nil {
		return err
	}
	st = state{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), CheckedAt: now, Titles: len(recs)}
	return d.saveState(st, func() error {
		final := filepath.Join(d.Dir, binName)
		if err := os.Rename(tmp, final); err != nil {
			os.Remove(tmp)
			return err
		}
		f, n, err := openBin(final)
		if err != nil {
			return err
		}
		if d.f != nil {
			d.f.Close()
		}
		d.f, d.n = f, n
		return nil
	})
}

// saveState writes st (after swap, run under d.mu) and puts it into effect.
func (d *Dataset) saveState(st state, swap func() error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if swap != nil {
		if err := swap(); err != nil {
			return err
		}
	}
	d.state = st
	b, _ := json.Marshal(st)
	p := filepath.Join(d.Dir, stateName)
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

type record struct {
	num    uint32
	rating uint8 // × 10
	votes  uint32
}

// parse reads the gzip TSV: "tconst\taverageRating\tnumVotes" and a line
// per title. Malformed lines are skipped; a broken gzip is an error.
func parse(r io.Reader) ([]record, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("imdb ratings: %w", err)
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	recs := make([]record, 0, 1<<20)
	sorted := true
	for first := true; sc.Scan(); first = false {
		line := sc.Text()
		if first && strings.HasPrefix(line, "tconst") {
			continue
		}
		id, rest, ok1 := strings.Cut(line, "\t")
		rs, vs, ok2 := strings.Cut(rest, "\t")
		if !ok1 || !ok2 {
			continue
		}
		num, ok := titleNumber(id)
		rating, err1 := strconv.ParseFloat(rs, 64)
		votes, err2 := strconv.ParseUint(strings.TrimSpace(vs), 10, 32)
		if !ok || err1 != nil || err2 != nil || rating < 0 || rating > 10 {
			continue
		}
		rec := record{num: num, rating: uint8(math.Round(rating * 10)), votes: uint32(votes)}
		if len(recs) > 0 && recs[len(recs)-1].num >= num {
			sorted = false
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("imdb ratings: %w", err)
	}
	if !sorted {
		slices.SortStableFunc(recs, func(a, b record) int { return cmpU32(a.num, b.num) })
		recs = slices.CompactFunc(recs, func(a, b record) bool { return a.num == b.num })
	}
	return recs, nil
}

// writeBin writes the records to a temporary file in dir and returns its path.
func writeBin(dir string, recs []record) (string, error) {
	f, err := os.CreateTemp(dir, binName+".*.tmp")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	var h [headerLen]byte
	copy(h[:], magic)
	binary.BigEndian.PutUint32(h[8:12], uint32(len(recs)))
	if _, err := w.Write(h[:]); err != nil {
		return fail(err)
	}
	var b [recLen]byte
	for _, r := range recs {
		binary.BigEndian.PutUint32(b[:4], r.num)
		b[4] = r.rating
		binary.BigEndian.PutUint32(b[5:], r.votes)
		if _, err := w.Write(b[:]); err != nil {
			return fail(err)
		}
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Close closes the file on disk.
func (d *Dataset) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		err := d.f.Close()
		d.f = nil
		return err
	}
	return nil
}
