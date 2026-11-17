package playlistimport

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// countingFinder is the library, counting its album lookups; before runs
// ahead of each MusicBrainz-id lookup (nil: nothing).
type countingFinder struct {
	lib    AlbumFinder
	mu     sync.Mutex
	n      int
	before func(rgid string)
}

func (c *countingFinder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *countingFinder) FindAlbumByMBID(ctx context.Context, rgid string) (library.Item, bool, error) {
	c.mu.Lock()
	c.n++
	before := c.before
	c.mu.Unlock()
	if before != nil {
		before(rgid)
	}
	return c.lib.FindAlbumByMBID(ctx, rgid)
}

func (c *countingFinder) FindAlbum(ctx context.Context, artist, album string) (library.Item, bool, error) {
	return c.lib.FindAlbum(ctx, artist, album)
}

func (c *countingFinder) AlbumFolderTracks(ctx context.Context, albumID int64) ([]library.Track, error) {
	return c.lib.AlbumFolderTracks(ctx, albumID)
}

func on() (bool, error) { return true, nil }

func TestAddonDownloader_LinksOnceLeavesWaiting(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	f := &countingFinder{lib: e.lib}
	d := NewAddonDownloader(e.st, f, on)
	if err := d.Step(ctx, o.id); err != nil {
		t.Fatal(err)
	}
	if g := mustGroup(t, e.st, o.groups["rg-have"]); g.State != GroupHave || g.AlbumItemID != libAlbum(t, e) {
		t.Fatalf("library's album %+v", g)
	}
	if tr := mustTrack(t, e.st, o.tracks["a"]); tr.State != TrackHave || tr.ItemID != e.items["a"] {
		t.Fatalf("a %+v", tr)
	}
	// the album the library lacks waits for the addon
	if g := mustGroup(t, e.st, o.groups["rg-absent"]); g.State != GroupWaiting {
		t.Fatalf("missing album %s", g.State)
	}
	if tr := mustTrack(t, e.st, o.tracks["x"]); tr.State != TrackMatched {
		t.Fatalf("x %s", tr.State)
	}
	n := f.count()
	if n == 0 {
		t.Fatal("no lookup")
	}
	// once per job
	for range 3 {
		if err := d.Step(ctx, o.id); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != n {
		t.Fatalf("looked up again: %d → %d", n, f.count())
	}
	if g := mustGroup(t, e.st, o.groups["rg-absent"]); g.State != GroupWaiting {
		t.Fatalf("missing album after more steps %s", g.State)
	}
}

// An addon restart (enabled, not running): what it downloads stays as it
// is, and so do its searches and waits.
func TestAddonDownloader_RestartKeepsDownloading(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	absent, have := o.groups["rg-absent"], o.groups["rg-have"]
	startOn(t, e.st, absent, "k1", []int64{o.tracks["x"]}, nil)
	if err := e.st.WaitFor(ctx, have, "w1", "Кино — Сборник", 0, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	d := NewAddonDownloader(e.st, e.lib, on) // a fresh adapter: a core restart too
	for range 2 {
		if err := d.Step(ctx, o.id); err != nil {
			t.Fatal(err)
		}
	}
	if g := mustGroup(t, e.st, absent); g.State != GroupDownloading || g.InfoHash != "k1" {
		t.Fatalf("downloading group %+v", g)
	}
	if tr := mustTrack(t, e.st, o.tracks["x"]); tr.State != TrackDownloading {
		t.Fatalf("x %s", tr.State)
	}
	if g := mustGroup(t, e.st, have); g.State != GroupWaitingDownload {
		t.Fatalf("waiting group %s (the library has it, but it waits for the addon's download)", g.State)
	}
	if busy, err := e.st.Unfinished(ctx, o.id); err != nil || !busy {
		t.Fatalf("job ended under a running download: %v %v", busy, err)
	}
}

func TestAddonDownloader_Disabled(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	enabled := false
	d := NewAddonDownloader(e.st, e.lib, func() (bool, error) { return enabled, nil })
	if err := d.Step(ctx, o.id); err != nil {
		t.Fatal(err)
	}
	if g := mustGroup(t, e.st, o.groups["rg-have"]); g.State != GroupHave {
		t.Fatalf("library's album %s", g.State)
	}
	g := mustGroup(t, e.st, o.groups["rg-absent"])
	if g.State != GroupNotFound || g.Error != MsgNotInLibrary {
		t.Fatalf("missing album %+v", g)
	}
	if tr := mustTrack(t, e.st, o.tracks["x"]); tr.State != TrackNotFound || tr.Reason != MsgNotInLibrary {
		t.Fatalf("x %+v", tr)
	}
	if busy, err := e.st.Unfinished(ctx, o.id); err != nil || busy {
		t.Fatalf("unfinished %v %v", busy, err)
	}
}

// The addons list cannot be read (a busy database, shutdown): the tick
// is skipped, so the addon's download and wait stay as they are — an
// error is not "no addon".
func TestAddonDownloader_EnabledErrorSkipsTick(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	absent, have := o.groups["rg-absent"], o.groups["rg-have"]
	startOn(t, e.st, absent, "k1", []int64{o.tracks["x"]}, nil)
	if err := e.st.WaitFor(ctx, have, "w1", "Кино — Сборник", 0, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("addons: database is locked")
	d := NewAddonDownloader(e.st, e.lib, func() (bool, error) { return false, boom })
	if err := d.Step(ctx, o.id); !errors.Is(err, boom) {
		t.Fatalf("Step = %v, want the addons-list error", err)
	}
	if g := mustGroup(t, e.st, absent); g.State != GroupDownloading || g.InfoHash != "k1" {
		t.Fatalf("downloading group %+v", g)
	}
	if tr := mustTrack(t, e.st, o.tracks["x"]); tr.State != TrackDownloading {
		t.Fatalf("x %s", tr.State)
	}
	if g := mustGroup(t, e.st, have); g.State != GroupWaitingDownload {
		t.Fatalf("waiting group %s", g.State)
	}
	if busy, err := e.st.Unfinished(ctx, o.id); err != nil || !busy {
		t.Fatalf("job ended under a running download: %v %v", busy, err)
	}
}

// The addon claims the album between the adapter's read and its link:
// the claim wins.
func TestAddonDownloader_HaveIsCAS(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	have := o.groups["rg-have"]
	f := &countingFinder{lib: e.lib, before: func(rgid string) {
		if rgid != "rg-have" {
			return
		}
		if ok, err := e.st.MoveGroup(ctx, have, GroupWaiting, GroupSearching, e.st.clock()); err != nil || !ok {
			t.Errorf("claim: %v %v", ok, err)
		}
	}}
	if err := NewAddonDownloader(e.st, f, on).Step(ctx, o.id); err != nil {
		t.Fatal(err)
	}
	if g := mustGroup(t, e.st, have); g.State != GroupSearching || g.AlbumItemID != 0 {
		t.Fatalf("claimed group overwritten: %+v", g)
	}
	for _, k := range []string{"a", "b"} {
		if tr := mustTrack(t, e.st, o.tracks[k]); tr.State != TrackMatched || tr.ItemID != 0 {
			t.Fatalf("%s linked under the addon's claim: %+v", k, tr)
		}
	}
}

func TestAddonDownloader_Progress(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g := o.groups["rg-absent"]
	d := NewAddonDownloader(e.st, e.lib, on)
	sink := d.(ProgressSink)
	// not downloading: nothing kept
	if err := sink.SetProgress(ctx, g, 10); err != nil {
		t.Fatal(err)
	}
	if _, known, err := d.Downloaded(ctx, "k1"); err != nil || known {
		t.Fatalf("progress of a waiting album: %v %v", known, err)
	}
	startOn(t, e.st, g, "k1", []int64{o.tracks["x"]}, nil)
	if err := sink.SetProgress(ctx, g, 40); err != nil {
		t.Fatal(err)
	}
	if done, known, err := d.Downloaded(ctx, "k1"); err != nil || !known || done != 40 {
		t.Fatalf("progress %d %v %v", done, known, err)
	}
	if _, known, _ := d.Downloaded(ctx, "other"); known {
		t.Fatal("another key known")
	}
	// it leaves downloading: dropped
	if err := e.st.FinishGroup(ctx, g, nil, []int64{o.tracks["x"]}, 0, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	if _, known, err := d.Downloaded(ctx, "k1"); err != nil || known {
		t.Fatalf("progress after the download: %v %v", known, err)
	}
}
