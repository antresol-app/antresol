package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

// Service is the channels feature: the store, schedules kept built, the
// generated channels kept in step with the library and the live encoder.
type Service struct {
	Store     *Store
	Builder   *Builder
	Generator *Generator
	Live      *Live
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
	opts   PlanOptions
	optsAt time.Time

	// Extras finds and plays titles outside the library (nil: none; set
	// with SetExtras).
	Extras Extras

	build sync.Mutex
	kick  chan struct{}
	once  sync.Once

	// streams of titles outside the library resolved ahead (prefetch)
	pmu      sync.Mutex
	prefetch map[entryKey]*prefetched
}

// New wires a service on db; live may be nil (no streaming: tests).
func New(db *sql.DB, live *Live) *Service {
	st := &Store{DB: db}
	cat := Catalog{DB: db}
	s := &Service{Store: st, Builder: &Builder{Store: st, Catalog: cat}, Generator: &Generator{Store: st, Catalog: cat}, Live: live}
	if live != nil && live.Source == nil {
		live.Source = s
	}
	return s
}

func (s *Service) init() { s.once.Do(func() { s.kick = make(chan struct{}, 1) }) }

// Kick asks for a refresh soon (after a library scan).
func (s *Service) Kick() {
	if s == nil {
		return
	}
	s.init()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run refreshes at once, then every interval and on Kick, until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	s.init()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
			log.Printf("channels: refresh: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

// Refresh brings the generated channels in step with the library, cuts
// schedules at programmes whose files went and builds every enabled
// channel to the horizon.
func (s *Service) Refresh(ctx context.Context) error {
	if _, err := s.Generator.Sync(ctx); err != nil {
		return err
	}
	all, err := s.Store.All(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range all {
		if !c.Enabled {
			continue
		}
		if err := s.BuildChannel(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

// BuildChannel cuts c's schedule at a vanished file and extends it.
func (s *Service) BuildChannel(ctx context.Context, c Channel) error {
	s.build.Lock()
	defer s.build.Unlock()
	if _, err := s.Store.CutMissing(ctx, c.ID); err != nil {
		return err
	}
	return s.Builder.Build(ctx, c)
}

// Program is the Source of Live: the schedule entry at t and its file.
// A channel without a schedule there is built first; a programme whose
// file went is cut and the schedule rebuilt.
func (s *Service) Program(ctx context.Context, ch int64, t time.Time) (Program, error) {
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
		var path string
		var probe sql.NullString
		err = s.Store.DB.QueryRowContext(ctx, `SELECT path, probe_json FROM files WHERE id=? AND missing_since IS NULL`, e.FileID).Scan(&path, &probe)
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
		if probe.Valid && probe.String != "" {
			if err := json.Unmarshal([]byte(probe.String), &p.Probe); err != nil {
				log.Printf("channels: probe of file %d: %v", e.FileID, err)
			}
		} else if s.Probe != nil {
			if pr, err := s.Probe(ctx, path); err == nil {
				p.Probe = pr
			}
		}
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
}

// Describe returns the Info of items (movies and episodes).
func (s *Service) Describe(ctx context.Context, ids []int64) (map[int64]Info, error) {
	out := map[int64]Info{}
	seen := map[int64]bool{}
	var uniq, extras []int64
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			if id < 0 {
				extras = append(extras, -id)
			} else {
				uniq = append(uniq, id)
			}
		}
	}
	if err := s.describeExtras(ctx, extras, out); err != nil {
		return nil, err
	}
	for part := range chunks(uniq, 500) {
		in, args := inArgs(part)
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT i.id, i.kind, i.title, COALESCE(i.season_no,0), COALESCE(i.episode_no,0), i.overview, i.year, i.rating, i.meta_json,
		  COALESCE(sr.id, 0), COALESCE(sr.title,''), COALESCE(sr.overview,''), COALESCE(sr.year,0), COALESCE(sr.rating,''), COALESCE(sr.meta_json,'{}'), COALESCE(sr.poster,''), COALESCE(i.poster,'')
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
