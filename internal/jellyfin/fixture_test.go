package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
)

// libEnv is env with a library: «Интерстеллар» (12+, tmdb 157336, a
// probed mkv, poster), «Дэдпул» (18+), «Мультик» (0+ movie) and the series
// «Маша и Медведь» (0+, poster and backdrop; S01E01–E03, S02E01, episodes
// without stills). «Миша» (6+) sees only «Мультик» and the series.
type libEnv struct {
	*env
	repo     *library.Repo
	prog     *progress.Store
	media    string
	imageDir string

	interstellar, deadpool, cartoon int64
	series, season1, season2        int64
	eps                             []int64 // S01E01, S01E02, S01E03, S02E01

	posterRef, seriesPoster, seriesBackdrop string
	anyaTok, mishaTok                       string
}

func newLibEnv(t *testing.T) *libEnv {
	t.Helper()
	e := &libEnv{env: newEnv(t), media: t.TempDir(), imageDir: t.TempDir()}
	e.repo, e.prog = library.NewRepo(e.db), progress.New(e.db)
	e.s.Library, e.s.Progress, e.s.ImageDir, e.s.DataDir = e.repo, e.prog, e.imageDir, t.TempDir()
	ctx := context.Background()

	e.posterRef = e.cacheImage("1", 400, 600)
	e.seriesPoster = e.cacheImage("2", 400, 600)
	e.seriesBackdrop = e.cacheImage("3", 1280, 720)

	probe, _ := json.Marshal(media.ProbeResult{Container: "mkv", DurationMs: 10_140_000,
		Video: &media.VideoStream{Codec: "h264", Width: 1920, Height: 800, BitRate: 8_000_000, PixFmt: "yuv420p", BitDepth: 8},
		Audio: []media.AudioStream{{Index: 1, Codec: "ac3", Lang: "rus", Title: "Дубляж", Channels: 6, Default: true},
			{Index: 2, Codec: "aac", Lang: "en", Channels: 2}},
		Subs: []media.SubStream{{Index: 3, Codec: "subrip", Lang: "rus"}, {Index: 4, Codec: "hdmv_pgs_subtitle", Lang: "eng"}}})
	e.interstellar = e.movie("Интерстеллар", 2014, "12+", 157336, "Interstellar.2014.mkv", string(probe))
	e.deadpool = e.movie("Дэдпул", 2016, "18+", 293660, "Deadpool.2016.mkv", "")
	e.cartoon = e.movie("Мультик", 2010, "0+", 0, "Cartoon.2010.mkv", "")
	if _, err := e.db.Exec(`UPDATE items SET poster=?, meta_json='{"genres":["Фантастика","Драма"]}' WHERE id=?`, e.posterRef, e.interstellar); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=10140000 WHERE item_id=?`, e.interstellar); err != nil {
		t.Fatal(err)
	}
	for _, se := range [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}} {
		p := filepath.Join(e.media, "Маша и Медведь", fmt.Sprintf("S%02dE%02d.mkv", se[0], se[1]))
		e.touch(p)
		id, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Маша и Медведь", SeriesYear: 2009, SeriesRating: "0+",
			SeriesPoster: e.seriesPoster, SeriesBackdrop: e.seriesBackdrop, Season: se[0], Episode: se[1],
			File: library.FileInput{Path: p, Size: 100, Mtime: 1}})
		if err != nil {
			t.Fatal(err)
		}
		e.eps = append(e.eps, id)
	}
	ep, _ := e.repo.Item(ctx, e.eps[0])
	s1, _ := e.repo.Item(ctx, ep.ParentID)
	e.season1, e.series = s1.ID, s1.ParentID
	ep4, _ := e.repo.Item(ctx, e.eps[3])
	e.season2 = ep4.ParentID
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=600000 WHERE item_id IN (?,?,?,?)`, e.eps[0], e.eps[1], e.eps[2], e.eps[3]); err != nil {
		t.Fatal(err)
	}

	if _, err := e.users.SetAppPassword(ctx, e.misha.ID); err != nil {
		t.Fatal(err)
	}
	mishaApp, _, _ := e.users.AppPassword(ctx, e.misha.ID)
	e.anyaTok, e.mishaTok = e.login("Аня", e.anyaApp), e.login("Миша", mishaApp)
	return e
}

// cacheImage writes a JPEG into the image cache and returns its name.
func (e *libEnv) cacheImage(n string, w, h int) string {
	e.t.Helper()
	name := strings.Repeat("a", 39) + n + ".jpg"
	f, err := os.Create(filepath.Join(e.imageDir, name))
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		e.t.Fatal(err)
	}
	return name
}

func (e *libEnv) touch(p string) {
	e.t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *libEnv) movie(title string, year int, rating string, tmdb int64, file, probe string) int64 {
	e.t.Helper()
	p := filepath.Join(e.media, file)
	e.touch(p)
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: title, Year: year, Rating: rating, TmdbID: tmdb,
		File: library.FileInput{Path: p, Size: 4_000_000_000, Mtime: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	if probe != "" {
		if _, err := e.db.Exec(`UPDATE files SET probe_json=? WHERE item_id=?`, probe, id); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

// addedAt sets an item's added_at.
func (e *libEnv) addedAt(id int64, t time.Time) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE items SET added_at=? WHERE id=?`, t.UTC().Format(time.RFC3339), id); err != nil {
		e.t.Fatal(err)
	}
}

// get sends a GET with the Swiftfin header of token tok.
func (e *libEnv) get(tok, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do("GET", path, swiftfinHdr(tok), nil)
}

// infuseHdr is an Emby-style quoted header (Infuse).
func infuseHdr(tok string) map[string]string {
	return map[string]string{"X-Emby-Authorization": `Emby UserId="", Client="Infuse-Direct", Device="Apple TV", DeviceId="ATV-1", Version="8.5.6", Token="` + tok + `"`}
}

type itemsPage struct {
	Items            []BaseItemDto
	TotalRecordCount int
	StartIndex       int
}

// items GETs a QueryResult of items as Аня (tok "" = Аня).
func (e *libEnv) items(tok, path string) itemsPage {
	e.t.Helper()
	if tok == "" {
		tok = e.anyaTok
	}
	w := e.get(tok, path)
	if w.Code != 200 {
		e.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var p itemsPage
	decode(e.t, w, &p)
	if p.Items == nil {
		e.t.Fatalf("%s: Items is null: %s", path, w.Body)
	}
	return p
}

func names(items []BaseItemDto) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func has(items []BaseItemDto, id string) bool {
	for _, it := range items {
		if it.Id == id {
			return true
		}
	}
	return false
}
