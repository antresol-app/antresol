package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// Service is the channels feature: the store, schedules kept built, the
// generated channels kept in step with the library and the live encoder.
type Service struct {
	// LLM helps Suggest read a sentence (VAULT_LLM_URL); nil: the words alone.
	LLM *LLM
	// LLMSource, when set, is asked instead of LLM (the admin's «ИИ-помощник»
	// settings over the environment's), for a task of that tier
	LLMSource func(context.Context, Tier) *LLM
	Store     *Store
	Builder   *Builder
	Generator *Generator
	// Franchises works the franchise channels' content out in the
	// background (SetFranchiseSources gives it TMDB and Wikidata).
	Franchises *Franchises
	Live       *Live
	// Probe reads a file the background probe has not reached yet.
	Probe func(ctx context.Context, path string) (media.ProbeResult, error)
	// ExternalAudio lists a file's external audio tracks the way the
	// players get them (the files beside it, probed and fitted, then its
	// cleaned voice-overs); nil: found by their names, unprobed.
	ExternalAudio func(ctx context.Context, fileID int64, path string, probe media.ProbeResult) []media.ExternalAudio
	// DataDir holds the automatic subtitles (subtitles.FindExternal).
	DataDir string

	tmu    sync.Mutex
	tracks map[int64]tracksEntry
	sets   map[int64]setEntry
	probes map[int64]probeEntry
	opts   PlanOptions
	optsAt time.Time
	lang   string // the metadata language (SetLanguage)

	// Extras finds and plays titles outside the library (nil: none; set
	// with SetExtras).
	Extras Extras

	build sync.Mutex
	kick  chan struct{}
	once  sync.Once

	// files whose segments changed, for the schedules (SegmentsChanged)
	segMu    sync.Mutex
	segFiles map[int64]bool
	segKick  chan struct{}

	warm     WarmFunc // SetWarm (nil: none)
	warmKick chan struct{}

	// streams of titles outside the library resolved ahead (prefetch); pctx
	// is Run's context, which ends the waiting for them and lets them go
	pmu      sync.Mutex
	prefetch map[entryKey]*prefetched
	pctx     context.Context

	// channels with fetching (fetch.go; SetFetch): what fetches, names
	// the titles' programmes and removes what aired (nil: none)
	Fetcher      Fetcher
	FetchCatalog FetchCatalog
	FetchRemove  func(ctx context.Context, itemID int64) error
	fetchKick    chan struct{}
	fmu          sync.Mutex
	fetchKeep    map[string]bool // the addon's last «keep» answers, by key
}

// New wires a service on db; live may be nil (no streaming: tests).
func New(db *sql.DB, live *Live) *Service {
	st := &Store{DB: db}
	cat := Catalog{DB: db}
	s := &Service{Store: st, Builder: &Builder{Store: st, Catalog: cat}, Generator: &Generator{Store: st, Catalog: cat}, Live: live, LLM: LLMFromEnv()}
	s.Franchises = &Franchises{Store: st, Model: s.Model, Changed: s.refreshSoon}
	if live != nil && live.Source == nil {
		live.Source = s
	}
	return s
}

// SetRoots sets the library folders picks and the editor's folder browse
// see (LibraryRoots).
func (s *Service) SetRoots(roots []Root) {
	s.Builder.Catalog.Roots = roots
	s.Generator.Catalog.Roots = roots
}

// Model is the language model Suggest asks now (nil: none): the medium
// tier (reading a sentence into filters).
func (s *Service) Model(ctx context.Context) *LLM { return s.ModelFor(ctx, TierMedium) }

// ModelFor is the language model a task of tier t asks now (nil: none).
func (s *Service) ModelFor(ctx context.Context, t Tier) *LLM {
	if s.LLMSource != nil {
		return s.LLMSource(ctx, t)
	}
	return s.LLM
}

func (s *Service) init() {
	s.once.Do(func() {
		s.kick, s.warmKick, s.segKick, s.fetchKick = make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
	})
}

// SetFranchiseSources gives the franchise pass TMDB's collections (nil: no
// franchises) and Wikidata's facts (nil: none).
func (s *Service) SetFranchiseSources(tmdb FranchiseSource, wiki FranchiseFacts) {
	s.Franchises.TMDB, s.Franchises.Wiki = tmdb, wiki
}

// Kick asks for a refresh soon (after a library scan), and a franchise
// pass (it does nothing when the library's titles are as they were).
func (s *Service) Kick() {
	if s == nil {
		return
	}
	s.refreshSoon()
	s.Franchises.Kick(false)
}

// refreshSoon asks for a refresh of the channels only.
func (s *Service) refreshSoon() {
	s.init()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// planEvery: the timer re-plans the generated channels (generate.go) at
// every this many ticks; a Kick (a library scan) or a failed plan does it
// at once. What the plan reads is the whole library, what a tick otherwise
// does is mostly nothing.
const planEvery = 6

// Run refreshes at once, then every interval and on Kick, until ctx ends
// (and the warm hook with it).
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	s.init()
	s.pmu.Lock()
	s.pctx = ctx
	s.pmu.Unlock()
	defer s.sweepPrefetch(true) // nobody takes a stream now
	var wg sync.WaitGroup
	defer wg.Wait()
	start := func(run func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); run(ctx) }()
	}
	if s.warm != nil {
		start(s.warmLoop)
		s.askWarm() // once at start: a restart retries failed guide texts
	}
	if s.Franchises != nil && s.Franchises.TMDB != nil {
		start(s.Franchises.Run)
	}
	start(s.segmentsLoop)
	start(s.fetchLoop)
	t := time.NewTicker(interval)
	defer t.Stop()
	plan := true
	for ticks := 0; ; {
		err := s.refresh(ctx, plan)
		if err != nil && ctx.Err() == nil {
			log.Printf("channels: refresh: %v", err)
		}
		s.sweepPrefetch(false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ticks++
			plan = err != nil || ticks%planEvery == 0
		case <-s.kick:
			plan = true
		}
	}
}

// Refresh brings the generated channels in step with the library, cuts
// schedules at programmes whose files went and builds every enabled
// channel to the horizon.
func (s *Service) Refresh(ctx context.Context) error { return s.refresh(ctx, true) }

// refresh is Refresh; plan false leaves the generated channels as they are
// (a failed plan does not keep the schedules from being extended).
func (s *Service) refresh(ctx context.Context, plan bool) error {
	var errs []error
	if plan {
		if _, err := s.Generator.Sync(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	s.autoLogos(ctx) // franchises and series show their titles' logo
	// one list 1…N (numbers.go): also what a version before it numbered
	if err := s.Store.Renumber(ctx); err != nil {
		return errors.Join(append(errs, err)...)
	}
	all, err := s.Store.All(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range all {
		if !c.Enabled || c.External() {
			continue
		}
		if err := s.BuildChannel(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

// BuildChannel cuts c's schedule at a vanished file and extends it (an
// external channel has none).
func (s *Service) BuildChannel(ctx context.Context, c Channel) error {
	if c.External() {
		return nil
	}
	s.build.Lock()
	defer s.build.Unlock()
	cut, err := s.Store.CutMissing(ctx, c.ID)
	if err != nil {
		return err
	}
	added, err := s.Builder.Build(ctx, c)
	if err != nil {
		return err
	}
	if cut || added { // a no-op build changes nothing the guide shows
		s.askWarm()
	}
	return nil
}

// Segments come a season at a time while the segments worker passes over
// the library: the schedules are redone once they stopped coming for
// segmentsQuiet, and at the latest segmentsMaxWait after the first.
var (
	segmentsQuiet   = time.Minute
	segmentsMaxWait = 10 * time.Minute
)

// SegmentsChanged tells the channels that these files' segments were
// found, changed or dropped (the segments worker's Changed): schedules
// built before keep the episodes as they were cut then, so the part not
// on air yet is made again (RecutSegments). Never blocks.
func (s *Service) SegmentsChanged(files []int64) {
	if s == nil || len(files) == 0 {
		return
	}
	s.init()
	s.segMu.Lock()
	if s.segFiles == nil {
		s.segFiles = map[int64]bool{}
	}
	for _, f := range files {
		s.segFiles[f] = true
	}
	s.segMu.Unlock()
	select {
	case s.segKick <- struct{}{}:
	default:
	}
}

func (s *Service) segmentsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.segKick:
		}
		limit := time.NewTimer(segmentsMaxWait)
	quiet:
		for {
			t := time.NewTimer(segmentsQuiet)
			select {
			case <-ctx.Done():
				t.Stop()
				limit.Stop()
				return
			case <-s.segKick:
				t.Stop()
			case <-t.C:
				break quiet
			case <-limit.C:
				t.Stop()
				break quiet
			}
		}
		limit.Stop()
		s.segMu.Lock()
		files := make([]int64, 0, len(s.segFiles))
		for f := range s.segFiles {
			files = append(files, f)
		}
		s.segFiles = nil
		s.segMu.Unlock()
		if err := s.RecutSegments(ctx, files); err != nil && ctx.Err() == nil {
			log.Printf("channels: segments: %v", err)
		}
	}
}

// RecutSegments redoes the not yet aired part of every channel that skips
// segments and airs one of files from a programme they cut otherwise
// (Builder.Recut).
func (s *Service) RecutSegments(ctx context.Context, files []int64) error {
	if len(files) == 0 {
		return nil
	}
	all, err := s.Store.All(ctx)
	if err != nil {
		return err
	}
	var errs []error
	cut := 0
	for _, c := range all {
		if !c.Enabled || c.External() || !c.Rules.SkipSegments {
			continue
		}
		s.build.Lock()
		ok, err := s.Builder.Recut(ctx, c, files)
		s.build.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", c.ID, err))
		}
		if ok {
			cut++
		}
	}
	if cut > 0 {
		log.Printf("channels: segments of %d files changed: %d schedules made again from their first programme cut otherwise", len(files), cut)
		s.askWarm()
	}
	return errors.Join(errs...)
}

// Program is the Source of Live: the schedule entry at t and its file.
// A channel without a schedule there is built first; a programme whose
// file went is cut and the schedule rebuilt. An external channel has
// nothing (ErrNothing): its stream is the provider's, never encoded here.
func (s *Service) Program(ctx context.Context, ch int64, t time.Time) (Program, error) {
	var kind string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT kind FROM channels WHERE id=?`, ch).Scan(&kind); err == nil && kind == KindExternal {
		return Program{}, ErrNothing
	}
	for try := 0; ; try++ {
		e, err := s.Store.At(ctx, ch, t)
		if errors.Is(err, ErrNothing) && try == 0 {
			if err := s.rebuild(ctx, ch); err != nil {
				return Program{}, err
			}
			continue
		}
		if err != nil {
			return Program{}, err
		}
		if e.FetchID != 0 {
			// a programme not on disk at its slot (the pass cuts it before
			// it airs; the server was off, or the fetch came late): the
			// slot goes to what is available
			if try >= maxExtraTries {
				return Program{Entry: e, Skip: true}, nil
			}
			if err := s.fetchCutAt(ctx, ch, e.Seq); err != nil {
				return Program{}, err
			}
			continue
		}
		if e.ExtraID != 0 {
			p, ok, err := s.extraProgram(ctx, e)
			if ok || err != nil {
				return p, err
			}
			if try >= maxExtraTries {
				return Program{Entry: e, Skip: true}, nil
			}
			continue // the schedule was made again without it
		}
		s.prefetchNext(ch, e)
		path, stored, err := s.fileProbe(ctx, e.FileID)
		if errors.Is(err, sql.ErrNoRows) {
			if try > 0 {
				return Program{}, ErrNothing
			}
			if err := s.rebuild(ctx, ch); err != nil {
				return Program{}, err
			}
			continue
		}
		if err != nil {
			return Program{}, err
		}
		p := Program{Entry: e, Path: path}
		if stored != nil {
			p.Probe = *stored
		} else if s.Probe != nil {
			if pr, err := s.Probe(ctx, path); err == nil {
				p.Probe = pr
			}
		}
		p.Probe = s.encoderProbe(ctx, e.FileID, path, p.Probe)
		t := s.plan(ctx, e.FileID, path, p.Probe, true)
		p.Tracks = &t
		return p, nil
	}
}

func (s *Service) rebuild(ctx context.Context, ch int64) error {
	c, err := s.Store.Get(ctx, ch)
	if err != nil {
		return err
	}
	return s.BuildChannel(ctx, c)
}

// Info describes a programme for the guide and the web app.
type Info struct {
	ItemID   int64
	Kind     string // movie | episode
	Title    string // the movie's or the series' title
	Episode  string // the episode's own title
	Season   int
	Number   int
	Overview string
	Year     int
	Rating   string
	Genres   []string
	// ArtItem: the item whose poster stands for the programme (the movie,
	// the series); ArtPoster its stored poster value.
	ArtItem   int64
	ArtPoster string
	// External: a title outside the library (ItemID is -its channel_extras
	// id); Poster and Backdrop are then its TMDB image paths ("/abc.jpg").
	External         bool
	Poster, Backdrop string
	// ExtraKey is the Extras implementation's key of a title outside the
	// library ("" otherwise).
	ExtraKey string
}

// Describe returns the Info of items (movies and episodes).
func (s *Service) Describe(ctx context.Context, ids []int64) (map[int64]Info, error) {
	out := map[int64]Info{}
	seen := map[int64]bool{}
	var uniq, extras, fetch []int64
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			if isFetchItem(id) {
				fetch = append(fetch, -id-fetchItemBase)
			} else if id < 0 {
				extras = append(extras, -id)
			} else {
				uniq = append(uniq, id)
			}
		}
	}
	if err := s.describeExtras(ctx, extras, out); err != nil {
		return nil, err
	}
	if err := s.describeFetch(ctx, fetch, out); err != nil {
		return nil, err
	}
	for part := range slices.Chunk(uniq, 500) {
		in, args := db.InList(part)
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT i.id, i.kind, i.title, COALESCE(i.season_no,0), COALESCE(i.episode_no,0), i.overview, i.year, `+library.RatingOf("i")+`, i.meta_json,
		  COALESCE(sr.id, 0), COALESCE(sr.title,''), COALESCE(sr.overview,''), COALESCE(sr.year,0), COALESCE(`+library.RatingOf("sr")+`,''), COALESCE(sr.meta_json,'{}'), COALESCE(sr.poster,''), COALESCE(i.poster,'')
		  FROM items i LEFT JOIN items se ON se.id=i.parent_id AND i.kind='episode' LEFT JOIN items sr ON sr.id=se.parent_id
		  WHERE i.id IN `+in, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var x Info
			var meta, srTitle, srOver, srRating, srMeta, srPoster, poster string
			var srID int64
			var srYear int
			if err := rows.Scan(&x.ItemID, &x.Kind, &x.Title, &x.Season, &x.Number, &x.Overview, &x.Year, &x.Rating, &meta,
				&srID, &srTitle, &srOver, &srYear, &srRating, &srMeta, &srPoster, &poster); err != nil {
				rows.Close()
				return nil, err
			}
			x.ArtItem, x.ArtPoster = x.ItemID, poster
			if x.Kind == "episode" && srID != 0 {
				x.Episode, x.Title = x.Title, srTitle
				if x.Overview == "" {
					x.Overview = srOver
				}
				if x.Year == 0 {
					x.Year = srYear
				}
				x.Rating, meta = srRating, srMeta
				x.ArtItem, x.ArtPoster = srID, srPoster
			}
			var m struct {
				Genres []string `json:"genres"`
			}
			_ = json.Unmarshal([]byte(meta), &m)
			x.Genres = m.Genres
			out[x.ItemID] = x
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
