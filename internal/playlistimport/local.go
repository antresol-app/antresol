package playlistimport

import (
	"context"
	"database/sql"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
)

// MsgNotInLibrary is the reason of a song (and the error of its album)
// that a download-less import found nowhere in the library.
const MsgNotInLibrary = "Нет в медиатеке"

// AlbumFinder is the part of *library.Repo that looks an album up.
type AlbumFinder interface {
	FindAlbumByMBID(ctx context.Context, rgid string) (library.Item, bool, error)
	FindAlbum(ctx context.Context, artist, album string) (library.Item, bool, error)
	AlbumFolderTracks(ctx context.Context, albumID int64) ([]library.Track, error)
}

// FindExisting finds the album in the library: the one its songs went to
// before, else (byTorrent, when not nil) the one the download it waited for
// went to, else by its MusicBrainz id, else by artist and title. It returns
// the album and the tracks of every album item in its folder (a file whose
// tag names the album differently may be an item of its own) — 0, nil:
// none.
func FindExisting(ctx context.Context, lib AlbumFinder, g Group, byTorrent func(ctx context.Context, ih string) ([]library.Track, error)) (int64, []library.Track, error) {
	try := func(id int64) ([]library.Track, error) {
		if id == 0 {
			return nil, nil
		}
		return lib.AlbumFolderTracks(ctx, id)
	}
	if ts, err := try(g.AlbumItemID); err != nil || len(ts) > 0 {
		return g.AlbumItemID, ts, err
	}
	if g.InfoHash != "" && byTorrent != nil {
		ts, err := byTorrent(ctx, g.InfoHash)
		if err != nil {
			return 0, nil, err
		}
		for _, t := range ts {
			if t.AlbumID == 0 {
				continue
			}
			if ts, err := try(t.AlbumID); err != nil || len(ts) > 0 {
				return t.AlbumID, ts, err
			}
		}
	}
	it, ok, err := lib.FindAlbumByMBID(ctx, g.ReleaseGroup)
	if err != nil {
		return 0, nil, err
	}
	if !ok {
		if it, ok, err = lib.FindAlbum(ctx, g.Artist, g.Album); err != nil {
			return 0, nil, err
		}
	}
	if !ok {
		return 0, nil, nil
	}
	ts, err := try(it.ID)
	return it.ID, ts, err
}

// LinkTracks pairs songs with library tracks of the same title (the
// MusicBrainz one or the service's, editions dropped); each library track
// once. Track id → library track id.
func LinkTracks(ts []Track, lib []library.Track) map[int64]int64 {
	out := map[int64]int64{}
	used := map[int64]bool{}
	for _, t := range ts {
		keys := []string{musicbrainz.Norm(musicbrainz.StripEditions(t.MBTitle)), musicbrainz.Norm(musicbrainz.StripEditions(t.Title))}
		for _, l := range lib {
			if used[l.ID] {
				continue
			}
			lk := musicbrainz.Norm(musicbrainz.StripEditions(l.Title))
			if lk != "" && (lk == keys[0] || lk == keys[1]) {
				out[t.ID] = l.ID
				used[l.ID] = true
				break
			}
		}
	}
	return out
}

// TrackIDs are the ids of the songs.
func TrackIDs(ts []Track) []int64 {
	out := make([]int64, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

// noAlbums is a library with no album in it (LibraryStep without one).
type noAlbums struct{}

func (noAlbums) FindAlbumByMBID(context.Context, string) (library.Item, bool, error) {
	return library.Item{}, false, nil
}
func (noAlbums) FindAlbum(context.Context, string, string) (library.Item, bool, error) {
	return library.Item{}, false, nil
}
func (noAlbums) AlbumFolderTracks(context.Context, int64) ([]library.Track, error) { return nil, nil }

// LibraryStep is the download phase of an import that downloads nothing:
// every unfinished album the library has gets its songs linked (the album
// is have), the songs it lacks are not found («Нет в медиатеке»). Albums another
// build left unfinished in the same database (in any of its own states)
// are settled the same way, or the job could never end.
func LibraryStep(st *Store, lib *library.Repo) Downloader {
	l := &libraryStep{st: st, lib: AlbumFinder(noAlbums{})}
	if lib != nil {
		l.lib = lib
	}
	return l
}

type libraryStep struct {
	st  *Store
	lib AlbumFinder
}

func (l *libraryStep) at() time.Time {
	if l.st.Now != nil {
		return l.st.Now()
	}
	return time.Now()
}

// Step settles every unfinished album of the job.
func (l *libraryStep) Step(ctx context.Context, jobID int64) error {
	j, err := l.st.Job(ctx, jobID)
	if err != nil || j.Status != StatusDownloading {
		return err
	}
	gs, err := l.st.GroupsWhere(ctx, `g.job_id = ? AND (g.state IN (?, ?, ?, ?, ?) OR g.state = ? AND g.attempts <= ?)`, jobID,
		GroupWaiting, GroupSearching, GroupWaitingSpace, GroupWaitingDownload, GroupDownloading, GroupFailed, AutoRetries)
	if err != nil {
		return err
	}
	for _, g := range gs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := l.settle(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

func (l *libraryStep) settle(ctx context.Context, g Group) error {
	// (another build's queue may have moved the songs on)
	want, err := l.st.GroupTracks(ctx, g.ID, TrackMatched, TrackQueued, TrackDownloading, TrackFailed)
	if err != nil {
		return err
	}
	album, tracks, err := FindExisting(ctx, l.lib, g, nil)
	if err != nil {
		return err
	}
	have := LinkTracks(want, tracks)
	var rest []int64
	for _, t := range want {
		if _, ok := have[t.ID]; !ok {
			rest = append(rest, t.ID)
		}
	}
	if len(have) > 0 || len(rest) == 0 {
		if err := l.st.MarkHave(ctx, g.ID, have, album, len(rest) == 0, l.at()); err != nil {
			return err
		}
	}
	if len(rest) == 0 {
		return nil
	}
	return l.st.InTx(ctx, func(tx *sql.Tx) error {
		if err := SetTracks(ctx, tx, TrackNotFound, MsgNotInLibrary, rest, nil); err != nil {
			return err
		}
		return Settle(ctx, tx, g.ID, GroupNotFound, MsgNotInLibrary, l.at())
	})
}
