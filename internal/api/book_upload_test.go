package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

type uploadEnv struct {
	*libEnv
	root string
}

func newUploadEnv(t *testing.T) *uploadEnv {
	root := filepath.Join(t.TempDir(), "ebooks")
	var scanner *library.Scanner
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		scanner = &library.Scanner{Repo: d.Library, Ebooks: root}
		d.Jobs = &library.Jobs{Scanner: scanner}
	})
	return &uploadEnv{e, root}
}

func (e *uploadEnv) post(name string, content []byte, c *http.Cookie, header bool) *httptest.ResponseRecorder {
	e.t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	w, _ := mw.CreateFormFile("file", name)
	w.Write(content)
	mw.Close()
	r := httptest.NewRequest("POST", "/api/books/upload", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if header {
		r.Header.Set("X-Vault-Upload", "1")
	}
	if c != nil {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *uploadEnv) bookTitles(query string, c *http.Cookie) string {
	e.t.Helper()
	w := e.do("GET", "/api/books?kind=book"+query, "", c)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return w.Body.String()
}

func (e *uploadEnv) names() []string {
	e.t.Helper()
	ents, err := os.ReadDir(e.root)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, x := range ents {
		out = append(out, x.Name())
	}
	return out
}

func TestUploadAppearsInLibraryAsUploadersBook(t *testing.T) {
	e := newUploadEnv(t)
	_, reader := e.profile(`{"name":"Читатель"}`)
	_, other := e.profile(`{"name":"Другой"}`)
	w := e.post("Личная.fb2", []byte(testFB2), reader, true)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Name      string `json:"name"`
		Duplicate bool   `json:"duplicate"`
		Indexed   bool   `json:"indexed"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Name != "Личная.fb2" || out.Duplicate || !out.Indexed {
		t.Errorf("%+v", out)
	}
	if strings.Contains(w.Body.String(), e.root) {
		t.Error("the answer carries a server path")
	}
	if got := e.names(); len(got) != 1 || got[0] != "Личная.fb2" {
		t.Errorf("files %v", got)
	}
	if !strings.Contains(e.bookTitles("", other), "Название") {
		t.Error("not in the shared library")
	}
	if !strings.Contains(e.bookTitles("&scope=mine", reader), "Название") {
		t.Error("not in the uploader's library")
	}
	if strings.Contains(e.bookTitles("&scope=mine", other), "Название") {
		t.Error("in someone else's «Моя медиатека»")
	}
}

func TestUploadRefusals(t *testing.T) {
	e := newUploadEnv(t)
	_, c := e.profile(`{"name":"Читатель"}`)
	if w := e.post("a.fb2", []byte(testFB2), c, false); w.Code != 403 {
		t.Errorf("no upload header: %d", w.Code)
	}
	if w := e.post("a.fb2", []byte(testFB2), nil, true); w.Code != 401 {
		t.Errorf("no session: %d", w.Code)
	}
	kid := e.kid("6+")
	if w := e.post("a.fb2", []byte(testFB2), kid, true); w.Code != 403 {
		t.Errorf("kid: %d", w.Code)
	}
	for name, content := range map[string]string{
		"a.exe":  "MZ",
		"a.epub": "not a zip",
		"a.pdf":  "not a pdf",
		"a.mobi": "not a mobi",
		"a.fb2":  "<html></html>",
		".fb2":   testFB2,
	} {
		if w := e.post(name, []byte(content), c, true); w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if _, err := os.Stat(e.root); err == nil && len(e.names()) != 0 {
		t.Errorf("left behind: %v", e.names())
	}
}

func TestUploadStaysInTheBooksFolder(t *testing.T) {
	e := newUploadEnv(t)
	for _, name := range []string{"../../evil.fb2", `..\..\evil2.fb2`, "/etc/evil3.fb2", "...evil4.fb2"} {
		if w := e.post(name, []byte(testFB2+name), e.admin, true); w.Code != 201 {
			t.Fatalf("%q: %d %s", name, w.Code, w.Body)
		}
	}
	got := strings.Join(e.names(), "|")
	for _, want := range []string{"evil.fb2", "evil2.fb2", "evil3.fb2", "evil4.fb2"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.root), "evil.fb2")); err == nil {
		t.Error("escaped the folder")
	}
}

func TestUploadNeverReplacesAFile(t *testing.T) {
	e := newUploadEnv(t)
	_, a := e.profile(`{"name":"Первый"}`)
	_, b := e.profile(`{"name":"Второй"}`)
	if w := e.post("Книга.fb2", []byte(testFB2), a, true); w.Code != 201 {
		t.Fatal(w.Body)
	}
	// the same content: one file, and the second reader owns the book too
	w := e.post("Книга.fb2", []byte(testFB2), b, true)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := e.names(); len(got) != 1 {
		t.Errorf("files %v", got)
	}
	if !strings.Contains(e.bookTitles("&scope=mine", b), "Название") {
		t.Error("the second reader does not have it")
	}
	// other content under the same name: both stay
	other := strings.Replace(testFB2, "Текст книги", "Другой текст", 1)
	w = e.post("Книга.fb2", []byte(other), a, true)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "Книга (2).fb2") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	first, _ := os.ReadFile(filepath.Join(e.root, "Книга.fb2"))
	if string(first) != testFB2 {
		t.Error("the first file changed")
	}
}

func TestUploadTooLarge(t *testing.T) {
	e := newUploadEnv(t)
	old := maxBookUpload
	maxBookUpload = 1000
	t.Cleanup(func() { maxBookUpload = old })
	big := append([]byte("%PDF-1.7\n"), make([]byte, 200<<10)...)
	if w := e.post("big.pdf", big, e.admin, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if ents, err := os.ReadDir(e.root); err == nil && len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}

func TestSafeBookNameNormalizes(t *testing.T) {
	// the й of macOS: и + combining breve
	if got := safeBookName("Войнай.epub"); got != "Войнай.epub" {
		t.Errorf("%q", got)
	}
	long := strings.Repeat("я", 300) + ".epub"
	if got := safeBookName(long); len(got) > 200 || !strings.HasSuffix(got, ".epub") {
		t.Errorf("%d %q", len(got), got)
	}
}

func TestUploadedBookCanGoToKindle(t *testing.T) {
	e := newUploadEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	id, c := e.profile(`{"name":"Читатель"}`)
	e.do("PATCH", "/api/users/"+itoa(id), `{"kindleEmail":"reader_x1@kindle.com"}`, e.admin)
	if w := e.post("Личная.fb2", []byte(testFB2), c, true); w.Code != 201 {
		t.Fatal(w.Body)
	}
	var bookID int64
	if err := e.db.QueryRowContext(context.Background(), `SELECT id FROM items WHERE kind='book'`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	if w := e.do("POST", "/api/books/"+itoa(bookID)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(sent()) != 1 {
		t.Error("not sent")
	}
}
