package opds

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/screentime"
)

const homeAddr, remoteAddr = "192.168.1.20:50000", "203.0.113.7:50000"

type env struct {
	t     *testing.T
	db    *sql.DB
	repo  *library.Repo
	users *auth.Store
	h     *Handler
	media string
	admin auth.User
	app   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &env{t: t, db: d, repo: library.NewRepo(d), users: auth.NewStore(d), media: t.TempDir()}
	e.h = &Handler{Library: e.repo, Users: e.users, Throttle: auth.NewThrottle(), ImageDir: t.TempDir(),
		Network: &auth.Network{Trusted: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}}
	ctx := context.Background()
	if e.admin, err = e.users.CreateFirstAdmin(ctx, "Папа", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	if e.app, err = e.users.SetAppPassword(ctx, e.admin.ID); err != nil {
		t.Fatal(err)
	}
	return e
}

// file writes a media file and returns its input.
func (e *env) file(rel, body string) library.FileInput {
	e.t.Helper()
	p := filepath.Join(e.media, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return library.FileInput{Path: p, Size: int64(len(body)), Mtime: 1}
}

func (e *env) book(title, author, series string, files ...string) int64 {
	e.t.Helper()
	var in []library.FileInput
	for _, f := range files {
		in = append(in, e.file(f, "book "+f))
	}
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Files: in, Title: title, Authors: []string{author}, Series: series})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// get sends GET path with Basic user:pass (user "" = none) from addr.
func (e *env) get(path, user, pass, addr string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = addr
	if user != "" {
		r.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) adminGet(path string) *httptest.ResponseRecorder {
	return e.get(path, e.admin.Name, e.app, homeAddr)
}

type tLink struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

type tEntry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Authors []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []tLink `xml:"link"`
}

type tFeed struct {
	XMLName xml.Name
	Title   string   `xml:"title"`
	Links   []tLink  `xml:"link"`
	Entries []tEntry `xml:"entry"`
}

func parse(t *testing.T, w *httptest.ResponseRecorder) tFeed {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var f tFeed
	if err := xml.Unmarshal(w.Body.Bytes(), &f); err != nil || f.XMLName.Local != "feed" || f.XMLName.Space != "http://www.w3.org/2005/Atom" {
		t.Fatalf("not an Atom feed (%v): %s", err, w.Body)
	}
	return f
}

func links(ls []tLink, rel string) []tLink {
	var out []tLink
	for _, l := range ls {
		if l.Rel == rel {
			out = append(out, l)
		}
	}
	return out
}

func TestOPDSRootFeedValid(t *testing.T) {
	e := newEnv(t)
	w := e.adminGet("/opds")
	if ct := w.Header().Get("Content-Type"); ct != "application/atom+xml;profile=opds-catalog;kind=navigation" {
		t.Fatalf("content type %q", ct)
	}
	f := parse(t, w)
	s := links(f.Links, "search")
	if len(s) != 2 || s[0].Type != "application/opensearchdescription+xml" || s[0].Href != "/opds/opensearch.xml" ||
		s[1].Type != "application/atom+xml;profile=opds-catalog;kind=acquisition" || s[1].Href != "/opds/search?q={searchTerms}" {
		t.Fatalf("search links %+v", f.Links)
	}
	if len(links(f.Links, "start")) != 1 || len(links(f.Links, "self")) != 1 {
		t.Fatalf("links %+v", f.Links)
	}
	want := map[string]string{"Новые книги": "/opds/new", "Авторы": "/opds/authors", "Серии": "/opds/series",
		"Аудиокниги": "/opds/audiobooks", "Манга и комиксы": "/opds/comics"}
	if len(f.Entries) != 5 {
		t.Fatalf("entries %+v", f.Entries)
	}
	for _, en := range f.Entries {
		sub := links(en.Links, "subsection")
		if len(sub) != 1 || want[en.Title] != sub[0].Href || !strings.HasPrefix(sub[0].Type, "application/atom+xml;profile=opds-catalog") || en.ID == "" {
			t.Errorf("entry %+v", en)
		}
	}
	// the OpenSearch description
	w = e.adminGet("/opds/opensearch.xml")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "opensearchdescription+xml") {
		t.Fatalf("opensearch %d %s", w.Code, w.Header())
	}
	var osd struct {
		URLs []struct {
			Type     string `xml:"type,attr"`
			Template string `xml:"template,attr"`
		} `xml:"Url"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &osd); err != nil || len(osd.URLs) == 0 || osd.URLs[0].Template != "http://example.com/opds/search?q={searchTerms}" {
		t.Fatalf("osd %v %s", err, w.Body)
	}
}

func TestOPDSAcquisitionLinks(t *testing.T) {
	e := newEnv(t)
	cover := e.file("ebooks/Булгаков/cover.jpg", "\xff\xd8\xff\xe0jpeg")
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{
		Files: []library.FileInput{e.file("ebooks/Булгаков/master.fb2", "<FictionBook/>"), e.file("ebooks/Булгаков/master.epub", "PK epub")},
		Title: "Мастер и Маргарита", Authors: []string{"Михаил Булгаков"}, Cover: "file:" + cover.Path, Description: "Роман",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := e.adminGet("/opds/new")
	if ct := w.Header().Get("Content-Type"); ct != "application/atom+xml;profile=opds-catalog;kind=acquisition" {
		t.Fatalf("content type %q", ct)
	}
	f := parse(t, w)
	if len(f.Entries) != 1 {
		t.Fatalf("entries %+v", f.Entries)
	}
	en := f.Entries[0]
	if en.Title != "Мастер и Маргарита" || len(en.Authors) != 1 || en.Authors[0].Name != "Михаил Булгаков" {
		t.Fatalf("entry %+v", en)
	}
	acq := links(en.Links, "http://opds-spec.org/acquisition")
	types := map[string]string{}
	for _, l := range acq {
		types[l.Type] = l.Href
	}
	if len(acq) != 2 || types["application/epub+zip"] == "" || types["application/x-fictionbook+xml"] == "" {
		t.Fatalf("acquisition %+v", acq)
	}
	if acq[0].Type != "application/epub+zip" {
		t.Fatalf("epub first: %+v", acq)
	}
	img, thumb := links(en.Links, "http://opds-spec.org/image"), links(en.Links, "http://opds-spec.org/image/thumbnail")
	if len(img) != 1 || len(thumb) != 1 || img[0].Href != fmt.Sprintf("/opds/covers/%d", id) {
		t.Fatalf("images %+v", en.Links)
	}
	if href := types["application/epub+zip"]; !strings.HasSuffix(href, "/master.epub") {
		t.Fatalf("href without the file name: %s", href)
	}
	dl := e.adminGet(types["application/epub+zip"])
	if dl.Code != 200 || dl.Body.String() != "PK epub" || dl.Header().Get("Content-Disposition") != `attachment; filename="master.epub"; filename*=UTF-8''master.epub` ||
		dl.Header().Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("download %d %v %q", dl.Code, dl.Header(), dl.Body)
	}
	if c := e.adminGet(img[0].Href); c.Code != 200 || c.Body.String() != "\xff\xd8\xff\xe0jpeg" {
		t.Fatalf("cover %d", c.Code)
	}
}

func TestOPDSBasicAuth(t *testing.T) {
	e := newEnv(t)
	w := e.get("/opds", "", "", homeAddr)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Basic realm="Vault"` {
		t.Fatalf("no auth: %d %v", w.Code, w.Header())
	}
	if w := e.get("/opds", "папа", e.app, homeAddr); w.Code != 200 {
		t.Fatalf("app password (case-folded name): %d", w.Code)
	}
	// only the app password: the login password is refused (a strike)
	if w := e.get("/opds", e.admin.Name, "secret-pass", remoteAddr); w.Code != 401 {
		t.Fatalf("login password: %d", w.Code)
	}
	if w := e.get("/opds", "Никто", "x", remoteAddr); w.Code != 401 {
		t.Fatalf("unknown profile: %d", w.Code)
	}
	for i := 0; i < 3; i++ {
		if w := e.get("/opds", e.admin.Name, "wrong", remoteAddr); w.Code != 401 {
			t.Fatalf("wrong %d: %d", i, w.Code)
		}
	}
	w = e.get("/opds", e.admin.Name, e.app, remoteAddr)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("after 5 failures: %d", w.Code)
	}
	// the profile's home key is separate: the family at home is not locked out
	if w := e.get("/opds", e.admin.Name, e.app, homeAddr); w.Code != 200 {
		t.Fatalf("home after a remote lockout: %d", w.Code)
	}
}

// TestOPDSNoAppPassword: a profile without an app password is told to set
// one, whatever password it sends.
func TestOPDSNoAppPassword(t *testing.T) {
	e := newEnv(t)
	if _, err := e.users.CreateUser(context.Background(), auth.NewUser{Name: "Мама", Password: "mama-pass-1"}); err != nil {
		t.Fatal(err)
	}
	w := e.get("/opds", "Мама", "mama-pass-1", homeAddr)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "пароль для приложений") {
		t.Fatalf("no app password: %d %s", w.Code, w.Body)
	}
}

func TestPasswordlessProfileEmptyPasswordRejected(t *testing.T) {
	e := newEnv(t)
	kid, err := e.users.CreateUser(context.Background(), auth.NewUser{Name: "Маша", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.get("/opds", "Маша", "", homeAddr); w.Code != 401 {
		t.Fatalf("empty password: %d", w.Code)
	}
	app, err := e.users.SetAppPassword(context.Background(), kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := e.get("/opds", "Маша", app, homeAddr); w.Code != 200 {
		t.Fatalf("kid app password at home: %d", w.Code)
	}
	if w := e.get("/opds", "Маша", app, remoteAddr); w.Code != 401 {
		t.Fatalf("passwordless profile from outside: %d", w.Code)
	}
}

func TestOPDSAudiobookLinks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m4b := e.file("audiobooks/Лем/Солярис.m4b", "m4b data")
	if _, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Files: []library.FileInput{m4b}, Title: "Солярис", Authors: []string{"Станислав Лем"}}); err != nil {
		t.Fatal(err)
	}
	var mp3 []library.FileInput
	for i := 1; i <= 3; i++ {
		mp3 = append(mp3, e.file(fmt.Sprintf("audiobooks/Стругацкие/Пикник/%02d.mp3", i), "mp3"))
	}
	cover := e.file("audiobooks/Стругацкие/Пикник/cover.jpg", "\xff\xd8\xff")
	if _, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: filepath.Dir(mp3[0].Path), Files: mp3, Title: "Пикник на обочине",
		Authors: []string{"Стругацкие"}, Description: "Повесть", Cover: "file:" + cover.Path}); err != nil {
		t.Fatal(err)
	}
	f := parse(t, e.adminGet("/opds/audiobooks"))
	if len(f.Entries) != 2 {
		t.Fatalf("entries %+v", f.Entries)
	}
	for _, en := range f.Entries {
		acq := links(en.Links, "http://opds-spec.org/acquisition")
		switch en.Title {
		case "Солярис":
			if len(acq) != 1 || acq[0].Type != "audio/mp4" {
				t.Fatalf("m4b %+v", acq)
			}
		case "Пикник на обочине":
			if len(acq) != 0 || len(links(en.Links, "http://opds-spec.org/image")) != 1 {
				t.Fatalf("folder %+v", en.Links)
			}
		default:
			t.Fatalf("entry %+v", en)
		}
	}
}

func TestOPDSPagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 60; i++ {
		e.book(fmt.Sprintf("Книга %02d", i), "Автор", "", fmt.Sprintf("ebooks/b%02d.epub", i))
	}
	f := parse(t, e.adminGet("/opds/new"))
	next := links(f.Links, "next")
	if len(f.Entries) != 50 || len(next) != 1 || next[0].Href != "/opds/new?page=1" {
		t.Fatalf("page 0: %d entries, next %+v", len(f.Entries), next)
	}
	f = parse(t, e.adminGet(next[0].Href))
	if self := links(f.Links, "self"); len(self) != 1 || self[0].Href != "/opds/new?page=1" {
		t.Fatalf("self of page 1: %+v", self)
	}
	if len(f.Entries) != 10 || len(links(f.Links, "next")) != 0 || len(links(f.Links, "previous")) != 1 {
		t.Fatalf("page 1: %d entries %+v", len(f.Entries), f.Links)
	}
}

func TestOPDSAuthorsSeriesSearch(t *testing.T) {
	e := newEnv(t)
	e.book("Пикник на обочине", "Аркадий Стругацкий", "", "ebooks/s/picnic.epub")
	e.book("Трудно быть богом", "Аркадий Стругацкий", "Мир Полудня", "ebooks/s/hard.epub")
	e.book("Мастер и Маргарита", "Михаил Булгаков", "", "ebooks/b/master.epub")
	f := parse(t, e.adminGet("/opds/authors"))
	if len(f.Entries) != 2 || f.Entries[0].Title != "Аркадий Стругацкий" || f.Entries[1].Title != "Михаил Булгаков" {
		t.Fatalf("authors %+v", f.Entries)
	}
	sub := links(f.Entries[0].Links, "subsection")
	f = parse(t, e.adminGet(sub[0].Href))
	if len(f.Entries) != 2 {
		t.Fatalf("author's books via %s: %+v", sub[0].Href, f.Entries)
	}
	f = parse(t, e.adminGet("/opds/series"))
	if len(f.Entries) != 1 || f.Entries[0].Title != "Мир Полудня" {
		t.Fatalf("series %+v", f.Entries)
	}
	f = parse(t, e.adminGet(links(f.Entries[0].Links, "subsection")[0].Href))
	if len(f.Entries) != 1 || f.Entries[0].Title != "Трудно быть богом" {
		t.Fatalf("series books %+v", f.Entries)
	}
	f = parse(t, e.adminGet("/opds/search?q=%D0%BC%D0%B0%D1%81%D1%82%D0%B5%D1%80"))
	if len(f.Entries) != 1 || f.Entries[0].Title != "Мастер и Маргарита" || len(links(f.Entries[0].Links, "http://opds-spec.org/acquisition")) != 1 {
		t.Fatalf("search %+v", f.Entries)
	}
	if w := e.adminGet("/opds/authors/Никто"); w.Code != 404 {
		t.Fatalf("unknown author %d", w.Code)
	}
}

func TestOPDSKidHidesAdultComic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Маша", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	adultVol, err := e.repo.UpsertComicVolume(ctx, library.ComicInput{File: e.file("manga/Berserk/v01.cbz", "PK"), SeriesDir: filepath.Join(e.media, "manga/Berserk"),
		Series: "Берсерк", Title: "Том 1", Volume: 1, Rating: "18+"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.UpsertComicVolume(ctx, library.ComicInput{File: e.file("manga/Chi/v01.cbz", "PK"), SeriesDir: filepath.Join(e.media, "manga/Chi"),
		Series: "Котёнок Чи", Title: "Том 1", Volume: 1}); err != nil {
		t.Fatal(err)
	}
	vol, err := e.repo.Item(ctx, adultVol)
	if err != nil {
		t.Fatal(err)
	}
	files, err := e.repo.FilesOf(ctx, adultVol)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	kidGet := func(p string) *httptest.ResponseRecorder { return e.get(p, "Маша", app, homeAddr) }

	f := parse(t, kidGet("/opds/comics"))
	if len(f.Entries) != 1 || f.Entries[0].Title != "Котёнок Чи" {
		t.Fatalf("kid comics %+v", f.Entries)
	}
	for _, p := range []string{fmt.Sprintf("/opds/comics/%d", vol.ParentID), fmt.Sprintf("/opds/files/%d", files[0].ID),
		fmt.Sprintf("/opds/covers/%d", vol.ParentID)} {
		if w := kidGet(p); w.Code != 404 {
			t.Errorf("kid %s: %d", p, w.Code)
		}
	}
	if f := parse(t, kidGet("/opds/search?q=берсерк")); len(f.Entries) != 0 {
		t.Fatalf("kid search %+v", f.Entries)
	}
	// the admin sees both, and the volumes of the series
	if f := parse(t, e.adminGet("/opds/comics")); len(f.Entries) != 2 {
		t.Fatalf("admin comics %+v", f.Entries)
	}
	f = parse(t, e.adminGet(fmt.Sprintf("/opds/comics/%d", vol.ParentID)))
	if len(f.Entries) != 1 || len(links(f.Entries[0].Links, "http://opds-spec.org/acquisition")) != 1 ||
		links(f.Entries[0].Links, "http://opds-spec.org/acquisition")[0].Type != "application/vnd.comicbook+zip" {
		t.Fatalf("admin volumes %+v", f.Entries)
	}
	if w := e.adminGet(fmt.Sprintf("/opds/files/%d", files[0].ID)); w.Code != 200 {
		t.Fatalf("admin volume download %d", w.Code)
	}
}

// TestOPDSFilesOnlyBooks: /opds/files serves books, audiobooks and comic
// volumes only, never a movie.
func TestOPDSFilesOnlyBooks(t *testing.T) {
	e := newEnv(t)
	movie := e.file("movies/Дюна.mkv", "video")
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Дюна", Year: 2021, Files: []library.FileInput{movie}})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := e.repo.FilesOf(context.Background(), id)
	if w := e.adminGet(fmt.Sprintf("/opds/files/%d", files[0].ID)); w.Code != 404 {
		t.Fatalf("movie via OPDS: %d", w.Code)
	}
	if w := e.adminGet("/opds/files/abc"); w.Code != 404 {
		t.Fatalf("bad id: %d", w.Code)
	}
}

// TestOPDSDetails: Cyrillic names get an ASCII fallback, the OpenSearch
// template follows a trusted proxy's https, entries without a description
// still have content, and covers are linked only when they can be served.
func TestOPDSDetails(t *testing.T) {
	if got := contentDisposition(7, "Мастер.fb2.zip"); got != `attachment; filename="vault-7.fb2.zip"; filename*=UTF-8''%D0%9C%D0%B0%D1%81%D1%82%D0%B5%D1%80.fb2.zip` {
		t.Fatalf("disposition %s", got)
	}
	e := newEnv(t)
	e.h.Network.Proxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}
	r := httptest.NewRequest("GET", "/opds/opensearch.xml", nil)
	r.Host = "vault.example.org"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-For", "192.168.1.20")
	r.SetBasicAuth(e.admin.Name, e.app)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `template="https://vault.example.org/opds/search?q={searchTerms}"`) {
		t.Fatalf("behind the proxy: %s", w.Body)
	}
	// not from the proxy: X-Forwarded-Proto is ignored
	r.RemoteAddr = homeAddr
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `template="http://vault.example.org/`) {
		t.Fatalf("direct: %s", w.Body)
	}

	ctx := context.Background()
	var mp3 []library.FileInput
	for i := 1; i <= 2; i++ {
		mp3 = append(mp3, e.file(fmt.Sprintf("audiobooks/X/%02d.mp3", i), "mp3"))
	}
	if _, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: filepath.Dir(mp3[0].Path), Files: mp3, Title: "Без описания",
		Cover: "https://example.org/remote.jpg"}); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Entries []struct {
			Content string  `xml:"content"`
			Links   []tLink `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(e.adminGet("/opds/audiobooks").Body.Bytes(), &raw); err != nil || len(raw.Entries) != 1 {
		t.Fatal(err)
	}
	if en := raw.Entries[0]; en.Content != "Аудиокнига из 2 файлов — слушайте в Antresol" || len(en.Links) != 0 {
		t.Fatalf("entry %+v", en)
	}
}

// TestOPDSKidHidesAdultBook: an 18+ book is in no feed of a kid.
func TestOPDSKidHidesAdultBook(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Маша", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := e.book("Взрослая книга", "Автор", "Серия", "ebooks/a/adult.epub")
	if err := e.repo.SetMetadata(ctx, id, library.Metadata{Rating: "18+"}); err != nil {
		t.Fatal(err)
	}
	e.book("Детская книга", "Автор", "", "ebooks/a/kid.epub")
	kidGet := func(p string) tFeed { return parse(t, e.get(p, "Маша", app, homeAddr)) }
	for _, p := range []string{"/opds/new", "/opds/authors/" + "%D0%90%D0%B2%D1%82%D0%BE%D1%80", "/opds/search?q=%D0%BA%D0%BD%D0%B8%D0%B3%D0%B0"} {
		f := kidGet(p)
		if len(f.Entries) != 1 || f.Entries[0].Title != "Детская книга" {
			t.Errorf("%s: %+v", p, f.Entries)
		}
	}
	if f := kidGet("/opds/series"); len(f.Entries) != 0 {
		t.Errorf("series of the adult book: %+v", f.Entries)
	}
	// a book without a title is still listed (named by the scanner)
	e.book("", "", "", "ebooks/a/безымянная.epub")
	if f := parse(t, e.adminGet("/opds/new")); len(f.Entries) != 3 || f.Entries[0].Title == "" {
		t.Fatalf("untitled %+v", f.Entries)
	}
}

// The feeds answer in the request language (Accept-Language or ?lang=);
// no language → Russian, as reading apps send none.
func TestOPDSLanguage(t *testing.T) {
	e := newEnv(t)
	get := func(path, lang string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = homeAddr
		r.SetBasicAuth(e.admin.Name, e.app)
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}
	for _, c := range []struct{ lang, first, search, unauth string }{
		{"", "Новые книги", "Поиск", "Неверное имя или пароль"},
		{"en-US,en;q=0.9", "New Books", "Search", "Incorrect name or password"},
		{"ru-RU", "Новые книги", "Поиск", "Неверное имя или пароль"},
	} {
		f := parse(t, get("/opds", c.lang))
		if f.Title != "Vault" || len(f.Entries) == 0 || f.Entries[0].Title != c.first {
			t.Errorf("%q: root %q %+v", c.lang, f.Title, f.Entries)
		}
		if s := links(f.Links, "search"); len(s) == 0 || s[0].Title != c.search {
			t.Errorf("%q: search link %+v", c.lang, s)
		}
		r := httptest.NewRequest("GET", "/opds", nil)
		r.RemoteAddr = homeAddr
		r.SetBasicAuth(e.admin.Name, "wrong")
		if c.lang != "" {
			r.Header.Set("Accept-Language", c.lang)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || strings.TrimSpace(w.Body.String()) != c.unauth {
			t.Errorf("%q: unauthorized %d %q", c.lang, w.Code, w.Body)
		}
	}
	if f := parse(t, get("/opds?lang=en", "ru")); f.Entries[0].Title != "New Books" {
		t.Errorf("?lang=en: %+v", f.Entries[0])
	}
}

// A kid whose audiobooks are limited («Экранное время») downloads no
// audiobook files over OPDS; books stay free.
func TestOPDSKidAudiobookScreenTime(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Маша", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.h.ScreenTime = screentime.New(e.db)
	ab, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Files: []library.FileInput{e.file("ab/Солярис.m4b", "m4b")}, Title: "Солярис"})
	if err != nil {
		t.Fatal(err)
	}
	abFiles, _ := e.repo.FilesOf(ctx, ab)
	book := e.book("Мастер", "Булгаков", "", "books/m.epub")
	bookFiles, _ := e.repo.FilesOf(ctx, book)
	get := func(id int64) int { return e.get(fmt.Sprintf("/opds/files/%d", id), kid.Name, app, homeAddr).Code }
	if c := get(abFiles[0].ID); c != 200 {
		t.Fatalf("free: %d", c)
	}
	if err := e.h.ScreenTime.SetLimits(ctx, kid.ID, screentime.Limits{AudioMin: new(int)}); err != nil {
		t.Fatal(err)
	}
	if c := get(abFiles[0].ID); c != 403 {
		t.Fatalf("limited audiobook: %d", c)
	}
	if c := get(bookFiles[0].ID); c != 200 {
		t.Fatalf("book: %d", c)
	}
}
