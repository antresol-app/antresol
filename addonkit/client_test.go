package addonkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// seen is one request the fake core got.
type seen struct {
	Method, Path, RawPath, Query, Auth, Type string
	Body                                     map[string]any
}

// fakeCore answers every request with status and body, recording it.
func fakeCore(t *testing.T, status int, body string) (*Client, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{Method: r.Method, Path: r.URL.Path, RawPath: r.URL.RawPath, Query: r.URL.RawQuery,
			Auth: r.Header.Get("Authorization"), Type: r.Header.Get("Content-Type")}
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			if err := json.Unmarshal(b, &s.Body); err != nil {
				t.Errorf("body is not a JSON object: %s", b)
			}
		}
		got = append(got, s)
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Env{CoreURL: srv.URL + "/", Token: "tok"}), &got
}

func one(t *testing.T, got *[]seen, method, path, query string) seen {
	t.Helper()
	if len(*got) != 1 {
		t.Fatalf("%d requests, want 1", len(*got))
	}
	s := (*got)[0]
	if s.Method != method || s.Path != "/api/addon-core/v1"+path || s.Query != query {
		t.Fatalf("got %s %s ?%s, want %s %s ?%s", s.Method, s.Path, s.Query, method, path, query)
	}
	if s.Auth != "Bearer tok" {
		t.Fatalf("Authorization %q", s.Auth)
	}
	if s.Body != nil && s.Type != "application/json" {
		t.Fatalf("Content-Type %q", s.Type)
	}
	return s
}

var ctx = context.Background()

func TestFromEnv(t *testing.T) {
	t.Setenv("ANTRESOL_ADDON_ID", "my-addon")
	t.Setenv("ANTRESOL_ADDON_DATA_DIR", "/d")
	t.Setenv("ANTRESOL_ADDON_SOCKET", "/s.sock")
	t.Setenv("ANTRESOL_CORE_URL", "http://127.0.0.1:1")
	t.Setenv("ANTRESOL_ADDON_TOKEN", "tok")
	t.Setenv("ANTRESOL_CONTRACT", "1")
	t.Setenv("ANTRESOL_LOCALE", "en")
	t.Setenv("ANTRESOL_MY_ADDON_KEY", "v")
	e, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Env{ID: "my-addon", DataDir: "/d", Socket: "/s.sock", CoreURL: "http://127.0.0.1:1", Token: "tok", Locale: "en", Contract: 1}
	if e != want {
		t.Fatalf("got %+v", e)
	}
	if e.Prefix() != "ANTRESOL_MY_ADDON_" || e.Var("KEY") != "v" || e.Var("NONE") != "" {
		t.Fatalf("prefix %q var %q", e.Prefix(), e.Var("KEY"))
	}
	t.Setenv("ANTRESOL_CONTRACT", "x")
	if _, err := FromEnv(); err == nil {
		t.Fatal("a bad contract: want an error")
	}
	t.Setenv("ANTRESOL_CONTRACT", "1")
	t.Setenv("ANTRESOL_ADDON_SOCKET", "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("no socket: want an error")
	}
}

func TestClient_InfoAndEvent(t *testing.T) {
	c, got := fakeCore(t, 200, `{"id":"a","version":"1.0.0","contract":1,"ffmpeg":"/ff","libraryRoots":{"movies":"/m"}}`)
	info, err := c.Info(ctx)
	if err != nil || info.ID != "a" || info.Contract != 1 || info.FFmpeg != "/ff" || info.LibraryRoots.Movies != "/m" {
		t.Fatalf("%+v %v", info, err)
	}
	one(t, got, "GET", "/info", "")

	c, got = fakeCore(t, 204, "")
	if err := c.Event(ctx, "ping", map[string]string{"from": "Мама"}); err != nil {
		t.Fatal(err)
	}
	s := one(t, got, "POST", "/events", "")
	if s.Body["name"] != "ping" || s.Body["data"].(map[string]any)["from"] != "Мама" {
		t.Fatalf("body %v", s.Body)
	}
}

func TestClient_APIError(t *testing.T) {
	c, _ := fakeCore(t, 403, `{"error":"У расширения нет такого разрешения"}`)
	_, err := c.Info(ctx)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 403 || ae.Message != "У расширения нет такого разрешения" {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error text %q", err)
	}
	// not JSON: the status text
	c, _ = fakeCore(t, 502, `oops`)
	if err := c.Event(ctx, "x", nil); !errors.As(err, &ae) || ae.Status != 502 || ae.Message == "" {
		t.Fatalf("%#v", err)
	}
	// a 404 is an error where nothing says "not found" is fine
	c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
	if _, err := c.Item(ctx, 5); !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("%#v", err)
	}
	if !IsNotFound(err404(c)) {
		t.Fatal("IsNotFound")
	}
}

func err404(c *Client) error { _, err := c.Item(ctx, 1); return err }

func TestClient_Items(t *testing.T) {
	c, got := fakeCore(t, 200, `{"id":7,"kind":"movie","title":"T","tmdbId":550,"meta":{"a":1}}`)
	it, err := c.Item(ctx, 7)
	if err != nil || it.ID != 7 || it.TMDBID != 550 || string(it.Meta) != `{"a":1}` {
		t.Fatalf("%+v %v", it, err)
	}
	one(t, got, "GET", "/library/items/7", "")

	c, got = fakeCore(t, 200, `[{"id":1,"itemId":7,"path":"/m/a.mkv","season":1,"episode":2}]`)
	fs, err := c.ItemFiles(ctx, 7)
	if err != nil || len(fs) != 1 || fs[0].Path != "/m/a.mkv" || fs[0].Episode != 2 {
		t.Fatalf("%+v %v", fs, err)
	}
	one(t, got, "GET", "/library/items/7/files", "")

	c, got = fakeCore(t, 200, `{"id":7,"kind":"series"}`)
	it, ok, err := c.ItemByTMDB(ctx, "series", 99)
	if err != nil || !ok || it.ID != 7 {
		t.Fatalf("%+v %v %v", it, ok, err)
	}
	one(t, got, "GET", "/library/tmdb/series/99", "")

	c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
	if _, ok, err := c.ItemByTMDB(ctx, "movie", 1); ok || err != nil {
		t.Fatalf("404: ok %v err %v", ok, err)
	}
	c, _ = fakeCore(t, 500, `{"error":"x"}`)
	if _, ok, err := c.ItemByTMDB(ctx, "movie", 1); ok || err == nil {
		t.Fatalf("500: ok %v err %v", ok, err)
	}

	c, got = fakeCore(t, 200, `{"550":7,"13":8}`)
	ids, err := c.ItemIDsByTMDB(ctx, "movie", []int64{550, 13, 1})
	if err != nil || !reflect.DeepEqual(ids, map[int64]int64{550: 7, 13: 8}) {
		t.Fatalf("%v %v", ids, err)
	}
	one(t, got, "GET", "/library/tmdb/movie", "ids=550%2C13%2C1")

	// no ids: no call; more than 500: several calls
	c, got = fakeCore(t, 200, `{}`)
	if ids, err := c.ItemIDsByTMDB(ctx, "movie", nil); err != nil || len(ids) != 0 || len(*got) != 0 {
		t.Fatalf("empty: %v %v %d calls", ids, err, len(*got))
	}
	many := make([]int64, 1001)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err := c.ItemIDsByTMDB(ctx, "movie", many); err != nil || len(*got) != 3 {
		t.Fatalf("1001 ids: %v, %d calls", err, len(*got))
	}
}

func TestClient_Finds(t *testing.T) {
	type call struct {
		name  string
		do    func(c *Client) (Item, bool, error)
		path  string
		query string
	}
	calls := []call{
		{"title", func(c *Client) (Item, bool, error) { return c.FindTitle(ctx, "movie", 550, "", "") }, "/library/find/title", "kind=movie&tmdbId=550"},
		{"title music", func(c *Client) (Item, bool, error) { return c.FindTitle(ctx, "music", 0, "Album & Co", "Артист") },
			"/library/find/title", "creator=%D0%90%D1%80%D1%82%D0%B8%D1%81%D1%82&kind=music&title=Album+%26+Co"},
		{"album", func(c *Client) (Item, bool, error) { return c.FindAlbum(ctx, "A", "B") }, "/library/find/album", "album=B&artist=A"},
		{"mbid", func(c *Client) (Item, bool, error) { return c.FindAlbumByMBID(ctx, "abc") }, "/library/find/album", "mbid=abc"},
		{"book", func(c *Client) (Item, bool, error) { return c.FindBook(ctx, "book", "", "T") }, "/library/find/book", "kind=book&title=T"},
		{"book author", func(c *Client) (Item, bool, error) { return c.FindBook(ctx, "audiobook", "Au", "T") }, "/library/find/book", "author=Au&kind=audiobook&title=T"},
	}
	for _, k := range calls {
		c, got := fakeCore(t, 200, `{"id":3}`)
		it, ok, err := k.do(c)
		if err != nil || !ok || it.ID != 3 {
			t.Fatalf("%s: %+v %v %v", k.name, it, ok, err)
		}
		one(t, got, "GET", k.path, k.query)
		c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
		if _, ok, err := k.do(c); ok || err != nil {
			t.Fatalf("%s 404: %v %v", k.name, ok, err)
		}
	}
}

func TestClient_Series(t *testing.T) {
	c, got := fakeCore(t, 200, `[[1,1],[1,2],[2,1]]`)
	have, err := c.HaveEpisodes(ctx, 42)
	if err != nil || !reflect.DeepEqual(have, map[[2]int]bool{{1, 1}: true, {1, 2}: true, {2, 1}: true}) {
		t.Fatalf("%v %v", have, err)
	}
	one(t, got, "GET", "/library/series/42/episodes", "")
	c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
	if have, err := c.HaveEpisodes(ctx, 42); err != nil || len(have) != 0 {
		t.Fatalf("404: %v %v", have, err)
	}

	c, got = fakeCore(t, 200, `{"path":"/tv/Show"}`)
	p, err := c.SeriesFolder(ctx, "/tv", 42)
	if err != nil || p != "/tv/Show" {
		t.Fatalf("%q %v", p, err)
	}
	one(t, got, "GET", "/library/series/42/folder", "root=%2Ftv")

	c, got = fakeCore(t, 200, `{"files":[{"season":1,"episode":1,"titles":["Рус"]}],"profiles":[{"userId":2,"audioTitle":"Рус"}]}`)
	sa, err := c.SeriesAudio(ctx, 42)
	if err != nil || len(sa.Files) != 1 || sa.Files[0].Titles[0] != "Рус" || sa.Profiles[0].UserID != 2 {
		t.Fatalf("%+v %v", sa, err)
	}
	one(t, got, "GET", "/library/series/42/audio", "")
}

func TestClient_Albums(t *testing.T) {
	c, got := fakeCore(t, 200, `{"path":"/music/A/B"}`)
	if p, err := c.AlbumDir(ctx, 9); err != nil || p != "/music/A/B" {
		t.Fatalf("%q %v", p, err)
	}
	one(t, got, "GET", "/library/albums/9/dir", "")
	c, got = fakeCore(t, 200, `[{"id":1,"kind":"track","title":"S","albumId":9,"no":1,"endMs":5}]`)
	tr, err := c.AlbumTracks(ctx, 9)
	if err != nil || len(tr) != 1 || tr[0].Title != "S" || tr[0].AlbumID != 9 || *tr[0].EndMs != 5 {
		t.Fatalf("%+v %v", tr, err)
	}
	one(t, got, "GET", "/library/albums/9/tracks", "")
}

func TestClient_Files(t *testing.T) {
	c, got := fakeCore(t, 200, `[{"id":1,"path":"/m/a b/x.mkv"}]`)
	fs, err := c.FilesUnder(ctx, "/m/a b")
	if err != nil || len(fs) != 1 {
		t.Fatalf("%v %v", fs, err)
	}
	one(t, got, "GET", "/library/files", "under=%2Fm%2Fa+b")

	c, got = fakeCore(t, 200, `[{"id":1,"path":"/m/x.mkv","probe":{"d":1}}]`)
	f, ok, err := c.FileByPath(ctx, "/m/x.mkv")
	if err != nil || !ok || f.ID != 1 || string(f.Probe) != `{"d":1}` {
		t.Fatalf("%+v %v %v", f, ok, err)
	}
	one(t, got, "GET", "/library/files", "path=%2Fm%2Fx.mkv")
	c, _ = fakeCore(t, 200, `[]`)
	if _, ok, err := c.FileByPath(ctx, "/m/y.mkv"); ok || err != nil {
		t.Fatalf("none: %v %v", ok, err)
	}

	c, got = fakeCore(t, 200, `["/m/a","/m/b"]`)
	ps, err := c.FilePaths(ctx)
	if err != nil || !reflect.DeepEqual(ps, []string{"/m/a", "/m/b"}) {
		t.Fatalf("%v %v", ps, err)
	}
	one(t, got, "GET", "/library/paths", "")

	c, got = fakeCore(t, 200, `[{"id":1},{"id":2}]`)
	items, err := c.SearchTitles(ctx, "movie", "Ёлки", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %v", items, err)
	}
	one(t, got, "GET", "/library/search", "kind=movie&limit=10&title=%D0%81%D0%BB%D0%BA%D0%B8")
	c, got = fakeCore(t, 200, `[]`)
	if _, err := c.SearchTitles(ctx, "movie", "T", 0); err != nil {
		t.Fatal(err)
	}
	one(t, got, "GET", "/library/search", "kind=movie&title=T")
}

func TestClient_Refs(t *testing.T) {
	c, got := fakeCore(t, 200, `[{"fileId":1,"itemId":2,"path":"/m/a"}]`)
	rf, err := c.RefFiles(ctx, "a/b c%")
	if err != nil || len(rf) != 1 || rf[0].FileID != 1 {
		t.Fatalf("%v %v", rf, err)
	}
	s := one(t, got, "GET", "/library/refs/a/b c%", "")
	if s.RawPath != "/api/addon-core/v1/library/refs/a%2Fb%20c%25" {
		t.Fatalf("raw path %q", s.RawPath)
	}

	c, got = fakeCore(t, 200, `[{"id":1,"fileId":3}]`)
	tr, err := c.TracksOfRef(ctx, "r 1")
	if err != nil || len(tr) != 1 || tr[0].FileID != 3 {
		t.Fatalf("%v %v", tr, err)
	}
	one(t, got, "GET", "/library/tracks", "ref=r+1")

	c, got = fakeCore(t, 200, `{"bound":1,"missing":["/m/x"],"taken":[]}`)
	b, err := c.BindRef(ctx, "r1", nil)
	if err != nil || b.Bound != 1 || b.Missing[0] != "/m/x" {
		t.Fatalf("%+v %v", b, err)
	}
	s = one(t, got, "PUT", "/library/refs/r1", "")
	if p, ok := s.Body["paths"].([]any); !ok || len(p) != 0 {
		t.Fatalf("nil paths must go as []: %v", s.Body)
	}

	c, got = fakeCore(t, 204, "")
	if err := c.UnbindRef(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	one(t, got, "DELETE", "/library/refs/r1", "")

	c, got = fakeCore(t, 200, `{"bound":2}`)
	if b, err := c.BindRefs(ctx, []RefPath{{Ref: "a", Path: "/m/a"}, {Ref: "b", Path: "/m/b"}}); err != nil || b.Bound != 2 {
		t.Fatalf("%+v %v", b, err)
	}
	s = one(t, got, "POST", "/library/refs", "")
	if refs := s.Body["refs"].([]any); len(refs) != 2 || refs[1].(map[string]any)["path"] != "/m/b" {
		t.Fatalf("%v", s.Body)
	}
}

func TestClient_Writes(t *testing.T) {
	c, got := fakeCore(t, 200, `{"files":[{"path":"/m/a","fileId":1,"itemId":2}],"missing":[],"taken":[]}`)
	res, err := c.Import(ctx, []string{"/m/a"}, "r1", 3)
	if err != nil || len(res.Files) != 1 || res.Files[0].ItemID != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	s := one(t, got, "POST", "/library/import", "")
	if s.Body["ref"] != "r1" || s.Body["ownerUserId"] != float64(3) || len(s.Body["paths"].([]any)) != 1 {
		t.Fatalf("%v", s.Body)
	}
	c, got = fakeCore(t, 200, `{}`)
	if _, err := c.Import(ctx, []string{"/m/a"}, "", 0); err != nil {
		t.Fatal(err)
	}
	s = one(t, got, "POST", "/library/import", "")
	if _, has := s.Body["ref"]; has {
		t.Fatalf("empty ref sent: %v", s.Body)
	}
	if _, has := s.Body["ownerUserId"]; has {
		t.Fatalf("zero owner sent: %v", s.Body)
	}

	c, got = fakeCore(t, 204, "")
	if err := c.ClaimOwner(ctx, "r1", nil, 4); err != nil {
		t.Fatal(err)
	}
	s = one(t, got, "POST", "/library/owners", "")
	if s.Body["ref"] != "r1" || s.Body["userId"] != float64(4) {
		t.Fatalf("%v", s.Body)
	}
	if _, has := s.Body["paths"]; has {
		t.Fatalf("paths with a ref: %v", s.Body)
	}
	c, got = fakeCore(t, 204, "")
	if err := c.ClaimOwner(ctx, "", []string{"/m/a"}, 4); err != nil {
		t.Fatal(err)
	}
	s = one(t, got, "POST", "/library/owners", "")
	if _, has := s.Body["ref"]; has || len(s.Body["paths"].([]any)) != 1 {
		t.Fatalf("%v", s.Body)
	}

	c, got = fakeCore(t, 200, `{"name":"abc.jpg"}`)
	if n, err := c.CacheImage(ctx, "https://img/x.jpg"); err != nil || n != "abc.jpg" {
		t.Fatalf("%q %v", n, err)
	}
	s = one(t, got, "POST", "/images", "")
	if s.Body["url"] != "https://img/x.jpg" {
		t.Fatalf("%v", s.Body)
	}

	c, got = fakeCore(t, 204, "")
	if err := c.PutHolds(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s = one(t, got, "PUT", "/files/holds", "")
	if h, ok := s.Body["holds"].([]any); !ok || len(h) != 0 {
		t.Fatalf("nil holds must go as []: %v", s.Body)
	}
	c, got = fakeCore(t, 204, "")
	if err := c.PutHolds(ctx, []Hold{{Key: "k", Root: "/d/p", Files: []HoldFile{{Path: "/d/p/a", Selected: true}}, Label: Text{RU: "р", EN: "e"}}}); err != nil {
		t.Fatal(err)
	}
	s = one(t, got, "PUT", "/files/holds", "")
	h := s.Body["holds"].([]any)[0].(map[string]any)
	if h["key"] != "k" || h["label"].(map[string]any)["en"] != "e" {
		t.Fatalf("%v", h)
	}
}

func TestClient_Holds(t *testing.T) {
	c, got := fakeCore(t, 200, `{"holds":[{"key":"k","root":"/d/p","files":[{"path":"/d/p/a","size":1,"selected":true}],"active":true,"itemId":0,"busy":false,"label":{"ru":"р","en":"e"},"moveWarning":{"ru":"","en":""},"keptWarning":{"ru":"","en":""}}]}`)
	hs, err := c.Holds(ctx)
	if err != nil || len(hs) != 1 || hs[0].Key != "k" || hs[0].Files[0].Path != "/d/p/a" || !hs[0].Active {
		t.Fatalf("%+v %v", hs, err)
	}
	one(t, got, "GET", "/files/holds", "")
	c, _ = fakeCore(t, 200, `{"holds":[]}`)
	if hs, err := c.Holds(ctx); err != nil || hs == nil || len(hs) != 0 {
		t.Fatalf("empty: %v %v", hs, err)
	}
	c, _ = fakeCore(t, 503, `{"error":"Файлы недоступны"}`)
	if _, err := c.Holds(ctx); err == nil {
		t.Fatal("503 not an error")
	}
}

func TestClient_Users(t *testing.T) {
	c, got := fakeCore(t, 200, `[{"id":1,"name":"Мама","isAdmin":true,"lang":"ru"}]`)
	us, err := c.Users(ctx)
	if err != nil || len(us) != 1 || !us[0].IsAdmin || us[0].Name != "Мама" {
		t.Fatalf("%v %v", us, err)
	}
	one(t, got, "GET", "/users", "")
}

func TestClient_Metadata(t *testing.T) {
	var auth, path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path, query = r.Header.Get("Authorization"), r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(Env{CoreURL: srv.URL, Token: "tok"})
	base := c.MetadataBase("tmdb")
	if base != srv.URL+"/api/addon-core/v1/metadata/tmdb" {
		t.Fatalf("base %q", base)
	}
	resp, err := c.MetadataHTTP().Get(base + "/movie/550?language=ru")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if auth != "Bearer tok" || path != "/api/addon-core/v1/metadata/tmdb/movie/550" || query != "language=ru" {
		t.Fatalf("auth %q path %q query %q", auth, path, query)
	}
	// the token goes to the core only
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
	}))
	defer other.Close()
	auth = "unset"
	resp, err = c.MetadataHTTP().Get(other.URL + "/api/addon-core/v1/metadata/tmdb/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if auth != "" {
		t.Fatalf("the token went to another host: %q", auth)
	}
}

func TestClient_Timeouts(t *testing.T) {
	var deadline time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := NewClient(Env{CoreURL: srv.URL, Token: "tok"})
	c.observe = func(ctx context.Context) {
		d, ok := ctx.Deadline()
		if !ok {
			deadline = -1
			return
		}
		deadline = time.Until(d)
	}
	if err := c.Event(ctx, "x", nil); err != nil {
		t.Fatal(err)
	}
	if deadline <= 0 || deadline > DefaultTimeout {
		t.Fatalf("event deadline %v", deadline)
	}
	if _, err := c.Import(ctx, []string{"/m/a"}, "", 0); err != nil {
		t.Fatal(err)
	}
	if deadline <= 29*time.Minute || deadline > ImportTimeout {
		t.Fatalf("import deadline %v", deadline)
	}
	// a caller's earlier deadline wins
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := c.Import(short, []string{"/m/a"}, "", 0); err != nil {
		t.Fatal(err)
	}
	if deadline > time.Second {
		t.Fatalf("caller deadline lost: %v", deadline)
	}
}

func TestClient_RegisterInput(t *testing.T) {
	c, got := fakeCore(t, 201, `{"id":"abc","expiresAt":"2026-11-14T10:00:00Z"}`)
	in := InputRequest{UserID: 3, Path: "/v/1", Name: "a/b.mkv", Size: 9, ItemID: 4,
		Title:    &InputTitle{Kind: "series", TMDBID: 7, Season: 1, Episode: 2},
		Sidecars: InputSidecars{Audio: []AudioSidecar{{Path: "/a", Name: "a.mka", Size: 1}}, Subtitles: []SubtitleSidecar{{Path: "/s", Name: "s.srt", Lang: "en"}}},
		Slow:     true, Meta: json.RawMessage(`{"k":1}`)}
	res, err := c.RegisterInput(ctx, in)
	if err != nil || res.ID != "abc" || res.ExpiresAt.Year() != 2026 {
		t.Fatalf("%+v %v", res, err)
	}
	s := one(t, got, "POST", "/play/inputs", "")
	if s.Body["userId"] != float64(3) || s.Body["path"] != "/v/1" || s.Body["name"] != "a/b.mkv" || s.Body["size"] != float64(9) || s.Body["itemId"] != float64(4) || s.Body["slow"] != true {
		t.Fatalf("%v", s.Body)
	}
	if tt := s.Body["title"].(map[string]any); tt["tmdbId"] != float64(7) || tt["episode"] != float64(2) {
		t.Fatalf("%v", s.Body)
	}
	sc := s.Body["sidecars"].(map[string]any)
	if sc["audio"].([]any)[0].(map[string]any)["name"] != "a.mka" || sc["subtitles"].([]any)[0].(map[string]any)["lang"] != "en" {
		t.Fatalf("%v", s.Body)
	}
	if s.Body["meta"].(map[string]any)["k"] != float64(1) {
		t.Fatalf("%v", s.Body)
	}

	c, _ = fakeCore(t, 400, `{"error":"Слишком много файлов"}`)
	var ae *APIError
	if _, err := c.RegisterInput(ctx, in); !errors.As(err, &ae) || ae.Status != 400 || ae.Message != "Слишком много файлов" {
		t.Fatalf("%#v", err)
	}
}

func TestClient_DropInput(t *testing.T) {
	c, got := fakeCore(t, 204, "")
	if err := c.DropInput(ctx, "a/b c"); err != nil {
		t.Fatal(err)
	}
	s := (*got)[0]
	if s.Method != "DELETE" || s.RawPath != "/api/addon-core/v1/play/inputs/a%2Fb%20c" {
		t.Fatalf("%+v", s)
	}
	c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
	if err := c.DropInput(ctx, "x"); err != nil {
		t.Fatalf("404: %v", err)
	}
	c, _ = fakeCore(t, 500, `{"error":"boom"}`)
	if err := c.DropInput(ctx, "x"); err == nil {
		t.Fatal("500 must be an error")
	}
}

func TestClient_FileByID(t *testing.T) {
	c, got := fakeCore(t, 200, `{"id":7,"itemId":3,"path":"/m/x.mkv","size":5,"probe":{"d":1}}`)
	f, ok, err := c.FileByID(ctx, 7)
	if err != nil || !ok || f.ID != 7 || f.ItemID != 3 || f.Path != "/m/x.mkv" || string(f.Probe) != `{"d":1}` {
		t.Fatalf("%+v %v %v", f, ok, err)
	}
	one(t, got, "GET", "/library/files/7", "")
	c, _ = fakeCore(t, 404, `{"error":"Файл не найден"}`)
	if _, ok, err := c.FileByID(ctx, 8); ok || err != nil {
		t.Fatalf("none: %v %v", ok, err)
	}
	c, _ = fakeCore(t, 403, `{"error":"Недостаточно прав"}`)
	if _, _, err := c.FileByID(ctx, 8); err == nil {
		t.Fatal("403 is an error")
	}
}

func TestClient_Studios(t *testing.T) {
	c, got := fakeCore(t, 200, `{"studios":["LostFilm","Кубик в Кубе"]}`)
	ss, err := c.Studios(ctx)
	if err != nil || !reflect.DeepEqual(ss, []string{"LostFilm", "Кубик в Кубе"}) {
		t.Fatalf("%v %v", ss, err)
	}
	one(t, got, "GET", "/settings/studios", "")
	c, _ = fakeCore(t, 200, `{"studios":null}`)
	if ss, err := c.Studios(ctx); err != nil || ss == nil || len(ss) != 0 {
		t.Fatalf("none: %#v %v", ss, err)
	}
}

func TestClient_InfoRoots(t *testing.T) {
	c, _ := fakeCore(t, 200, `{"id":"a","mediaRoot":"/media","fileRoots":["/media/movies","/media"]}`)
	info, err := c.Info(ctx)
	if err != nil || info.MediaRoot != "/media" || !reflect.DeepEqual(info.FileRoots, []string{"/media/movies", "/media"}) {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestClient_Notify(t *testing.T) {
	c, got := fakeCore(t, 202, `{"queued":2}`)
	n, err := c.Notify(ctx, NotifyRequest{Topic: "news", Key: "k1", Subject: Text{RU: "Готово", EN: "Done"},
		Body: &Text{RU: "Всё", EN: "All"}, Link: "/item/3", Poster: "https://image.tmdb.org/t/p/a.jpg"})
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	s := one(t, got, "POST", "/notify", "")
	if s.Body["topic"] != "news" || s.Body["key"] != "k1" || s.Body["link"] != "/item/3" || s.Body["poster"] == nil {
		t.Fatalf("%v", s.Body)
	}
	if sub := s.Body["subject"].(map[string]any); sub["ru"] != "Готово" || sub["en"] != "Done" {
		t.Fatalf("%v", s.Body)
	}
	if _, ok := s.Body["userIds"]; ok {
		t.Fatalf("empty userIds sent: %v", s.Body)
	}
	if _, ok := s.Body["audience"]; ok {
		t.Fatalf("empty audience sent: %v", s.Body)
	}

	c, got = fakeCore(t, 202, `{"queued":1}`)
	if n, err := c.Notify(ctx, NotifyRequest{Topic: "news", Key: "k", Subject: Text{RU: "x"}, UserIDs: []int64{4}}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if s := one(t, got, "POST", "/notify", ""); s.Body["userIds"].([]any)[0] != float64(4) || s.Body["body"] != nil {
		t.Fatalf("%v", s.Body)
	}

	c, _ = fakeCore(t, 400, `{"error":"Детскому профилю это недоступно"}`)
	var ae *APIError
	if _, err := c.Notify(ctx, NotifyRequest{Topic: "news", Key: "k", Subject: Text{RU: "x"}}); !errors.As(err, &ae) || ae.Status != 400 || ae.Message != "Детскому профилю это недоступно" {
		t.Fatalf("%#v", err)
	}
}

func TestClient_ApproveRequests(t *testing.T) {
	c, got := fakeCore(t, 200, `{"approved":2}`)
	n, err := c.ApproveRequests(ctx, ApproveRequest{AdminID: 1, Kind: "music", ExtID: "mb-1", Title: "Группа крови", Artist: "Кино"})
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	s := one(t, got, "POST", "/requests/approve", "")
	if s.Body["adminId"] != float64(1) || s.Body["kind"] != "music" || s.Body["extId"] != "mb-1" || s.Body["title"] != "Группа крови" ||
		s.Body["artist"] != "Кино" || s.Body["tmdbId"] != float64(0) {
		t.Fatalf("%v", s.Body)
	}
	c, _ = fakeCore(t, 400, `{"error":"Одобрить может только администратор"}`)
	var ae *APIError
	if _, err := c.ApproveRequests(ctx, ApproveRequest{AdminID: 2, Kind: "movie", TMDBID: 7}); !errors.As(err, &ae) || ae.Status != 400 {
		t.Fatalf("%#v", err)
	}
}

func TestClient_Watched(t *testing.T) {
	c, got := fakeCore(t, 200, `{"watched":{"7":true,"9":true}}`)
	m, err := c.Watched(ctx, 2, []int64{7, 8}, []int64{9})
	if err != nil || !reflect.DeepEqual(m, map[int64]bool{7: true, 9: true}) {
		t.Fatalf("%v %v", m, err)
	}
	one(t, got, "GET", "/watched", "items=7%2C8&series=9&userId=2")

	// nothing asked: no call; more than 500 in a list: several calls
	c, got = fakeCore(t, 200, `{"watched":{}}`)
	if m, err := c.Watched(ctx, 2, nil, nil); err != nil || len(m) != 0 || len(*got) != 0 {
		t.Fatalf("empty: %v %v %d calls", m, err, len(*got))
	}
	many := make([]int64, 1001)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err := c.Watched(ctx, 2, many, many[:600]); err != nil || len(*got) != 3 {
		t.Fatalf("1001+600 ids: %v, %d calls", err, len(*got))
	}
	for _, s := range *got {
		for _, k := range []string{"items", "series"} {
			if v, _ := url.ParseQuery(s.Query); len(strings.Split(v.Get(k), ",")) > 500 {
				t.Fatalf("%s: more than 500 ids in a call", k)
			}
		}
	}
	c, _ = fakeCore(t, 200, `{"watched":{"x":true}}`)
	if _, err := c.Watched(ctx, 2, []int64{1}, nil); err == nil {
		t.Fatal("a bad id is an error")
	}
	c, _ = fakeCore(t, 400, `{"error":"Нет такого профиля"}`)
	if _, err := c.Watched(ctx, 99, []int64{1}, nil); err == nil {
		t.Fatal("400 is an error")
	}
}

func TestUser_Languages(t *testing.T) {
	c, _ := fakeCore(t, 200, `[{"id":1,"name":"Мама","lang":"ru","languages":["en","ru"]},{"id":2,"name":"Вася","languages":[]}]`)
	us, err := c.Users(ctx)
	if err != nil || len(us) != 2 || !reflect.DeepEqual(us[0].Languages, []string{"en", "ru"}) || us[1].Languages == nil || len(us[1].Languages) != 0 {
		t.Fatalf("%+v %v", us, err)
	}
	// the header does not carry them
	r := httptest.NewRequest("GET", "/api/x", nil)
	r.Header.Set(UserHeader, `{"id":1,"name":"A","lang":"ru"}`)
	if u, ok := UserFrom(r); !ok || u.Languages != nil {
		t.Fatalf("header %+v %v", u, ok)
	}
}

// golden reads a recorded answer of the core (testdata/golden/<name>.json).
func golden(t *testing.T, name string) (method, path string, status int, body string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g.Method, strings.TrimPrefix(g.Path, "/api/addon-core/v1"), g.Status, string(g.Body)
}

func TestClient_TrackingTitles(t *testing.T) {
	m, p, code, body := golden(t, "tracking-title")
	if m != "POST" || p != "/tracking/titles" {
		t.Fatalf("golden %s %s", m, p)
	}
	c, got := fakeCore(t, code, body)
	ti, err := c.EnsureTitle(ctx, "series", 126308, "Сёгун", "/shogun.jpg")
	if err != nil || ti.ID == 0 || ti.Kind != "series" || ti.TMDBID != 126308 || ti.Name != "Сёгун" || ti.Poster != "/shogun.jpg" ||
		ti.Status != "ended" || ti.Cert != "18+" {
		t.Fatalf("%+v %v", ti, err)
	}
	s := one(t, got, "POST", "/tracking/titles", "")
	if s.Body["kind"] != "series" || s.Body["tmdbId"] != float64(126308) || s.Body["name"] != "Сёгун" || s.Body["poster"] != "/shogun.jpg" {
		t.Fatalf("%v", s.Body)
	}

	c, got = fakeCore(t, code, body)
	byTMDB, ok, err := c.TitleByTMDB(ctx, "series", 126308)
	if err != nil || !ok || byTMDB != ti {
		t.Fatalf("%+v %v %v", byTMDB, ok, err)
	}
	one(t, got, "GET", "/tracking/titles", "kind=series&tmdbId=126308")
	c, got = fakeCore(t, code, body)
	byID, ok, err := c.TitleByID(ctx, ti.ID)
	if err != nil || !ok || byID != ti {
		t.Fatalf("%+v %v %v", byID, ok, err)
	}
	one(t, got, "GET", fmt.Sprintf("/tracking/titles/%d", ti.ID), "")

	c, _ = fakeCore(t, 404, `{"error":"Тайтл не отслеживается"}`)
	if _, ok, err := c.TitleByTMDB(ctx, "movie", 1); ok || err != nil {
		t.Fatalf("404 by tmdb: %v %v", ok, err)
	}
	if _, ok, err := c.TitleByID(ctx, 1); ok || err != nil {
		t.Fatalf("404 by id: %v %v", ok, err)
	}
	if _, err := c.EnsureTitle(ctx, "movie", 1, "", ""); err == nil {
		t.Fatal("ensure: a 404 is an error")
	}
	c, _ = fakeCore(t, 500, `{"error":"x"}`)
	if _, ok, err := c.TitleByID(ctx, 1); ok || err == nil {
		t.Fatalf("500: %v %v", ok, err)
	}

	c, got = fakeCore(t, 204, "")
	if err := c.KickTitle(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if s := one(t, got, "POST", "/tracking/titles/7/kick", ""); s.Body != nil {
		t.Fatalf("kick body %v", s.Body)
	}
	c, _ = fakeCore(t, 404, `{"error":"Тайтл не отслеживается"}`)
	if err := c.KickTitle(ctx, 7); !IsNotFound(err) {
		t.Fatalf("kick 404: %v", err)
	}
}

func TestClient_TrackingEpisodesSources(t *testing.T) {
	_, p, code, body := golden(t, "tracking-episodes")
	c, got := fakeCore(t, code, body)
	id := int64(0)
	fmt.Sscanf(p, "/tracking/titles/%d/episodes", &id)
	eps, err := c.TitleEpisodes(ctx, id)
	if err != nil || len(eps) != 2 || eps[0].TitleID != id || eps[0].Season != 1 || eps[0].Number != 1 || eps[1].Number != 2 ||
		eps[0].Name != "Анджин" || eps[0].AirDate != "2024-02-27" {
		t.Fatalf("%+v %v", eps, err)
	}
	one(t, got, "GET", p, "")

	_, p, code, body = golden(t, "tracking-sources")
	c, got = fakeCore(t, code, body)
	srcs, err := c.TitleSources(ctx, id)
	if err != nil || len(srcs) != 3 {
		t.Fatalf("%+v %v", srcs, err)
	}
	one(t, got, "GET", p, "")
	kinds := map[string]TrackingSource{}
	for _, s := range srcs {
		kinds[s.Kind] = s
	}
	lib, a, b := kinds["library"], kinds["alpha"], kinds["beta"]
	if lib.Meta.ItemID == 0 || lib.Meta.FileID == 0 || lib.Episode != 1 {
		t.Fatalf("library %+v", lib)
	}
	if a.Meta.FileIndex == nil || *a.Meta.FileIndex != 3 || a.FirstSeen.IsZero() || a.Ref != "alpha:1" || a.Quality != "1080p" || len(a.Labels) != 1 ||
		a.Meta.SourceID != "s-1" || a.Meta.Audio != "MVO" || len(a.Meta.Studios) != 1 || len(a.Meta.Subs) != 1 {
		t.Fatalf("alpha %+v", a)
	}
	if b.Meta.Provider != "b-host" || b.Meta.Lang != "ja" || b.Meta.FileIndex != nil {
		t.Fatalf("beta %+v", b)
	}

	// none: empty, not nil
	c, _ = fakeCore(t, 200, `null`)
	if eps, err := c.TitleEpisodes(ctx, 1); err != nil || eps == nil || len(eps) != 0 {
		t.Fatalf("null episodes: %#v %v", eps, err)
	}
	if srcs, err := c.TitleSources(ctx, 1); err != nil || srcs == nil || len(srcs) != 0 {
		t.Fatalf("null sources: %#v %v", srcs, err)
	}
	c, _ = fakeCore(t, 404, `{"error":"Тайтл не отслеживается"}`)
	if _, err := c.TitleSources(ctx, 1); !IsNotFound(err) {
		t.Fatalf("404: %v", err)
	}
}

func TestClient_ItemAliases(t *testing.T) {
	c, _ := fakeCore(t, 200, `{"id":3,"kind":"series","title":"Миллиарды","originalTitle":"Billions","aliases":["Деньги"]}`)
	it, err := c.Item(ctx, 3)
	if err != nil || !reflect.DeepEqual(it.Aliases, []string{"Деньги"}) {
		t.Fatalf("%+v %v", it, err)
	}
	// the core's recorded answers ({time} stamps: only the aliases read):
	// [] when the item has none
	type aliases struct {
		Aliases []string `json:"aliases"`
	}
	for _, name := range []string{"item", "tmdb-item"} {
		_, _, code, body := golden(t, name)
		var it aliases
		if code != 200 || json.Unmarshal([]byte(body), &it) != nil || it.Aliases == nil || len(it.Aliases) != 0 {
			t.Fatalf("%s: %+v (%s)", name, it, body)
		}
	}
	_, _, _, body := golden(t, "search")
	var items []aliases
	if json.Unmarshal([]byte(body), &items) != nil || len(items) != 1 || items[0].Aliases == nil {
		t.Fatalf("search: %s", body)
	}
	// the other answers leave the field out
	if b, _ := json.Marshal(Item{ID: 1}); strings.Contains(string(b), "aliases") {
		t.Fatalf("nil aliases marshalled: %s", b)
	}
}

func TestClient_VoiceoverFit(t *testing.T) {
	c, got := fakeCore(t, 202, "")
	if err := c.QueueFit(ctx, "/m/Show/S01E01.rus.mka", 7, "ja"); err != nil {
		t.Fatal(err)
	}
	s := one(t, got, "POST", "/voiceover/fit", "")
	if s.Body["path"] != "/m/Show/S01E01.rus.mka" || s.Body["fileId"] != float64(7) || s.Body["origLang"] != "ja" {
		t.Fatalf("%v", s.Body)
	}
	c, _ = fakeCore(t, 400, `{"error":"Файл не найден в медиатеке"}`)
	var ae *APIError
	if err := c.QueueFit(ctx, "/etc/passwd", 7, ""); !errors.As(err, &ae) || ae.Status != 400 || ae.Message != "Файл не найден в медиатеке" {
		t.Fatalf("400: %v", err)
	}

	c, got = fakeCore(t, 200, `{"state":"ok","offsetMs":-1500,"tempo":1}`)
	f, ok, err := c.FitState(ctx, "/m/Show/S01E01.rus.mka")
	if err != nil || !ok || f != (FitState{State: FitOK, OffsetMs: -1500, Tempo: 1}) {
		t.Fatalf("%+v %v %v", f, ok, err)
	}
	one(t, got, "GET", "/voiceover/fit", "path=%2Fm%2FShow%2FS01E01.rus.mka")
	c, _ = fakeCore(t, 404, `{"error":"Озвучка не найдена"}`)
	if _, ok, err := c.FitState(ctx, "/m/x.mka"); ok || err != nil {
		t.Fatalf("404: %v %v", ok, err)
	}
	c, _ = fakeCore(t, 500, `{"error":"x"}`)
	if _, ok, err := c.FitState(ctx, "/m/x.mka"); ok || err == nil {
		t.Fatalf("500: %v %v", ok, err)
	}
}

// fromGolden is a fake core answering with the recorded answer of name,
// checked to be the request the client makes; query is the request's
// encoded query.
func fromGolden(t *testing.T, name string) (*Client, func() seen) {
	t.Helper()
	m, p, code, body := golden(t, name)
	c, got := fakeCore(t, code, body)
	return c, func() seen {
		t.Helper()
		path, q, _ := strings.Cut(p, "?")
		if q != "" {
			v, err := url.ParseQuery(q)
			if err != nil {
				t.Fatal(err)
			}
			q = v.Encode()
		}
		return one(t, got, m, path, q)
	}
}

func TestClient_ImportsQueueReads(t *testing.T) {
	c, check := fromGolden(t, "imports-active-count")
	if n, err := c.ImportActiveCount(ctx); err != nil || n != 0 {
		t.Fatalf("active %d %v", n, err)
	}
	check()

	c, check = fromGolden(t, "imports-job-ids")
	if ids, err := c.ImportJobIDs(ctx, "downloading"); err != nil || !reflect.DeepEqual(ids, []int64{1}) {
		t.Fatalf("job ids %v %v", ids, err)
	}
	check()

	c, check = fromGolden(t, "imports-job")
	j, ok, err := c.ImportJob(ctx, 1)
	if err != nil || !ok || j.ID != 1 || j.Status != "downloading" || j.Service != "file" || !j.Favorites ||
		!j.CreatedAt.Equal(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)) || j.StartedAt == nil || j.FinishedAt != nil {
		t.Fatalf("job %+v %v %v", j, ok, err)
	}
	check()
	c, _ = fakeCore(t, 404, `{"error":"Не найдено"}`)
	if _, ok, err := c.ImportJob(ctx, 9); ok || err != nil {
		t.Fatalf("404 job: %v %v", ok, err)
	}

	c, check = fromGolden(t, "imports-candidates")
	gs, err := c.ImportCandidates(ctx, 1, time.Date(2026, 11, 1, 15, 0, 0, 0, time.FixedZone("MSK", 3*3600)))
	if err != nil || len(gs) != 2 || gs[0].ReleaseGroup != "rg-one" || gs[0].State != "waiting" || gs[0].Songs != 2 ||
		gs[0].Clock.Base != -1 || gs[0].WaitAt != nil || gs[0].Aliases == nil || gs[0].Dead == nil {
		t.Fatalf("candidates %+v %v", gs, err)
	}
	check()

	c, check = fromGolden(t, "imports-groups")
	if gs, err := c.ImportGroups(ctx, 1, "waiting"); err != nil || len(gs) != 2 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	check()
	c, check = fromGolden(t, "imports-groups-all")
	gs, err = c.ImportGroups(ctx, 1, "")
	if err != nil || len(gs) != 2 || gs[0].State != "done" || gs[0].AlbumItemID == 0 || gs[1].State != "not_found" ||
		gs[1].SkipKey != "k-0002" || !reflect.DeepEqual(gs[1].Dead, []string{"k-0001"}) || gs[1].Attempts != 1 {
		t.Fatalf("all groups %+v %v", gs, err)
	}
	check()

	c, check = fromGolden(t, "imports-still-downloading")
	if ok, err := c.ImportStillDownloading(ctx, 1); err != nil || !ok {
		t.Fatalf("still %v %v", ok, err)
	}
	check()

	c, check = fromGolden(t, "imports-group-tracks")
	ts, err := c.ImportGroupTracks(ctx, 1, "matched")
	if err != nil || len(ts) != 2 || ts[0].Key != "a" || ts[0].MBReleaseGroup != "rg-one" || ts[0].GroupID != 1 || ts[0].State != "matched" {
		t.Fatalf("tracks %+v %v", ts, err)
	}
	check()
	if ts, err := c.ImportGroupTracks(ctx, 1); err != nil || len(ts) != 0 {
		t.Fatalf("no states: %v %v", ts, err)
	}

	c, check = fromGolden(t, "imports-open")
	if ok, err := c.ImportOpen(ctx, 2, "file"); err != nil || !ok {
		t.Fatalf("open %v %v", ok, err)
	}
	check()
}

func TestClient_ImportsQueueWrites(t *testing.T) {
	type call struct {
		golden string
		do     func(c *Client) error
		body   map[string]any
	}
	ids := func(v ...float64) []any {
		out := []any{}
		for _, x := range v {
			out = append(out, x)
		}
		return out
	}
	for _, k := range []call{
		{"imports-move", func(c *Client) error {
			ok, err := c.MoveImportGroup(ctx, 2, "waiting", "searching")
			if !ok && err == nil {
				err = errors.New("not moved")
			}
			return err
		}, map[string]any{"from": "waiting", "to": "searching"}},
		{"imports-start", func(c *Client) error {
			ok, err := c.StartImportGroup(ctx, 2, "k-0001", 1000, []int64{3}, nil)
			if !ok && err == nil {
				err = errors.New("not started")
			}
			return err
		}, map[string]any{"key": "k-0001", "bytes": 1000.0, "downloading": ids(3), "missing": ids()}},
		{"imports-progress", func(c *Client) error { return c.ImportProgress(ctx, 2, 250) }, map[string]any{"done": 250.0}},
		{"imports-clock", func(c *Client) error {
			return c.SaveImportClock(ctx, 2, "k-0001", ImportRunClock{RunSec: 60, Done: 250})
		}, map[string]any{"key": "k-0001", "clock": map[string]any{"runSec": 60.0, "quietSec": 0.0, "base": 0.0, "done": 250.0}}},
		{"imports-given-up", func(c *Client) error { return c.ImportGivenUp(ctx, 2, "k-0001", false) },
			map[string]any{"key": "k-0001", "slow": false}},
		{"imports-requeue", func(c *Client) error { return c.RequeueImportGroup(ctx, 2, "k-0001", "", 3) },
			map[string]any{"key": "k-0001", "msg": "", "maxDead": 3.0}},
		{"imports-wait", func(c *Client) error {
			return c.WaitImportGroup(ctx, 2, "k-0002", "Кино — Звезда по имени Солнце", 10)
		},
			map[string]any{"key": "k-0002", "name": "Кино — Звезда по имени Солнце", "done": 10.0}},
		{"imports-give-up-wait", func(c *Client) error { return c.GiveUpImportWait(ctx, 2, "k-0002") }, map[string]any{"key": "k-0002"}},
		{"imports-no-format", func(c *Client) error { return c.ImportNoFormat(ctx, 2, []int64{3}, "x") },
			map[string]any{"trackIds": ids(3), "msg": "x"}},
		{"imports-fail", func(c *Client) error { return c.FailImportGroup(ctx, 2, nil, "Ошибка", true) },
			map[string]any{"trackIds": ids(), "msg": "Ошибка", "retry": true}},
		{"imports-not-found", func(c *Client) error { return c.ImportNotFound(ctx, 2, []int64{3}, "y") },
			map[string]any{"trackIds": ids(3), "msg": "y"}},
		{"imports-have", func(c *Client) error { return c.ImportHave(ctx, 1, map[int64]int64{1: 5}, 7, false) },
			map[string]any{"have": map[string]any{"1": 5.0}, "album": 7.0, "done": false}},
		{"imports-finish", func(c *Client) error { return c.FinishImportGroup(ctx, 1, nil, []int64{2}, 7) },
			map[string]any{"done": map[string]any{}, "failed": ids(2), "album": 7.0}},
		{"imports-rebuild", func(c *Client) error { return c.RebuildImport(ctx, 1) }, nil},
		{"imports-delete", func(c *Client) error { return c.DeleteImportJob(ctx, 3) }, nil},
	} {
		c, check := fromGolden(t, k.golden)
		if err := k.do(c); err != nil {
			t.Fatalf("%s: %v", k.golden, err)
		}
		if s := check(); !reflect.DeepEqual(s.Body, k.body) {
			t.Errorf("%s: body %v, want %v", k.golden, s.Body, k.body)
		}
	}
	// a claim another took: ok false, no error; other errors stay errors
	c, _ := fakeCore(t, 409, `{"error":"Альбом уже взят"}`)
	if ok, err := c.MoveImportGroup(ctx, 2, "waiting", "searching"); ok || err != nil {
		t.Fatalf("lost move: %v %v", ok, err)
	}
	if ok, err := c.StartImportGroup(ctx, 2, "k", 1, nil, nil); ok || err != nil {
		t.Fatalf("lost start: %v %v", ok, err)
	}
	if err := c.ImportProgress(ctx, 2, 1); !IsConflict(err) {
		t.Fatalf("409 of another call: %v", err)
	}
	c, _ = fakeCore(t, 500, `{"error":"x"}`)
	if ok, err := c.StartImportGroup(ctx, 2, "k", 1, nil, nil); ok || err == nil || IsConflict(err) {
		t.Fatalf("500 start: %v %v", ok, err)
	}
}

func TestClient_ImportsReadingJobs(t *testing.T) {
	c, check := fromGolden(t, "imports-create")
	id, err := c.CreateReadingJob(ctx, 2, "apple")
	if err != nil || id != 4 {
		t.Fatalf("create %d %v", id, err)
	}
	if s := check(); !reflect.DeepEqual(s.Body, map[string]any{"userId": 2.0, "service": "apple", "reading": true}) {
		t.Fatalf("create body %v", s.Body)
	}

	c, check = fromGolden(t, "imports-reads")
	if ids, err := c.ImportReads(ctx, 2); err != nil || !reflect.DeepEqual(ids, []int64{2, 3, 4}) {
		t.Fatalf("reads %v %v", ids, err)
	}
	check()

	c, check = fromGolden(t, "imports-snapshot")
	added := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	snap := ImportSnapshot{Service: "apple", Account: "masha", Tracks: []ImportSnapTrack{{Key: "t1", Artist: "Кино", Title: "Кукушка",
		Liked: true, AddedAt: &added}}, Playlists: []ImportSnapPlaylist{{ID: "p1", Name: "Дорога", Readable: true, Entries: []string{"t1"}}}}
	if err := c.FillImportJob(ctx, 4, snap); err != nil {
		t.Fatal(err)
	}
	s := check()
	sn, _ := s.Body["snapshot"].(map[string]any)
	tr, _ := sn["tracks"].([]any)
	if sn["service"] != "apple" || len(tr) != 1 || tr[0].(map[string]any)["addedAt"] != "2026-01-02T00:00:00Z" {
		t.Fatalf("snapshot body %v", s.Body)
	}

	c, check = fromGolden(t, "imports-job-fail")
	if ok, err := c.FailImportJob(ctx, 2, "Не удалось прочитать медиатеку"); err != nil || !ok {
		t.Fatalf("fail %v %v", ok, err)
	}
	if s := check(); s.Body["error"] != "Не удалось прочитать медиатеку" {
		t.Fatalf("fail body %v", s.Body)
	}
	c, _ = fakeCore(t, 409, `{"error":"Импорт уже начат"}`)
	if ok, err := c.FailImportJob(ctx, 2, "x"); ok || err != nil {
		t.Fatalf("fail of a job no longer reading: %v %v", ok, err)
	}

	c, check = fromGolden(t, "imports-fail-reading")
	if ids, err := c.FailReadingJobs(ctx, "Не удалось подключиться"); err != nil || !reflect.DeepEqual(ids, []int64{3}) {
		t.Fatalf("fail reading %v %v", ids, err)
	}
	check()
}
