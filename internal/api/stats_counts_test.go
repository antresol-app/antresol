package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
)

// The counts leave out missing items, as the per-kind counts of present
// items did.
func TestLibraryCountsLeaveOutMissing(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	kinds := []string{"movie", "series", "season", "episode", "artist", "album", "track", "book", "audiobook", "comic", "comic_volume"}
	for i := range 90 {
		var missing any
		if i%4 == 0 {
			missing = "2026-01-01T00:00:00Z"
		}
		if _, err := d.Exec(`INSERT INTO items(kind,title,sort_title,added_at,updated_at,missing_since) VALUES(?,'t','t','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?)`,
			kinds[i%len(kinds)], missing); err != nil {
			t.Fatal(err)
		}
	}
	got, err := libraryCounts(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var want statsCounts
	if err := d.QueryRow(`SELECT
		(SELECT COUNT(*) FROM items WHERE kind='movie' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='series' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='episode' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='album' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='track' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='book' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='audiobook' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='comic' AND missing_since IS NULL)`).Scan(&want.Movies, &want.Series, &want.Episodes,
		&want.Albums, &want.Tracks, &want.Books, &want.Audiobooks, &want.Comics); err != nil {
		t.Fatal(err)
	}
	if got != want || want.Movies == 0 || want.Comics == 0 {
		t.Fatalf("counts %+v, want %+v", got, want)
	}
}
