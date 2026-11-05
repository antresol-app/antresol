package voclean

import (
	"context"
	"database/sql"
)

// MethodSubtraction: the reference subtraction of this package (step 1;
// step 2, the ML version, will be "ml").
const MethodSubtraction = "subtraction"

// Cleaned is a cleaned voice-over ready to play: the file holds the
// cleaned version of the file's audio stream VoIndex; Studio, Lang and
// Title are the voice-over's own (the player adds the badge or the
// TitleSuffix).
type Cleaned struct {
	VoIndex                           int
	Path, Studio, Lang, Title, Method string
	// VoPath: the external voice-over it was made from (a file beside the
	// video, VoIndex its ExternalIndex); "" for the file's stream VoIndex.
	VoPath string
	// StartMs: where the cleaned track's first sample sits on the source
	// file's timestamps (the file's start_time; ffmpeg, which presents
	// the file from 0, plays them together at offset 0).
	StartMs float64
}

// OffsetMs is where the track plays on the file's timeline as ffmpeg
// presents it now (its timeline from fileStartMs, the format start_time;
// nil: unknown, taken as the start the track was made on): 0 while the
// file begins where it did when the track was made.
func (c Cleaned) OffsetMs(fileStartMs *float64) float64 {
	if fileStartMs == nil {
		return 0
	}
	return c.StartMs - *fileStartMs
}

// Tracks returns the cleaned voice-overs of a library file, by stream
// index: only those made from the file as it is now (a file replaced by a
// new download has none until they are made again).
func Tracks(ctx context.Context, d *sql.DB, fileID int64) ([]Cleaned, error) {
	rows, err := d.QueryContext(ctx, `SELECT c.vo_index, c.path, c.studio, c.lang, c.title, c.method, c.start_ms, c.vo_path
		FROM cleaned_audio c JOIN files f ON f.id=c.file_id
		WHERE c.file_id=? AND c.state='done' AND c.path<>'' AND c.file_size=f.size AND c.file_mtime=f.mtime
		ORDER BY c.vo_index`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cleaned
	for rows.Next() {
		var c Cleaned
		if err := rows.Scan(&c.VoIndex, &c.Path, &c.Studio, &c.Lang, &c.Title, &c.Method, &c.StartMs, &c.VoPath); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
