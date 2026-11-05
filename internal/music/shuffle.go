package music

import (
	"context"
	"errors"
	"strings"
)

// ShuffleLimit is the most tracks one shuffle returns.
const ShuffleLimit = 500

// ShuffleSource is what a shuffle plays: Kind "all" (the whole library),
// "genre" (albums of Genre, any case) or "artist" (the albums of the artist
// item ArtistID plus the tracks tagged ArtistName on other albums, the
// AppearsOn rule).
type ShuffleSource struct {
	Kind       string
	Genre      string
	ArtistID   int64
	ArtistName string
}

// errShuffleKind: a ShuffleSource with an unknown Kind (a caller bug).
var errShuffleKind = errors.New("music: unknown shuffle source")

// ShuffleTrackIDs returns up to limit distinct track ids of src in random
// order: tracks of non-missing albums whose root is allowed for maxRating
// ("" = no limit). An unknown or blank genre has no tracks.
func (s *Store) ShuffleTrackIDs(ctx context.Context, src ShuffleSource, maxRating string, limit int) ([]int64, error) {
	q, args := visibleTracksQuery(maxRating)
	switch src.Kind {
	case "all":
	case "genre":
		ids, n, err := s.genreAlbumIDs(ctx, src.Genre, maxRating)
		if err != nil {
			return nil, err
		}
		if n == 0 { // unknown or blank genre
			return []int64{}, nil
		}
		q += ` AND al.id IN (SELECT value FROM json_each(?))`
		args = append(args, ids)
	case "artist":
		name := strings.TrimSpace(src.ArtistName)
		if name == "" { // an empty tag must not match untagged tracks
			q += ` AND al.parent_id = ?`
			args = append(args, src.ArtistID)
		} else {
			q += ` AND (al.parent_id = ? OR t.artist = ? COLLATE NOCASE)`
			args = append(args, src.ArtistID, name)
		}
	default:
		return nil, errShuffleKind
	}
	if limit <= 0 || limit > ShuffleLimit {
		limit = ShuffleLimit
	}
	args = append(args, limit)
	return s.ids(ctx, q+` ORDER BY random() LIMIT ?`, args...)
}
