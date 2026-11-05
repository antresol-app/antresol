package segments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// The public databases of skip timestamps.
const (
	DefaultTheIntroDB = "https://api.theintrodb.org"
	DefaultIntroDB    = "https://api.introdb.app"
)

// OnlineTTL: an answer, «nothing there» included, is asked again after this.
const OnlineTTL = 30 * 24 * time.Hour

// dayCap bounds the requests a day to each service (TheIntroDB allows
// 5000 a day without a key); dayCapReset is how often the count restarts.
const (
	dayCap      = 4000
	dayCapReset = 24 * time.Hour
)

// ErrLimited: the service said «too many requests» (or our own daily cap
// is reached); the queue pauses.
var ErrLimited = errors.New("segments: online lookups limited")

// ErrNetwork: the service could not be reached (no answer at all). Only
// these, not a bad answer about one episode, pause the whole queue.
var ErrNetwork = errors.New("segments: online database unreachable")

// Lookup names an episode (or a film: Season and Episode 0) in the online
// databases.
type Lookup struct {
	TmdbID          int64  // the series' (or the film's) TMDB id
	IMDb            string // the series' (or the film's) IMDb id, "tt…"
	Season, Episode int
	Movie           bool
	// DurationMs is our file's: TheIntroDB answers for the release
	// closest to it, so its answers are kept per minute of it.
	DurationMs int64
}

// key is the cache key of the lookup for a service ("" when the service
// cannot be asked: TheIntroDB needs a TMDB or IMDb id, IntroDB an IMDb id).
func (l Lookup) key(service string) string {
	var id string
	switch {
	case service == SourceTheIntroDB && l.TmdbID > 0:
		id = "tmdb:" + strconv.FormatInt(l.TmdbID, 10)
	case l.IMDb != "":
		id = "imdb:" + l.IMDb
	default:
		return ""
	}
	if !l.Movie {
		if l.Episode <= 0 {
			return "" // an episode without a number: it would be asked as episode 0
		}
		id = fmt.Sprintf("%s/%d/%d", id, l.Season, l.Episode)
	}
	if service == SourceTheIntroDB {
		id += fmt.Sprintf("@%dm", l.DurationMs/60000)
	}
	return id
}

// Answer is a database's segments of one episode or film. EndMs < 0: to the
// end of the file. GroupMs: the duration of the release the timestamps
// were measured on (0: unknown).
type Answer struct {
	Segments []Segment `json:"segments"`
	GroupMs  int64     `json:"groupMs,omitempty"`
}

// maxDrift: an answer measured on a release whose duration differs from
// the file's by more than this share is not used for it.
const maxDrift = 0.03

// ForFile is what of a database's answer applies to a file of durationMs:
// nothing when the answer was measured on a release of another length or
// any of its segments ends past the file; else per kind the first segment
// (the credits: the first starting in the second half), open ends closed
// at the end of the file.
func (a Answer) ForFile(durationMs int64) []Segment {
	if durationMs <= 0 {
		return nil
	}
	if a.GroupMs > 0 && math.Abs(float64(a.GroupMs-durationMs)) > maxDrift*float64(durationMs) {
		return nil
	}
	segs := append([]Segment(nil), a.Segments...)
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].StartMs < segs[j].StartMs })
	for _, s := range segs {
		if s.EndMs > durationMs {
			return nil
		}
	}
	var out []Segment
	seen := map[string]bool{}
	for _, s := range segs {
		if s.EndMs < 0 {
			s.EndMs = durationMs
		}
		if seen[s.Kind] || s.EndMs <= s.StartMs || (s.Kind == Credits && s.StartMs < durationMs/2) {
			continue
		}
		seen[s.Kind] = true
		out = append(out, s)
	}
	return out
}

// Online asks TheIntroDB and IntroDB: anonymous, read-only, one request at
// a time (callers are the background queue), spaced by Limit, at most
// dayCap a day per service. A nil Online asks nothing.
type Online struct {
	TheIntroDB string // base URL; "" not asked
	IntroDB    string // base URL; "" not asked
	HTTP       *http.Client
	UA         string
	Limit      *metadata.Limiter
	Now        func() time.Time

	mu    sync.Mutex
	day   map[string]int // requests this day per service
	dayAt time.Time
}

// NewOnline returns the client of the public databases, one request every
// 1.5 s (TheIntroDB allows 30 in 10 s).
func NewOnline(ua string) *Online {
	return &Online{TheIntroDB: DefaultTheIntroDB, IntroDB: DefaultIntroDB, UA: ua,
		HTTP: &http.Client{Timeout: 15 * time.Second}, Limit: &metadata.Limiter{Interval: 1500 * time.Millisecond}}
}

func (o *Online) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Enabled reports whether service is asked.
func (o *Online) Enabled(service string) bool {
	if o == nil {
		return false
	}
	switch service {
	case SourceTheIntroDB:
		return o.TheIntroDB != ""
	case SourceIntroDB:
		return o.IntroDB != ""
	}
	return false
}

// spend counts a request to service against the daily cap.
func (o *Online) spend(service string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if now := o.now(); o.day == nil || now.Sub(o.dayAt) >= dayCapReset {
		o.day, o.dayAt = map[string]int{}, now
	}
	if o.day[service] >= dayCap {
		return false
	}
	o.day[service]++
	return true
}

// get fetches rawURL into v: false for «nothing there» (404, 400: an id
// the service does not take).
func (o *Online) get(ctx context.Context, service, rawURL string, v any) (bool, error) {
	if !o.spend(service) {
		return false, ErrLimited
	}
	if err := o.Limit.Wait(ctx); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", o.UA)
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("%w: %s: %v", ErrNetwork, service, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests:
		return false, fmt.Errorf("%w: %s %s", ErrLimited, service, resp.Status)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest:
		return false, nil
	default:
		return false, fmt.Errorf("%s: %s", service, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return false, fmt.Errorf("%s: %w", service, err)
	}
	return true, nil
}

type tidbStamp struct {
	Start *int64 `json:"start_ms"`
	End   *int64 `json:"end_ms"`
}

type tidbMedia struct {
	GroupMs int64       `json:"group_duration_ms"`
	Intro   []tidbStamp `json:"intro"`
	Recap   []tidbStamp `json:"recap"`
	Credits []tidbStamp `json:"credits"`
}

// stamp turns a database's start/end into a segment: no start is the start
// of the file, no end its end (EndMs -1).
func stamp(kind, source string, start, end *int64) (Segment, bool) {
	s := Segment{Kind: kind, Source: source, EndMs: -1}
	if start != nil {
		s.StartMs = *start
	}
	if end != nil {
		s.EndMs = *end
	} else if kind != Credits {
		return s, false // an intro or recap without an end is not one
	}
	return s, s.StartMs >= 0
}

// AskTheIntroDB asks TheIntroDB v4 for l, measured closest to a file of
// durationMs. An empty Answer: nothing there.
func (o *Online) AskTheIntroDB(ctx context.Context, l Lookup, durationMs int64) (Answer, error) {
	q := url.Values{"details": {"true"}}
	if l.TmdbID > 0 {
		q.Set("tmdb_id", strconv.FormatInt(l.TmdbID, 10))
	} else if l.IMDb != "" {
		q.Set("imdb_id", l.IMDb)
	} else {
		return Answer{}, nil
	}
	if !l.Movie {
		q.Set("season", strconv.Itoa(l.Season))
		q.Set("episode", strconv.Itoa(l.Episode))
	}
	if durationMs > 0 {
		q.Set("duration_ms", strconv.FormatInt(durationMs, 10))
	}
	var m tidbMedia
	ok, err := o.get(ctx, SourceTheIntroDB, o.TheIntroDB+"/v4/media?"+q.Encode(), &m)
	if err != nil || !ok {
		return Answer{}, err
	}
	a := Answer{GroupMs: m.GroupMs}
	for _, k := range []struct {
		kind string
		list []tidbStamp
	}{{Recap, m.Recap}, {Intro, m.Intro}, {Credits, m.Credits}} {
		for _, st := range k.list {
			if s, ok := stamp(k.kind, SourceTheIntroDB, st.Start, st.End); ok {
				a.Segments = append(a.Segments, s)
			}
		}
	}
	return a, nil
}

type idbStamp struct {
	Start *int64 `json:"start_ms"`
	End   *int64 `json:"end_ms"`
}

type idbMedia struct {
	Intro *idbStamp `json:"intro"`
	Recap *idbStamp `json:"recap"`
	Outro *idbStamp `json:"outro"`
}

// AskIntroDB asks IntroDB for l (by IMDb id only). An empty Answer:
// nothing there.
func (o *Online) AskIntroDB(ctx context.Context, l Lookup) (Answer, error) {
	if l.IMDb == "" {
		return Answer{}, nil
	}
	q := url.Values{"imdb_id": {l.IMDb}}
	if !l.Movie {
		q.Set("season", strconv.Itoa(l.Season))
		q.Set("episode", strconv.Itoa(l.Episode))
	}
	var m idbMedia
	ok, err := o.get(ctx, SourceIntroDB, o.IntroDB+"/segments?"+q.Encode(), &m)
	if err != nil || !ok {
		return Answer{}, err
	}
	var a Answer
	for _, k := range []struct {
		kind string
		st   *idbStamp
	}{{Recap, m.Recap}, {Intro, m.Intro}, {Credits, m.Outro}} {
		if k.st == nil {
			continue
		}
		if s, ok := stamp(k.kind, SourceIntroDB, k.st.Start, k.st.End); ok {
			a.Segments = append(a.Segments, s)
		}
	}
	return a, nil
}
