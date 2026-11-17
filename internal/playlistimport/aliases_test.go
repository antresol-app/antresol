package playlistimport

import (
	"errors"
	"testing"

	"github.com/antresol-app/antresol/internal/playlistimport/model"
)

// TestAliasesSame: the leaf package's types are the playlist import's own
// (assignment both ways compiles), and its errors keep their identity.
func TestAliasesSame(t *testing.T) {
	var s Snapshot = model.Snapshot{Service: ServiceFile}
	var m model.Snapshot = s
	var j Job = model.Job{Report: model.Report{Unresolved: 1}}
	var g model.Group = Group{Clock: RunClock{Base: -1}}
	var c model.Catalog = Catalog(nil)
	_, _, _ = j, g, c
	var _ []model.Track = []Track{}
	var _ model.Decision = Decision{}
	var _ model.Candidate = Candidate{}
	var _ model.ServiceInfo = ServiceInfo{}
	var _ model.PublicPlaylist = PublicPlaylist{}
	var _ model.SnapTrack = SnapTrack{}
	var _ model.SnapPlaylist = SnapPlaylist{}
	var _ model.SnapAlbum = SnapAlbum{}
	if m.Service != ServiceFile {
		t.Fatal("snapshot not carried")
	}
	for _, e := range []struct{ old, leaf error }{
		{ErrNotFound, model.ErrNotFound}, {ErrNotReview, model.ErrNotReview},
		{ErrNotRetryable, model.ErrNotRetryable}, {ErrBusy, model.ErrBusy},
		{ErrBadLink, model.ErrBadLink}, {ErrTooLarge, model.ErrTooLarge},
		{ErrUnknownService, model.ErrUnknownService},
	} {
		if !errors.Is(e.old, e.leaf) || !errors.Is(e.leaf, e.old) {
			t.Errorf("%v: not the leaf package's error", e.old)
		}
	}
	// the three «Не найдено» errors stay distinct
	if errors.Is(ErrNotFound, ErrNotReview) || errors.Is(ErrNotFound, ErrNotRetryable) || errors.Is(ErrNotReview, ErrNotRetryable) {
		t.Error("errors of one text became one")
	}
	if &Services[0] != &model.Services[0] || AutoRetries != model.AutoRetries || MaxTracks != model.MaxTracks {
		t.Error("values not re-exported")
	}
	if got := TrackIDs([]Track{{ID: 3}, {ID: 5}}); len(got) != 2 || got[1] != 5 {
		t.Errorf("TrackIDs = %v", got)
	}
}
