package playlistimport

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ProgressSink takes a downloading album's bytes on disk from the queue
// that downloads it (POST /imports/groups/{id}/progress); the
// Downloader's DownloadProgress answers from it.
type ProgressSink interface {
	SetProgress(ctx context.Context, groupID, done int64) error
}

// NewAddonDownloader is the download phase when an addon may download
// the albums (Service.Queue, set once at start). enabled reports whether
// one is installed and on (running or not); when it cannot tell (the
// addons list did not read), Step returns its error and touches nothing
// that tick.
//
// While enabled, Step links each job's albums the library has, once per
// job (and core start): a compare-and-set from waiting, so an album the
// addon claimed meanwhile is left to it. It never touches an album that
// is searching, waiting for space or for a download, or downloading, and
// it leaves the rest waiting for the addon — also while the addon
// restarts. While not enabled, it is LibraryStep (the rest are not
// found; «Повторить» puts them back in the queue).
//
// The progress the addon posts is kept in memory, per album, and
// dropped once the album leaves downloading.
func NewAddonDownloader(st *Store, find AlbumFinder, enabled func() (bool, error)) interface {
	Downloader
	DownloadProgress
} {
	if find == nil {
		find = noAlbums{}
	}
	if enabled == nil {
		enabled = func() (bool, error) { return false, nil }
	}
	return &addonDownloader{st: st, find: find, enabled: enabled, linked: map[int64]bool{}, progress: map[int64]liveProgress{}}
}

type addonDownloader struct {
	st      *Store
	find    AlbumFinder
	enabled func() (bool, error)

	mu       sync.Mutex
	linked   map[int64]bool         // jobs whose waiting albums were looked up in the library
	progress map[int64]liveProgress // by album
}

// liveProgress is an album's download (key) and its bytes on disk.
type liveProgress struct {
	jobID int64
	key   string
	done  int64
}

var _ ProgressSink = (*addonDownloader)(nil)

func (d *addonDownloader) at() time.Time { return d.st.clock() }

// Step is LibraryStep while no addon is enabled; else it links the job's
// waiting albums the library has, once. An enabled error skips the tick.
func (d *addonDownloader) Step(ctx context.Context, jobID int64) error {
	on, err := d.enabled()
	if err != nil {
		return err // not "no addon": the tick is skipped, nothing is settled
	}
	if !on {
		return (&libraryStep{st: d.st, lib: d.find}).Step(ctx, jobID)
	}
	j, err := d.st.Job(ctx, jobID)
	if err != nil || j.Status != StatusDownloading {
		return err
	}
	if err := d.prune(ctx, jobID); err != nil {
		return err
	}
	d.mu.Lock()
	done := d.linked[jobID]
	d.mu.Unlock()
	if done {
		return nil
	}
	gs, err := d.st.JobGroups(ctx, jobID, GroupWaiting)
	if err != nil {
		return err
	}
	for _, g := range gs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.link(ctx, g); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.linked[jobID] = true
	d.mu.Unlock()
	return nil
}

// link links a waiting album's songs the library has (the album is have
// when that is all of them), unless it left waiting meanwhile.
func (d *addonDownloader) link(ctx context.Context, g Group) error {
	want, err := d.st.GroupTracks(ctx, g.ID, TrackMatched)
	if err != nil {
		return err
	}
	album, tracks, err := FindExisting(ctx, d.find, g, nil)
	if err != nil {
		return err
	}
	have := LinkTracks(want, tracks)
	rest := len(want) - len(have)
	if len(have) == 0 && rest > 0 {
		return nil
	}
	_, err = d.st.markHaveFrom(ctx, g.ID, GroupWaiting, have, album, rest == 0, d.at())
	return err
}

// prune drops the progress of the job's albums no longer downloading.
func (d *addonDownloader) prune(ctx context.Context, jobID int64) error {
	d.mu.Lock()
	has := false
	for _, p := range d.progress {
		if p.jobID == jobID {
			has = true
			break
		}
	}
	d.mu.Unlock()
	if !has {
		return nil
	}
	gs, err := d.st.JobGroups(ctx, jobID, GroupDownloading)
	if err != nil {
		return err
	}
	keep := map[int64]string{}
	for _, g := range gs {
		keep[g.ID] = g.InfoHash
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, p := range d.progress {
		if key, ok := keep[id]; p.jobID == jobID && (!ok || key != p.key) {
			delete(d.progress, id)
		}
	}
	return nil
}

// SetProgress keeps a downloading album's bytes on disk; for an album
// not downloading it drops what was kept.
func (d *addonDownloader) SetProgress(ctx context.Context, groupID, done int64) error {
	g, err := d.st.Group(ctx, groupID)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if g.State != GroupDownloading || g.InfoHash == "" {
		delete(d.progress, groupID)
		return nil
	}
	d.progress[groupID] = liveProgress{jobID: g.JobID, key: g.InfoHash, done: max(done, 0)}
	return nil
}

// Downloaded is the bytes the addon last posted for the album downloading
// key; known false when none (or it no longer downloads key).
func (d *addonDownloader) Downloaded(ctx context.Context, key string) (int64, bool, error) {
	d.mu.Lock()
	var id int64
	var p liveProgress
	for gid, lp := range d.progress {
		if lp.key == key {
			id, p = gid, lp
			break
		}
	}
	d.mu.Unlock()
	if id == 0 {
		return 0, false, nil
	}
	g, err := d.st.Group(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, false, err
	}
	if err != nil || g.State != GroupDownloading || g.InfoHash != key {
		d.mu.Lock()
		if cur, ok := d.progress[id]; ok && cur == p {
			delete(d.progress, id)
		}
		d.mu.Unlock()
		return 0, false, nil
	}
	return p.done, true, nil
}
