package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
)

// corePlay is the addon env with the input registry and a small library:
// an unrated movie, a 16+ movie, a series with one episode. The addon
// "pl" has play (access "adults"), "pladm" has play with the default
// (admin-only) access, "hello" has no play.
type corePlay struct {
	*coreWrite
	inputs                      *addons.Inputs
	movie, rated, show, episode int64
	adminID, adultID, kidID     int64
	limitedID                   int64 // an adult limited to 12+
}

func newCorePlay(t *testing.T) *corePlay {
	t.Helper()
	c := &corePlay{}
	cw := &coreWrite{}
	cw.addonEnv = newAddonEnv(t, func(d *Deps) {
		c.inputs = &addons.Inputs{Serving: d.Addons.Serving}
		d.AddonInputs = c.inputs
	})
	c.coreWrite = cw
	ctx := context.Background()
	repo := library.NewRepo(c.db)
	var err error
	if c.movie, err = repo.UpsertMovie(ctx, library.MovieInput{Title: "Матрица", TmdbID: 603,
		File: library.FileInput{Path: "/m/Matrix (1999)/Matrix.mkv", Size: 10}}); err != nil {
		t.Fatal(err)
	}
	if c.rated, err = repo.UpsertMovie(ctx, library.MovieInput{Title: "Чужой", TmdbID: 348, Rating: "16+",
		File: library.FileInput{Path: "/m/Alien (1979)/Alien.mkv", Size: 10}}); err != nil {
		t.Fatal(err)
	}
	if c.episode, err = repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: 1, Episode: 1,
		File: library.FileInput{Path: "/s/Shogun/S01/Shogun.S01E01.mkv", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	c.db.QueryRow(`SELECT p.parent_id FROM items i JOIN items p ON p.id=i.parent_id WHERE i.id=?`, c.episode).Scan(&c.show)
	if c.show == 0 {
		t.Fatal("no series item")
	}
	users, err := c.store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		switch {
		case u.IsAdmin:
			c.adminID = u.ID
		case u.IsKid:
			c.kidID = u.ID
		default:
			c.adultID = u.ID
		}
	}
	lim, err := c.store.CreateUser(ctx, auth.NewUser{Name: "Лена", MaxRating: "12+"})
	if err != nil {
		t.Fatal(err)
	}
	c.limitedID = lim.ID

	hello := c.fake.installed["hello"]
	pl := hello
	pl.ID, pl.Manifest.ID, pl.Manifest.Permissions, pl.Manifest.Access = "pl", "pl", []string{"play"}, "adults"
	pladm := pl
	pladm.ID, pladm.Manifest.ID, pladm.Manifest.Access = "pladm", "pladm", ""
	c.fake.mu.Lock()
	c.fake.installed["pl"], c.fake.installed["pladm"] = pl, pladm
	c.fake.tokens["tok-pl"], c.fake.tokens["tok-pladm"] = "pl", "pladm"
	for _, id := range []string{"pl", "pladm"} {
		c.fake.status[id], c.fake.sock[id] = addons.StatusRunning, "/run/"+id+".1.sock"
	}
	c.fake.mu.Unlock()
	return c
}

func (c *corePlay) req(user int64) addonkit.InputRequest {
	return addonkit.InputRequest{UserID: user, Path: "/media/1", Name: "Matrix/Matrix.mkv", Size: 1 << 20}
}

func TestAddonCorePlay_Register(t *testing.T) {
	c := newCorePlay(t)
	var got addonkit.InputCreated
	before := time.Now()
	c.call("POST", "tok-pl", "/play/inputs", c.req(c.adultID), 201, &got)
	if len(got.ID) != 32 {
		t.Fatalf("id %q", got.ID)
	}
	in, ok := c.inputs.Get(got.ID)
	if !ok || in.AddonID != "pl" || in.Socket != "/run/pl.1.sock" || in.Req.UserID != c.adultID {
		t.Fatalf("registry: %+v %v", in, ok)
	}
	if got.ExpiresAt.Before(before.Add(addons.InputTTL-time.Minute)) || got.ExpiresAt.After(time.Now().Add(addons.InputTTL+time.Second)) {
		t.Errorf("expiresAt %v is not about now + %v", got.ExpiresAt, addons.InputTTL)
	}
	// tied to a movie and to an episode
	for _, item := range []int64{c.movie, c.episode} {
		r := c.req(c.adultID)
		r.ItemID = item
		c.call("POST", "tok-pl", "/play/inputs", r, 201, nil)
	}
	// the admin may see the 16+ movie
	r := c.req(c.adminID)
	r.ItemID = c.rated
	c.call("POST", "tok-pl", "/play/inputs", r, 201, nil)
}

func TestAddonCorePlay_Permission(t *testing.T) {
	c := newCorePlay(t)
	if s := c.call("POST", "tok-hello", "/play/inputs", c.req(c.adultID), 403, nil); !strings.Contains(s, "нет такого разрешения") {
		t.Errorf("POST: %s", s)
	}
	var got addonkit.InputCreated
	c.call("POST", "tok-pl", "/play/inputs", c.req(c.adultID), 201, &got)
	if s := c.call("DELETE", "tok-hello", "/play/inputs/"+got.ID, nil, 403, nil); !strings.Contains(s, "нет такого разрешения") {
		t.Errorf("DELETE: %s", s)
	}
	if _, ok := c.inputs.Get(got.ID); !ok {
		t.Fatal("refused DELETE dropped the input")
	}
	// permission first: even an invalid body is 403
	w := c.do("POST", "/api/addon-core/v1/play/inputs", "{", nil, "Authorization", "Bearer tok-hello")
	if w.Code != 403 {
		t.Errorf("bad body without play: %d", w.Code)
	}
}

func TestAddonCorePlay_Refusals(t *testing.T) {
	c := newCorePlay(t)
	cases := []struct {
		name string
		mod  func(*addonkit.InputRequest)
		want string
	}{
		{"hook path", func(r *addonkit.InputRequest) { r.Path = "/hooks/files.plan" }, "Неверное описание видео: неверный путь"},
		{"escaped path", func(r *addonkit.InputRequest) { r.Path = "/a%2fb" }, "Неверное описание видео: неверный путь"},
		{"no user", func(r *addonkit.InputRequest) { r.UserID = 0 }, "Неверное описание видео: неверный профиль"},
		{"user unknown", func(r *addonkit.InputRequest) { r.UserID = 9999 }, "Профиль не найден"},
		{"kid", func(r *addonkit.InputRequest) { r.UserID = c.kidID }, "Детскому профилю это недоступно"},
		{"item absent", func(r *addonkit.InputRequest) { r.ItemID = 9999 }, "Элемент медиатеки не найден"},
		{"item is a series", func(r *addonkit.InputRequest) { r.ItemID = c.show }, "Это не фильм и не серия"},
		{"item above the ceiling", func(r *addonkit.InputRequest) { r.UserID, r.ItemID = c.limitedID, c.rated }, "Элемент медиатеки не найден"},
		{"unrated item, limited profile", func(r *addonkit.InputRequest) { r.UserID, r.ItemID = c.limitedID, c.movie }, "Элемент медиатеки не найден"},
	}
	for _, tc := range cases {
		r := c.req(c.adultID)
		tc.mod(&r)
		s := c.call("POST", "tok-pl", "/play/inputs", r, 400, nil)
		if !strings.Contains(s, tc.want) {
			t.Errorf("%s: %s (want %q)", tc.name, s, tc.want)
		}
	}
}

func TestAddonCorePlay_AdminOnlyAddon(t *testing.T) {
	c := newCorePlay(t)
	c.call("POST", "tok-pladm", "/play/inputs", c.req(c.adminID), 201, nil)
	if s := c.call("POST", "tok-pladm", "/play/inputs", c.req(c.adultID), 400, nil); !strings.Contains(s, "Профилю это расширение недоступно") {
		t.Errorf("adult with an admin-only addon: %s", s)
	}
	c.fake.mu.Lock()
	in := c.fake.installed["pladm"]
	in.Manifest.Access = "adults"
	c.fake.installed["pladm"] = in
	c.fake.mu.Unlock()
	c.call("POST", "tok-pladm", "/play/inputs", c.req(c.adultID), 201, nil)
}

func TestAddonCorePlay_BodyLimit(t *testing.T) {
	c := newCorePlay(t)
	r := c.req(c.adultID)
	b, _ := json.Marshal(r)
	big := `{"pad":"` + strings.Repeat("x", 65<<10) + `",` + string(b[1:])
	w := c.do("POST", "/api/addon-core/v1/play/inputs", big, nil, "Authorization", "Bearer tok-pl", "Content-Type", "application/json")
	if w.Code != 413 || !strings.Contains(w.Body.String(), "больше 64 КБ") {
		t.Fatalf("65 KiB body: %d %s", w.Code, w.Body)
	}
	// just under the limit is read (and judged on its content)
	ok := `{"pad":"` + strings.Repeat("x", 60<<10) + `",` + string(b[1:])
	if w := c.do("POST", "/api/addon-core/v1/play/inputs", ok, nil, "Authorization", "Bearer tok-pl", "Content-Type", "application/json"); w.Code != 201 {
		t.Fatalf("60 KiB body: %d %s", w.Code, w.Body)
	}
}

func TestAddonCorePlay_NotServing(t *testing.T) {
	c := newCorePlay(t)
	c.fake.mu.Lock()
	c.fake.status["pl"] = addons.StatusStarting
	c.fake.mu.Unlock()
	if s := c.call("POST", "tok-pl", "/play/inputs", c.req(c.adultID), 503, nil); !strings.Contains(s, "Расширение запускается") {
		t.Errorf("starting: %s", s)
	}
	c.fake.mu.Lock()
	c.fake.status["pl"] = addons.StatusCrashed
	c.fake.mu.Unlock()
	if s := c.call("POST", "tok-pl", "/play/inputs", c.req(c.adultID), 503, nil); !strings.Contains(s, "Расширение не запущено") {
		t.Errorf("crashed: %s", s)
	}
}

func TestAddonCorePlay_Drop(t *testing.T) {
	c := newCorePlay(t)
	var mine, theirs addonkit.InputCreated
	c.call("POST", "tok-pl", "/play/inputs", c.req(c.adultID), 201, &mine)
	c.call("POST", "tok-pladm", "/play/inputs", c.req(c.adminID), 201, &theirs)
	c.call("DELETE", "tok-pl", "/play/inputs/"+mine.ID, nil, 204, nil)
	if _, ok := c.inputs.Get(mine.ID); ok {
		t.Fatal("dropped input still there")
	}
	if s := c.call("DELETE", "tok-pl", "/play/inputs/"+theirs.ID, nil, 404, nil); !strings.Contains(s, "Видео не найдено") {
		t.Errorf("another addon's: %s", s)
	}
	if _, ok := c.inputs.Get(theirs.ID); !ok {
		t.Fatal("another addon's input was dropped")
	}
	c.call("DELETE", "tok-pl", "/play/inputs/"+mine.ID, nil, 404, nil)
	c.call("DELETE", "tok-pl", "/play/inputs/nope", nil, 404, nil)
}

func TestAddonCorePlay_NoRegistry(t *testing.T) {
	cw := &coreWrite{}
	cw.addonEnv = newAddonEnv(t)
	hello := cw.fake.installed["hello"]
	hello.Manifest.Permissions = []string{"play"}
	cw.fake.mu.Lock()
	cw.fake.installed["hello"] = hello
	cw.fake.mu.Unlock()
	cw.call("POST", "tok-hello", "/play/inputs", addonkit.InputRequest{UserID: 1, Path: "/a", Name: "a.mkv", Size: 1}, 503, nil)
	cw.call("DELETE", "tok-hello", "/play/inputs/x", nil, 503, nil)
}

func TestAddonCorePlay_NotJSON(t *testing.T) {
	c := newCorePlay(t)
	b, _ := json.Marshal(c.req(c.adultID))
	for name, body := range map[string]string{
		"not JSON":       "hello",
		"cut short":      string(b[:len(b)-1]),
		"trailing JSON":  string(b) + `{"userId":1}`,
		"trailing bytes": string(b) + " x",
	} {
		w := c.do("POST", "/api/addon-core/v1/play/inputs", body, nil, "Authorization", "Bearer tok-pl", "Content-Type", "application/json")
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// trailing white space is still one JSON value
	if w := c.do("POST", "/api/addon-core/v1/play/inputs", string(b)+"\n ", nil, "Authorization", "Bearer tok-pl", "Content-Type", "application/json"); w.Code != 201 {
		t.Errorf("trailing new line: %d %s", w.Code, w.Body)
	}
}

func TestAddonCorePlay_EpisodeOfRatedSeries(t *testing.T) {
	c := newCorePlay(t)
	ep, err := library.NewRepo(c.db).UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Игра", SeriesTmdbID: 77,
		SeriesRating: "16+", Season: 1, Episode: 1, File: library.FileInput{Path: "/s/Game/S01/Game.S01E01.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	r := c.req(c.limitedID)
	r.ItemID = ep
	if s := c.call("POST", "tok-pl", "/play/inputs", r, 400, nil); !strings.Contains(s, "Элемент медиатеки не найден") {
		t.Errorf("episode of a 16+ series for a 12+ profile: %s", s)
	}
	r.UserID = c.adultID
	c.call("POST", "tok-pl", "/play/inputs", r, 201, nil)
}
