package music

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/music/musictest"
)

func (e *env) shuffleLib() musictest.Shuffle {
	e.t.Helper()
	return musictest.ShuffleLib(e.t, e.repo, e.s.db, e.dir)
}

func (e *env) shuffle(src ShuffleSource, maxRating string) []int64 {
	e.t.Helper()
	ids, err := e.s.ShuffleTrackIDs(context.Background(), src, maxRating, ShuffleLimit)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(musictest.Set(ids)) != len(ids) {
		e.t.Fatalf("%+v: repeated ids %v", src, ids)
	}
	return ids
}

func TestShuffleSources(t *testing.T) {
	e := newEnv(t)
	f := e.shuffleLib()
	set, same := musictest.Set, musictest.SameSet
	if got := e.shuffle(ShuffleSource{Kind: "all"}, ""); !same(got, set(f.First, f.Comp, f.Adult, f.NoArtist)) {
		t.Fatalf("all %v", got)
	}
	if got := e.shuffle(ShuffleSource{Kind: "genre", Genre: "рок"}, ""); !same(got, set(f.First, f.Adult, f.NoArtist)) {
		t.Fatalf("genre %v", got)
	}
	if got := e.shuffle(ShuffleSource{Kind: "artist", ArtistID: f.TestArtist, ArtistName: "Тестовый артист"}, ""); !same(got, set(f.First, []int64{f.CompTest})) {
		t.Fatalf("artist %v", got)
	}
	if got := e.shuffle(ShuffleSource{Kind: "artist", ArtistID: f.OtherArtist, ArtistName: "Другой артист"}, ""); !same(got, set(f.Adult, []int64{f.CompOther})) {
		t.Fatalf("other artist %v", got)
	}
	for _, g := range []string{"Джаз", "", "  "} {
		if got := e.shuffle(ShuffleSource{Kind: "genre", Genre: g}, ""); len(got) != 0 {
			t.Fatalf("genre %q: %v", g, got)
		}
	}
	if _, err := e.s.ShuffleTrackIDs(context.Background(), ShuffleSource{Kind: "bogus"}, "", 10); err == nil {
		t.Fatal("unknown kind must fail")
	}
	// an artist known only from a compilation: their compilation track
	guest := musictest.Artist(t, e.s.db, "Гость")
	musictest.Exec(t, e.s.db, `UPDATE tracks SET artist='Гость' WHERE item_id=?`, f.CompTest)
	if got := e.shuffle(ShuffleSource{Kind: "artist", ArtistID: guest, ArtistName: "Гость"}, ""); !same(got, set([]int64{f.CompTest})) {
		t.Fatalf("compilation-only artist %v", got)
	}
}

// RandomTracks (Subsonic) shares the visible-tracks query and the genre
// lookup with the shuffle.
func TestRandomTracksSharesShuffleRules(t *testing.T) {
	e := newEnv(t)
	f := e.shuffleLib()
	ctx := context.Background()
	got, err := e.s.RandomTracks(ctx, 100, "РОК", 0, 0, "12+")
	if err != nil || !musictest.SameSet(got, musictest.Set(f.First)) {
		t.Fatalf("kid rock %v %v", got, err)
	}
	if got, _ = e.s.RandomTracks(ctx, 100, "Джаз", 0, 0, ""); len(got) != 0 {
		t.Fatalf("unknown genre %v", got)
	}
	if got, _ = e.s.RandomTracks(ctx, 100, "", 0, 0, ""); !musictest.SameSet(got, musictest.Set(f.First, f.Comp, f.Adult, f.NoArtist)) {
		t.Fatalf("all %v", got)
	}
}

func TestShuffleKidNeverHearsHidden(t *testing.T) {
	e := newEnv(t)
	f := e.shuffleLib()
	hidden := musictest.Set(f.Adult, f.NoArtist, f.Missing)
	sawComp := false
	for i := 0; i < 50; i++ {
		for _, src := range []ShuffleSource{{Kind: "all"}, {Kind: "genre", Genre: "Рок"}} {
			for _, id := range e.shuffle(src, "12+") {
				if hidden[id] {
					t.Fatalf("kid %+v got hidden %d", src, id)
				}
				sawComp = sawComp || (src.Kind == "all" && id == f.CompOther)
			}
		}
	}
	if !sawComp {
		t.Fatal("the compilation track by «Другой артист» belongs to the kid's «все»")
	}
}

func TestShuffleAlbumRatingWithoutArtist(t *testing.T) {
	e := newEnv(t)
	f := e.shuffleLib()
	if got := musictest.Set(e.shuffle(ShuffleSource{Kind: "all"}, "12+")); got[f.NoArtist[0]] {
		t.Fatal("kid hears the 18+ album without an artist")
	}
	if got := musictest.Set(e.shuffle(ShuffleSource{Kind: "all"}, "")); !got[f.NoArtist[0]] {
		t.Fatal("admin misses the album without an artist")
	}
}

func TestShuffleCapAndDistinct(t *testing.T) {
	e := newEnv(t)
	artists := make([]string, 600)
	for i := range artists {
		artists[i] = "Много"
	}
	musictest.GenreAlbum(t, e.repo, e.dir, "Много", "Большой", "Рок", artists...)
	a := e.shuffle(ShuffleSource{Kind: "all"}, "")
	b := e.shuffle(ShuffleSource{Kind: "all"}, "")
	if len(a) != ShuffleLimit || len(b) != ShuffleLimit {
		t.Fatalf("len %d %d", len(a), len(b))
	}
	same := true
	for i := 0; i < 20; i++ {
		same = same && a[i] == b[i]
	}
	if same {
		t.Fatal("two shuffles start the same")
	}
}
