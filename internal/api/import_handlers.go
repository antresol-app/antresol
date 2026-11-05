package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/playlistimport/files"
	"github.com/go-chi/chi/v5"
)

// maxSnapshotBody bounds POST /api/import/jobs.
const maxSnapshotBody = 32 << 20

// songBytes is the preview's size estimate of one song.
const songBytes = 30 << 20

// mountImport: «Импорт медиатеки» (admins only), in both editions: the
// jobs, the upload of files, the review and the playlists' pending songs.
// Another build adds its own (import_handlers_full.go).
func mountImport(r chi.Router, deps Deps) {
	h := &importAPI{svc: deps.PlaylistImport, full: newPlaylistFull(deps)}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Post("/api/import/jobs", h.create)
		r.Post("/api/import/files", h.files)
		r.Get("/api/import/services", h.services)
		r.Get("/api/import/jobs", h.list)
		r.Get("/api/import/jobs/{id}", h.get)
		r.Patch("/api/import/jobs/{id}", h.patch)
		r.Post("/api/import/jobs/{id}/start", h.start)
		r.Post("/api/import/jobs/{id}/cancel", h.cancel)
		r.Delete("/api/import/jobs/{id}", h.remove)
		r.Get("/api/import/jobs/{id}/review", h.review)
		r.Get("/api/import/tracks/{id}/candidates", h.candidates)
		r.Post("/api/import/tracks/{id}/resolve", h.resolve)
		r.Get("/api/import/search", h.search)
		r.Get("/api/import/jobs/{id}/groups", h.groups)
		h.mountFull(r)
	})
	// a playlist page's «Ещё не скачано» rows: any profile, its own playlists
	r.Get("/api/import/playlists/{playlistId}/pending", h.pending)
}

type importAPI struct {
	svc  *playlistimport.Service
	full playlistFull
}

// backgroundReader makes a reading job whose snapshot is read in the
// background (musicimport.OAuth in the sources build; the core build has
// none).
type backgroundReader interface {
	ReadInBackground(ctx context.Context, userID int64, service string, read func(ctx context.Context, jobID int64) (playlistimport.Snapshot, error)) (int64, error)
}

func (h *importAPI) err(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case r.Context().Err() != nil:
		return
	case errors.Is(err, playlistimport.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Не найдено")
	case errors.Is(err, playlistimport.ErrNotReview), errors.Is(err, playlistimport.ErrNotRetryable):
		writeErr(w, r, http.StatusConflict, "Не найдено")
	case errors.Is(err, playlistimport.ErrBusy):
		writeErr(w, r, http.StatusConflict, "Импорт уже идёт")
	case errors.Is(err, playlistimport.ErrStarted):
		writeErr(w, r, http.StatusConflict, "Импорт уже начат")
	case errors.Is(err, playlistimport.ErrCatalogOffline):
		writeErr(w, r, http.StatusServiceUnavailable, playlistimport.CatalogOffline)
	case errors.Is(err, playlistimport.ErrUnknownService):
		writeErr(w, r, http.StatusBadRequest, "Неизвестный сервис")
	case errors.Is(err, playlistimport.ErrTooLarge):
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Файл слишком большой")
	case h.full.err(w, r, err):
	default:
		internalErr(w, r, what, err)
	}
}

type importDisk struct {
	Free  int64 `json:"free"`
	Total int64 `json:"total"`
}

type importPlaylistDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Songs    int    `json:"songs"`
	Readable bool   `json:"readable"`
	Selected bool   `json:"selected"`
}

// JobDTO is an import job as the «Импорт» page shows it.
type JobDTO struct {
	ID             int64                   `json:"id"`
	Service        string                  `json:"service"`
	Status         string                  `json:"status"`
	CreatedAt      time.Time               `json:"createdAt"`
	Counts         playlistimport.Counts   `json:"counts"`
	EstimatedBytes int64                   `json:"estimatedBytes"`
	Disk           importDisk              `json:"disk"`
	Favorites      bool                    `json:"favorites"`
	Playlists      []importPlaylistDTO     `json:"playlists"`
	Progress       playlistimport.Progress `json:"progress"`
	Error          string                  `json:"error"`
	Report         playlistimport.Report   `json:"report"`
	// MatchPausedUntil: matching waits for MusicBrainz until then (a
	// matching job only; absent when not paused).
	MatchPausedUntil *time.Time `json:"matchPausedUntil,omitempty"`
}

func (h *importAPI) dto(r *http.Request, j playlistimport.Job) (JobDTO, error) {
	ctx := r.Context()
	st := h.svc.Store
	out := JobDTO{ID: j.ID, Service: j.Service, Status: j.Status, CreatedAt: j.CreatedAt, Favorites: j.Favorites,
		Error: i18n.Localize(ctx, j.Error), Report: j.Report, Playlists: []importPlaylistDTO{}}
	var err error
	if out.Counts, err = st.Counts(ctx, j.ID); err != nil {
		return out, err
	}
	out.EstimatedBytes = int64(out.Counts.Songs) * songBytes
	if out.Progress, err = st.Progress(ctx, j.ID); err != nil {
		return out, err
	}
	pls, err := st.Playlists(ctx, j.ID)
	if err != nil {
		return out, err
	}
	for _, p := range pls {
		out.Playlists = append(out.Playlists, importPlaylistDTO{ID: p.ID, Name: p.Name, Songs: p.Songs, Readable: p.Readable, Selected: p.Selected})
	}
	if j.Status == playlistimport.StatusMatching {
		if until := h.svc.MatchPausedUntil(); !until.IsZero() {
			out.MatchPausedUntil = &until
		}
	}
	if h.svc.Disk != nil {
		switch d, err := h.svc.Disk(); {
		case err != nil:
			log.Printf("api: import disk: %v", err) // shown as unknown (zeros)
		case d.Offline == "":
			out.Disk = importDisk{Free: max(d.Total-d.Used, 0), Total: d.Total}
		}
	}
	return out, nil
}

// job loads the job of the URL; another profile's job is «Не найдено».
func (h *importAPI) job(w http.ResponseWriter, r *http.Request) (playlistimport.Job, bool) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return playlistimport.Job{}, false
	}
	j, err := h.svc.Store.Job(r.Context(), id)
	if err == nil && j.UserID != userOf(r).ID {
		err = playlistimport.ErrNotFound
	}
	if err != nil {
		h.err(w, r, "import job", err)
		return j, false
	}
	return j, true
}

func (h *importAPI) writeJob(w http.ResponseWriter, r *http.Request, code int, id int64) {
	j, err := h.svc.Store.Job(r.Context(), id)
	if err != nil {
		h.err(w, r, "import job", err)
		return
	}
	out, err := h.dto(r, j)
	if err != nil {
		h.err(w, r, "import job view", err)
		return
	}
	writeJSON(w, code, out)
}

func (h *importAPI) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSnapshotBody)
	var snap playlistimport.Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, r, http.StatusRequestEntityTooLarge, "Файл слишком большой")
			return
		}
		writeErr(w, r, http.StatusBadRequest, "Не удалось разобрать запрос")
		return
	}
	id, err := h.svc.Create(r.Context(), userOf(r).ID, snap)
	if err != nil {
		h.err(w, r, "import create", err)
		return
	}
	h.writeJob(w, r, http.StatusCreated, id)
}

// maxUploadFiles bounds the files of one upload.
const maxUploadFiles = 200

// POST /api/import/files: the «Файл» tile's upload (multipart, one or more
// «file» fields, files.MaxUpload in all) → a preview job. A Library.xml is
// an Apple Music job, a Takeout a YouTube one, anything else (or a mix) a
// file job.
func (h *importAPI) files(w http.ResponseWriter, r *http.Request) {
	tooBig := func() { writeErr(w, r, http.StatusRequestEntityTooLarge, "Файл слишком большой") }
	unreadable := func() { writeErr(w, r, http.StatusBadRequest, files.ErrUnreadable.Error()) }
	// the files, plus room for the multipart framing
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxUpload+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		unreadable()
		return
	}
	// the whole upload is read (and bounded) before any file is parsed
	type upload struct {
		name string
		data []byte
	}
	var ups []upload
	var total int64
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			tooBig()
			return
		case err != nil:
			unreadable()
			return
		}
		if part.FormName() != "file" {
			continue // the next part skips it
		}
		if len(ups) >= maxUploadFiles {
			unreadable()
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, files.MaxUpload-total+1))
		switch {
		case errors.As(err, &mbe):
			tooBig()
			return
		case err != nil:
			unreadable()
			return
		}
		if total += int64(len(data)); total > files.MaxUpload {
			tooBig()
			return
		}
		ups = append(ups, upload{part.FileName(), data})
	}
	// merged file by file: the song limit counts unique songs
	merged := files.NewMerger()
	for i, u := range ups {
		snap, err := files.Parse(u.name, u.data)
		ups[i].data = nil // parsed: let it go
		if err == nil {
			err = merged.Add(snap)
		}
		switch {
		case errors.Is(err, playlistimport.ErrTooLarge):
			tooBig()
			return
		case errors.Is(err, files.ErrNoColumns):
			writeErr(w, r, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			unreadable()
			return
		}
	}
	if len(ups) == 0 {
		unreadable()
		return
	}
	snap := merged.Snapshot()
	unresolved := files.Unresolved(snap)
	if unresolved > 0 && h.svc.Resolver != nil && h.full.reader() != nil {
		h.resolveFiles(w, r, snap)
		return
	}
	id, err := h.svc.Create(r.Context(), userOf(r).ID, snap)
	if err != nil {
		h.err(w, r, "import files", err)
		return
	}
	if unresolved > 0 {
		// the job exists: a report that failed to save is not worth a 500
		if err := h.svc.Store.SetReport(r.Context(), id, func(rep *playlistimport.Report) { rep.Unresolved = unresolved }); err != nil {
			log.Printf("api: import files report: %v", err)
		}
	}
	h.writeJob(w, r, http.StatusCreated, id)
}

// resolveBudget bounds naming an upload's videos, within a read's 30 min.
var resolveBudget = 20 * time.Minute

// resolveFiles makes the job of an upload with videos known only by id (a
// Google Takeout) in the background: a reading job while YouTube names
// them (oEmbed, 2 a second), then the preview with the ones still unknown
// counted in the report.
func (h *importAPI) resolveFiles(w http.ResponseWriter, r *http.Request, snap playlistimport.Snapshot) {
	if !h.noOpenJob(w, r, snap.Service) {
		return
	}
	resolver := h.svc.Resolver
	id, err := h.full.reader().ReadInBackground(r.Context(), userOf(r).ID, snap.Service, func(ctx context.Context, jobID int64) (playlistimport.Snapshot, error) {
		// naming has its own budget: when it runs out, the songs named so
		// far and the whole rest of the upload are kept
		rctx, cancel := context.WithTimeout(ctx, resolveBudget)
		named, err := files.Resolve(rctx, snap, resolver)
		cancel()
		if ctx.Err() != nil {
			return playlistimport.Snapshot{}, ctx.Err() // shutdown, or the job cancelled or deleted
		}
		if err != nil {
			log.Printf("api: import files: naming videos: %v", err) // what was named is kept
		}
		if n := files.Unresolved(named); n > 0 {
			if err := h.svc.Store.SetReport(ctx, jobID, func(rep *playlistimport.Report) { rep.Unresolved = n }); err != nil {
				log.Printf("api: import files report: %v", err)
			}
		}
		return named, nil
	})
	if err != nil {
		h.err(w, r, "import files", err)
		return
	}
	h.writeJob(w, r, http.StatusCreated, id)
}

// GET /api/import/services: the tiles, in the order the page shows them
// ([] without connectors).
func (h *importAPI) services(w http.ResponseWriter, r *http.Request) {
	out := []playlistimport.ServiceInfo{}
	if h.svc.Connectors != nil {
		out = h.svc.Connectors.Services(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": out})
}

func (h *importAPI) list(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.Store.Jobs(r.Context(), userOf(r).ID)
	if err != nil {
		h.err(w, r, "import jobs", err)
		return
	}
	out := make([]JobDTO, 0, len(jobs))
	for _, j := range jobs {
		d, err := h.dto(r, j)
		if err != nil {
			h.err(w, r, "import job view", err)
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (h *importAPI) get(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	out, err := h.dto(r, j)
	if err != nil {
		h.err(w, r, "import job view", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *importAPI) patch(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	var req struct {
		Favorites *bool           `json:"favorites"`
		Playlists map[string]bool `json:"playlists"`
	}
	if !decode(w, r, &req) {
		return
	}
	pls := make(map[int64]bool, len(req.Playlists))
	for k, on := range req.Playlists {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, r, http.StatusBadRequest, "Неверный id")
			return
		}
		pls[id] = on
	}
	if err := h.svc.Store.SetOptions(r.Context(), j.ID, req.Favorites, pls); err != nil {
		h.err(w, r, "import options", err)
		return
	}
	h.writeJob(w, r, http.StatusOK, j.ID)
}

func (h *importAPI) start(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	if err := h.svc.Start(r.Context(), j.ID); err != nil {
		h.err(w, r, "import start", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *importAPI) cancel(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	if err := h.svc.Cancel(r.Context(), j.ID); err != nil {
		h.err(w, r, "import cancel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *importAPI) remove(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), j.ID); err != nil {
		h.err(w, r, "import delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// track loads the song of the URL; another profile's song is «Не найдено».
func (h *importAPI) track(w http.ResponseWriter, r *http.Request) (playlistimport.Track, bool) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return playlistimport.Track{}, false
	}
	t, err := h.svc.Store.Track(r.Context(), id)
	if err == nil {
		var j playlistimport.Job
		if j, err = h.svc.Store.Job(r.Context(), t.JobID); err == nil && j.UserID != userOf(r).ID {
			err = playlistimport.ErrNotFound
		}
	}
	if err != nil {
		h.err(w, r, "import track", err)
		return t, false
	}
	return t, true
}

type reviewTrackDTO struct {
	ID         int64                      `json:"id"`
	Artist     string                     `json:"artist"`
	Title      string                     `json:"title"`
	Album      string                     `json:"album"`
	Reason     string                     `json:"reason"`
	Candidates []playlistimport.Candidate `json:"candidates"`
}

// GET /api/import/jobs/{id}/review: the songs waiting for a decision.
// Their candidates are fetched one song at a time (…/candidates):
// MusicBrainz allows 1 request/s.
func (h *importAPI) review(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	ts, err := h.svc.Store.ReviewTracks(r.Context(), j.ID)
	if err != nil {
		h.err(w, r, "import review", err)
		return
	}
	out := make([]reviewTrackDTO, 0, len(ts))
	for _, t := range ts {
		out = append(out, reviewTrackDTO{ID: t.ID, Artist: t.Artist, Title: t.Title, Album: t.Album, Reason: i18n.Localize(r.Context(), t.Reason),
			Candidates: []playlistimport.Candidate{}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": out})
}

// discover is the music catalog search (with matchedTrack).
func (h *importAPI) discover(w http.ResponseWriter, r *http.Request, q string) {
	if h.svc.Catalog == nil {
		writeErr(w, r, http.StatusServiceUnavailable, playlistimport.CatalogOffline)
		return
	}
	cs, err := h.svc.Catalog.DiscoverMedia(r.Context(), library.KindMusic, q)
	if err != nil {
		h.full.catalogErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": cs})
}

// GET /api/import/tracks/{id}/candidates: albums for «<artist> <title>».
func (h *importAPI) candidates(w http.ResponseWriter, r *http.Request) {
	t, ok := h.track(w, r)
	if !ok {
		return
	}
	h.discover(w, r, t.Artist+" "+t.Title)
}

// GET /api/import/search?q=: «Найти вручную».
func (h *importAPI) search(w http.ResponseWriter, r *http.Request) {
	h.discover(w, r, r.URL.Query().Get("q"))
}

// mbidRe is the shape of a MusicBrainz id (a release group's extId).
var mbidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// POST /api/import/tracks/{id}/resolve {"extId","title","artist","year",
// "aliases"} (the album chosen) or {"skip":true}.
func (h *importAPI) resolve(w http.ResponseWriter, r *http.Request) {
	t, ok := h.track(w, r)
	if !ok {
		return
	}
	var req struct {
		ExtID   string   `json:"extId"`
		Title   string   `json:"title"`
		Artist  string   `json:"artist"`
		Year    int      `json:"year"`
		Aliases []string `json:"aliases"`
		Skip    bool     `json:"skip"`
	}
	if !decode(w, r, &req) {
		return
	}
	var err error
	switch {
	case req.Skip:
		err = h.svc.Skip(r.Context(), t.ID)
	case !mbidRe.MatchString(strings.TrimSpace(req.ExtID)):
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return
	default:
		err = h.svc.Resolve(r.Context(), t.ID, playlistimport.Decision{ReleaseGroup: strings.TrimSpace(req.ExtID),
			Title: t.Title, Artist: req.Artist, Album: req.Title, Year: req.Year, Aliases: req.Aliases})
	}
	if err != nil {
		h.err(w, r, "import resolve", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type importGroupDTO struct {
	ID       int64  `json:"id"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Year     int    `json:"year"`
	Songs    int    `json:"songs"`
	State    string `json:"state"`
	Bytes    int64  `json:"bytes"`
	Done     int64  `json:"done"` // bytes downloaded so far
	Error    string `json:"error"`
	InfoHash string `json:"infoHash"`
	// WaitingFor: the download a waiting_download album waits for (as
	// «Загрузки» names it).
	WaitingFor string `json:"waitingFor,omitempty"`
}

// GET /api/import/jobs/{id}/groups: the job's albums and their downloads.
func (h *importAPI) groups(w http.ResponseWriter, r *http.Request) {
	j, ok := h.job(w, r)
	if !ok {
		return
	}
	gs, err := h.svc.Store.Groups(r.Context(), j.ID)
	if err != nil {
		h.err(w, r, "import groups", err)
		return
	}
	out := make([]importGroupDTO, 0, len(gs))
	for _, g := range gs {
		d := importGroupDTO{ID: g.ID, Artist: g.Artist, Album: g.Album, Year: g.Year, Songs: g.Songs, State: g.State, Bytes: g.Bytes,
			Error: i18n.Localize(r.Context(), g.Error), InfoHash: g.InfoHash, WaitingFor: g.WaitingFor}
		switch {
		case g.State == playlistimport.GroupDone:
			d.Done = g.Bytes
		case g.State == playlistimport.GroupDownloading && g.InfoHash != "":
			if p, ok := h.svc.Queue.(playlistimport.DownloadProgress); ok {
				done, known, err := p.Downloaded(r.Context(), g.InfoHash)
				if err != nil {
					h.err(w, r, "import group download", err)
					return
				}
				if known {
					d.Done = done
				}
			}
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// pending lists the songs of one of the profile's playlists that its
// import has not brought yet (another profile's playlist: «Не найдено»).
func (h *importAPI) pending(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "playlistId")
	if !ok {
		return
	}
	out, err := h.svc.Store.PlaylistPending(r.Context(), userOf(r).ID, id)
	if err != nil {
		h.err(w, r, "import playlist pending", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": out})
}

// noOpenJob answers 409 «Импорт уже идёт» while the profile has an open job
// of the service: what a sign-in reads would be refused (false: answered).
func (h *importAPI) noOpenJob(w http.ResponseWriter, r *http.Request, service string) bool {
	if !slices.Contains(playlistimport.Services, service) {
		return true
	}
	switch open, err := h.svc.Store.OpenJob(r.Context(), userOf(r).ID, service); {
	case err != nil:
		h.err(w, r, "import connect", err)
		return false
	case open:
		h.err(w, r, "import connect", playlistimport.ErrBusy)
		return false
	}
	return true
}
