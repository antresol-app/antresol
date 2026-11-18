package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/progress"
)

// progFix: the library of newCoreLib, three profiles (the admin, Маша an
// adult, Вася a kid) and an addon "prog" (token tok-prog) with only the
// progress permission.
type progFix struct {
	*coreLib
	admin, masha, vasya int64
	eps                 [2]int64
	season              int64
	prog                *progress.Store
}

func newProgFix(t *testing.T) *progFix {
	t.Helper()
	c := newCoreLib(t)
	f := &progFix{coreLib: c, prog: progress.New(c.db)}
	c.db.QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&f.admin)
	c.db.QueryRow(`SELECT id FROM users WHERE name='Маша'`).Scan(&f.masha)
	c.db.QueryRow(`SELECT id FROM users WHERE name='Вася'`).Scan(&f.vasya)
	rows, err := c.db.Query(`SELECT e.id, se.id FROM items e JOIN items se ON se.id=e.parent_id WHERE se.parent_id=? ORDER BY e.episode_no`, c.show)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i := 0; rows.Next(); i++ {
		if i >= len(f.eps) {
			t.Fatal("more than two episodes")
		}
		if err := rows.Scan(&f.eps[i], &f.season); err != nil {
			t.Fatal(err)
		}
	}
	if f.eps[0] == 0 || f.eps[1] == 0 || f.season == 0 {
		t.Fatalf("episodes %v season %d", f.eps, f.season)
	}
	pr := c.fake.installed["lib"]
	pr.ID, pr.Manifest.ID, pr.Manifest.Permissions = "prog", "prog", []string{"progress"}
	c.fake.mu.Lock()
	c.fake.installed["prog"], c.fake.tokens["tok-prog"] = pr, "prog"
	c.fake.mu.Unlock()
	return f
}

// limit gives Маша a rating limit and rates the movie (rating "" leaves
// it unrated, which a limited profile does not see) and the series.
func (f *progFix) limit(maxRating, movie, series string) {
	f.t.Helper()
	if _, err := f.db.Exec(`UPDATE users SET max_rating=? WHERE id=?`, maxRating, f.masha); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE items SET rating=? WHERE id=?`, movie, f.movie); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE items SET rating=? WHERE id=?`, series, f.show); err != nil {
		f.t.Fatal(err)
	}
}

// put calls PUT /progress as the addon of tok; body is a JSON object or
// a map.
func (f *progFix) put(tok string, body any, want int) string {
	f.t.Helper()
	raw, ok := body.(string)
	if !ok {
		b, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		raw = string(b)
	}
	w := f.do("PUT", "/api/addon-core/v1/progress", raw, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	if w.Code != want {
		f.t.Fatalf("PUT /progress %s: %d %s (want %d)", raw, w.Code, w.Body, want)
	}
	return w.Body.String()
}

// save is a PUT body of the profile's position in an item.
func save(user, item, pos, dur int64, at time.Time, played bool) map[string]any {
	m := map[string]any{"userId": user, "itemId": item, "positionMs": pos, "durationMs": dur, "played": played}
	if !at.IsZero() {
		m["at"] = at.UTC().Format(time.RFC3339Nano)
	}
	return m
}

func idList(list ...int64) string {
	parts := make([]string, len(list))
	for i, id := range list {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// progressOf reads GET /progress as the addon of tok.
func (f *progFix) progressOf(tok string, user int64, items string) map[string]addonkit.ProgressEntry {
	f.t.Helper()
	var out struct {
		Progress map[string]addonkit.ProgressEntry `json:"progress"`
	}
	f.get(tok, fmt.Sprintf("/progress?userId=%d&items=%s", user, items), 200, &out)
	if out.Progress == nil {
		f.t.Fatal("no progress object")
	}
	return out.Progress
}

// errText is the {"error"} of an answer.
func progErrText(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return e.Error
}

func watchTimeMs(f *progFix, user, item int64) int64 {
	var ms int64
	f.db.QueryRow(`SELECT COALESCE(SUM(ms),0) FROM watch_time WHERE user_id=? AND item_id=?`, user, item).Scan(&ms)
	return ms
}

// PUT /progress then GET /progress: the position comes back as written,
// with the time of the write; only the profile's own, only what was asked;
// the stale-write rule of the store holds (an older write changes nothing).
func TestAddonProgress_SaveAndRead(t *testing.T) {
	f := newProgFix(t)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	f.put("tok-prog", save(f.masha, f.movie, 600_000, 8_160_000, at, false), 204)
	k := strconv.FormatInt(f.movie, 10)

	got := f.progressOf("tok-prog", f.masha, idList(f.movie, f.eps[0]))
	e, ok := got[k]
	if !ok || len(got) != 1 {
		t.Fatalf("progress %+v", got)
	}
	if e.PositionMs != 600_000 || e.DurationMs != 8_160_000 || e.Watched || !e.UpdatedAt.Equal(at) {
		t.Fatalf("entry %+v, want 600000/8160000, unwatched, %v", e, at)
	}
	// the store has it, and no played time: SaveAt adds none
	if s, err := f.prog.Get(context.Background(), f.masha, f.movie); err != nil || s.PositionMs != 600_000 {
		t.Fatalf("store %+v %v", s, err)
	}
	if ms := watchTimeMs(f, f.masha, f.movie); ms != 0 {
		t.Fatalf("played: false counted %d ms of watch time", ms)
	}
	// another profile's view of the same item shows nothing
	if got := f.progressOf("tok-prog", f.admin, idList(f.movie)); len(got) != 0 {
		t.Fatalf("admin sees %+v", got)
	}
	// an older write is ignored, a newer one replaces
	f.put("tok-prog", save(f.masha, f.movie, 1_000, 8_160_000, at.Add(-time.Minute), false), 204)
	if got := f.progressOf("tok-prog", f.masha, idList(f.movie)); got[k].PositionMs != 600_000 {
		t.Fatalf("stale write won: %+v", got[k])
	}
	f.put("tok-prog", save(f.masha, f.movie, 900_000, 8_160_000, at.Add(time.Minute), false), 204)
	if got := f.progressOf("tok-prog", f.masha, idList(f.movie)); got[k].PositionMs != 900_000 {
		t.Fatalf("newer write lost: %+v", got[k])
	}
	// an episode, and no «at»: now
	before := time.Now().Add(-time.Second)
	f.put("tok-prog", save(f.masha, f.eps[1], 5_000, 3_480_000, time.Time{}, false), 204)
	if e := f.progressOf("tok-prog", f.masha, idList(f.eps[1]))[strconv.FormatInt(f.eps[1], 10)]; e.PositionMs != 5_000 || e.UpdatedAt.Before(before) {
		t.Fatalf("episode %+v", e)
	}
	// nothing asked: an empty object
	if body := f.get("tok-prog", fmt.Sprintf("/progress?userId=%d", f.masha), 200, nil); strings.TrimSpace(body) != `{"progress":{}}` {
		t.Fatalf("nothing asked: %s", body)
	}
}

// A position from 92% of the duration is watched, whether it is saved
// (played: false) or reported by a player (played: true); only the report
// adds played time.
func TestAddonProgress_PlayedMarksWatched(t *testing.T) {
	f := newProgFix(t)
	at := time.Now().Add(-time.Minute)
	f.put("tok-prog", save(f.masha, f.movie, 7_600_000, 8_160_000, at, true), 204)
	f.put("tok-prog", save(f.masha, f.eps[0], 3_300_000, 3_480_000, at, false), 204)
	got := f.progressOf("tok-prog", f.masha, idList(f.movie, f.eps[0]))
	if e := got[strconv.FormatInt(f.movie, 10)]; !e.Watched || e.PositionMs != 7_600_000 {
		t.Fatalf("played movie %+v", e)
	}
	if e := got[strconv.FormatInt(f.eps[0], 10)]; !e.Watched {
		t.Fatalf("saved episode %+v", e)
	}
	if ms := watchTimeMs(f, f.masha, f.movie); ms <= 0 {
		t.Fatalf("played: true counted no watch time")
	}
	if ms := watchTimeMs(f, f.masha, f.eps[0]); ms != 0 {
		t.Fatalf("played: false counted %d ms", ms)
	}
	// below 92% is not watched
	f.put("tok-prog", save(f.masha, f.eps[1], 3_000_000, 3_480_000, at, true), 204)
	if e := f.progressOf("tok-prog", f.masha, idList(f.eps[1]))[strconv.FormatInt(f.eps[1], 10)]; e.Watched {
		t.Fatalf("86%% is watched: %+v", e)
	}
}

// An item over the profile's limit is 404 «Элемент медиатеки не найден»,
// as one that does not exist: nothing says it is there, nothing is written.
func TestAddonProgress_HiddenItem404(t *testing.T) {
	f := newProgFix(t)
	f.limit("6+", "16+", "0+")
	body := f.put("tok-prog", save(f.masha, f.movie, 1_000, 8_160_000, time.Time{}, false), 404)
	if got := progErrText(t, body); got != "Элемент медиатеки не найден" {
		t.Fatalf("message %q", got)
	}
	if _, err := f.prog.Get(context.Background(), f.masha, f.movie); err == nil {
		t.Fatal("a hidden item got progress")
	}
	// an unrated film is hidden from a limited profile too
	f.limit("6+", "", "0+")
	f.put("tok-prog", save(f.masha, f.movie, 1_000, 8_160_000, time.Time{}, false), 404)
	// an episode takes its series' rating: this one is allowed
	f.put("tok-prog", save(f.masha, f.eps[0], 1_000, 3_480_000, time.Time{}, false), 204)
	// no such item is the same 404
	body = f.put("tok-prog", save(f.masha, 999_999, 1, 2, time.Time{}, false), 404)
	if got := progErrText(t, body); got != "Элемент медиатеки не найден" {
		t.Fatalf("unknown item: %q", got)
	}
	// the admin has no limit
	f.put("tok-prog", save(f.admin, f.movie, 1_000, 8_160_000, time.Time{}, false), 204)
}

// A kid profile has no progress to read or write here: 400.
func TestAddonProgress_KidRefused(t *testing.T) {
	f := newProgFix(t)
	body := f.put("tok-prog", save(f.vasya, f.movie, 1_000, 8_160_000, time.Time{}, false), 400)
	if got := progErrText(t, body); got != "Детскому профилю это недоступно" {
		t.Fatalf("PUT message %q", got)
	}
	// the kid's own writes (the player) are there, and stay unread
	if err := f.prog.Save(context.Background(), f.vasya, f.movie, 5_000, 8_160_000); err != nil {
		t.Fatal(err)
	}
	body = f.get("tok-prog", fmt.Sprintf("/progress?userId=%d&items=%d", f.vasya, f.movie), 400, nil)
	if got := progErrText(t, body); got != "Детскому профилю это недоступно" {
		t.Fatalf("GET message %q", got)
	}
	if e, err := f.prog.Get(context.Background(), f.vasya, f.movie); err != nil || e.PositionMs != 5_000 {
		t.Fatalf("the kid's own row changed: %+v %v", e, err)
	}
}

// Only a movie or an episode has progress here: a season, a series, an
// album or a book is «Это не фильм и не серия».
func TestAddonProgress_SeasonRefused(t *testing.T) {
	f := newProgFix(t)
	for name, id := range map[string]int64{"season": f.season, "series": f.show, "album": f.album, "book": f.book} {
		body := f.put("tok-prog", save(f.masha, id, 1_000, 2_000, time.Time{}, false), 400)
		if got := progErrText(t, body); got != "Это не фильм и не серия" {
			t.Errorf("%s: message %q", name, got)
		}
		if _, err := f.prog.Get(context.Background(), f.masha, id); err == nil {
			t.Errorf("%s got progress", name)
		}
	}
	// over the limit it is hidden first: the 404 does not tell what it is
	f.limit("6+", "16+", "16+")
	f.put("tok-prog", save(f.masha, f.season, 1_000, 2_000, time.Time{}, false), 404)
}

// «at» may run at most 5 minutes ahead of the server's clock.
func TestAddonProgress_FutureAt400(t *testing.T) {
	f := newProgFix(t)
	f.put("tok-prog", save(f.masha, f.movie, 1_000, 8_160_000, time.Now().Add(10*time.Minute), false), 400)
	if _, err := f.prog.Get(context.Background(), f.masha, f.movie); err == nil {
		t.Fatal("a write from the future was kept")
	}
	// a little ahead is a clock that differs: accepted, kept as now
	f.put("tok-prog", save(f.masha, f.movie, 1_000, 8_160_000, time.Now().Add(2*time.Minute), false), 204)
	e := f.progressOf("tok-prog", f.masha, idList(f.movie))[strconv.FormatInt(f.movie, 10)]
	if e.PositionMs != 1_000 || e.UpdatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("entry %+v", e)
	}
}

// Without the progress permission every call is 403, before the request
// is looked at.
func TestAddonProgress_WithoutPermission403(t *testing.T) {
	f := newProgFix(t)
	for _, tok := range []string{"tok-lib", "tok-hello", "tok-ppl", "tok-both"} {
		f.get(tok, "/progress?userId=x&items=y", 403, nil)
		f.put(tok, `not json`, 403)
		f.put(tok, save(f.masha, f.movie, 1, 2, time.Time{}, false), 403)
	}
	if _, err := f.prog.Get(context.Background(), f.masha, f.movie); err == nil {
		t.Fatal("a write without the permission was kept")
	}
	w := f.do("GET", "/api/addon-core/v1/progress?userId=1&items=1", "", nil)
	if w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}

// At most 500 ids per GET.
func TestAddonProgress_501Ids400(t *testing.T) {
	f := newProgFix(t)
	many := make([]int64, 501)
	for i := range many {
		many[i] = int64(i + 1)
	}
	body := f.get("tok-prog", fmt.Sprintf("/progress?userId=%d&items=%s", f.masha, idList(many...)), 400, nil)
	if got := progErrText(t, body); got != "Не больше 500 id за раз" {
		t.Fatalf("message %q", got)
	}
	f.get("tok-prog", fmt.Sprintf("/progress?userId=%d&items=%s", f.masha, idList(many[:500]...)), 200, nil)
}

// GET leaves out what the profile cannot see (over its limit, or not a
// movie or an episode) rather than refuse: it says nothing of what was
// watched there.
func TestAddonProgress_GetOmitsHidden(t *testing.T) {
	f := newProgFix(t)
	ctx := context.Background()
	for _, id := range []int64{f.movie, f.eps[0], f.season, f.show, f.book} {
		if err := f.prog.SaveAt(ctx, f.masha, id, 1_000, 2_000, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	// no limit: the movie and the episode only (the rest are not video leaves)
	got := f.progressOf("tok-prog", f.masha, idList(f.movie, f.eps[0], f.season, f.show, f.book))
	if len(got) != 2 || got[strconv.FormatInt(f.movie, 10)].PositionMs != 1_000 || got[strconv.FormatInt(f.eps[0], 10)].PositionMs != 1_000 {
		t.Fatalf("no limit: %+v", got)
	}
	// limited, the movie over it, the series within
	f.limit("6+", "16+", "0+")
	got = f.progressOf("tok-prog", f.masha, idList(f.movie, f.eps[0], 999_999))
	if _, hidden := got[strconv.FormatInt(f.movie, 10)]; hidden || len(got) != 1 || got[strconv.FormatInt(f.eps[0], 10)].PositionMs != 1_000 {
		t.Fatalf("limited: %+v", got)
	}
	// and the series over it too
	f.limit("6+", "0+", "16+")
	got = f.progressOf("tok-prog", f.masha, idList(f.movie, f.eps[0]))
	if _, hidden := got[strconv.FormatInt(f.eps[0], 10)]; hidden || len(got) != 1 {
		t.Fatalf("series over the limit: %+v", got)
	}
}

// What a malformed request gets.
func TestAddonProgress_BadRequests(t *testing.T) {
	f := newProgFix(t)
	for _, q := range []string{
		"/progress?items=1",                                    // no profile
		"/progress?userId=999999&items=1",                      // unknown profile
		"/progress?userId=x&items=1",                           // not an id
		fmt.Sprintf("/progress?userId=%d&items=x", f.masha),    // not an id
		fmt.Sprintf("/progress?userId=%d&items=1,,2", f.masha), // empty id
		fmt.Sprintf("/progress?userId=%d&items=0", f.masha),    // not positive
	} {
		f.get("tok-prog", q, 400, nil)
	}
	at := time.Now().Add(-time.Minute)
	for _, body := range []any{
		`{`, // not JSON
		map[string]any{"itemId": f.movie, "positionMs": 1, "durationMs": 2}, // no profile
		save(999_999, f.movie, 1, 2, at, false),                             // unknown profile
		save(f.masha, 0, 1, 2, at, false),                                   // no item
		save(f.masha, f.movie, -1, 2, at, false),                            // negative position
		save(f.masha, f.movie, 1, -2, at, false),                            // negative duration
		`{"userId":` + itoa(f.masha) + `,"itemId":` + itoa(f.movie) + `,"positionMs":1,"durationMs":2,"at":"yesterday"}`, // bad at
	} {
		f.put("tok-prog", body, 400)
	}
	if _, err := f.prog.Get(context.Background(), f.masha, f.movie); err == nil {
		t.Fatal("a refused write was kept")
	}
}

// Both routes of the progress API are mounted.
func TestAddonProgress_RoutesMounted(t *testing.T) {
	_, routes := addonCoreRoutes(t)
	for _, rt := range []string{"GET /api/addon-core/v1/progress", "PUT /api/addon-core/v1/progress"} {
		if !routes[rt] {
			t.Errorf("%s is not mounted", rt)
		}
	}
}
