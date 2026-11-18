package api

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/releaseprefs"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/antresol-app/antresol/internal/vofit"
)

var updateAddonGolden = flag.Bool("update", false, "rewrite addonkit/testdata/golden from the core API's answers")

// addonGoldenDir: where addonkit/addonkittest replays them from.
const addonGoldenDir = "../../addonkit/testdata/golden"

// addonGolden is one recorded answer (addonkittest.Golden).
type addonGolden struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

var (
	goldenTime = regexp.MustCompile(`"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)"`)
	// the scanner's change signature hashes the (temporary) paths
	goldenSig = regexp.MustCompile(`"scan_sig":"[0-9a-f]+"`)
	// a profile's uid is random: the golden keeps its shape
	goldenUID = regexp.MustCompile(`"uid":"[0-9a-f]{32}"`)
)

// TestAddonCoreGoldens records (-update) or checks the core API's answers
// to every call the addons' adapters make, over a library the scanner
// builds from files in a temporary folder: addonkit/testdata/golden, one
// file per call, replayed by addonkittest.ReplayGolden. The folder reads
// {root}, timestamps {time} and the scanner's signatures {sig}; the
// answers are compared as JSON.
func TestAddonCoreGoldens(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "media")
	movies, series, music, books := filepath.Join(root, "movies"), filepath.Join(root, "series"),
		filepath.Join(root, "music"), filepath.Join(root, "books")
	stamp := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	put := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	matrix := filepath.Join(movies, "Матрица (1999)", "Матрица (1999).mkv")
	put(matrix, "video")
	put(filepath.Join(movies, "Матрица (1999)", "movie.nfo"),
		`<movie><title>Матрица</title><originaltitle>The Matrix</originaltitle><year>1999</year><tmdbid>603</tmdbid></movie>`)
	show := filepath.Join(series, "Сёгун (2024)")
	put(filepath.Join(show, "tvshow.nfo"), `<tvshow><title>Сёгун</title><year>2024</year><tmdbid>126308</tmdbid></tvshow>`)
	ep1, ep2 := filepath.Join(show, "Season 1", "Сёгун.S01E01.mkv"), filepath.Join(show, "Season 1", "Сёгун.S01E02.mkv")
	put(ep1, "video")
	put(ep2, "video")
	album := filepath.Join(music, "Кино", "Группа крови")
	put(filepath.Join(album, "01 - Группа крови.mp3"), "audio")
	put(filepath.Join(album, "02 - Закрой за мной дверь.mp3"), "audio")
	put(filepath.Join(books, "Михаил Булгаков", "Мастер и Маргарита.fb2"),
		`<?xml version="1.0" encoding="utf-8"?><FictionBook><description><title-info><author><first-name>Михаил</first-name>`+
			`<last-name>Булгаков</last-name></author><book-title>Мастер и Маргарита</book-title><lang>ru</lang></title-info></description>`+
			`<body><section><p>x</p></section></body></FictionBook>`)
	dune := filepath.Join(movies, "Дюна (2021)", "Дюна (2021).mkv") // brought in by the import below

	var (
		lib     *library.Repo
		scanner *library.Scanner
		track   *tracking.Store
		imports *playlistimport.Service
	)
	e := newAddonEnv(t, func(d *Deps) {
		lib = d.Library
		track = &tracking.Store{DB: d.DB}
		d.Tracking = &tracking.Service{Store: track}
		scanner = &library.Scanner{Repo: d.Library, Movies: movies, Series: series, Music: music, Ebooks: books}
		d.MediaRoot = root
		d.Jobs = &library.Jobs{Scanner: scanner}
		d.AddonRefs = &addons.RefStore{DB: d.DB}
		d.Files = files.NewService(files.ServiceConfig{DB: d.DB, Sandbox: files.NewSandbox(root, []files.Root{
			{ID: "movies", Title: "Фильмы", Dir: movies},
			{ID: "series", Title: "Сериалы", Dir: series},
			{ID: "music", Title: "Музыка", Dir: music},
			{ID: "books", Title: "Книги", Dir: books},
			{ID: "disk", Title: "Весь диск", Dir: root, AdminOnly: true},
		})})
		d.FFmpeg, d.FFprobe = "/usr/bin/ffmpeg", "/usr/bin/ffprobe"
		ist := &playlistimport.Store{DB: d.DB, Now: func() time.Time { return stamp }}
		imports = &playlistimport.Service{Store: ist, Bus: d.Events, Library: d.Library}
		imports.Queue = playlistimport.NewAddonDownloader(ist, d.Library, func() (bool, error) { return true, nil })
		d.PlaylistImport = imports
		d.VoiceOverFit = &vofit.Worker{DB: d.DB} // queues; never runs here
	})
	kit := e.fake.installed["hello"]
	kit.ID, kit.Manifest.ID, kit.Manifest.Permissions = "kit", "kit", []string{"library.read", "library.write", "users.read", "tracking", "imports",
		"events", "voiceover", "progress"}
	e.fake.mu.Lock()
	e.fake.installed["kit"], e.fake.tokens["tok-kit"] = kit, "kit"
	e.fake.mu.Unlock()

	ctx := context.Background()
	// the kid's own limit, in the canonical form the users golden shows
	// (the fixture stores the older «PG» for it)
	if _, err := e.db.Exec(`UPDATE users SET max_rating='6+' WHERE name='Вася'`); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	put(dune, "video")
	fileID := func(p string) int64 {
		t.Helper()
		f, err := lib.FileByPath(ctx, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return f.ID
	}
	if err := lib.SetProbe(ctx, fileID(matrix), `{"Audio":[{"Title":"Дубляж"}]}`, 8160000); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetProbe(ctx, fileID(ep1), `{"Audio":[{"Title":"Дубляж"},{"Title":"Оригинал"}]}`, 3480000); err != nil {
		t.Fatal(err)
	}
	if err := (releaseprefs.Store{DB: e.db}).SetStudios(ctx, []string{"Кубик в Кубе", "LostFilm"}); err != nil {
		t.Fatal(err)
	}
	var masha int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE name='Маша'`).Scan(&masha); err != nil {
		t.Fatal(err)
	}
	movieItem, err := lib.ItemByTMDB(ctx, library.KindMovie, 603)
	if err != nil {
		t.Fatal(err)
	}
	showItem, err := lib.ItemByTMDB(ctx, library.KindSeries, 126308)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO series_tracks(user_id, series_id, has_audio, audio_title, updated_at) VALUES(?,?,1,'Оригинал','2026-11-02T10:00:00Z')`,
		masha, showItem.ID); err != nil {
		t.Fatal(err)
	}
	if err := progress.New(e.db).MarkWatched(ctx, masha, movieItem.ID, true); err != nil {
		t.Fatal(err)
	}
	albumItem, found, err := lib.FindAlbum(ctx, "Кино", "Группа крови")
	if err != nil || !found {
		t.Fatalf("album: %v %v", found, err)
	}
	// the tracked series: its episode list and sources of three kinds
	shogun, err := track.EnsureTitle(ctx, "series", 126308, "Сёгун", "/shogun.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := track.SetChecked(ctx, shogun.ID, "ended", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := track.SetInfo(ctx, shogun.ID, tracking.TitleInfo{Cert: "18+"}); err != nil {
		t.Fatal(err)
	}
	ep1File, err := lib.FileByPath(ctx, ep1)
	if err != nil {
		t.Fatal(err)
	}
	putTrackingSources(t, track, shogun, ep1File.ItemID, ep1File.ID)
	ep2File, err := lib.FileByPath(ctx, ep2)
	if err != nil {
		t.Fatal(err)
	}

	// the imports: a downloading job (rg-one: a, b; rg-two: x), two
	// reading jobs made ahead (one the read fails, one the addon's restart
	// fails), and the one the calls make next (its id follows theirs)
	ij := makeImportsJob(t, imports.Store, masha)
	readFails, err := imports.Store.CreateReadingJob(ctx, masha, "apple")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := imports.Store.CreateReadingJob(ctx, masha, "apple")
	if err != nil {
		t.Fatal(err)
	}
	reading := restarted + 1
	albumTracks, err := lib.AlbumTracks(ctx, albumItem.ID)
	if err != nil || len(albumTracks) != 2 {
		t.Fatalf("album tracks %v %v", albumTracks, err)
	}
	g1, g2 := ij.groups["rg-one"], ij.groups["rg-two"]
	ta, tb, tx := ij.tracks["a"], ij.tracks["b"], ij.tracks["x"]
	gp := func(g int64, op string) string { return fmt.Sprintf("/imports/groups/%d/%s", g, op) }

	// q is a query whose values may read {root}.
	q := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}
	const refA, refB, refC = "k-0001", "k/0002", "k-0003"           // a ref holding '/' too
	dubbed := "{root}/movies/Матрица (1999)/Матрица (1999).rus.mka" // a placed voice-over: no file needed
	type call struct {
		name, method, path string
		query              url.Values
		body               any
	}
	calls := []call{
		// writes first: the reads below see their effect
		{"import", "POST", "/library/import", nil, map[string]any{"paths": []string{"{root}/movies/Дюна (2021)"}, "ref": refA, "ownerUserId": masha}},
		{"refs-batch", "POST", "/library/refs", nil, map[string]any{"refs": []map[string]string{
			{"ref": refB, "path": "{root}/movies/Матрица (1999)/Матрица (1999).mkv"}, {"ref": refB, "path": "{root}/movies/Нет (2000)/x.mkv"}}}},
		{"owners", "POST", "/library/owners", nil, map[string]any{"ref": refA, "userId": masha}},
		{"info", "GET", "/info", nil, nil},
		{"item", "GET", fmt.Sprintf("/library/items/%d", movieItem.ID), nil, nil},
		{"item-files", "GET", fmt.Sprintf("/library/items/%d/files", showItem.ID), nil, nil},
		{"tmdb-item", "GET", "/library/tmdb/movie/603", nil, nil},
		{"tmdb-ids", "GET", "/library/tmdb/series", q("ids", "126308,603,7"), nil},
		{"find-title", "GET", "/library/find/title", q("kind", "movie", "tmdbId", "603"), nil},
		{"find-album", "GET", "/library/find/album", q("artist", "Кино", "album", "Группа крови"), nil},
		{"find-book", "GET", "/library/find/book", q("kind", "book", "author", "Михаил Булгаков", "title", "Мастер и Маргарита"), nil},
		{"series-episodes", "GET", "/library/series/126308/episodes", nil, nil},
		{"series-episode-items", "GET", "/library/series/126308/episode-items", q("userId", fmt.Sprint(masha)), nil},
		{"series-folder", "GET", "/library/series/126308/folder", q("root", "{root}/series"), nil},
		{"series-audio", "GET", "/library/series/126308/audio", nil, nil},
		{"album-dir", "GET", fmt.Sprintf("/library/albums/%d/dir", albumItem.ID), nil, nil},
		{"files-under", "GET", "/library/files", q("under", "{root}/movies"), nil},
		{"file-by-path", "GET", "/library/files", q("path", "{root}/movies/Матрица (1999)/Матрица (1999).mkv"), nil},
		{"file-by-id", "GET", fmt.Sprintf("/library/files/%d", fileID(matrix)), nil, nil},
		{"paths", "GET", "/library/paths", nil, nil},
		{"search", "GET", "/library/search", q("kind", "movie", "title", "матрица"), nil},
		{"ref-files", "GET", "/library/refs/" + url.PathEscape(refA), nil, nil},
		{"users", "GET", "/users", nil, nil},
		// the profile's position in an episode, at a fixed time (its answer keeps it)
		{"progress-put", "PUT", "/progress", nil, map[string]any{"userId": masha, "itemId": ep1File.ItemID, "positionMs": 600000, "durationMs": 3480000,
			"at": stamp.Format(time.RFC3339), "played": false}},
		{"progress", "GET", "/progress", q("userId", fmt.Sprint(masha), "items", fmt.Sprintf("%d,%d", ep1File.ItemID, ep2File.ItemID)), nil},
		{"watched", "GET", "/watched", q("userId", fmt.Sprint(masha), "items", fmt.Sprintf("%d", movieItem.ID), "series", fmt.Sprint(showItem.ID)), nil},
		{"studios", "GET", "/settings/studios", nil, nil},
		{"tracking-title", "POST", "/tracking/titles", nil, map[string]any{"kind": "series", "tmdbId": 126308, "name": "Сёгун", "poster": "/shogun.jpg"}},
		{"tracking-episodes", "GET", fmt.Sprintf("/tracking/titles/%d/episodes", shogun.ID), nil, nil},
		{"tracking-sources", "GET", fmt.Sprintf("/tracking/titles/%d/sources", shogun.ID), nil, nil},
		// the import queue, in the order a queue makes them
		{"imports-active-count", "GET", "/imports/active-count", nil, nil},
		{"imports-job-ids", "GET", "/imports/jobs", q("status", "downloading"), nil},
		{"imports-job", "GET", fmt.Sprintf("/imports/jobs/%d", ij.id), nil, nil},
		{"imports-candidates", "GET", fmt.Sprintf("/imports/jobs/%d/candidates", ij.id), q("retryBefore", "2026-11-01T12:00:00Z"), nil},
		{"imports-groups", "GET", fmt.Sprintf("/imports/jobs/%d/groups", ij.id), q("state", "waiting"), nil},
		{"imports-still-downloading", "GET", fmt.Sprintf("/imports/jobs/%d/still-downloading", ij.id), nil, nil},
		{"imports-group-tracks", "GET", gp(g1, "tracks"), q("state", "matched"), nil},
		{"imports-move", "POST", gp(g2, "move"), nil, map[string]any{"from": "waiting", "to": "searching"}},
		{"imports-start", "POST", gp(g2, "start"), nil, map[string]any{"key": "k-0001", "bytes": 1000, "downloading": []int64{tx}, "missing": []int64{}}},
		{"imports-progress", "POST", gp(g2, "progress"), nil, map[string]any{"done": 250}},
		{"imports-clock", "POST", gp(g2, "clock"), nil, map[string]any{"key": "k-0001", "clock": map[string]any{"runSec": 60, "quietSec": 0, "base": 0, "done": 250}}},
		{"imports-given-up", "POST", gp(g2, "given-up"), nil, map[string]any{"key": "k-0001", "slow": false}},
		{"imports-requeue", "POST", gp(g2, "requeue"), nil, map[string]any{"key": "k-0001", "msg": "", "maxDead": 3}},
		{"imports-wait", "POST", gp(g2, "wait"), nil, map[string]any{"key": "k-0002", "name": "Кино — Звезда по имени Солнце", "done": 10}},
		{"imports-give-up-wait", "POST", gp(g2, "give-up-wait"), nil, map[string]any{"key": "k-0002"}},
		{"imports-no-format", "POST", gp(g2, "no-format"), nil, map[string]any{"trackIds": []int64{tx}, "msg": "Нет релиза в нужном формате"}},
		{"imports-fail", "POST", gp(g2, "fail"), nil, map[string]any{"trackIds": []int64{tx}, "msg": "Ошибка", "retry": true}},
		{"imports-not-found", "POST", gp(g2, "not-found"), nil, map[string]any{"trackIds": []int64{tx}, "msg": "Подходящих релизов не нашлось"}},
		{"imports-have", "POST", gp(g1, "have"), nil, map[string]any{"have": map[string]int64{fmt.Sprint(ta): albumTracks[0].ID}, "album": albumItem.ID, "done": false}},
		{"imports-finish", "POST", gp(g1, "finish"), nil, map[string]any{"done": map[string]int64{fmt.Sprint(tb): albumTracks[1].ID}, "failed": []int64{}, "album": albumItem.ID}},
		{"imports-rebuild", "POST", fmt.Sprintf("/imports/jobs/%d/rebuild", ij.id), nil, nil},
		{"imports-groups-all", "GET", fmt.Sprintf("/imports/jobs/%d/groups", ij.id), nil, nil},
		{"imports-open", "GET", "/imports/open", q("userId", fmt.Sprint(masha), "service", "file"), nil},
		// reading jobs
		{"imports-create", "POST", "/imports/jobs", nil, map[string]any{"userId": masha, "service": "apple", "reading": true}},
		{"imports-reads", "GET", "/imports/reads", q("userId", fmt.Sprint(masha)), nil},
		{"imports-snapshot", "PUT", fmt.Sprintf("/imports/jobs/%d/snapshot", reading), nil, map[string]any{"snapshot": map[string]any{
			"service": "apple", "account": "masha", "tracks": []map[string]any{{"key": "t1", "artist": "Кино", "title": "Кукушка", "liked": true}},
			"playlists": []map[string]any{{"id": "p1", "name": "Дорога", "readable": true, "entries": []string{"t1"}}}}}},
		{"imports-job-fail", "POST", fmt.Sprintf("/imports/jobs/%d/fail", readFails), nil, map[string]any{"error": "Не удалось прочитать медиатеку"}},
		{"imports-fail-reading", "POST", "/imports/jobs/fail-reading", nil, map[string]any{"error": "Не удалось подключиться"}},
		{"imports-delete", "DELETE", fmt.Sprintf("/imports/jobs/%d", restarted), nil, nil},
		// recorded after the rest, so nothing above sees their writes
		{"album-tracks", "GET", fmt.Sprintf("/library/albums/%d/tracks", albumItem.ID), nil, nil},
		{"tracking-title-tmdb", "GET", "/tracking/titles", q("kind", "series", "tmdbId", "126308"), nil},
		{"tracking-title-id", "GET", fmt.Sprintf("/tracking/titles/%d", shogun.ID), nil, nil},
		{"tracking-kick", "POST", fmt.Sprintf("/tracking/titles/%d/kick", shogun.ID), nil, nil},
		{"event", "POST", "/events", nil, map[string]any{"name": "ping", "data": map[string]int{"n": 1}}},
		{"refs-replace", "PUT", "/library/refs/" + url.PathEscape(refC), nil, map[string]any{"paths": []string{"{root}/music/Кино/Группа крови/01 - Группа крови.mp3"}}},
		{"ref-tracks", "GET", "/library/tracks", q("ref", refC), nil},
		{"refs-delete", "DELETE", "/library/refs/" + url.PathEscape(refC), nil, nil},
		{"voiceover-queue", "POST", "/voiceover/fit", nil, map[string]any{"path": dubbed, "fileId": fileID(matrix), "origLang": "en"}},
		{"voiceover-state", "GET", "/voiceover/fit", q("path", dubbed), nil},
	}
	real := func(s string) string { return strings.ReplaceAll(s, "{root}", root) }
	var names []string
	for _, c := range calls {
		reqPath, tmplPath := "/api/addon-core/v1"+c.path, "/api/addon-core/v1"+c.path
		if len(c.query) > 0 {
			rq := url.Values{}
			for k, vs := range c.query {
				for _, v := range vs {
					rq.Add(k, real(v))
				}
			}
			reqPath += "?" + rq.Encode()
			tmplPath += "?" + c.query.Encode()
		}
		var reqBody string
		if c.body != nil {
			b, _ := json.Marshal(c.body)
			reqBody = real(string(b))
		}
		w := e.do(c.method, reqPath, reqBody, nil, "Authorization", "Bearer tok-kit", "Content-Type", "application/json")
		// the tracking, import and progress rows' times are this test's own,
		// fixed: addonkit decodes them
		keepTimes := strings.HasPrefix(c.name, "tracking-") || strings.HasPrefix(c.name, "imports-") || strings.HasPrefix(c.name, "progress")
		checkAddonGolden(t, c.name, c.method, reqPath, tmplPath, root, w, keepTimes)
		names = append(names, c.name)
	}
	names = append(names, serviceGoldens(t)...)
	checkNoStaleGoldens(t, names)
	// the recorded files are what addonkit decodes
	if !*updateAddonGolden {
		var info addonkit.Info
		raw, _ := os.ReadFile(filepath.Join(addonGoldenDir, "info.json"))
		var g addonGolden
		json.Unmarshal(raw, &g)
		if err := json.Unmarshal(g.Body, &info); err != nil || info.MediaRoot != "{root}" || len(info.FileRoots) != 5 {
			t.Fatalf("info golden %+v %v", info, err)
		}
	}
}

// checkAddonGolden records (-update) or checks the golden file name: the
// answer w to method reqPath, filed under tmplPath (the path as an
// addon's client writes it, {root} unexpanded). The answer's root reads
// {root}, its timestamps {time} unless keepTimes, and scrub's matches
// their placeholders (random ids and names).
func checkAddonGolden(t *testing.T, name, method, reqPath, tmplPath, root string, w *httptest.ResponseRecorder, keepTimes bool, scrub ...goldenScrub) {
	t.Helper()
	if w.Code/100 != 2 {
		t.Fatalf("%s: %s %s: %d %s", name, method, reqPath, w.Code, w.Body)
	}
	body := bytes.TrimSpace(w.Body.Bytes())
	if root != "" {
		body = bytes.ReplaceAll(body, []byte(root), []byte("{root}"))
	}
	if !keepTimes {
		body = scrubTimes(body)
	}
	body = goldenSig.ReplaceAll(body, []byte(`"scan_sig":"{sig}"`))
	body = goldenUID.ReplaceAll(body, []byte(`"uid":"{uid}"`))
	for _, s := range scrub {
		body = s.re.ReplaceAll(body, []byte(s.with))
	}
	if len(body) > 0 && !json.Valid(body) {
		t.Fatalf("%s: not JSON: %s", name, body)
	}
	got := addonGolden{Method: method, Path: tmplPath, Status: w.Code, Body: json.RawMessage(body)}
	file := filepath.Join(addonGoldenDir, name+".json")
	if *updateAddonGolden {
		out, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v (record with -update)", name, err)
	}
	var want addonGolden
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if want.Method != got.Method || want.Path != got.Path || want.Status != got.Status || !sameJSON(t, want.Body, got.Body) {
		t.Errorf("%s: the core's answer changed (re-record with -update if deliberate)\ngot  %s %s %d %s\nwant %s %s %d %s",
			name, got.Method, got.Path, got.Status, got.Body, want.Method, want.Path, want.Status, want.Body)
	}
}

// scrubTimes files each timestamp as "{time}", but for the zero time,
// which stays as it is: a time the core forgot to set shows in the golden.
func scrubTimes(b []byte) []byte {
	return goldenTime.ReplaceAllFunc(b, func(m []byte) []byte {
		if bytes.HasPrefix(m, []byte(`"0001-01-01T00:00:00`)) {
			return m
		}
		return []byte(`"{time}"`)
	})
}

func TestScrubTimes(t *testing.T) {
	in := `{"a":"2026-11-17T00:59:44.867877Z","b":"0001-01-01T00:00:00Z","c":"2026-11-01T12:00:00+03:00"}`
	want := `{"a":"{time}","b":"0001-01-01T00:00:00Z","c":"{time}"}`
	if got := string(scrubTimes([]byte(in))); got != want {
		t.Fatalf("%s", got)
	}
}

// goldenScrub: a random part of an answer and the placeholder it is
// filed as.
type goldenScrub struct {
	re   *regexp.Regexp
	with string
}

// checkNoStaleGoldens: every file of the golden folder is one the
// recorder writes, so a golden whose call went does not count as
// coverage (TestAddonCoreRouteCoverage).
func checkNoStaleGoldens(t *testing.T, names []string) {
	t.Helper()
	want := map[string]bool{}
	for _, n := range names {
		if want[n] {
			t.Errorf("golden %s recorded twice", n)
		}
		want[n] = true
	}
	files, err := filepath.Glob(filepath.Join(addonGoldenDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if n := strings.TrimSuffix(filepath.Base(f), ".json"); !want[n] {
			t.Errorf("golden %s: no call records it (delete the file, or record it)", n)
		}
	}
}

// sameJSON: a and b are the same JSON value (both empty counts too).
func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	if len(bytes.TrimSpace(a)) == 0 || len(bytes.TrimSpace(b)) == 0 {
		return len(bytes.TrimSpace(a)) == len(bytes.TrimSpace(b))
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
