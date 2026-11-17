package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/go-chi/chi/v5"
)

// The core API's library imports (imports): an addon that fetches the
// albums an import lacks drives their queue through these operations,
// each one transaction of the core (a compare-and-set that lost is 409),
// and reads the services' libraries into reading jobs. Albums are named
// by the download they wait for or fetch: key, never the download
// client's own words. No sign-in token travels here.

const (
	msgAddonNoImports    = "Импорт недоступен"
	msgAddonTaken        = "Альбом уже взят"
	msgAddonBadState     = "Неверное состояние"
	addonImportsBody     = 64 << 10
	addonImportsSnapshot = 32 << 20 // MaxTracks songs, ≈600 bytes each
)

func mountAddonCoreImports(r chi.Router, a *addonsAPI) {
	r.Get("/imports/active-count", a.coreImportsActive)
	r.Get("/imports/open", a.coreImportsOpen)
	r.Get("/imports/reads", a.coreImportsReads)
	r.Get("/imports/jobs", a.coreImportsJobIDs)
	r.Post("/imports/jobs", a.coreImportsCreate)
	r.Post("/imports/jobs/fail-reading", a.coreImportsFailReading)
	r.Get("/imports/jobs/{id}", a.coreImportsJob)
	r.Delete("/imports/jobs/{id}", a.coreImportsDelete)
	r.Get("/imports/jobs/{id}/candidates", a.coreImportsCandidates)
	r.Get("/imports/jobs/{id}/groups", a.coreImportsGroups)
	r.Get("/imports/jobs/{id}/still-downloading", a.coreImportsStill)
	r.Post("/imports/jobs/{id}/rebuild", a.coreImportsRebuild)
	r.Put("/imports/jobs/{id}/snapshot", a.coreImportsSnapshot)
	r.Post("/imports/jobs/{id}/fail", a.coreImportsFailJob)
	r.Get("/imports/groups/{id}/tracks", a.coreImportsTracks)
	for op, h := range map[string]func(http.ResponseWriter, *http.Request, playlistimport.Group){
		"move": a.coreGroupMove, "wait": a.coreGroupWait, "give-up-wait": a.coreGroupGiveUpWait, "start": a.coreGroupStart,
		"clock": a.coreGroupClock, "given-up": a.coreGroupGivenUp, "requeue": a.coreGroupRequeue, "no-format": a.coreGroupNoFormat,
		"fail": a.coreGroupFail, "finish": a.coreGroupFinish, "have": a.coreGroupHave, "not-found": a.coreGroupNotFound,
		"progress": a.coreGroupProgress,
	} {
		r.Post("/imports/groups/{id}/"+op, a.groupOp(h))
	}
}

// importsCaller: the calling addon has the imports permission (403 first)
// and the server runs imports (503).
func (a *addonsAPI) importsCaller(w http.ResponseWriter, r *http.Request) (*playlistimport.Service, bool) {
	if _, ok := a.callingAddon(w, r, "imports"); !ok {
		return nil, false
	}
	if a.imports == nil || a.imports.Store == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNoImports)
		return nil, false
	}
	return a.imports, true
}

// importsErr answers a store error.
func importsErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, playlistimport.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Не найдено")
	case errors.Is(err, playlistimport.ErrBusy):
		writeErr(w, r, http.StatusConflict, "Импорт уже идёт")
	case errors.Is(err, playlistimport.ErrStarted):
		writeErr(w, r, http.StatusConflict, "Импорт уже начат")
	case errors.Is(err, playlistimport.ErrUnknownService):
		writeErr(w, r, http.StatusBadRequest, "Неизвестный сервис")
	case errors.Is(err, playlistimport.ErrTooLarge):
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Файл слишком большой")
	default:
		internalErr(w, r, "addon imports "+what, err)
	}
}

// importsJob is the job of the path's {id} (400, 404).
func (a *addonsAPI) importsJob(w http.ResponseWriter, r *http.Request) (*playlistimport.Service, playlistimport.Job, bool) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return nil, playlistimport.Job{}, false
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
		return nil, playlistimport.Job{}, false
	}
	j, err := svc.Store.Job(r.Context(), id)
	if err != nil {
		importsErr(w, r, "job", err)
		return nil, playlistimport.Job{}, false
	}
	return svc, j, true
}

func importJobOut(j playlistimport.Job) addonkit.ImportJob {
	return addonkit.ImportJob{ID: j.ID, UserID: j.UserID, Service: j.Service, Account: j.Account, Status: j.Status,
		Favorites: j.Favorites, Error: j.Error, Report: addonkit.ImportReport{SavedAlbumsMissing: j.Report.SavedAlbumsMissing,
			Unresolved: j.Report.Unresolved}, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, StartedAt: j.StartedAt,
		FinishedAt: j.FinishedAt}
}

func importGroupOut(g playlistimport.Group) addonkit.ImportGroup {
	out := addonkit.ImportGroup{ID: g.ID, JobID: g.JobID, ReleaseGroup: g.ReleaseGroup, Artist: g.Artist, Album: g.Album, Year: g.Year,
		Aliases: nonNilStrings(g.Aliases), AlbumItemID: g.AlbumItemID, State: g.State, Key: g.InfoHash, Bytes: g.Bytes,
		Attempts: g.Attempts, Error: g.Error, UpdatedAt: g.UpdatedAt, Songs: g.Songs, WaitingFor: g.WaitingFor, SkipKey: g.SkipInfoHash,
		AnyFormat: g.AnyFormat, Dead: nonNilStrings(g.Dead), Slow: nonNilStrings(g.Slow), WaitDone: g.WaitDone,
		Clock: addonkit.ImportRunClock{RunSec: int64(g.Clock.Run / time.Second), QuietSec: int64(g.Clock.Quiet / time.Second),
			Base: g.Clock.Base, Done: g.Clock.Done}}
	if !g.WaitAt.IsZero() {
		at := g.WaitAt
		out.WaitAt = &at
	}
	return out
}

func importGroupsOut(gs []playlistimport.Group) []addonkit.ImportGroup {
	out := make([]addonkit.ImportGroup, 0, len(gs))
	for _, g := range gs {
		out = append(out, importGroupOut(g))
	}
	return out
}

func importTrackOut(t playlistimport.Track) addonkit.ImportTrack {
	return addonkit.ImportTrack{ID: t.ID, JobID: t.JobID, Key: t.Key, Artist: t.Artist, Title: t.Title, Album: t.Album,
		AlbumArtist: t.AlbumArtist, ISRC: t.ISRC, DurationMs: t.DurationMs, AddedAt: t.AddedAt, Liked: t.Liked, State: t.State,
		Reason: t.Reason, MBRecording: t.MBRecording, MBReleaseGroup: t.MBReleaseGroup, MBTitle: t.MBTitle, MBArtist: t.MBArtist,
		MBAlbum: t.MBAlbum, MBYear: t.MBYear, GroupID: t.GroupID, ItemID: t.ItemID}
}

func importSnapshotIn(s addonkit.ImportSnapshot) playlistimport.Snapshot {
	out := playlistimport.Snapshot{Service: s.Service, Account: s.Account}
	for _, t := range s.Tracks {
		out.Tracks = append(out.Tracks, playlistimport.SnapTrack{Key: t.Key, Artist: t.Artist, Title: t.Title, Album: t.Album,
			AlbumArtist: t.AlbumArtist, ISRC: t.ISRC, DurationMs: t.DurationMs, AddedAt: t.AddedAt, Liked: t.Liked})
	}
	for _, p := range s.Playlists {
		out.Playlists = append(out.Playlists, playlistimport.SnapPlaylist{ID: p.ID, Name: p.Name, Readable: p.Readable, Entries: p.Entries})
	}
	for _, a := range s.Albums {
		out.Albums = append(out.Albums, playlistimport.SnapAlbum{Key: a.Key, Artist: a.Artist, Title: a.Title, Year: a.Year, AddedAt: a.AddedAt})
	}
	return out
}

// GET /imports/active-count → {count}
func (a *addonsAPI) coreImportsActive(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	n, err := svc.Store.ActiveGroups(r.Context())
	if err != nil {
		importsErr(w, r, "active", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// queryUser is a positive ?userId (400 otherwise).
func queryUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := positiveID(r.URL.Query().Get("userId"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
	}
	return id, ok
}

// GET /imports/open?userId&service → {open}
func (a *addonsAPI) coreImportsOpen(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	user, ok := queryUser(w, r)
	if !ok {
		return
	}
	open, err := svc.Store.OpenJob(r.Context(), user, r.URL.Query().Get("service"))
	if err != nil {
		importsErr(w, r, "open", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"open": open})
}

// GET /imports/reads?userId → {ids}: the profile's jobs still reading.
func (a *addonsAPI) coreImportsReads(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	user, ok := queryUser(w, r)
	if !ok {
		return
	}
	ids, err := svc.Store.ReadingJobs(r.Context(), user)
	if err != nil {
		importsErr(w, r, "reads", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]int64{"ids": ids})
}

// GET /imports/jobs?status → {ids}
func (a *addonsAPI) coreImportsJobIDs(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !playlistimport.ValidStatus(status) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadState)
		return
	}
	ids, err := svc.Store.JobIDs(r.Context(), status)
	if err != nil {
		importsErr(w, r, "jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]int64{"ids": ids})
}

// POST /imports/jobs {userId, service, reading: true} → {id}: an empty
// job the addon fills once its read of the service ends; a kid's
// profile is 404.
func (a *addonsAPI) coreImportsCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		UserID  int64  `json:"userId"`
		Service string `json:"service"`
		Reading bool   `json:"reading"`
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	if in.UserID <= 0 || !in.Reading {
		badRequest(w, r)
		return
	}
	var one int
	// never a kid's: kids get what addons bring only through the core's
	// own gating, and a job's rebuild writes into the profile's library
	switch err := svc.Store.DB.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id = ? AND is_kid = 0`, in.UserID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	case err != nil:
		importsErr(w, r, "profile", err)
		return
	}
	id, err := svc.Store.CreateReadingJob(r.Context(), in.UserID, strings.TrimSpace(in.Service))
	if err != nil {
		importsErr(w, r, "create", err)
		return
	}
	svc.Publish(r.Context(), id, true)
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

// POST /imports/jobs/fail-reading {error} → {ids}: every job still
// reading fails (the addon's reads do not survive its restart).
func (a *addonsAPI) coreImportsFailReading(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		Error string `json:"error"`
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	ids, err := svc.Store.FailReading(r.Context(), in.Error)
	if err != nil {
		importsErr(w, r, "fail reading", err)
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	for _, id := range ids {
		svc.Publish(r.Context(), id, true)
	}
	writeJSON(w, http.StatusOK, map[string][]int64{"ids": ids})
}

// GET /imports/jobs/{id} → Job
func (a *addonsAPI) coreImportsJob(w http.ResponseWriter, r *http.Request) {
	if _, j, ok := a.importsJob(w, r); ok {
		writeJSON(w, http.StatusOK, importJobOut(j))
	}
}

// DELETE /imports/jobs/{id} → 204 (a matching or downloading job: 409).
func (a *addonsAPI) coreImportsDelete(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	if err := svc.Delete(r.Context(), j.ID); err != nil {
		importsErr(w, r, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /imports/jobs/{id}/candidates?retryBefore → [Group]
func (a *addonsAPI) coreImportsCandidates(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	before, err := time.Parse(time.RFC3339, r.URL.Query().Get("retryBefore"))
	if err != nil {
		badRequest(w, r)
		return
	}
	gs, err := svc.Store.QueueCandidates(r.Context(), j.ID, before)
	if err != nil {
		importsErr(w, r, "candidates", err)
		return
	}
	writeJSON(w, http.StatusOK, importGroupsOut(gs))
}

// GET /imports/jobs/{id}/groups?state → [Group] (no state: all)
func (a *addonsAPI) coreImportsGroups(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	state := r.URL.Query().Get("state")
	if state != "" && !playlistimport.ValidGroupState(state) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadState)
		return
	}
	gs, err := svc.Store.JobGroups(r.Context(), j.ID, state)
	if err != nil {
		importsErr(w, r, "groups", err)
		return
	}
	writeJSON(w, http.StatusOK, importGroupsOut(gs))
}

// GET /imports/jobs/{id}/still-downloading → {downloading}
func (a *addonsAPI) coreImportsStill(w http.ResponseWriter, r *http.Request) {
	if _, j, ok := a.importsJob(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]bool{"downloading": j.Status == playlistimport.StatusDownloading})
	}
}

// POST /imports/jobs/{id}/rebuild → 204: the job's playlists and stars
// from what it has brought so far.
func (a *addonsAPI) coreImportsRebuild(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	if svc.Rebuild != nil {
		if err := svc.Rebuild(r.Context(), j.ID); err != nil {
			importsErr(w, r, "rebuild", err)
			return
		}
	}
	svc.Publish(r.Context(), j.ID, false)
	w.WriteHeader(http.StatusNoContent)
}

// PUT /imports/jobs/{id}/snapshot {snapshot} → 204: the reading job moves
// to preview (one no longer reading: 409). Up to 32 MiB.
func (a *addonsAPI) coreImportsSnapshot(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	var in struct {
		Snapshot addonkit.ImportSnapshot `json:"snapshot"`
	}
	if !decodeMax(w, r, &in, addonImportsSnapshot) {
		return
	}
	if err := svc.Fill(r.Context(), j.ID, importSnapshotIn(in.Snapshot)); err != nil {
		importsErr(w, r, "snapshot", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /imports/jobs/{id}/fail {error} → 204: the reading job failed (one
// no longer reading: 409).
func (a *addonsAPI) coreImportsFailJob(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := a.importsJob(w, r)
	if !ok {
		return
	}
	var in struct {
		Error string `json:"error"`
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	moved, err := svc.Advance(r.Context(), j.ID, playlistimport.StatusReading, playlistimport.StatusFailed, in.Error)
	if err != nil {
		importsErr(w, r, "fail", err)
		return
	}
	if !moved {
		writeErr(w, r, http.StatusConflict, "Импорт уже начат")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /imports/groups/{id}/tracks?state=…(repeated) → [Track]: the
// album's songs the job imports, in those states.
func (a *addonsAPI) coreImportsTracks(w http.ResponseWriter, r *http.Request) {
	svc, g, ok := a.importsGroup(w, r)
	if !ok {
		return
	}
	states := r.URL.Query()["state"]
	for _, s := range states {
		if !playlistimport.ValidTrackState(s) {
			writeErr(w, r, http.StatusBadRequest, msgAddonBadState)
			return
		}
	}
	ts, err := svc.Store.GroupTracks(r.Context(), g.ID, states...)
	if err != nil {
		importsErr(w, r, "tracks", err)
		return
	}
	out := make([]addonkit.ImportTrack, 0, len(ts))
	for _, t := range ts {
		out = append(out, importTrackOut(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// importsGroup is the album of the path's {id} (400, 404).
func (a *addonsAPI) importsGroup(w http.ResponseWriter, r *http.Request) (*playlistimport.Service, playlistimport.Group, bool) {
	svc, ok := a.importsCaller(w, r)
	if !ok {
		return nil, playlistimport.Group{}, false
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
		return nil, playlistimport.Group{}, false
	}
	g, err := svc.Store.Group(r.Context(), id)
	if err != nil {
		importsErr(w, r, "group", err)
		return nil, playlistimport.Group{}, false
	}
	return svc, g, true
}

// groupOp is a write on the album of the path's {id}, done by h once the
// album is found (each h answers through groupDone or on its own).
func (a *addonsAPI) groupOp(h func(http.ResponseWriter, *http.Request, playlistimport.Group)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, g, ok := a.importsGroup(w, r)
		if ok {
			h(w, r, g)
		}
	}
}

// groupDone answers a group write: 409 when its compare-and-set lost,
// else 204, and the job's progress goes out (at most every 2 s).
func (a *addonsAPI) groupDone(w http.ResponseWriter, r *http.Request, g playlistimport.Group, what string, won bool, err error) {
	switch {
	case err != nil:
		importsErr(w, r, what, err)
	case !won:
		writeErr(w, r, http.StatusConflict, msgAddonTaken)
	default:
		a.imports.Publish(context.WithoutCancel(r.Context()), g.JobID, false)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *addonsAPI) st() *playlistimport.Store { return a.imports.Store }

func (a *addonsAPI) coreGroupMove(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct{ From, To string }
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	if !playlistimport.ValidMove(in.From, in.To) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadState)
		return
	}
	moved, err := a.st().MoveGroup(r.Context(), g.ID, in.From, in.To, a.st().Clock())
	a.groupDone(w, r, g, "move", moved, err)
}

func (a *addonsAPI) coreGroupWait(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Key, Name string
		Done      int64
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	if in.Key == "" {
		badRequest(w, r)
		return
	}
	a.groupDone(w, r, g, "wait", true, a.st().WaitFor(r.Context(), g.ID, in.Key, in.Name, in.Done, a.st().Clock()))
}

func (a *addonsAPI) coreGroupGiveUpWait(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct{ Key string }
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	a.groupDone(w, r, g, "give up wait", true, a.st().GiveUpWait(r.Context(), g.ID, in.Key, a.st().Clock()))
}

func (a *addonsAPI) coreGroupStart(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Key                  string
		Bytes                int64
		Downloading, Missing []int64
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	if in.Key == "" || in.Bytes < 0 {
		badRequest(w, r)
		return
	}
	started, err := a.st().StartGroup(r.Context(), g.ID, in.Key, in.Bytes, in.Downloading, in.Missing, a.st().Clock())
	a.groupDone(w, r, g, "start", started, err)
}

func (a *addonsAPI) coreGroupClock(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Key   string
		Clock addonkit.ImportRunClock
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	c := playlistimport.RunClock{Run: time.Duration(in.Clock.RunSec) * time.Second, Quiet: time.Duration(in.Clock.QuietSec) * time.Second,
		Base: in.Clock.Base, Done: in.Clock.Done}
	a.groupDone(w, r, g, "clock", true, a.st().SaveClock(r.Context(), g.ID, in.Key, c))
}

func (a *addonsAPI) coreGroupGivenUp(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Key  string
		Slow bool
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	a.groupDone(w, r, g, "given up", true, a.st().MarkGivenUp(r.Context(), g.ID, in.Key, in.Slow))
}

func (a *addonsAPI) coreGroupRequeue(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Key, Msg string
		MaxDead  int
	}
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	if in.MaxDead <= 0 {
		badRequest(w, r)
		return
	}
	a.groupDone(w, r, g, "requeue", true, a.st().Requeue(r.Context(), g.ID, in.Key, in.Msg, in.MaxDead, a.st().Clock()))
}

type importTracksMsg struct {
	TrackIDs []int64 `json:"trackIds"`
	Msg      string  `json:"msg"`
	Retry    bool    `json:"retry"`
}

func (a *addonsAPI) coreGroupNoFormat(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in importTracksMsg
	if decodeMax(w, r, &in, addonImportsBody) {
		a.groupDone(w, r, g, "no format", true, a.st().NoFormat(r.Context(), g.ID, in.TrackIDs, in.Msg, a.st().Clock()))
	}
}

func (a *addonsAPI) coreGroupFail(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in importTracksMsg
	if decodeMax(w, r, &in, addonImportsBody) {
		a.groupDone(w, r, g, "fail", true, a.st().FailGroup(r.Context(), g.ID, in.TrackIDs, in.Msg, in.Retry, a.st().Clock()))
	}
}

func (a *addonsAPI) coreGroupNotFound(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in importTracksMsg
	if decodeMax(w, r, &in, addonImportsBody) {
		a.groupDone(w, r, g, "not found", true, a.st().NotFound(r.Context(), g.ID, in.TrackIDs, in.Msg, a.st().Clock()))
	}
}

func (a *addonsAPI) coreGroupFinish(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Done   map[int64]int64
		Failed []int64
		Album  int64
	}
	if decodeMax(w, r, &in, addonImportsBody) {
		a.groupDone(w, r, g, "finish", true, a.st().FinishGroup(r.Context(), g.ID, in.Done, in.Failed, in.Album, a.st().Clock()))
	}
}

func (a *addonsAPI) coreGroupHave(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct {
		Have  map[int64]int64
		Album int64
		Done  bool
	}
	if decodeMax(w, r, &in, addonImportsBody) {
		a.groupDone(w, r, g, "have", true, a.st().MarkHave(r.Context(), g.ID, in.Have, in.Album, in.Done, a.st().Clock()))
	}
}

// POST …/progress {done} → 204: kept in memory while the album downloads
// (the album view's bytes so far).
func (a *addonsAPI) coreGroupProgress(w http.ResponseWriter, r *http.Request, g playlistimport.Group) {
	var in struct{ Done int64 }
	if !decodeMax(w, r, &in, addonImportsBody) {
		return
	}
	sink, ok := a.imports.Queue.(playlistimport.ProgressSink)
	if !ok {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNoImports)
		return
	}
	if err := sink.SetProgress(r.Context(), g.ID, in.Done); err != nil {
		importsErr(w, r, "progress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
