package playlistimport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

// ErrCatalogOffline: Start without MusicBrainz (VAULT_MUSICBRAINZ_BASEURL
// off): the job would stay matching for ever.
var ErrCatalogOffline = errors.New(CatalogOffline)

// CatalogOffline is the message when the music catalog is off or does not
// answer (acquire.CatalogOffline is the same text).
const CatalogOffline = "Каталог недоступен — проверьте интернет"

// Matcher finds a song's MusicBrainz recording and release group.
type Matcher interface {
	Match(ctx context.Context, t Track) (Decision, error)
}

// Decision is what matching decided for a song: State matched (with the
// recording and release group) or review (with the Russian Reason).
type Decision struct {
	State        string
	Reason       string
	RecordingID  string
	ReleaseGroup string
	Title        string
	Artist       string
	Album        string
	Year         int
	Aliases      []string
}

// matchBatch is how many songs one tick matches at most (MusicBrainz
// allows 1 request/s: about 20 s of work).
const matchBatch = 20

// groupEvery: matched songs are grouped per album after this many
// matches (and when matching ends).
const groupEvery = 50

// maxMatchFails: a song MusicBrainz keeps answering badly (a 5xx other
// than an overload, a broken body) goes to review after this many tries.
const maxMatchFails = 3

// maxBackoff caps the pause after MusicBrainz is offline or overloaded
// (429, 502, 503, 504) many times in a row, as a multiple of MatchPause.
const maxBackoff = 16

// Candidate is an album the catalog offers for a song in review (the
// fields the import's search and candidates endpoints serialize).
type Candidate struct {
	TMDBID        int    `json:"tmdbId"`
	ExtID         string `json:"extId"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	Year          int    `json:"year"`
	Poster        string `json:"poster"`
	Overview      string `json:"overview"`
	InLibrary     bool   `json:"inLibrary"`
	LibraryItemID *int64 `json:"libraryItemId"`
	// Aliases: other names of the album, sent back with the request.
	Aliases []string `json:"aliases,omitempty"`
	// MatchedTrack: music found by a song title — the song of the query
	// the album holds; omitted otherwise.
	MatchedTrack string `json:"matchedTrack,omitempty"`
	// ByArtist: music of the artist the query names, listed first; omitted
	// for the other matches.
	ByArtist string `json:"byArtist,omitempty"`
}

// Catalog is the music catalog search for the review list (in the full
// build acquire.Service.DiscoverMedia, adapted).
type Catalog interface {
	DiscoverMedia(ctx context.Context, kind, q string) ([]Candidate, error)
}

// Downloader fetches the albums of a downloading job that the library
// lacks (*musicimport.Queue); nil in Service.Queue means LibraryStep.
type Downloader interface {
	Step(ctx context.Context, jobID int64) error
}

// DownloadProgress is what a Downloader may also tell: how much of an
// album's download (by infohash) is on disk (known false: it cannot say).
type DownloadProgress interface {
	Downloaded(ctx context.Context, infoHash string) (done int64, known bool, err error)
}

// Stopper stops a library still being read for a job (musicimport.OAuth).
type Stopper interface{ Stop(jobID int64) }

// ServiceInfo is a tile of «Импорт»: how the service connects (redirect,
// paste, device code, a file, the app), the owner's setup steps while it is
// not configured, and the files it can also be read from.
type ServiceInfo struct {
	ID         string   `json:"id"`
	Configured bool     `json:"configured"`
	Connect    string   `json:"connect"`
	Setup      []string `json:"setup"`
	Files      []string `json:"files"`
}

// ConnectorStatus lists the tiles for GET /api/import/services
// (musicimport.OAuth).
type ConnectorStatus interface {
	Services(ctx context.Context) []ServiceInfo
}

// IDResolver names songs a file gives only by the service's id (a
// Takeout's YouTube video ids): the ones it could, by id.
type IDResolver interface {
	Resolve(ctx context.Context, ids []string) (map[string]SnapTrack, error)
}

// PublicPlaylist is a playlist anyone with its link can open.
type PublicPlaylist struct {
	ID   string
	Name string
}

// ErrBadLink: the text is not a link to a playlist of the service.
var ErrBadLink = errors.New("musicimport: not a playlist link")

// PublicReader reads a public playlist by its link, with no sign-in
// (connect.Deezer).
type PublicReader interface {
	// FindPublic is the playlist a link opens (ErrBadLink, ErrNotFound).
	FindPublic(ctx context.Context, link string) (PublicPlaylist, error)
	// ReadPublic reads its songs: one playlist, nothing liked.
	ReadPublic(ctx context.Context, p PublicPlaylist) (Snapshot, error)
}

// Service runs import jobs: one job at a time goes through matching and
// downloading; every phase resumes from the database after a restart.
// Nil phase hooks (Matcher, Rebuild) are skipped; a nil Queue downloads
// nothing: the albums the library has are linked, the rest are not found.
type Service struct {
	Store   *Store
	Bus     *events.Bus
	Log     *slog.Logger
	Disk    func() (disk.Usage, error) // the preview's free space; nil: unknown
	Matcher Matcher
	Catalog Catalog                                      // the review's manual search; nil: none
	Queue   Downloader                                   // nil: LibraryStep over Library
	Library *library.Repo                                // what LibraryStep looks albums up in; nil: it has none
	Rebuild func(ctx context.Context, jobID int64) error // playlists and stars of a job
	// Connectors lists the sign-in services of GET /api/import/services; nil:
	// none.
	Connectors ConnectorStatus
	// Reads stops the library read of a cancelled or deleted job; nil: no-op.
	Reads Stopper
	// Resolver names the videos an uploaded Google Takeout gives only by
	// id (connect.YouTubeResolver); nil: they stay unknown.
	Resolver IDResolver
	// Public reads a public Deezer playlist by its link (no sign-in); nil:
	// none.
	Public PublicReader

	// Tick is the worker's interval (0: 1 s).
	Tick time.Duration
	// MatchPause is how long matching waits after MusicBrainz failed to
	// answer (0: 1 min); the song stays pending.
	MatchPause time.Duration

	mu        sync.Mutex
	published map[int64]time.Time // last import.progress per job
	pausedTo  time.Time           // matching waits until then
	ungrouped int                 // matches since the last AssignGroups
	fails     map[int64]int       // bad answers per song (track id)
	outages   int                 // offline/overloaded answers in a row
}

func (s *Service) stop(id int64) {
	if s.Reads != nil {
		s.Reads.Stop(id)
	}
}

// Logger is the service's logger (Log, else the default).
func (s *Service) Logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Create stores a snapshot as a job in preview.
func (s *Service) Create(ctx context.Context, userID int64, snap Snapshot) (int64, error) {
	id, err := s.Store.CreateJob(ctx, userID, snap)
	if err == nil {
		s.Publish(ctx, id, true)
	}
	return id, err
}

// Start begins matching a previewed job (ErrStarted, ErrBusy).
func (s *Service) Start(ctx context.Context, id int64) error {
	if s.Matcher == nil {
		return ErrCatalogOffline // its songs could never be matched
	}
	if err := s.Store.StartJob(ctx, id); err != nil {
		return err
	}
	s.Publish(ctx, id, true)
	return nil
}

// Cancel stops a job: groups still waiting are dropped; acquisitions
// already started are left to the downloads screen.
func (s *Service) Cancel(ctx context.Context, id int64) error {
	if err := s.Store.CancelJob(ctx, id); err != nil {
		return err
	}
	s.stop(id) // a library still being read is read no further
	s.Publish(ctx, id, true)
	return nil
}

// Advance moves a job from one status to another only if it is still in
// from, and tells the admins when it moved.
func (s *Service) Advance(ctx context.Context, id int64, from, to, errText string) (bool, error) {
	moved, err := s.Store.Advance(ctx, id, from, to, errText)
	if moved {
		s.Publish(ctx, id, true)
	}
	return moved, err
}

// Fill stores what a connector read into a reading job (→ preview).
func (s *Service) Fill(ctx context.Context, id int64, snap Snapshot) error {
	if err := s.Store.FillJob(ctx, id, snap); err != nil {
		return err
	}
	s.Publish(ctx, id, true)
	return nil
}

// Delete removes a job that is not running (ErrBusy).
func (s *Service) Delete(ctx context.Context, id int64) error {
	// the read stops first: a job made meanwhile cannot take this id while
	// the job still exists (a matching or downloading job, which DeleteJob
	// refuses, has no read)
	s.stop(id)
	if err := s.Store.DeleteJob(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.published, id)
	s.mu.Unlock()
	return nil
}

// Resolve settles a song in review with the album the admin chose; a
// done job goes back to downloading.
func (s *Service) Resolve(ctx context.Context, trackID int64, d Decision) error {
	t, err := s.Store.Track(ctx, trackID)
	if err != nil {
		return err
	}
	if err := s.Store.Resolve(ctx, trackID, d); err != nil {
		return err
	}
	s.Publish(ctx, t.JobID, true)
	return nil
}

// Retry is «Повторить» of an album that was not found, failed or waits
// for space; a done job downloads again.
func (s *Service) Retry(ctx context.Context, groupID int64) error {
	return s.retry(ctx, groupID, false)
}

// RetryAnyFormat is «Скачать в другом формате»: Retry whose search takes a
// release of any format.
func (s *Service) RetryAnyFormat(ctx context.Context, groupID int64) error {
	return s.retry(ctx, groupID, true)
}

func (s *Service) retry(ctx context.Context, groupID int64, anyFormat bool) error {
	g, err := s.Store.Group(ctx, groupID)
	if err != nil {
		return err
	}
	retry := s.Store.RetryGroup
	if anyFormat {
		retry = s.Store.RetryGroupAnyFormat
	}
	if err := retry(ctx, groupID); err != nil {
		return err
	}
	s.Publish(ctx, g.JobID, true)
	return nil
}

// Skip leaves a song in review out of the import.
func (s *Service) Skip(ctx context.Context, trackID int64) error {
	t, err := s.Store.Track(ctx, trackID)
	if err != nil {
		return err
	}
	if err := s.Store.Skip(ctx, trackID); err != nil {
		return err
	}
	s.Publish(ctx, t.JobID, true)
	return nil
}

// SetStatus changes a job's status and tells the admins (a cancelled job
// stays cancelled).
func (s *Service) SetStatus(ctx context.Context, id int64, status, errText string) error {
	if err := s.Store.SetStatus(ctx, id, status, errText); err != nil {
		return err
	}
	s.Publish(ctx, id, true)
	return nil
}

// Run works on the active job every tick until ctx ends. A release search
// a stop interrupted starts again (the attempt is not counted).
func (s *Service) Run(ctx context.Context) {
	if err := s.Store.ResetSearching(ctx); err != nil && ctx.Err() == nil {
		s.Logger().Error("import: reset searching groups", "err", err)
	}
	tick := s.Tick
	if tick <= 0 {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// step advances the active job by one phase step.
func (s *Service) step(ctx context.Context) {
	j, ok, err := s.Store.ActiveJob(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.Logger().Error("import: active job", "err", err)
		}
		return
	}
	if !ok {
		return
	}
	switch j.Status {
	case StatusMatching:
		s.matchStep(ctx, j.ID)
		if n, err := s.Store.Pending(ctx, j.ID); err != nil {
			s.logErr(ctx, "pending songs", j.ID, err)
		} else if n == 0 && s.group(ctx, j.ID, true) {
			s.advance(ctx, j.ID, StatusMatching, StatusDownloading)
		}
	case StatusDownloading:
		if err := s.downloader().Step(ctx, j.ID); err != nil {
			s.logErr(ctx, "queue", j.ID, err)
		}
		s.rematchStep(ctx, j.ID)
		if busy, err := s.Store.Unfinished(ctx, j.ID); err != nil {
			s.logErr(ctx, "unfinished", j.ID, err)
		} else if !busy {
			if s.Rebuild != nil {
				if err := s.Rebuild(ctx, j.ID); err != nil {
					s.logErr(ctx, "rebuild", j.ID, err)
				}
			}
			// a cancel during the rebuild wins
			s.advance(ctx, j.ID, StatusDownloading, StatusDone)
		}
	}
	s.Publish(ctx, j.ID, false)
}

// downloader is the download phase: Queue, else the library alone.
func (s *Service) downloader() Downloader {
	if s.Queue != nil {
		return s.Queue
	}
	return LibraryStep(s.Store, s.Library)
}

// matchStep matches the next batch of pending songs; it stops when the
// job leaves matching (cancelled) or at the first error: the songs stay
// pending and matching pauses (MatchPause) when MusicBrainz did not
// answer.
func (s *Service) matchStep(ctx context.Context, jobID int64) {
	if s.Matcher == nil || s.paused() {
		return
	}
	defer s.group(ctx, jobID, false)
	tracks, err := s.Store.NextPending(ctx, jobID, matchBatch)
	if err != nil {
		s.logErr(ctx, "next pending", jobID, err)
		return
	}
	for _, t := range tracks {
		if ctx.Err() != nil || !s.still(ctx, jobID, StatusMatching) {
			return
		}
		d, err := s.Matcher.Match(ctx, t)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if transient(err) {
				p := s.pause()
				s.Logger().Warn("import: musicbrainz offline or overloaded, matching paused", "job", jobID, "for", p, "err", err)
				return
			}
			if !s.failed(t.ID) {
				s.Logger().Warn("import: musicbrainz answered badly, matching paused", "job", jobID, "err", err)
				s.pauseOnce()
				return
			}
			s.Logger().Warn("import: musicbrainz keeps failing for a song, sent to review", "job", jobID, "track", t.ID, "err", err)
			d = Decision{State: TrackReview, Reason: ReasonNoExact}
		}
		if err := s.Store.SetDecision(ctx, t.ID, d); err != nil {
			s.logErr(ctx, "save match", jobID, err)
			return
		}
		s.mu.Lock()
		delete(s.fails, t.ID)
		s.outages = 0
		if d.State == TrackMatched {
			s.ungrouped++
		}
		s.mu.Unlock()
		s.Publish(ctx, jobID, false)
	}
}

// rematchBatch is how many songs one tick of a downloading job matches
// again (a few requests each, at 1 request/s: the queue is not held up
// for long).
const rematchBatch = 2

// rematchStep matches again the songs in review that older matching
// rules decided (Store.MatcherVersion), a few per tick: a song now matched
// joins its album, which the queue fetches; the others stay in review. A
// song the admin skipped is never asked again (only the admin's Skip
// makes one). MusicBrainz offline pauses it as it pauses matching.
func (s *Service) rematchStep(ctx context.Context, jobID int64) {
	if s.Matcher == nil || s.paused() {
		return
	}
	tracks, err := s.Store.NextRematch(ctx, jobID, rematchBatch)
	if err != nil {
		s.logErr(ctx, "next rematch", jobID, err)
		return
	}
	for _, t := range tracks {
		if ctx.Err() != nil || !s.still(ctx, jobID, StatusDownloading) {
			return
		}
		d, err := s.Matcher.Match(ctx, t)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if transient(err) {
				p := s.pause()
				s.Logger().Warn("import: musicbrainz offline or overloaded, re-matching paused", "job", jobID, "for", p, "err", err)
				return
			}
			if !s.failed(t.ID) {
				s.pauseOnce()
				return
			}
			d = Decision{State: t.State, Reason: t.Reason} // as it was, not asked again
		}
		matched, err := s.Store.SetRematch(ctx, t.ID, d)
		if err != nil {
			s.logErr(ctx, "save rematch", jobID, err)
			return
		}
		s.mu.Lock()
		delete(s.fails, t.ID)
		s.outages = 0
		s.mu.Unlock()
		if matched {
			s.Logger().Info("import: matched on a second look", "job", jobID, "track", t.ID, "artist", t.Artist, "title", t.Title,
				"album", d.Album)
			s.Publish(ctx, jobID, true)
		}
	}
}

// transient: MusicBrainz is offline or overloaded — every song would
// fail the same way, so it never counts against the song.
func transient(err error) bool {
	var he *metadata.HTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	return metadata.Classify(err) == metadata.Offline
}

// failed counts a bad answer for a song; true when it is the last try.
func (s *Service) failed(trackID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails == nil {
		s.fails = map[int64]int{}
	}
	s.fails[trackID]++
	if s.fails[trackID] < maxMatchFails {
		return false
	}
	delete(s.fails, trackID)
	return true
}

// MatchPausedUntil is when matching resumes after a MusicBrainz outage
// (zero: not paused).
func (s *Service) MatchPausedUntil() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !time.Now().Before(s.pausedTo) {
		return time.Time{}
	}
	return s.pausedTo
}

func (s *Service) paused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.pausedTo)
}

// pause stops matching after an outage answer, longer with every one in
// a row (backoff); it returns the pause.
func (s *Service) pause() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outages++
	d := s.backoff(s.outages)
	s.pausedTo = time.Now().Add(d)
	return d
}

// pauseOnce stops matching for MatchPause (a bad answer for one song).
func (s *Service) pauseOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pausedTo = time.Now().Add(s.backoff(1))
}

// backoff is the pause after n outage answers in a row: MatchPause (0:
// 1 min), doubled each time, at most maxBackoff × MatchPause.
func (s *Service) backoff(n int) time.Duration {
	base := s.MatchPause
	if base <= 0 {
		base = time.Minute
	}
	m := 1
	for i := 1; i < n && m < maxBackoff; i++ {
		m *= 2
	}
	return time.Duration(min(m, maxBackoff)) * base
}

// group gives matched songs their album groups: every groupEvery matches,
// or always when all (matching is over). ok false: it failed.
func (s *Service) group(ctx context.Context, jobID int64, all bool) bool {
	s.mu.Lock()
	n := s.ungrouped
	s.mu.Unlock()
	if !all && n < groupEvery {
		return true
	}
	if err := s.Store.AssignGroups(ctx, jobID); err != nil {
		s.logErr(ctx, "group songs", jobID, err)
		return false
	}
	s.mu.Lock()
	s.ungrouped = max(s.ungrouped-n, 0)
	s.mu.Unlock()
	return true
}

// still reports whether the job is still in status (a cancel may have
// come in between).
func (s *Service) still(ctx context.Context, jobID int64, status string) bool {
	j, err := s.Store.Job(ctx, jobID)
	return err == nil && j.Status == status
}

// advance is the worker's compare-and-set move (a cancel always wins).
func (s *Service) advance(ctx context.Context, jobID int64, from, to string) {
	if _, err := s.Advance(ctx, jobID, from, to, ""); err != nil {
		s.logErr(ctx, "status "+to, jobID, err)
	}
}

func (s *Service) logErr(ctx context.Context, what string, jobID int64, err error) {
	if ctx.Err() != nil {
		return
	}
	s.Logger().Error("import: "+what, "job", jobID, "err", err)
}
