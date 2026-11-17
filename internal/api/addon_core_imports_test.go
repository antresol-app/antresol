package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/playlistimport"
)

type coreImports struct {
	*addonEnv
	svc   *playlistimport.Service
	owner int64
}

// importsNow is the import store's clock in these tests.
var importsNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// newCoreImports: the addon env with «Импорт медиатеки» (its queue the
// addons' adapter, on); the addon "im" has the imports permission,
// "hello" does not.
func newCoreImports(t *testing.T) *coreImports {
	c := &coreImports{}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		st := &playlistimport.Store{DB: d.DB, Now: func() time.Time { return importsNow }}
		c.svc = &playlistimport.Service{Store: st, Bus: d.Events, Library: d.Library}
		c.svc.Queue = playlistimport.NewAddonDownloader(st, d.Library, func() (bool, error) { return true, nil })
		d.PlaylistImport = c.svc
	})
	c.fake.mu.Lock()
	in := c.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "im", "im", []string{"imports"}
	c.fake.installed["im"], c.fake.tokens["tok-im"], c.fake.status["im"] = in, "im", addons.StatusRunning
	c.fake.mu.Unlock()
	if err := c.db.QueryRow(`SELECT id FROM users WHERE name = 'Папа'`).Scan(&c.owner); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *coreImports) call(tok, method, path, body string) *httptest.ResponseRecorder {
	return c.do(method, "/api/addon-core/v1"+path, body, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
}

// importsJob is a downloading job of profile user: songs a, b on the
// album rg-one and x on rg-two, grouped; its groups and songs by name.
type importsJob struct {
	id             int64
	groups, tracks map[string]int64
}

func makeImportsJob(t *testing.T, st *playlistimport.Store, user int64) importsJob {
	t.Helper()
	return makeImportsJobOf(t, st, user, "file")
}

// makeImportsJobOf is makeImportsJob of the service (one open job per
// profile and service).
func makeImportsJobOf(t *testing.T, st *playlistimport.Store, user int64, service string) importsJob {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateJob(ctx, user, playlistimport.Snapshot{Service: service, Tracks: []playlistimport.SnapTrack{
		{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}, {Key: "b", Artist: "Кино", Title: "Звезда", Liked: true},
		{Key: "x", Artist: "Кино", Title: "Пачка сигарет", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	j := importsJob{id: id, groups: map[string]int64{}, tracks: map[string]int64{}}
	rows, err := st.DB.Query(`SELECT id, key FROM import_tracks WHERE job_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tid int64
		var k string
		rows.Scan(&tid, &k)
		j.tracks[k] = tid
	}
	rows.Close()
	for k, tid := range j.tracks {
		d := playlistimport.Decision{State: playlistimport.TrackMatched, RecordingID: "rec-" + k, ReleaseGroup: "rg-one", Title: k,
			Artist: "Кино", Album: "Группа крови", Year: 1988}
		if k == "x" {
			d.ReleaseGroup, d.Album, d.Year = "rg-two", "Звезда по имени Солнце", 1989
		}
		if err := st.SetDecision(ctx, tid, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetStatus(ctx, id, playlistimport.StatusDownloading, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignGroups(ctx, id); err != nil {
		t.Fatal(err)
	}
	gs, err := st.Groups(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		j.groups[g.ReleaseGroup] = g.ID
	}
	return j
}

func TestImportsAPI_StartCAS(t *testing.T) {
	c := newCoreImports(t)
	j := makeImportsJob(t, c.svc.Store, c.owner)
	g := j.groups["rg-two"]
	move := fmt.Sprintf("/imports/groups/%d/move", g)
	if w := c.call("tok-im", "POST", move, `{"from":"waiting","to":"searching"}`); w.Code != 204 {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	// the other claimer
	w := c.call("tok-im", "POST", move, `{"from":"waiting","to":"searching"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Альбом уже взят") {
		t.Fatalf("second move: %d %s", w.Code, w.Body)
	}
	start := fmt.Sprintf("/imports/groups/%d/start", g)
	body := fmt.Sprintf(`{"key":"k1","bytes":100,"downloading":[%d],"missing":[]}`, j.tracks["x"])
	if w := c.call("tok-im", "POST", start, body); w.Code != 204 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "POST", start, strings.Replace(body, "k1", "k2", 1))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Альбом уже взят") {
		t.Fatalf("second start: %d %s", w.Code, w.Body)
	}
	gr, _ := c.svc.Store.Group(context.Background(), g)
	if gr.State != playlistimport.GroupDownloading || gr.InfoHash != "k1" {
		t.Fatalf("group %+v", gr)
	}
	// a core restart between the claim and the start: the start fails
	g1 := j.groups["rg-one"]
	if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/groups/%d/move", g1), `{"from":"waiting","to":"searching"}`); w.Code != 204 {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	if err := c.svc.Store.ResetSearching(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = c.call("tok-im", "POST", fmt.Sprintf("/imports/groups/%d/start", g1), `{"key":"k3","bytes":1,"downloading":[],"missing":[]}`)
	if w.Code != 409 {
		t.Fatalf("start after a core restart: %d %s", w.Code, w.Body)
	}
	// bad states, unknown group
	if w := c.call("tok-im", "POST", move, `{"from":"downloading","to":"done"}`); w.Code != 400 {
		t.Fatalf("bad move: %d %s", w.Code, w.Body)
	}
	if w := c.call("tok-im", "POST", "/imports/groups/9999/start", body); w.Code != 404 {
		t.Fatalf("unknown group: %d %s", w.Code, w.Body)
	}
}

func groupIDs(t *testing.T, w *httptest.ResponseRecorder) []int64 {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var gs []addonkit.ImportGroup
	if err := json.Unmarshal(w.Body.Bytes(), &gs); err != nil {
		t.Fatal(err)
	}
	out := []int64{}
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}

func TestImportsAPI_Candidates(t *testing.T) {
	c := newCoreImports(t)
	st := c.svc.Store
	ctx := context.Background()
	j := makeImportsJob(t, st, c.owner)
	j2 := makeImportsJobOf(t, st, c.owner, "apple") // (a second job: its groups are not this one's)
	one, two := j.groups["rg-one"], j.groups["rg-two"]
	path := func(before time.Time) string {
		return fmt.Sprintf("/imports/jobs/%d/candidates?retryBefore=%s", j.id, before.Format(time.RFC3339))
	}
	if got := groupIDs(t, c.call("tok-im", "GET", path(importsNow), "")); !slices.Equal(got, []int64{one, two}) {
		t.Fatalf("waiting: %v", got)
	}
	for _, state := range []string{"waiting_space", "waiting_download"} {
		c.db.Exec(`UPDATE import_groups SET state = ? WHERE id = ?`, state, two)
		if got := groupIDs(t, c.call("tok-im", "GET", path(importsNow), "")); !slices.Equal(got, []int64{one, two}) {
			t.Fatalf("%s: %v", state, got)
		}
	}
	if err := st.FailGroup(ctx, two, []int64{j.tracks["x"]}, "Ошибка", true, importsNow); err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(t, c.call("tok-im", "GET", path(importsNow.Add(-time.Minute)), "")); !slices.Equal(got, []int64{one}) {
		t.Fatalf("failed, not due: %v", got)
	}
	if got := groupIDs(t, c.call("tok-im", "GET", path(importsNow), "")); !slices.Equal(got, []int64{one, two}) {
		t.Fatalf("failed, due: %v", got)
	}
	if w := c.call("tok-im", "GET", fmt.Sprintf("/imports/jobs/%d/candidates?retryBefore=yesterday", j.id), ""); w.Code != 400 {
		t.Fatalf("bad time: %d", w.Code)
	}
	// groups by state, the job's only
	if got := groupIDs(t, c.call("tok-im", "GET", fmt.Sprintf("/imports/jobs/%d/groups?state=failed", j.id), "")); !slices.Equal(got, []int64{two}) {
		t.Fatalf("failed groups: %v", got)
	}
	if got := groupIDs(t, c.call("tok-im", "GET", fmt.Sprintf("/imports/jobs/%d/groups", j2.id), "")); len(got) != 2 || slices.Contains(got, one) {
		t.Fatalf("second job's groups: %v", got)
	}
	// the job, the downloading ones, active count, songs
	w := c.call("tok-im", "GET", "/imports/jobs?status=downloading", "")
	var ids struct{ IDs []int64 }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ids) != nil || !slices.Equal(ids.IDs, []int64{j.id, j2.id}) {
		t.Fatalf("downloading jobs: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "GET", fmt.Sprintf("/imports/jobs/%d", j.id), "")
	var job addonkit.ImportJob
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &job) != nil || job.ID != j.id || job.UserID != c.owner ||
		job.Status != "downloading" || job.Service != "file" {
		t.Fatalf("job: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "GET", fmt.Sprintf("/imports/groups/%d/tracks?state=matched", one), "")
	var ts []addonkit.ImportTrack
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ts) != nil || len(ts) != 2 || ts[0].MBReleaseGroup != "rg-one" {
		t.Fatalf("tracks: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "GET", "/imports/active-count", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"count":0}` {
		t.Fatalf("active: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "GET", fmt.Sprintf("/imports/jobs/%d/still-downloading", j.id), "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"downloading":true}` {
		t.Fatalf("still: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-im", "GET", fmt.Sprintf("/imports/open?userId=%d&service=file", c.owner), "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"open":true}` {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
}

// waitEvent waits for import.progress of job in status.
func waitEvent(t *testing.T, ch <-chan events.Event, job int64, status string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if p, ok := ev.Data.(playlistimport.ProgressEvent); ok && ev.Type == events.ImportProgress && p.JobID == job &&
				p.Status == status {
				return
			}
		case <-timeout:
			t.Fatalf("no import.progress of job %d in %s", job, status)
		}
	}
}

func TestImportsAPI_ReadingJob(t *testing.T) {
	c := newCoreImports(t)
	ch, cancel := c.bus.Subscribe()
	defer cancel()
	w := c.call("tok-im", "POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, c.owner))
	var made struct{ ID int64 }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &made) != nil || made.ID == 0 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	waitEvent(t, ch, made.ID, "reading")
	w = c.call("tok-im", "GET", fmt.Sprintf("/imports/reads?userId=%d", c.owner), "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != fmt.Sprintf(`{"ids":[%d]}`, made.ID) {
		t.Fatalf("reads: %d %s", w.Code, w.Body)
	}
	snap := `{"snapshot":{"service":"apple","account":"m","tracks":[{"key":"t1","artist":"Кино","title":"Кукушка","liked":true}]}}`
	if w := c.call("tok-im", "PUT", fmt.Sprintf("/imports/jobs/%d/snapshot", made.ID), snap); w.Code != 204 {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body)
	}
	waitEvent(t, ch, made.ID, "preview")
	j, err := c.svc.Store.Job(context.Background(), made.ID)
	if err != nil || j.Status != "preview" || j.Account != "m" {
		t.Fatalf("filled job %+v %v", j, err)
	}
	// filled once
	if w := c.call("tok-im", "PUT", fmt.Sprintf("/imports/jobs/%d/snapshot", made.ID), snap); w.Code != 409 {
		t.Fatalf("snapshot again: %d %s", w.Code, w.Body)
	}
	// a read that failed
	w = c.call("tok-im", "POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, c.owner))
	var second struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &second)
	if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/jobs/%d/fail", second.ID), `{"error":"Не удалось прочитать"}`); w.Code != 204 {
		t.Fatalf("fail: %d %s", w.Code, w.Body)
	}
	if j, _ := c.svc.Store.Job(context.Background(), second.ID); j.Status != "failed" || j.Error != "Не удалось прочитать" {
		t.Fatalf("failed job %+v", j)
	}
	if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/jobs/%d/fail", second.ID), `{"error":"x"}`); w.Code != 409 {
		t.Fatalf("fail again: %d %s", w.Code, w.Body)
	}
	// the addon's Recover at its start: every read still going fails
	w = c.call("tok-im", "POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, c.owner))
	var third struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &third)
	w = c.call("tok-im", "POST", "/imports/jobs/fail-reading", `{"error":"Перезапуск"}`)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != fmt.Sprintf(`{"ids":[%d]}`, third.ID) {
		t.Fatalf("fail reading: %d %s", w.Code, w.Body)
	}
	waitEvent(t, ch, third.ID, "failed")
	if w := c.call("tok-im", "DELETE", fmt.Sprintf("/imports/jobs/%d", third.ID), ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := c.svc.Store.Job(context.Background(), third.ID); err == nil {
		t.Fatal("deleted job still there")
	}
	// bad requests
	for _, body := range []string{`{"userId":0,"service":"apple","reading":true}`, fmt.Sprintf(`{"userId":%d,"service":"apple"}`, c.owner),
		fmt.Sprintf(`{"userId":%d,"service":"nope","reading":true}`, c.owner)} {
		if w := c.call("tok-im", "POST", "/imports/jobs", body); w.Code != 400 {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := c.call("tok-im", "POST", "/imports/jobs", `{"userId":99999,"service":"apple","reading":true}`); w.Code != 404 {
		t.Errorf("unknown profile: %d %s", w.Code, w.Body)
	}
}

// The snapshot body may be large (MaxTracks songs); others keep 64 KiB.
func TestImportsAPI_SnapshotBodyLimit(t *testing.T) {
	c := newCoreImports(t)
	w := c.call("tok-im", "POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, c.owner))
	var made struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &made)
	var b strings.Builder
	b.WriteString(`{"snapshot":{"service":"apple","tracks":[`)
	for i := range 3000 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"key":"t%d","artist":"Исполнитель %d","title":"Песня номер %d с длинным названием"}`, i, i, i)
	}
	b.WriteString(`]}}`)
	if b.Len() <= 64<<10 {
		t.Fatalf("body %d bytes: not over 64 KiB", b.Len())
	}
	if w := c.call("tok-im", "PUT", fmt.Sprintf("/imports/jobs/%d/snapshot", made.ID), b.String()); w.Code != 204 {
		t.Fatalf("large snapshot: %d %s", w.Code, w.Body)
	}
	big := `{"error":"` + strings.Repeat("x", 65<<10) + `"}`
	if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/jobs/%d/fail", made.ID), big); w.Code != 400 {
		t.Fatalf("large fail body: %d", w.Code)
	}
}

func TestImportsAPI_Progress(t *testing.T) {
	c := newCoreImports(t)
	j := makeImportsJob(t, c.svc.Store, c.owner)
	g := j.groups["rg-two"]
	for _, step := range []struct{ path, body string }{
		{"move", `{"from":"waiting","to":"searching"}`},
		{"start", fmt.Sprintf(`{"key":"k1","bytes":1000,"downloading":[%d],"missing":[]}`, j.tracks["x"])},
		{"progress", `{"done":250}`},
	} {
		if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/groups/%d/%s", g, step.path), step.body); w.Code != 204 {
			t.Fatalf("%s: %d %s", step.path, w.Code, w.Body)
		}
	}
	view := func() map[int64]int64 {
		t.Helper()
		w := c.do("GET", fmt.Sprintf("/api/import/jobs/%d/groups", j.id), "", c.admin)
		var out struct {
			Groups []struct {
				ID    int64
				State string
				Done  int64
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("groups view: %d %s", w.Code, w.Body)
		}
		m := map[int64]int64{}
		for _, gr := range out.Groups {
			m[gr.ID] = gr.Done
		}
		return m
	}
	if got := view(); got[g] != 250 {
		t.Fatalf("done %v", got)
	}
	// it leaves downloading: the progress goes; a done album is its bytes
	w := c.call("tok-im", "POST", fmt.Sprintf("/imports/groups/%d/requeue", g), `{"key":"k1","msg":"","maxDead":3}`)
	if w.Code != 204 {
		t.Fatalf("requeue: %d %s", w.Code, w.Body)
	}
	if got := view(); got[g] != 0 {
		t.Fatalf("done after requeue %v", got)
	}
	if _, known, _ := c.svc.Queue.(playlistimport.DownloadProgress).Downloaded(context.Background(), "k1"); known {
		t.Fatal("progress kept after the album left downloading")
	}
}

// The queue's other writes, through the API.
func TestImportsAPI_Writes(t *testing.T) {
	c := newCoreImports(t)
	st := c.svc.Store
	ctx := context.Background()
	j := makeImportsJob(t, st, c.owner)
	one, two := j.groups["rg-one"], j.groups["rg-two"]
	post := func(g int64, op, body string) {
		t.Helper()
		if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/groups/%d/%s", g, op), body); w.Code != 204 {
			t.Fatalf("%s: %d %s", op, w.Code, w.Body)
		}
	}
	post(two, "wait", `{"key":"w1","name":"Кино","done":5}`)
	if gr, _ := st.Group(ctx, two); gr.State != "waiting_download" || gr.WaitingFor != "Кино" {
		t.Fatalf("wait %+v", gr)
	}
	post(two, "give-up-wait", `{"key":"w1"}`)
	if gr, _ := st.Group(ctx, two); gr.State != "waiting" || gr.SkipInfoHash != "w1" {
		t.Fatalf("give up %+v", gr)
	}
	post(two, "move", `{"from":"waiting","to":"searching"}`)
	post(two, "start", fmt.Sprintf(`{"key":"k1","bytes":10,"downloading":[%d],"missing":[]}`, j.tracks["x"]))
	post(two, "clock", `{"key":"k1","clock":{"runSec":60,"quietSec":30,"base":1,"done":7}}`)
	if gr, _ := st.Group(ctx, two); gr.Clock.Run != time.Minute || gr.Clock.Quiet != 30*time.Second || gr.Clock.Done != 7 {
		t.Fatalf("clock %+v", gr.Clock)
	}
	post(two, "given-up", `{"key":"k1","slow":false}`)
	if gr, _ := st.Group(ctx, two); !slices.Equal(gr.Dead, []string{"k1"}) {
		t.Fatalf("dead %v", gr.Dead)
	}
	post(two, "requeue", `{"key":"k1","msg":"","maxDead":3}`)
	post(two, "no-format", fmt.Sprintf(`{"trackIds":[%d],"msg":"Нет формата"}`, j.tracks["x"]))
	if gr, _ := st.Group(ctx, two); gr.State != "no_format" {
		t.Fatalf("no format %s", gr.State)
	}
	if err := st.RetryGroup(ctx, two); err != nil {
		t.Fatal(err)
	}
	post(two, "fail", fmt.Sprintf(`{"trackIds":[%d],"msg":"Ошибка","retry":true}`, j.tracks["x"]))
	if gr, _ := st.Group(ctx, two); gr.State != "failed" || gr.Attempts != 1 {
		t.Fatalf("fail %+v", gr)
	}
	post(two, "not-found", fmt.Sprintf(`{"trackIds":[%d],"msg":"Не нашлось"}`, j.tracks["x"]))
	if gr, _ := st.Group(ctx, two); gr.State != "not_found" || gr.Error != "Не нашлось" {
		t.Fatalf("not found %+v", gr)
	}
	// the library's album: have, owned by the importer
	album := libraryAlbum(t, c)
	post(one, "have", fmt.Sprintf(`{"have":{"%d":%d},"album":%d,"done":false}`, j.tracks["a"], album.track, album.id))
	if tr, _ := st.Track(ctx, j.tracks["a"]); tr.State != "have" || tr.ItemID != album.track {
		t.Fatalf("have track %+v", tr)
	}
	var owners int
	c.db.QueryRow(`SELECT COUNT(*) FROM item_owners WHERE item_id = ? AND user_id = ?`, album.id, c.owner).Scan(&owners)
	if owners != 1 {
		t.Fatalf("owners %d", owners)
	}
	post(one, "move", `{"from":"waiting","to":"searching"}`)
	post(one, "start", fmt.Sprintf(`{"key":"k9","bytes":10,"downloading":[%d],"missing":[]}`, j.tracks["b"]))
	post(one, "finish", fmt.Sprintf(`{"done":{"%d":%d},"failed":[],"album":%d}`, j.tracks["b"], album.track, album.id))
	if gr, _ := st.Group(ctx, one); gr.State != "done" {
		t.Fatalf("finish %s", gr.State)
	}
	if w := c.call("tok-im", "POST", fmt.Sprintf("/imports/jobs/%d/rebuild", j.id), ""); w.Code != 204 {
		t.Fatalf("rebuild: %d %s", w.Code, w.Body)
	}
}

type libAlbumIDs struct{ id, track int64 }

// libraryAlbum puts an album with one song in the library.
func libraryAlbum(t *testing.T, c *coreImports) libAlbumIDs {
	t.Helper()
	ctx := context.Background()
	lib := c.svc.Library
	id, err := lib.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{{File: library.FileInput{Path: "/m/Кино/Группа крови/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Группа крови", Album: "Группа крови", Title: "Кукушка", Track: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ts, err := lib.AlbumTracks(ctx, id)
	if err != nil || len(ts) != 1 {
		t.Fatalf("tracks %v %v", ts, err)
	}
	return libAlbumIDs{id: id, track: ts[0].ID}
}

func TestImportsAPI_NoPermission(t *testing.T) {
	c := newCoreImports(t)
	j := makeImportsJob(t, c.svc.Store, c.owner)
	g := j.groups["rg-two"]
	for _, r := range [][3]string{
		{"GET", "/imports/active-count", ""},
		{"GET", "/imports/jobs?status=downloading", ""},
		{"GET", fmt.Sprintf("/imports/jobs/%d", j.id), ""},
		{"GET", fmt.Sprintf("/imports/jobs/%d/candidates?retryBefore=2026-11-01T12:00:00Z", j.id), ""},
		{"GET", fmt.Sprintf("/imports/groups/%d/tracks?state=matched", g), ""},
		{"POST", fmt.Sprintf("/imports/groups/%d/move", g), `{"from":"waiting","to":"searching"}`},
		{"POST", fmt.Sprintf("/imports/groups/%d/start", g), `{"key":"k"}`},
		{"POST", fmt.Sprintf("/imports/groups/%d/progress", g), `{"done":1}`},
		{"POST", fmt.Sprintf("/imports/jobs/%d/rebuild", j.id), ""},
		{"GET", fmt.Sprintf("/imports/open?userId=%d&service=file", c.owner), ""},
		{"POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, c.owner)},
		{"PUT", fmt.Sprintf("/imports/jobs/%d/snapshot", j.id), `{"snapshot":{}}`},
		{"POST", "/imports/jobs/fail-reading", `{"error":"x"}`},
		{"DELETE", fmt.Sprintf("/imports/jobs/%d", j.id), ""},
		{"GET", fmt.Sprintf("/imports/reads?userId=%d", c.owner), ""},
	} {
		if w := c.call("tok-hello", r[0], r[1], r[2]); w.Code != 403 {
			t.Errorf("%s %s: %d, want 403", r[0], r[1], w.Code)
		}
	}
	if gr, _ := c.svc.Store.Group(context.Background(), g); gr.State != "waiting" {
		t.Fatalf("a refused call changed the group: %s", gr.State)
	}
	// no import service: 503
	e := newAddonEnv(t)
	e.fake.mu.Lock()
	in := e.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "im", "im", []string{"imports"}
	e.fake.installed["im"], e.fake.tokens["tok-im"], e.fake.status["im"] = in, "im", addons.StatusRunning
	e.fake.mu.Unlock()
	if w := e.do("GET", "/api/addon-core/v1/imports/active-count", "", nil, "Authorization", "Bearer tok-im"); w.Code != 503 {
		t.Fatalf("no service: %d", w.Code)
	}
}

// Kids get what addons bring only through the core's own gating: an
// addon cannot make an import job for a kid's profile, whose rebuild
// would write playlists and stars into that library.
func TestCoreImportsCreateNoKids(t *testing.T) {
	c := newCoreImports(t)
	for _, u := range []struct {
		name string
		kid  int
		code int
	}{{"Малыш", 1, 404}, {"Гость", 0, 200}} {
		res, err := c.db.Exec(`INSERT INTO users(name, avatar, is_kid) VALUES (?, '', ?)`, u.name, u.kid)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if w := c.call("tok-im", "POST", "/imports/jobs", fmt.Sprintf(`{"userId":%d,"service":"apple","reading":true}`, id)); w.Code != u.code {
			t.Errorf("%s: %d %s (want %d)", u.name, w.Code, w.Body, u.code)
		}
	}
}
