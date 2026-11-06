package music

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// recentAlbumsBefore is RecentAlbums's grouped query before the walk of the
// newest plays: its answer is the reference.
const recentAlbumsBefore = `SELECT t.parent_id FROM plays p JOIN items t ON t.id=p.item_id
  JOIN items al ON al.id=t.parent_id AND al.kind='album' AND al.missing_since IS NULL
  WHERE p.user_id=? GROUP BY t.parent_id ORDER BY MAX(p.played_at) DESC, MAX(p.rowid) DESC LIMIT ?`

// The walk answers as the grouped query did: ties on the time broken by
// the album's latest stored play, plays stored late with an earlier time
// (the offline queue), missing albums left out, other users' plays ignored.
func TestRecentAlbumsSameAsBefore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u"), e.user("o")
	var tracks [][]int64
	var albums []int64
	for i := range 12 {
		al, tr := e.album(fmt.Sprintf("A%d", i%4), fmt.Sprintf("Album %d", i), 3, "")
		albums, tracks = append(albums, al), append(tracks, tr)
	}
	if _, err := e.s.db.Exec(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, albums[5]); err != nil {
		t.Fatal(err)
	}
	rnd := rand.New(rand.NewPCG(1, 2))
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for range 300 {
		a := rnd.IntN(len(tracks))
		tr := tracks[a][rnd.IntN(3)]
		// few distinct times: many ties, and later rows with older times
		at := base.Add(time.Duration(rnd.IntN(40)) * time.Minute)
		user := u
		if rnd.IntN(5) == 0 {
			user = other
		}
		if err := e.s.Played(ctx, user, tr, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, 1, 2, 3, 5, 8, 11, 20} {
		got, err := e.s.RecentAlbums(ctx, u, limit)
		if err != nil {
			t.Fatal(err)
		}
		l := limit
		if l <= 0 {
			l = -1
		}
		want, err := e.s.ids(ctx, recentAlbumsBefore, u, l)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limit %d:\n got %v\nwant %v", limit, got, want)
		}
	}
}
