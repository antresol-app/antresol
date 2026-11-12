package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/releaseprefs"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/antresol-app/antresol/internal/voclean"
)

// tracksEntry is a file's planned tracks; full: planned for the encoder
// (external tracks probed), else from their names.
type tracksEntry struct {
	t    Tracks
	at   time.Time
	full bool
}

// How long a file's plan, a channel's track set (when it is not running)
// and the household's options are kept.
const (
	tracksTTL  = 10 * time.Minute
	setTTL     = 5 * time.Minute
	optionsTTL = time.Minute
	maxTracks  = 2048
)

// setEntry is a channel's track set as it would be made now.
type setEntry struct {
	set TrackSet
	at  time.Time
}

// plan lists the tracks of a file's programme. full (the encoder): the
// external tracks as the players get them (ExternalAudio); else (the
// guide) a plan kept from before, or one from the tracks' names.
func (s *Service) plan(ctx context.Context, fileID int64, path string, probe media.ProbeResult, full bool) Tracks {
	now := time.Now()
	s.tmu.Lock()
	e, ok := s.tracks[fileID]
	s.tmu.Unlock()
	if ok && !full && now.Sub(e.at) < tracksTTL {
		return e.t
	}
	var ext []media.ExternalAudio
	if full && s.ExternalAudio != nil {
		ext = s.ExternalAudio(ctx, fileID, path, probe)
	} else {
		ext = s.namedAudio(ctx, fileID, path)
	}
	t := Plan(probe, ext, subtitles.FindExternal(path, s.DataDir, fileID), s.options(ctx))
	s.tmu.Lock()
	if s.tracks == nil || len(s.tracks) >= maxTracks {
		s.tracks = map[int64]tracksEntry{}
	}
	s.tracks[fileID] = tracksEntry{t: t, at: now, full: full}
	s.tmu.Unlock()
	return t
}

// namedAudio are a file's external tracks as their names describe them
// (extaudio.Find, nothing probed) and its cleaned voice-overs, in the
// order the API lists them.
func (s *Service) namedAudio(ctx context.Context, fileID int64, path string) []media.ExternalAudio {
	var out []media.ExternalAudio
	var paths []string
	for _, t := range extaudio.Find(path) {
		out = append(out, media.ExternalAudio{Path: t.Path, Studio: t.Studio, Stream: media.AudioStream{Lang: t.Lang, Title: t.Title}})
		paths = append(paths, t.Path)
	}
	found := len(out)
	cs, err := voclean.Tracks(ctx, s.Store.DB, fileID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("channels: cleaned tracks of file %d: %v", fileID, err)
		}
		return out
	}
	for _, c := range cs {
		from := c.VoIndex
		if c.VoPath != "" {
			k := slices.Index(paths[:found], c.VoPath)
			if k < 0 {
				continue
			}
			from = externalBase + k
		}
		title := c.Title
		if title == "" {
			title = c.Studio
		}
		out = append(out, media.ExternalAudio{Path: c.Path, Studio: c.Studio, Stream: media.AudioStream{Lang: c.Lang, Title: title},
			Kind: media.KindCleaned, CleanedOf: from})
	}
	return out
}

// SetLanguage sets the server's metadata language («Язык метаданных»):
// the language of the renditions' names.
func (s *Service) SetLanguage(lang string) {
	s.tmu.Lock()
	s.lang, s.optsAt = lang, time.Time{}
	s.sets = nil
	s.tmu.Unlock()
}

// TrackSet is the track set a run of channel ch starting at from would
// have (Live's SetSource): the keys of the programmes scheduled from then
// to SetHorizon ahead, each file's tracks from their names (cheap: no
// probing); kept setTTL.
func (s *Service) TrackSet(ctx context.Context, ch int64, from time.Time) TrackSet {
	s.tmu.Lock()
	e, ok := s.sets[ch]
	s.tmu.Unlock()
	if ok && time.Since(e.at) < setTTL {
		return e.set
	}
	es, err := s.Store.Range(ctx, ch, from, from.Add(SetHorizon))
	if err != nil && ctx.Err() == nil {
		log.Printf("channels: track set of channel %d: %v", ch, err)
	}
	var progs []Tracks
	var last int64 = -1
	for _, en := range es {
		if en.ExtraID != 0 || en.FileID == 0 || en.FileID == last {
			continue // a title outside the library (nothing known), a programme in several entries
		}
		last = en.FileID
		t, err := s.FileTracks(ctx, en.FileID)
		if err != nil && ctx.Err() == nil {
			log.Printf("channels: tracks of file %d: %v", en.FileID, err)
		}
		if t != nil {
			progs = append(progs, *t)
		}
	}
	set := BuildSet(progs, s.options(ctx))
	if err == nil && ctx.Err() == nil {
		s.tmu.Lock()
		if s.sets == nil || len(s.sets) >= maxTracks {
			s.sets = map[int64]setEntry{}
		}
		s.sets[ch] = setEntry{set: set, at: time.Now()}
		s.tmu.Unlock()
	}
	return set
}

// ProgrammeTracks are the renditions a file's programme on channel ch
// really has, labelled in lang: within the running run's set, else the
// set as it would be made now. ok false: the file's tracks are unknown.
func (s *Service) ProgrammeTracks(ctx context.Context, ch, fileID int64, lang string) (audio, subs []TrackLabel, ok bool, err error) {
	t, err := s.FileTracks(ctx, fileID)
	if err != nil || t == nil {
		return nil, nil, false, err
	}
	set, running := TrackSet{}, false
	if s.Live != nil {
		set, running = s.Live.RunSet(ch)
	}
	if !running {
		set = s.TrackSet(ctx, ch, time.Now())
	}
	audio, subs = t.Labels(set, lang)
	return audio, subs, true, nil
}

// encoderProbe is p with every audio stream's profile known: a probe
// stored before profiles were kept is read again (kept tracksTTL), so an
// AAC-LC track can be copied instead of encoded.
func (s *Service) encoderProbe(ctx context.Context, fileID int64, path string, p media.ProbeResult) media.ProbeResult {
	if s.Probe == nil || !slices.ContainsFunc(p.Audio, func(a media.AudioStream) bool { return a.Profile == "" && strings.EqualFold(a.Codec, "aac") }) {
		return p
	}
	s.tmu.Lock()
	e, ok := s.probes[fileID]
	s.tmu.Unlock()
	if ok && time.Since(e.at) < tracksTTL {
		return e.p
	}
	fresh, err := s.Probe(ctx, path)
	if err != nil || len(fresh.Audio) != len(p.Audio) {
		if err != nil && ctx.Err() == nil {
			log.Printf("channels: probing file %d again: %v", fileID, err)
		}
		fresh = p
	}
	s.tmu.Lock()
	if s.probes == nil || len(s.probes) >= maxTracks {
		s.probes = map[int64]probeEntry{}
	}
	s.probes[fileID] = probeEntry{p: fresh, at: time.Now()}
	s.tmu.Unlock()
	return fresh
}

type probeEntry struct {
	p  media.ProbeResult
	at time.Time
}

// options are the household's: «Любимые озвучки» and whether a cleaned
// voice-over plays instead of the studio's track — on when an
// administrator's profile has «Выбирать очищенную озвучку» (the setting
// is a profile's; a channel's one encoder serves the whole household).
func (s *Service) options(ctx context.Context) PlanOptions {
	s.tmu.Lock()
	o, at, lang := s.opts, s.optsAt, s.lang
	s.tmu.Unlock()
	if time.Since(at) < optionsTTL {
		return o
	}
	o = PlanOptions{Lang: lang}
	if st, err := (releaseprefs.Store{DB: s.Store.DB}).Studios(ctx); err == nil {
		o.Studios = st
	} else if ctx.Err() == nil {
		log.Printf("channels: favourite studios: %v", err)
	}
	err := s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_flags f JOIN users u ON u.id=f.user_id
		WHERE f.prefer_cleaned=1 AND u.is_admin=1)`).Scan(&o.PreferCleaned)
	if err != nil && ctx.Err() == nil {
		log.Printf("channels: cleaned voice-overs setting: %v", err)
	}
	s.tmu.Lock()
	s.opts, s.optsAt = o, time.Now()
	s.tmu.Unlock()
	return o
}

// FileTracks are the tracks a library file has when it plays on a
// channel (from its stored probe and the tracks beside it; kept a few
// minutes): nil when the file or its probe is not known.
func (s *Service) FileTracks(ctx context.Context, fileID int64) (*Tracks, error) {
	s.tmu.Lock()
	e, ok := s.tracks[fileID]
	s.tmu.Unlock()
	if ok && time.Since(e.at) < tracksTTL {
		return &e.t, nil
	}
	var path string
	var probe sql.NullString
	err := s.Store.DB.QueryRowContext(ctx, `SELECT path, probe_json FROM files WHERE id=? AND missing_since IS NULL`, fileID).Scan(&path, &probe)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !probe.Valid || probe.String == "" {
		return nil, nil
	}
	var p media.ProbeResult
	if err := json.Unmarshal([]byte(probe.String), &p); err != nil {
		return nil, nil
	}
	t := s.plan(ctx, fileID, path, p, false)
	return &t, nil
}
