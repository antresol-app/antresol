package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// DefaultLangs are the subtitle languages wanted unless set otherwise.
var DefaultLangs = []string{"ru", "en"}

const (
	defaultMaxPerRun = 10
	retryAfter       = 7 * 24 * time.Hour
	pageSize         = 200
)

// Attempt results (subtitle_attempts.result).
const (
	ResultFound = "found"
	ResultNone  = "none"
	ResultError = "error"
	ResultQuota = "quota"
)

// Auto finds subtitles a video lacks (a library.Enricher in the slow
// worker): for each probed movie or episode, newest first, every wanted
// language that is neither in an embedded or external subtitle nor the
// language of the first/default audio track is searched on OpenSubtitles
// and saved next to the video (WriteSidecar). A (file, language) is tried
// at most once in 7 days; when the daily download quota is used up nothing
// is requested until the next midnight UTC. OpenSubtitles being
// unreachable is not an error: the next run tries again.
type Auto struct {
	Client   *Client
	Repo     *library.Repo
	DB       *sql.DB
	Settings func(ctx context.Context) (langs []string, enabled bool) // nil: DefaultLangs, on
	DataDir  string
	// MaxPerRun caps the search requests of one run (10 when 0).
	MaxPerRun int
	Now       func() time.Time
	Log       *slog.Logger

	mu          sync.Mutex
	quotaLoaded bool
}

func (a *Auto) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Auto) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

// nextMidnight is when OpenSubtitles' daily quota counts again (UTC).
func nextMidnight(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour) }

// QuotaUntil is the time downloads are paused until (zero when not); the
// pause lives in Client, so manual downloads from the API share it.
func (a *Auto) QuotaUntil() time.Time { return a.Client.QuotaUntil() }

func (a *Auto) setQuota(now time.Time) { a.Client.PauseUntil(a.Client.QuotaReset(now)) }

// quotaActive also remembers a quota hit of today from before a restart.
func (a *Auto) quotaActive(ctx context.Context, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.quotaLoaded {
		a.quotaLoaded = true
		var last sql.NullString
		if err := a.DB.QueryRowContext(ctx, `SELECT MAX(tried_at) FROM subtitle_attempts WHERE result=?`, ResultQuota).Scan(&last); err == nil && last.Valid {
			if t, err := time.Parse(time.RFC3339, last.String); err == nil {
				a.Client.PauseUntil(nextMidnight(t))
			}
		}
	}
	return now.Before(a.Client.QuotaUntil())
}

// EnrichPending runs one pass (library.Enricher).
func (a *Auto) EnrichPending(ctx context.Context) error {
	if a.Client == nil || a.Repo == nil || a.DB == nil {
		return nil
	}
	langs, enabled := DefaultLangs, true
	if a.Settings != nil {
		langs, enabled = a.Settings(ctx)
	}
	if !enabled || len(langs) == 0 {
		return nil
	}
	now := a.now()
	if a.quotaActive(ctx, now) {
		return nil
	}
	budget := a.MaxPerRun
	if budget <= 0 {
		budget = defaultMaxPerRun
	}
	var before int64
	for budget > 0 {
		files, err := a.Repo.VideoFiles(ctx, before, pageSize)
		if err != nil || len(files) == 0 {
			return err
		}
		for _, f := range files {
			before = f.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			need, err := a.missing(ctx, f, langs, now)
			if err != nil {
				return err
			}
			if len(need) == 0 {
				continue
			}
			searched, stop, err := a.process(ctx, f, need, now)
			if err != nil || stop {
				return err
			}
			if searched {
				if budget--; budget == 0 {
					break
				}
			}
		}
	}
	return nil
}

// process searches f's missing languages; searched: a search request was
// sent; stop: the run ends (offline, quota, refused login).
func (a *Auto) process(ctx context.Context, f library.File, need []string, now time.Time) (searched, stop bool, err error) {
	q, part, total, err := QueryFor(ctx, a.Repo, f, need)
	if err != nil {
		return false, false, err
	}
	if !q.Identifies() { // a search by language alone returns anyone's subtitles
		return false, false, a.record(ctx, f.ID, need, ResultNone, now)
	}
	subs, err := a.Client.Search(ctx, q)
	switch {
	case ctx.Err() != nil:
		return true, true, ctx.Err()
	case errors.Is(err, ErrOffline):
		a.log().Info("subtitles: OpenSubtitles unavailable, trying later", "err", err)
		return true, true, nil
	case errors.Is(err, ErrAuth):
		a.authWarn(err)
		return true, true, nil
	case err != nil:
		a.log().Warn("subtitles: search", "file", f.Path, "err", err)
		return true, false, a.record(ctx, f.ID, need, ResultError, now)
	}
	for _, lang := range need {
		id, ok := Pick(subs, lang, part, total)
		if !ok {
			if err := a.record(ctx, f.ID, []string{lang}, ResultNone, now); err != nil {
				return true, false, err
			}
			continue
		}
		data, err := a.Client.Download(ctx, id)
		quota := errors.Is(err, ErrQuota)
		switch {
		case ctx.Err() != nil:
			return true, true, ctx.Err()
		case errors.Is(err, ErrOffline):
			a.log().Info("subtitles: OpenSubtitles unavailable, trying later", "err", err)
			return true, true, nil
		case errors.Is(err, ErrAuth):
			a.authWarn(err)
			return true, true, nil
		case quota && len(data) == 0:
			a.setQuota(now)
			a.log().Info("subtitles: OpenSubtitles download quota used up until midnight UTC")
			return true, true, a.record(ctx, f.ID, []string{lang}, ResultQuota, now)
		case err != nil && !quota:
			a.log().Warn("subtitles: download", "file", f.Path, "lang", lang, "err", err)
			if err := a.record(ctx, f.ID, []string{lang}, ResultError, now); err != nil {
				return true, false, err
			}
			continue
		}
		res := ResultFound
		if p, werr := WriteSidecar(f.Path, a.DataDir, f.ID, lang, data); werr != nil {
			a.log().Warn("subtitles: save", "file", f.Path, "lang", lang, "err", werr)
			res = ResultError
		} else {
			a.log().Info("subtitles: saved", "path", p)
		}
		if err := a.record(ctx, f.ID, []string{lang}, res, now); err != nil {
			return true, false, err
		}
		if quota { // that was the last download of the day
			a.setQuota(now)
			return true, true, nil
		}
	}
	return true, false, nil
}

// authWarn: a refused login stops every run (no attempt is recorded, so
// nothing is locked out) until the configuration is fixed.
func (a *Auto) authWarn(err error) {
	a.log().Warn("subtitles: OpenSubtitles refused the login: check VAULT_OPENSUBTITLES_KEY, VAULT_OPENSUBTITLES_USER and VAULT_OPENSUBTITLES_PASSWORD", "err", err)
}

func (a *Auto) record(ctx context.Context, fileID int64, langs []string, result string, now time.Time) error {
	for _, l := range langs {
		if _, err := a.DB.ExecContext(ctx, `INSERT INTO subtitle_attempts(file_id, lang, tried_at, result) VALUES(?,?,?,?)
			ON CONFLICT(file_id, lang) DO UPDATE SET tried_at=excluded.tried_at, result=excluded.result`,
			fileID, l, now.UTC().Format(time.RFC3339), result); err != nil {
			return err
		}
	}
	return nil
}

// missing lists the wanted languages f still needs (sorted, as
// OpenSubtitles wants them).
func (a *Auto) missing(ctx context.Context, f library.File, langs []string, now time.Time) ([]string, error) {
	var p media.ProbeResult
	if json.Unmarshal([]byte(f.ProbeJSON), &p) != nil {
		return nil, nil
	}
	if _, err := os.Stat(f.Path); err != nil {
		return nil, nil // not on disk (an unmounted disk): nothing to hash or save next to
	}
	have := Covered(p, FindExternal(f.Path, a.DataDir, f.ID))
	var need []string
	for _, l := range langs {
		if have[l] {
			continue
		}
		var tried, result string
		err := a.DB.QueryRowContext(ctx, `SELECT tried_at, result FROM subtitle_attempts WHERE file_id=? AND lang=?`, f.ID, l).Scan(&tried, &result)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && result != ResultQuota {
			if t, perr := time.Parse(time.RFC3339, tried); perr == nil && now.Sub(t) < retryAfter {
				continue
			}
		}
		need = append(need, l)
	}
	sort.Strings(need)
	return need, nil
}

// Covered is the set of languages ("ru", "en") a video needs no subtitles
// in: a full (not forced) embedded or external subtitle track, or the
// language of its first or default audio track (a Russian film or a
// Russian dub needs no Russian subtitles).
func Covered(p media.ProbeResult, ext []External) map[string]bool {
	have := map[string]bool{}
	for _, s := range p.Subs {
		if !s.Forced {
			have[NormLang(s.Lang)] = true
		}
	}
	for _, s := range ext {
		if !s.Forced {
			have[NormLang(s.Lang)] = true
		}
	}
	for i, au := range p.Audio {
		if i == 0 || au.Default {
			have[NormLang(au.Lang)] = true
		}
	}
	delete(have, "")
	return have
}

// QueryFor builds the search for f in langs: its OpenSubtitles hash, the
// movie's TMDB id or the episode's series id, season and number. part and
// total say which part f is of a movie split in several files (1 of 1 for
// a whole one).
func QueryFor(ctx context.Context, repo *library.Repo, f library.File, langs []string) (q Query, part, total int, err error) {
	q.Languages = langs
	part, total = 1, 1
	if h, herr := Hash(f.Path); herr == nil {
		q.MovieHash = h
	}
	it, err := repo.Item(ctx, f.ItemID)
	if errors.Is(err, library.ErrNotFound) {
		return q, part, total, nil
	}
	if err != nil {
		return q, part, total, err
	}
	switch it.Kind {
	case "movie":
		q.TMDBID = int(it.TmdbID)
		files, err := repo.FilesOf(ctx, it.ID)
		if err != nil {
			return q, part, total, err
		}
		if len(files) > 1 {
			total = len(files)
			for i, g := range files {
				if g.ID == f.ID {
					part = i + 1
				}
			}
		}
	case "episode":
		q.Season, q.Episode = it.SeasonNo, it.EpisodeNo
		root, err := repo.Root(ctx, it)
		if err != nil && !errors.Is(err, library.ErrNotFound) {
			return q, part, total, err
		}
		q.ParentTMDBID = int(root.TmdbID)
		if q.ParentTMDBID == 0 {
			q.Season, q.Episode = 0, 0 // without the series these say nothing
		}
	}
	return q, part, total, nil
}

// Pick chooses the file to download for lang. For part n of a movie split
// in total files it takes a subtitle made for the same split (file n of
// total) or one matching this file's hash; a subtitle for the whole movie
// would run out of sync after the first part.
func Pick(subs []Subtitle, lang string, part, total int) (int, bool) {
	if total <= 1 {
		s, ok := Best(subs, lang)
		return s.FileID, ok
	}
	var c []Subtitle
	for _, s := range subs {
		if len(s.Files) == total || s.HashMatch && len(s.Files) == 1 {
			c = append(c, s)
		}
	}
	s, ok := Best(c, lang)
	if !ok {
		return 0, false
	}
	return FileFor(s, part, total), true
}

// FileFor is the file of s for part of total (its only file otherwise).
func FileFor(s Subtitle, part, total int) int {
	if total > 1 && len(s.Files) == total && part >= 1 && part <= total {
		return s.Files[part-1]
	}
	return s.FileID
}
