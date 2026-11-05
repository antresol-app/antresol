package playlistimport

import "context"

// The read-only views of a job: preview counts, playlists and progress.

// Playlist is a service playlist as the job's preview shows it.
type Playlist struct {
	ID       int64
	Name     string
	Songs    int
	Readable bool
	Selected bool
}

// Counts are the preview numbers. Songs and Albums count the selected
// songs (liked ones when «Избранное» is on, and those of ticked playlists).
type Counts struct {
	Songs       int `json:"songs"`
	Liked       int `json:"liked"`
	Albums      int `json:"albums"`
	Playlists   int `json:"playlists"`
	SavedAlbums int `json:"savedAlbums"`
}

// Progress counts the selected songs by state (Total includes pending).
type Progress struct {
	Total       int `json:"total"`
	Matched     int `json:"matched"`
	Review      int `json:"review"`
	NotFound    int `json:"notFound"`
	Have        int `json:"have"`
	Queued      int `json:"queued"`
	Downloading int `json:"downloading"`
	Done        int `json:"done"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
}

// Progress counts a job's selected songs by state.
func (s *Store) Progress(ctx context.Context, jobID int64) (Progress, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.state, COUNT(*) FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND `+wantedSQL+` GROUP BY t.state`, jobID)
	if err != nil {
		return Progress{}, err
	}
	defer rows.Close()
	var p Progress
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return p, err
		}
		p.Total += n
		switch state {
		case TrackMatched:
			p.Matched += n
		case TrackReview:
			p.Review += n
		case TrackNotFound:
			p.NotFound += n
		case TrackHave:
			p.Have += n
		case TrackQueued:
			p.Queued += n
		case TrackDownloading:
			p.Downloading += n
		case TrackDone:
			p.Done += n
		case TrackFailed:
			p.Failed += n
		case TrackSkipped:
			p.Skipped += n
		}
	}
	return p, rows.Err()
}

// Counts returns the preview numbers of a job.
func (s *Store) Counts(ctx context.Context, jobID int64) (Counts, error) {
	var c Counts
	if err := s.DB.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM import_tracks WHERE job_id = ?1 AND liked = 1),
		(SELECT COUNT(*) FROM import_playlists WHERE job_id = ?1 AND selected = 1),
		(SELECT COUNT(*) FROM import_albums WHERE job_id = ?1)`, jobID).Scan(&c.Liked, &c.Playlists, &c.SavedAlbums); err != nil {
		return c, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.artist, t.album_artist, t.album FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND `+wantedSQL, jobID)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	albums := map[string]bool{}
	for rows.Next() {
		var artist, albumArtist, album string
		if err := rows.Scan(&artist, &albumArtist, &album); err != nil {
			return c, err
		}
		c.Songs++
		if album == "" {
			continue
		}
		if albumArtist == "" {
			albumArtist = artist
		}
		albums[norm(albumArtist)+"|"+norm(album)] = true
	}
	c.Albums = len(albums)
	return c, rows.Err()
}

// Playlists returns the playlists a job read, in the order it read them.
func (s *Store) Playlists(ctx context.Context, jobID int64) ([]Playlist, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id, p.name, p.readable, p.selected,
		(SELECT COUNT(*) FROM import_playlist_entries e WHERE e.import_playlist_id = p.id)
		FROM import_playlists p WHERE p.job_id = ? ORDER BY p.id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		var p Playlist
		var readable, selected int
		if err := rows.Scan(&p.ID, &p.Name, &readable, &selected, &p.Songs); err != nil {
			return nil, err
		}
		p.Readable, p.Selected = readable != 0, selected != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
