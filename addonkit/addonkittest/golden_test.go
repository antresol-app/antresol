package addonkittest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

func writeGolden(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, method, u string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

func TestReplayGolden(t *testing.T) {
	dir := t.TempDir()
	writeGolden(t, dir, "info", `{"method":"GET","path":"/api/addon-core/v1/info","status":200,"body":{"id":"kit"}}`)
	writeGolden(t, dir, "find", `{"method":"GET","path":"/api/addon-core/v1/library/find/album?artist=%D0%9A%D0%B8%D0%BD%D0%BE&album=x","status":404,"body":{"error":"Не найдено"}}`)
	writeGolden(t, dir, "owners", `{"method":"POST","path":"/api/addon-core/v1/library/owners","status":204}`)
	writeGolden(t, dir, "ref", `{"method":"GET","path":"/api/addon-core/v1/library/refs/a%2Fb","status":200,"body":[]}`)
	srv := ReplayGolden(t, dir)
	base := srv.URL + "/api/addon-core/v1"

	if code, typ, body := get(t, "GET", base+"/info"); code != 200 || typ != "application/json" || body != `{"id":"kit"}` {
		t.Fatalf("info: %d %q %s", code, typ, body)
	}
	// the query in another order and spelling is the same request
	if code, _, body := get(t, "GET", base+"/library/find/album?album=x&artist=Кино"); code != 404 || !strings.Contains(body, "Не найдено") {
		t.Fatalf("find: %d %s", code, body)
	}
	if code, _, body := get(t, "POST", base+"/library/owners"); code != 204 || body != "" {
		t.Fatalf("owners: %d %s", code, body)
	}
	if code, _, body := get(t, "GET", base+"/library/refs/a%2Fb"); code != 200 || body != "[]" {
		t.Fatalf("ref: %d %s", code, body)
	}
	for _, c := range [][2]string{{"POST", "/info"}, {"GET", "/users"}, {"GET", "/info?x=1"},
		{"GET", "/library/find/album?artist=Кино"}, {"GET", "/library/refs/a/b"}} {
		if code, _, _ := get(t, c[0], base+c[1]); code != 404 {
			t.Errorf("%s %s: %d, want 404", c[0], c[1], code)
		}
	}
}

func TestReplayGolden_Duplicate(t *testing.T) {
	dir := t.TempDir()
	writeGolden(t, dir, "a", `{"method":"GET","path":"/x?a=1&b=2","status":200,"body":1}`)
	writeGolden(t, dir, "b", `{"method":"GET","path":"/x?b=2&a=1","status":200,"body":2}`)
	ft := &fatalT{TB: t}
	func() {
		defer func() { recover() }()
		ReplayGolden(ft, dir)
	}()
	if !ft.failed {
		t.Fatal("two answers to one request were accepted")
	}
}

// fatalT records Fatalf instead of failing the test (and stops the call).
type fatalT struct {
	testing.TB
	failed bool
}

func (f *fatalT) Helper() {}
func (f *fatalT) Fatalf(string, ...any) {
	f.failed = true
	panic("fatal")
}

// The core's recorded answers: every route an adapter calls has one, and
// the client reads them.
func TestCoreGoldens(t *testing.T) {
	gs := LoadGoldens(t, GoldenDir())
	want := []string{"info", "item", "item-files", "tmdb-item", "tmdb-ids", "find-title", "find-album", "find-book",
		"series-episodes", "series-episode-items", "series-folder", "series-audio", "album-dir", "files-under", "file-by-path", "file-by-id",
		"paths", "search", "ref-files", "users", "watched", "studios", "import", "refs-batch", "owners",
		"tracking-title", "tracking-episodes", "tracking-sources",
		"imports-active-count", "imports-job-ids", "imports-job", "imports-candidates", "imports-groups", "imports-groups-all",
		"imports-still-downloading", "imports-group-tracks", "imports-move", "imports-start", "imports-progress", "imports-clock",
		"imports-given-up", "imports-requeue", "imports-wait", "imports-give-up-wait", "imports-no-format", "imports-fail",
		"imports-not-found", "imports-have", "imports-finish", "imports-rebuild", "imports-open", "imports-create", "imports-reads",
		"imports-snapshot", "imports-job-fail", "imports-fail-reading", "imports-delete",
		"album-tracks", "tracking-title-tmdb", "tracking-title-id", "tracking-kick", "event", "refs-replace", "ref-tracks",
		"refs-delete", "voiceover-queue", "voiceover-state", "holds-put", "holds", "play-input", "play-input-drop", "notify",
		"requests-approve", "image", "progress", "progress-put"}
	var got []string
	for n := range gs {
		got = append(got, n)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("golden answers %v\nwant %v", got, want)
	}
	srv := ReplayGolden(t, GoldenDir())
	c := addonkit.NewClient(addonkit.Env{CoreURL: srv.URL, Token: "tok"})
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil || info.Contract != 1 || info.MediaRoot != "{root}" || len(info.FileRoots) == 0 {
		t.Fatalf("info %+v %v", info, err)
	}
	if ss, err := c.Studios(ctx); err != nil || len(ss) == 0 {
		t.Fatalf("studios %v %v", ss, err)
	}
	if us, err := c.Users(ctx); err != nil || len(us) < 2 || len(us[0].Languages) == 0 {
		t.Fatalf("users %v %v", us, err)
	}
	// the core's recording: Маша (2) watched the movie (1), not the series (2)
	if m, err := c.Watched(ctx, 2, []int64{1}, []int64{2}); err != nil || len(m) != 1 || !m[1] {
		t.Fatalf("watched %v %v", m, err)
	}
	// Маша's saved position in an episode (4), none in the next (5)
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := c.SaveProgress(ctx, addonkit.ProgressSave{UserID: 2, ItemID: 4, PositionMs: 600000, DurationMs: 3480000, At: at}); err != nil {
		t.Fatalf("save progress %v", err)
	}
	if m, err := c.Progress(ctx, 2, []int64{4, 5}); err != nil || len(m) != 1 || m[4].PositionMs != 600000 || m[4].Watched || !m[4].UpdatedAt.Equal(at) {
		t.Fatalf("progress %+v %v", m, err)
	}
	// the series' two episodes' item ids (4, 5), for Маша, who may see them
	if eps, err := c.EpisodeItems(ctx, 2, 126308); err != nil || len(eps) != 2 || eps[0] != (addonkit.EpisodeItem{Season: 1, Episode: 1, ItemID: 4}) ||
		eps[1] != (addonkit.EpisodeItem{Season: 1, Episode: 2, ItemID: 5}) {
		t.Fatalf("episode items %+v %v", eps, err)
	}
	// the tracked series, its two episodes and three sources
	ti, err := c.EnsureTitle(ctx, "series", 126308, "Сёгун", "/shogun.jpg")
	if err != nil || ti.ID == 0 || ti.TMDBID != 126308 {
		t.Fatalf("tracking title %+v %v", ti, err)
	}
	if eps, err := c.TitleEpisodes(ctx, ti.ID); err != nil || len(eps) != 2 {
		t.Fatalf("tracking episodes %v %v", eps, err)
	}
	if srcs, err := c.TitleSources(ctx, ti.ID); err != nil || len(srcs) != 3 {
		t.Fatalf("tracking sources %v %v", srcs, err)
	}
	// the import queue: a claim and a start replayed, the candidates
	// read with the core's clock (the client sends the time in UTC)
	before := time.Date(2026, 11, 1, 15, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	if gs, err := c.ImportCandidates(ctx, 1, before); err != nil || len(gs) != 2 || gs[1].ReleaseGroup != "rg-two" {
		t.Fatalf("import candidates %+v %v", gs, err)
	}
	if ok, err := c.MoveImportGroup(ctx, 2, "waiting", "searching"); err != nil || !ok {
		t.Fatalf("import move %v %v", ok, err)
	}
	if ok, err := c.StartImportGroup(ctx, 2, "k-0001", 1000, []int64{3}, nil); err != nil || !ok {
		t.Fatalf("import start %v %v", ok, err)
	}
	if ts, err := c.ImportGroupTracks(ctx, 1, "matched"); err != nil || len(ts) != 2 {
		t.Fatalf("import tracks %v %v", ts, err)
	}
	if id, err := c.CreateReadingJob(ctx, 2, "apple"); err != nil || id == 0 {
		t.Fatalf("reading job %d %v", id, err)
	}
	if ok, err := c.ImportOpen(ctx, 2, "file"); err != nil || !ok {
		t.Fatalf("import open %v %v", ok, err)
	}
	// a recorded {time} decodes (GoldenTime)
	if it, err := c.Item(ctx, 1); err != nil || !it.AddedAt.Equal(GoldenTime) {
		t.Fatalf("item %+v %v", it, err)
	}
	if ts, err := c.AlbumTracks(ctx, 7); err != nil || len(ts) != 2 || !ts[0].AddedAt.Equal(GoldenTime) {
		t.Fatalf("album tracks %+v %v", ts, err)
	}
	if ti, ok, err := c.TitleByTMDB(ctx, "series", 126308); err != nil || !ok || ti.ID != 1 {
		t.Fatalf("title by tmdb %+v %v %v", ti, ok, err)
	}
	if ti, ok, err := c.TitleByID(ctx, 1); err != nil || !ok || ti.Name != "Сёгун" {
		t.Fatalf("title by id %+v %v %v", ti, ok, err)
	}
	if err := c.KickTitle(ctx, 1); err != nil {
		t.Fatalf("kick %v", err)
	}
	if err := c.Event(ctx, "ping", map[string]int{"n": 1}); err != nil {
		t.Fatalf("event %v", err)
	}
	if b, err := c.BindRef(ctx, "k-0003", []string{"{root}/music/Кино/Группа крови/01 - Группа крови.mp3"}); err != nil || b.Bound != 1 {
		t.Fatalf("bind ref %+v %v", b, err)
	}
	if ts, err := c.TracksOfRef(ctx, "k-0003"); err != nil || len(ts) != 1 {
		t.Fatalf("tracks of ref %+v %v", ts, err)
	}
	if err := c.UnbindRef(ctx, "k-0003"); err != nil {
		t.Fatalf("unbind ref %v", err)
	}
	dubbed := "{root}/movies/Матрица (1999)/Матрица (1999).rus.mka"
	if err := c.QueueFit(ctx, dubbed, 1, "en"); err != nil {
		t.Fatalf("queue fit %v", err)
	}
	if f, ok, err := c.FitState(ctx, dubbed); err != nil || !ok || f.State != "pending" {
		t.Fatalf("fit state %+v %v %v", f, ok, err)
	}
	if err := c.PutHolds(ctx, nil); err != nil {
		t.Fatalf("put holds %v", err)
	}
	if hs, err := c.Holds(ctx); err != nil || len(hs) != 1 || hs[0].Key != "k-0001" {
		t.Fatalf("holds %+v %v", hs, err)
	}
	// an input's random id reads {input}, so the drop that follows is replayed
	in, err := c.RegisterInput(ctx, addonkit.InputRequest{UserID: 3, Path: "/media/1", Name: "Matrix/Matrix.mkv", Size: 1 << 20})
	if err != nil || in.ID != "{input}" || !in.ExpiresAt.Equal(GoldenTime) {
		t.Fatalf("register input %+v %v", in, err)
	}
	if err := c.DropInput(ctx, in.ID); err != nil {
		t.Fatalf("drop input %v", err)
	}
	if n, err := c.Notify(ctx, addonkit.NotifyRequest{Topic: "downloads", Key: "k-0001", Subject: addonkit.Text{RU: "Готово"}}); err != nil || n != 1 {
		t.Fatalf("notify %d %v", n, err)
	}
	if n, err := c.ApproveRequests(ctx, addonkit.ApproveRequest{AdminID: 1, Kind: "music", Title: "Группа крови", Artist: "Кино"}); err != nil || n != 1 {
		t.Fatalf("approve %d %v", n, err)
	}
	if name, err := c.CacheImage(ctx, "https://example.com/p.png"); err != nil || name != "{image}" {
		t.Fatalf("image %q %v", name, err)
	}
}

// A recorded timestamp reads {time} in the files; the replay answers
// GoldenTime instead, so a client decodes it into a time.Time.
func TestReplayGolden_Time(t *testing.T) {
	dir := t.TempDir()
	writeGolden(t, dir, "item", `{"method":"GET","path":"/item","status":200,"body":{"addedAt":"{time}","note":"{time} stays in text"}}`)
	srv := ReplayGolden(t, dir)
	_, _, body := get(t, "GET", srv.URL+"/item")
	var got struct {
		AddedAt time.Time `json:"addedAt"`
		Note    string    `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	if !got.AddedAt.Equal(GoldenTime) || got.Note != "{time} stays in text" {
		t.Fatalf("%+v", got)
	}
}
