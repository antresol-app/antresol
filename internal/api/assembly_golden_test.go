package api

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/music"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/assembly from the current answers")

// TestAssemblyGolden: the answers of the calls whose assembly is batched
// (series detail, the grids, home, search, music home, an artist) are
// byte for byte those of testdata/assembly, recorded before the batching.
// A deliberate change of an answer rewrites them: go test -run
// AssemblyGolden -update-golden.
func TestAssemblyGolden(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	probe := func(w, h int, dur int64) string {
		b, _ := json.Marshal(media.ProbeResult{Container: "matroska,webm", DurationMs: dur,
			Video: &media.VideoStream{Codec: "hevc", Width: w, Height: h, BitDepth: 10},
			Audio: []media.AudioStream{{Index: 1, Codec: "eac3", Lang: "rus", Title: "Дубляж", Channels: 6, Default: true}, {Index: 2, Codec: "ac3", Lang: "eng", Channels: 2}},
			Subs:  []media.SubStream{{Index: 3, Codec: "subrip", Lang: "rus"}, {Index: 4, Codec: "subrip", Lang: "eng"}}})
		return string(b)
	}
	setProbe := func(item int64, w, h int, dur int64) {
		t.Helper()
		fs, err := e.repo.FilesOf(ctx, item)
		if err != nil || len(fs) == 0 {
			t.Fatal(err, fs)
		}
		for _, f := range fs {
			if err := e.repo.SetProbe(ctx, f.ID, probe(w, h, dur), dur); err != nil {
				t.Fatal(err)
			}
		}
	}
	// films
	var movies []int64
	for i, title := range []string{"Брат", "Брат 2", "Фильм про филина", "Сталкер", "Солярис", "Зеркало"} {
		id, _ := e.movie(title, []string{"16+", "18+", "12+"}[i%3], fmt.Sprintf("film %d", i))
		setProbe(id, []int{1920, 3840, 1280}[i%3], []int{1080, 2160, 720}[i%3], int64(5_400_000+i*60_000))
		movies = append(movies, id)
	}
	// series: two seasons of «Тьма», one of «Филин»
	episode := func(series string, season, ep int) int64 {
		t.Helper()
		p := filepath.Join(e.media, fmt.Sprintf("%s s%02de%02d.mkv", series, season, ep))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: series, SeriesRating: "16+", SeriesYear: 2017,
			Season: season, Episode: ep, Title: fmt.Sprintf("Серия %d", ep), File: library.FileInput{Path: p, Size: 1, Mtime: 1}})
		if err != nil {
			t.Fatal(err)
		}
		setProbe(id, 1920, 1080, int64(3_000_000+ep*1000))
		return id
	}
	var dark, owl []int64
	for s := 1; s <= 2; s++ {
		for ep := 1; ep <= 4; ep++ {
			dark = append(dark, episode("Тьма", s, ep))
		}
	}
	for ep := 1; ep <= 3; ep++ {
		owl = append(owl, episode("Филин", 1, ep))
	}
	saga := episode("Сага", 2, 5)
	darkEp, _ := e.repo.Item(ctx, dark[0])
	darkSeason, _ := e.repo.Item(ctx, darkEp.ParentID)
	darkSeries := darkSeason.ParentID
	// books, an audiobook, comics
	book := e.book("Мастер и Маргарита", ".epub", ".fb2")
	e.book("Собачье сердце", ".epub")
	audiobook, _ := e.audiobook("Война и мир", 3)
	_, vols := e.comic("Берсерк", "18+", 3)
	// music
	_, kinoTracks := e.simpleAlbum("Кино", "Группа крови", 4)
	e.simpleAlbum("Кино", "Звезда по имени Солнце", 3)
	_, aqTracks := e.simpleAlbum("Аквариум", "Радио Африка", 3)
	compDir := filepath.Join(e.media, "music", "Various", "Сборник")
	var comp []library.TrackInput
	for i, artist := range []string{"Кино", "Аквариум", "кино"} {
		p := filepath.Join(compDir, fmt.Sprintf("%d.flac", i+1))
		os.MkdirAll(compDir, 0o755)
		os.WriteFile(p, []byte("audio"), 0o644)
		comp = append(comp, library.TrackInput{File: library.FileInput{Path: p, Size: 5, Mtime: 1}, AlbumDir: compDir, Album: "Сборник",
			AlbumArtist: "Various Artists", Artist: artist, Title: fmt.Sprintf("Песня %d", i+1), Track: i + 1, Year: 2001, Codec: "flac"})
	}
	if _, err := e.repo.UpsertAlbumTracks(ctx, "Various Artists", comp); err != nil {
		t.Fatal(err)
	}
	// fixed times everywhere: the answers must not depend on when the test runs
	// (progress within «Семья смотрит»'s 30 days: relative to now, but no
	// answer carries a time, only the order)
	base := time.Now().UTC().Truncate(time.Hour).Add(-72 * time.Hour)
	e.mustExec(`UPDATE items SET added_at=strftime('%Y-%m-%dT%H:%M:%SZ', '2026-09-01', '+'||id||' hours'),
		updated_at=strftime('%Y-%m-%dT%H:%M:%SZ', '2026-09-01', '+'||id||' hours')`)
	uid := e.meID(e.admin)
	for i, p := range []struct{ item, pos, dur int64 }{
		{movies[0], 600_000, 5_400_000}, {movies[3], 5_390_000, 5_400_000}, {dark[0], 3_001_000, 3_001_000},
		{dark[1], 1_000_000, 3_002_000}, {owl[0], 3_001_000, 3_001_000}, {book, 3, 10}, {audiobook, 40_000, 100_000}, {vols[0].id, 2, 3},
	} {
		if err := e.prog.Save(ctx, uid, p.item, p.pos, p.dur); err != nil {
			t.Fatal(err)
		}
		e.mustExec(`UPDATE progress SET updated_at=? WHERE user_id=? AND item_id=?`,
			base.Add(time.Duration(i)*time.Hour).Format("2006-01-02T15:04:05.000000000Z"), uid, p.item)
	}
	e.mustExec(`UPDATE progress SET finished=1 WHERE item_id IN (?,?,?)`, movies[3], dark[0], owl[0])
	for _, id := range []int64{movies[1], darkSeries, book} {
		if err := e.prog.AddToWatchlist(ctx, uid, id); err != nil {
			t.Fatal(err)
		}
	}
	for i, tr := range []int64{kinoTracks[0], aqTracks[1], kinoTracks[2], kinoTracks[0]} {
		if err := music.New(e.db).Played(ctx, uid, tr, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// a profile limited to 16+: «Брат 2» (18+) and «Берсерк» (18+) are hidden
	kidCookie := e.kid("16+")
	kidID := e.meID(kidCookie)
	for i, p := range []struct{ item, pos, dur int64 }{
		{movies[1], 600_000, 5_400_000}, {movies[0], 700_000, 5_400_000}, {dark[2], 1_000_000, 3_002_000}, {owl[1], 3_002_000, 3_002_000},
		{book, 5, 10}, {vols[1].id, 1, 3}, {audiobook, 10_000, 100_000}, {movies[4], 100_000, 5_400_000},
		{saga, 200_000, 3_005_000}, {movies[5], 300_000, 5_400_000},
	} {
		if err := e.prog.Save(ctx, kidID, p.item, p.pos, p.dur); err != nil {
			t.Fatal(err)
		}
		e.mustExec(`UPDATE progress SET updated_at=? WHERE user_id=? AND item_id=?`,
			base.Add(time.Duration(i)*time.Hour).Format("2006-01-02T15:04:05.000000000Z"), kidID, p.item)
	}
	e.mustExec(`UPDATE progress SET finished=1 WHERE user_id=? AND item_id=?`, kidID, owl[1])
	for _, id := range []int64{movies[1], movies[2], darkSeries, vols[0].id, book} {
		if err := e.prog.AddToWatchlist(ctx, kidID, id); err != nil {
			t.Fatal(err)
		}
	}
	for i, tr := range []int64{aqTracks[0], kinoTracks[1]} {
		if err := music.New(e.db).Played(ctx, kidID, tr, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var kino int64
	if err := e.db.QueryRow(`SELECT id FROM items WHERE kind='artist' AND title='Кино'`).Scan(&kino); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"series-detail":  "/api/items/" + itoa(darkSeries),
		"season-detail":  "/api/items/" + itoa(darkSeason.ID),
		"episode-detail": "/api/items/" + itoa(dark[1]),
		"movie-detail":   "/api/items/" + itoa(movies[0]),
		"series-grid":    "/api/items?kind=series&limit=120",
		"movie-grid":     "/api/items?kind=movie&sort=recent&limit=100",
		"home":           "/api/home",
		"home-family":    "/api/home?family=1",
		"search-fil":     "/api/search?q=%D1%84%D0%B8%D0%BB",
		"search-kino":    "/api/search?q=%D0%BA%D0%B8%D0%BD%D0%BE",
		"search-brat":    "/api/search?q=%D0%B1%D1%80%D0%B0%D1%82",
		"search-none":    "/api/search?q=zzz",
		"music-home":     "/api/music/home",
		"artist":         "/api/music/artists/" + itoa(kino),
		"books-home":     "/api/books/home",
	}
	kidPaths := map[string]string{
		"kid-home":          "/api/home",
		"kid-home-family":   "/api/home?family=1",
		"kid-series-detail": "/api/items/" + itoa(darkSeries),
		"kid-series-grid":   "/api/items?kind=series&limit=120",
		"kid-search-brat":   "/api/search?q=%D0%B1%D1%80%D0%B0%D1%82",
		"kid-books-home":    "/api/books/home",
		"kid-music-home":    "/api/music/home",
	}
	dir := filepath.Join("testdata", "assembly")
	if *updateGolden {
		os.MkdirAll(dir, 0o755)
	}
	type call struct {
		path string
		c    *http.Cookie
	}
	calls := map[string]call{}
	for name, path := range paths {
		calls[name] = call{path, e.admin}
	}
	for name, path := range kidPaths {
		calls[name] = call{path, kidCookie}
	}
	for name, c := range calls {
		path := c.path
		w := e.do("GET", path, "", c.c)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		got := w.Body.Bytes()
		file := filepath.Join(dir, name+".json")
		if *updateGolden {
			if err := os.WriteFile(file, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s (%s) differs from %s:\n got %s\nwant %s", name, path, file, goldenClip(got), goldenClip(want))
		}
	}
}

func goldenClip(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}
