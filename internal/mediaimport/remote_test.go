package mediaimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// Fake Jellyfin, Kavita and Audiobookshelf servers answering the JSON the
// real ones do (Jellyfin 10.10 /Users, /Items; Kavita 0.8
// /api/Plugin/authenticate, /api/Series/all-v2, /api/Series/volumes;
// Audiobookshelf 2.x /api/me, /api/users, /api/users/{id}, /api/items/{id}),
// with the fields the importers read and some neighbours.

func writeJSONT(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

const jfAPIKey = "jf-test-key"

// fakeJellyfin serves users and per-user items by filter; a request
// without the token is 401.
type fakeJellyfin struct {
	users    []map[string]any
	items    map[string]map[string][]map[string]any // user → filter → items
	byID     map[string]map[string]any              // for Ids=
	channels int
	login    map[string]string // user name → password (AuthenticateByName)
	nonAdmin bool              // /Users is 403 (a login of a plain user)
	calls    atomic.Int64
}

func (f *fakeJellyfin) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		auth := r.Header.Get("Authorization")
		if r.URL.Path == "/Users/AuthenticateByName" && r.Method == http.MethodPost {
			if !strings.HasPrefix(auth, "MediaBrowser ") || !strings.Contains(auth, `Client="`) {
				http.Error(w, "no client", http.StatusBadRequest)
				return
			}
			var body struct{ Username, Pw string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if pw, ok := f.login[body.Username]; !ok || pw != body.Pw {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSONT(w, map[string]any{"AccessToken": "tok-" + body.Username, "ServerId": "srv",
				"User": map[string]any{"Id": "U1", "Name": body.Username, "Policy": map[string]any{"IsAdministrator": !f.nonAdmin}}})
			return
		}
		if !strings.Contains(auth, `Token="`+jfAPIKey+`"`) && !strings.Contains(auth, `Token="tok-`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/Users":
			if f.nonAdmin {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeJSONT(w, f.users)
		case "/Users/Me":
			writeJSONT(w, f.users[0])
		case "/LiveTv/Channels":
			writeJSONT(w, map[string]any{"Items": []any{}, "TotalRecordCount": f.channels, "StartIndex": 0})
		case "/Items":
			q := r.URL.Query()
			if q.Get("Recursive") != "true" || !strings.Contains(q.Get("Fields"), "Path") || !strings.Contains(q.Get("Fields"), "ProviderIds") {
				http.Error(w, "fields", http.StatusBadRequest)
				return
			}
			var list []map[string]any
			if ids := q.Get("Ids"); ids != "" {
				for _, id := range strings.Split(ids, ",") {
					if it, ok := f.byID[id]; ok {
						list = append(list, it)
					}
				}
			} else {
				list = f.items[q.Get("userId")][q.Get("Filters")]
			}
			start, _ := strconv.Atoi(q.Get("StartIndex"))
			limit, _ := strconv.Atoi(q.Get("Limit"))
			total := len(list)
			if start > total {
				start = total
			}
			end := total
			if limit > 0 && start+limit < end {
				end = start + limit
			}
			writeJSONT(w, map[string]any{"Items": list[start:end], "TotalRecordCount": total, "StartIndex": start})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func jfAPIEntry(id, typ, name, path string, ud map[string]any, extra map[string]any) map[string]any {
	m := map[string]any{"Id": id, "Type": typ, "Name": name, "ServerId": "srv", "IsFolder": false, "UserData": ud}
	if path != "" {
		m["Path"] = path
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// TestJellyfinAPIImport: the same marks over the API as from jellyfin.db
// — a started movie by path, a played episode by its series' TMDB id, a
// favourite movie and series to «Хочу посмотреть», the TV channels and the
// marks that do not move as warnings.
func TestJellyfinAPIImport(t *testing.T) {
	e := newProgEnv(t)
	movie, _ := e.interstellar()
	e.episode("Breaking Bad", 1396, 1, 1, e.file("series/BB/S01E01.mkv"))
	ep2 := e.episode("Breaking Bad", 1396, 1, 2, e.file("series/BB/S01E02.mkv"))
	series, err := e.lib.ItemByTMDB(context.Background(), "series", 1396)
	if err != nil {
		t.Fatal(err)
	}
	mv := jfAPIEntry("M1", "Movie", "Interstellar", jfMedia+"/movies/Interstellar (2014)/Interstellar.mkv",
		map[string]any{"PlaybackPositionTicks": 40_000_000_000, "PlayCount": 1, "IsFavorite": true, "Played": false, "LastPlayedDate": "2026-10-01T19:30:00.1234567Z"},
		map[string]any{"RunTimeTicks": 100_000_000_000, "ProviderIds": map[string]string{"Tmdb": "157336", "Imdb": "tt0816692"}})
	ep := jfAPIEntry("E2", "Episode", "Cat's in the Bag", "/old/shows/Breaking Bad/s1e2.mkv",
		map[string]any{"PlaybackPositionTicks": 0, "Played": true, "IsFavorite": false, "LastPlayedDate": "2026-10-01T19:30:00.1234567Z"},
		map[string]any{"RunTimeTicks": 30_000_000_000, "IndexNumber": 2, "ParentIndexNumber": 1, "SeriesName": "Breaking Bad", "SeriesId": "S", "ProviderIds": map[string]string{}})
	ser := jfAPIEntry("S", "Series", "Breaking Bad", "/old/Breaking Bad",
		map[string]any{"IsFavorite": true, "Played": false}, map[string]any{"ProviderIds": map[string]string{"Tmdb": "1396"}})
	season := jfAPIEntry("SE", "Season", "Season 1", "", map[string]any{"IsFavorite": true}, nil)
	song := jfAPIEntry("A1", "Audio", "Song", "/music/song.mp3", map[string]any{"Played": true}, nil)
	f := &fakeJellyfin{
		users: []map[string]any{{"Id": "U1", "Name": "Alex", "HasPassword": true}, {"Id": "U2", "Name": "Гость"}},
		items: map[string]map[string][]map[string]any{"U1": {
			"IsPlayed":    {ep, song, ser /* a series counts as played when its episodes are: not a mark of its own */},
			"IsResumable": {mv},
			"IsFavorite":  {mv, ser, season},
		}},
		byID:     map[string]map[string]any{"S": ser},
		channels: 2,
	}
	srv := f.server(t)
	o := e.jfOpts("")
	o.JellyfinDB = filepath.Join(e.root, "missing.db") // the API wins; no database needed
	o.Remotes = map[string]Remote{SourceJellyfin: {URL: srv.URL, APIKey: jfAPIKey}}
	rep := e.run(ImportJellyfin, o)
	if r, ok := e.row(movie); !ok || r.Pos != 4_000_000 || r.Dur != 10_000_000 || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("movie %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if r, ok := e.row(ep2); !ok || !r.Finished || r.Pos != 3_000_000 {
		t.Fatalf("episode %+v %v; lines %+v", r, ok, rep.Lines)
	}
	got := e.watchlist()
	if len(got) != 2 || !(got[0] == movie && got[1] == series.ID || got[1] == movie && got[0] == series.ID) {
		t.Fatalf("watchlist %v; lines %+v", got, rep.Lines)
	}
	var warns []string
	for _, l := range linesWith(rep, StatusWarning) {
		warns = append(warns, l.Reason)
	}
	want := []string{"Jellyfin: ТВ-каналы не переносятся (2 канала)", "Jellyfin: не переносятся 2 отметки (музыка, сезоны)"}
	if !reflect.DeepEqual(warns, want) {
		t.Fatalf("warnings %q", warns)
	}
	if n := len(linesWith(rep, StatusFailed)); n != 0 {
		t.Fatalf("failed lines %+v", rep.Lines)
	}
	// a second run changes nothing
	rep = e.run(ImportJellyfin, o)
	for _, l := range rep.Lines {
		if l.Kind == "progress" && l.Status != StatusSkipped {
			t.Fatalf("second run %+v", l)
		}
	}
	// the users' proposal reads the API too
	users, err := ProposeUsers(context.Background(), o, e.users)
	if err != nil {
		t.Fatal(err)
	}
	uid := e.uid
	if len(users) != 1 || users[0].SourceID != "U1" || users[0].SourceName != "Alex" || users[0].Rows != 4 || users[0].VaultUserID == nil || *users[0].VaultUserID != uid {
		t.Fatalf("users %+v", users)
	}
}

// TestJellyfinAPILogin: a user name and a password sign in; a plain user
// (no right to /Users) moves its own marks.
func TestJellyfinAPILogin(t *testing.T) {
	e := newProgEnv(t)
	movie, _ := e.interstellar()
	mv := jfAPIEntry("M1", "Movie", "Interstellar", jfMedia+"/movies/Interstellar (2014)/Interstellar.mkv",
		map[string]any{"Played": true, "LastPlayedDate": "2026-10-01T19:30:00Z"}, map[string]any{"RunTimeTicks": 100_000_000_000})
	f := &fakeJellyfin{users: []map[string]any{{"Id": "U1", "Name": "alex"}}, nonAdmin: true, login: map[string]string{"alex": "пароль"},
		items: map[string]map[string][]map[string]any{"U1": {"IsPlayed": {mv}}}}
	srv := f.server(t)
	c, tok, err := jellyfinClient(context.Background(), Remote{URL: srv.URL, Username: "alex", Password: "wrong"}, nil)
	var re *RemoteError
	if !errors.As(err, &re) || re.Msg != "Jellyfin не принял имя пользователя или пароль" {
		t.Fatalf("wrong password: %v %v", c, err)
	}
	c, tok, err = jellyfinClient(context.Background(), Remote{URL: srv.URL, Username: "alex", Password: "пароль"}, nil)
	if err != nil || tok != "tok-alex" {
		t.Fatalf("login %q %v", tok, err)
	}
	o := e.jfOpts("")
	o.Remotes = map[string]Remote{SourceJellyfin: {URL: srv.URL, Username: "alex", token: tok}}
	rep := e.run(ImportJellyfin, o)
	if r, ok := e.row(movie); !ok || !r.Finished {
		t.Fatalf("movie %+v %v; lines %+v", r, ok, rep.Lines)
	}
}

func TestJellyfinAPIWrongKey(t *testing.T) {
	e := newProgEnv(t)
	srv := (&fakeJellyfin{users: []map[string]any{{"Id": "U1", "Name": "Alex"}}}).server(t)
	o := e.jfOpts("")
	o.Remotes = map[string]Remote{SourceJellyfin: {URL: srv.URL, APIKey: "nope"}}
	l := onlyLine(t, e.run(ImportJellyfin, o))
	if l.Status != StatusFailed || l.Reason != jfAuthHint {
		t.Fatalf("line %+v", l)
	}
}

// --- Kavita ---

func kvToken(uid int) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"nameid": strconv.Itoa(uid), "unique_name": "x", "role": []string{"Pleb"}})
	return enc([]byte(`{"alg":"HS512","typ":"JWT"}`)) + "." + enc(payload) + ".sig"
}

func fakeKavita(t *testing.T, keys map[string]int, names map[int]string, series map[int][]map[string]any, volumes map[int]map[int][]map[string]any) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/Plugin/authenticate" && r.Method == http.MethodPost {
			uid, ok := keys[r.URL.Query().Get("apiKey")]
			if !ok || r.URL.Query().Get("pluginName") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSONT(w, map[string]any{"username": names[uid], "email": "", "token": kvToken(uid), "refreshToken": "r", "apiKey": "k",
				"preferences": map[string]any{}, "ageRestriction": map[string]any{}, "kavitaVersion": "0.8.4.0"})
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		uid := 0
		for u := range names {
			if tok == kvToken(u) {
				uid = u
			}
		}
		if uid == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/Series/all-v2":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("PageNumber"))
			size, _ := strconv.Atoi(r.URL.Query().Get("PageSize"))
			list := series[uid]
			from := min((page-1)*size, len(list))
			to := min(from+size, len(list))
			w.Header().Set("Pagination", `{"currentPage":`+strconv.Itoa(page)+`,"itemsPerPage":`+strconv.Itoa(size)+`,"totalItems":`+strconv.Itoa(len(list))+`}`)
			writeJSONT(w, list[from:to])
		case "/api/Series/volumes":
			id, _ := strconv.Atoi(r.URL.Query().Get("seriesId"))
			writeJSONT(w, volumes[uid][id])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func kvAPIChap(id, pages, read int, at string, files ...string) map[string]any {
	var fs []map[string]any
	for i, f := range files {
		fs = append(fs, map[string]any{"id": id*10 + i, "filePath": f, "pages": pages, "format": 1, "bytes": 1})
	}
	return map[string]any{"id": id, "range": "1", "number": "1", "pages": pages, "pagesRead": read, "isSpecial": false,
		"lastReadingProgressUtc": at, "files": fs}
}

func TestKavitaAPIImport(t *testing.T) {
	e := newProgEnv(t)
	v1 := e.comic("books/manga/Berserk/Berserk v01.cbz")
	bk := e.book("books/ebooks/Tolstoy/War and Peace.epub")
	at := "2026-10-01T19:30:00.1234567Z"
	srv := fakeKavita(t, map[string]int{"key-alex": 1, "key-kid": 2}, map[int]string{1: "Alex", 2: "Kid"},
		map[int][]map[string]any{
			1: {{"id": 10, "name": "Berserk", "pagesRead": 12, "pages": 80}, {"id": 11, "name": "Unread", "pagesRead": 0, "pages": 10},
				{"id": 12, "name": "War and Peace", "pagesRead": 25, "pages": 100}},
			2: {},
		},
		map[int]map[int][]map[string]any{1: {
			10: {{"id": 1, "number": 1, "chapters": []any{kvAPIChap(1, 40, 12, at, "/books/manga/Berserk/Berserk v01.cbz"), kvAPIChap(2, 40, 0, "", "/books/manga/Berserk/Berserk v02.cbz")}}},
			12: {{"id": 2, "number": 0, "chapters": []any{kvAPIChap(3, 100, 25, at, "/books/ebooks/Tolstoy/War and Peace.epub")}}},
		}})
	o := e.opts()
	o.Paths, o.UserMap = e.bookPaths(), map[string]int64{"kavita:1": e.uid}
	o.Remotes = map[string]Remote{SourceKavita: {URL: srv.URL, APIKey: "key-alex, key-kid"}}
	rep := e.run(ImportKavita, o)
	if r, ok := e.row(v1); !ok || r.Locator != "page:12" || r.Pos != 300_000 || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("v01 %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if r, ok := e.row(bk); !ok || r.Locator != "" || r.Pos != 250_000 {
		t.Fatalf("epub %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if len(rep.Lines) != 2 || len(linesWith(rep, StatusImported)) != 2 {
		t.Fatalf("lines %+v", rep.Lines)
	}
	o.Remotes = map[string]Remote{SourceKavita: {URL: srv.URL, APIKey: "bad"}}
	l := onlyLine(t, e.run(ImportKavita, o))
	if l.Status != StatusFailed || l.Reason != kvAuthHint {
		t.Fatalf("bad key %+v", l)
	}
}

// --- Audiobookshelf ---

func fakeABS(t *testing.T, token string, admin bool, users []absAPIUser, items map[string]absAPIItem) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/me":
			writeJSONT(w, users[0])
		case r.URL.Path == "/api/users":
			if !admin {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var min []map[string]any // the list is minimal: no mediaProgress
			for _, u := range users {
				min = append(min, map[string]any{"id": u.ID, "username": u.Username, "type": "user", "isActive": true})
			}
			writeJSONT(w, map[string]any{"users": min})
		case strings.HasPrefix(r.URL.Path, "/api/users/"):
			for _, u := range users {
				if u.ID == strings.TrimPrefix(r.URL.Path, "/api/users/") {
					writeJSONT(w, u)
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/items/"):
			it, ok := items[strings.TrimPrefix(r.URL.Path, "/api/items/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSONT(w, it)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func absItemOf(id, path, title string) absAPIItem {
	var it absAPIItem
	it.ID, it.Path, it.MediaType = id, path, "book"
	it.Media.Metadata.Title = title
	return it
}

func TestABSAPIImport(t *testing.T) {
	e := newProgEnv(t)
	var files []library.FileInput
	for _, n := range []string{"01.mp3", "02.mp3"} {
		files = append(files, library.FileInput{Path: e.file("books/audiobooks/Author/Book/" + n), Size: 1, Mtime: 1})
	}
	audio, err := e.lib.UpsertAudiobook(context.Background(), library.AudiobookInput{Dir: filepath.Join(e.root, "media/books/audiobooks/Author/Book"), Files: files, Title: "Book"})
	if err != nil {
		t.Fatal(err)
	}
	pdf := e.book("books/ebooks/B/Doc.pdf")
	ms := time.Date(2026, 10, 1, 19, 30, 0, 123e6, time.UTC).UnixMilli()
	users := []absAPIUser{
		{ID: "root", Username: "admin"},
		{ID: "u-1", Username: "alex", MediaProgress: []absAPIProgress{
			{LibraryItemID: "li-1", MediaItemType: "book", Duration: 3600, CurrentTime: 1200.5, LastUpdate: ms},
			{LibraryItemID: "li-2", MediaItemType: "book", EbookLocation: "17", EbookProgress: 0.17, LastUpdate: ms},
			{LibraryItemID: "li-gone", MediaItemType: "book", Duration: 10, CurrentTime: 5, LastUpdate: ms},
			{LibraryItemID: "li-pod", EpisodeID: "ep-1", MediaItemType: "podcastEpisode", Duration: 100, CurrentTime: 50, LastUpdate: ms},
		}},
	}
	srv := fakeABS(t, "tok", true, users, map[string]absAPIItem{
		"li-1": absItemOf("li-1", "/audiobooks/Author/Book", "Book"),
		"li-2": absItemOf("li-2", "/ebooks/B/Doc.pdf", "Doc"),
	})
	o := e.opts()
	o.Paths, o.UserMap = e.bookPaths(), map[string]int64{"audiobookshelf:u-1": e.uid}
	o.Remotes = map[string]Remote{SourceABS: {URL: srv.URL, APIKey: "tok"}}
	rep := e.run(ImportABS, o)
	if r, ok := e.row(audio); !ok || r.Pos != 1_200_500 || r.Dur != 3_600_000 || r.UpdatedAt != tsOf(time.UnixMilli(ms)) {
		t.Fatalf("audio %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if r, ok := e.row(pdf); !ok || r.Locator != "page:17" {
		t.Fatalf("pdf %+v %v", r, ok)
	}
	w := linesWith(rep, StatusWarning)
	if len(w) != 1 || w[0].Reason != "Audiobookshelf: не переносятся 1 отметка (подкасты)" {
		t.Fatalf("warnings %+v", w)
	}
	// a plain user's token: its own positions only (/api/me)
	srv2 := fakeABS(t, "tok", false, users[1:], map[string]absAPIItem{"li-1": absItemOf("li-1", "/audiobooks/Author/Book", "Book")})
	list, _, err := readABSAPI(context.Background(), Remote{URL: srv2.URL, APIKey: "tok"}, nil)
	if err != nil || len(list) != 1 || list[0].userID != "u-1" {
		t.Fatalf("own %+v %v", list, err)
	}
}

// --- the service: «Подключить», the screen, the run ---

func TestServiceConnectJellyfin(t *testing.T) {
	e := newSvcEnv(t)
	mv := jfAPIEntry("M1", "Movie", "Interstellar", "/old/Interstellar.mkv",
		map[string]any{"Played": true, "LastPlayedDate": "2026-10-01T19:30:00Z"}, map[string]any{"RunTimeTicks": 1})
	f := &fakeJellyfin{users: []map[string]any{{"Id": "U1", "Name": "Александр"}}, items: map[string]map[string][]map[string]any{"U1": {"IsPlayed": {mv}}}}
	srv := f.server(t)
	ctx := context.Background()

	st := discoveredCore(t, e, SourceJellyfin)
	if st.Found || !strings.HasPrefix(st.Detail, "Не подключён — укажите адрес и ключ ниже (или смонтируйте ") {
		t.Fatalf("before: %+v", st)
	}
	if v, ok := e.s.Remote(SourceJellyfin); !ok || v.Kind != RemoteAPIKeyOrLogin || v.Connected || v.Mounted {
		t.Fatalf("view %+v %v", v, ok)
	}
	for _, bad := range []string{"ftp://x", "http://u:p@host", "http://host/?api_key=1", ""} {
		var re *RemoteError
		if _, err := e.s.Connect(ctx, SourceJellyfin, Remote{URL: bad, APIKey: "k"}); !errors.As(err, &re) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if _, err := e.s.Connect(ctx, SourceJellyfin, Remote{URL: srv.URL, APIKey: "wrong"}); err == nil || err.Error() != jfAuthHint {
		t.Fatalf("wrong key: %v", err)
	}
	detail, err := e.s.Connect(ctx, SourceJellyfin, Remote{URL: srv.URL + "/", APIKey: jfAPIKey})
	if err != nil || detail != "1 пользователь, 1 отметка" {
		t.Fatalf("connect %q %v", detail, err)
	}
	v, _ := e.s.Remote(SourceJellyfin)
	if !v.Connected || v.URL != srv.URL || !v.HasSecret {
		t.Fatalf("view %+v", v)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), jfAPIKey) {
		t.Fatalf("the key is answered: %s", b)
	}
	st = discoveredCore(t, e, SourceJellyfin)
	if !st.Found || st.Detail != "1 пользователь, 1 отметка" || st.Path != srv.URL || st.Error != "" {
		t.Fatalf("after: %+v", st)
	}
	// a blank key keeps the kept one for the same address
	if _, err := e.s.Connect(ctx, SourceJellyfin, Remote{URL: srv.URL}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	users, err := e.s.Users(ctx)
	if err != nil || len(users) != 1 || users[0].VaultUserID == nil || *users[0].VaultUserID != e.admin.ID {
		t.Fatalf("users %+v %v", users, err)
	}
	// the screen is kept: a second look does not call the server
	discoveredCore(t, e, SourceJellyfin)
	n := f.calls.Load()
	discoveredCore(t, e, SourceJellyfin)
	if _, err := e.s.Users(ctx); err != nil || f.calls.Load() != n {
		t.Fatalf("looked again: %d → %d calls", n, f.calls.Load())
	}
	// a check reads the API
	um := map[string]int64{"jellyfin:U1": e.admin.ID}
	id, err := e.s.Start(ctx, RunRequest{Sources: []string{SourceJellyfin}, DryRun: true, UserMap: um})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Wait()
	rep, err := e.s.Report(id)
	if err != nil || rep.FinishedAt.IsZero() {
		t.Fatalf("report %+v %v", rep, err)
	}
	if l := linesWith(rep, StatusFailed); len(l) != 1 || !strings.HasPrefix(l[0].Reason, "Не найдено в Antresol: Interstellar") {
		t.Fatalf("lines %+v", rep.Lines) // the library is empty: read over the API, matched like the database
	}
	if err := e.s.Forget(SourceJellyfin); err != nil {
		t.Fatal(err)
	}
	if st := discoveredCore(t, e, SourceJellyfin); st.Found {
		t.Fatalf("after forget %+v", st)
	}
}

// A redirect to another host is not followed (the key would go there).
func TestRemoteRedirectToAnotherHost(t *testing.T) {
	var got atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got.Add(1) }))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	_, _, err := readABSAPI(context.Background(), Remote{URL: srv.URL, APIKey: "tok"}, nil)
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Msg, "перенаправляет на другой адрес") || got.Load() != 0 {
		t.Fatalf("%v (other host got %d)", err, got.Load())
	}
}

func TestDialControlRefusesLinkLocal(t *testing.T) {
	for addr, ok := range map[string]bool{"169.254.169.254:80": false, "[fe80::1]:80": false, "0.0.0.0:80": false,
		"127.0.0.1:8096": true, "172.16.5.4:8096": true, "10.0.0.2:5000": true} {
		if err := dialControl("tcp", addr, nil); (err == nil) != ok {
			t.Fatalf("%s: %v", addr, err)
		}
	}
}

func discoveredCore(t *testing.T, e *svcEnv, id string) SourceStatus {
	t.Helper()
	for _, st := range e.s.Discover(context.Background()) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("no %s", id)
	return SourceStatus{}
}
