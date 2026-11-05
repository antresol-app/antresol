package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mailer"
)

const testFB2 = `<?xml version="1.0" encoding="UTF-8"?><FictionBook><description><title-info><book-title>Название</book-title></title-info></description><body><section><p>Текст книги</p></section></body></FictionBook>`

type sentMail struct {
	cfg  mailer.Config
	msg  mailer.Message
	data []byte
}

// stubKindle records what would have been sent; err is what sending returns.
func stubKindle(t *testing.T, err error) func() []sentMail {
	var mu sync.Mutex
	var sent []sentMail
	old := kindleSend
	kindleSend = func(_ context.Context, c mailer.Config, m mailer.Message) error {
		b, _ := io.ReadAll(m.Attachment)
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMail{c, m, b})
		return err
	}
	t.Cleanup(func() { kindleSend = old })
	return func() []sentMail {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentMail(nil), sent...)
	}
}

func newKindleEnv(t *testing.T) *libEnv {
	return newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
}

// putSettings is a PUT of the full body the settings screen sends.
func (e *libEnv) putSettings(fields string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do("PUT", "/api/settings", `{"quality":1080,"voice":"original","voices":["original"],"streamCacheGB":10,`+fields+`}`, e.admin)
}

func (e *libEnv) configureMail() {
	e.t.Helper()
	w := e.putSettings(`"mail":{"host":"smtp.example.com","port":587,"username":"me@example.com","password":"abcd efgh","from":"me@example.com"}`)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// kindleProfile is a profile with a Kindle address.
func (e *libEnv) kindleProfile(extra string) *http.Cookie {
	e.t.Helper()
	id, c := e.profile(`{"name":"Читатель"` + extra + `}`)
	w := e.do("PATCH", "/api/users/"+itoa(id), `{"kindleEmail":"reader_x1@kindle.com"}`, e.admin)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return c
}

// bookFile adds a book with one file of the given name and content.
func (e *libEnv) bookFile(title, name string, content []byte) int64 {
	e.t.Helper()
	p := filepath.Join(e.media, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		e.t.Fatal(err)
	}
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Title: title, Files: []library.FileInput{{Path: p, Size: int64(len(content))}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *libEnv) canSend(id int64, c *http.Cookie) bool {
	e.t.Helper()
	w := e.do("GET", "/api/books/"+itoa(id), "", c)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var d struct {
		Can bool `json:"canSendToKindle"`
	}
	json.Unmarshal(w.Body.Bytes(), &d)
	return d.Can
}

func TestKindleSendsEPUBAsIs(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	id := e.bookFile("Война/мир", "a.epub", []byte("epub-bytes"))
	if !e.canSend(id, c) {
		t.Fatal("no button")
	}
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s := sent()
	if len(s) != 1 {
		t.Fatalf("%d messages", len(s))
	}
	m := s[0]
	if string(m.data) != "epub-bytes" || m.msg.To != "reader_x1@kindle.com" || m.msg.Filename != "Война_мир.epub" || m.msg.ContentType != "application/epub+zip" {
		t.Errorf("%+v", m.msg)
	}
	if m.cfg.Host != "smtp.example.com" || m.cfg.Port != 587 || m.cfg.Username != "me@example.com" || m.cfg.From != "me@example.com" || m.cfg.Password != "abcd efgh" {
		t.Errorf("%+v", m.cfg)
	}
}

func epubText(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, f := range zr.File {
		rc, _ := f.Open()
		x, _ := io.ReadAll(rc)
		rc.Close()
		out.Write(x)
	}
	return out.String()
}

func TestKindleConvertsFB2AndFB2Zip(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("inner.fb2")
	io.WriteString(w, testFB2)
	zw.Close()
	for name, content := range map[string][]byte{"a.fb2": []byte(testFB2), "b.fb2.zip": zipped.Bytes()} {
		t.Run(name, func(t *testing.T) {
			e := newKindleEnv(t)
			sent := stubKindle(t, nil)
			e.configureMail()
			c := e.kindleProfile("")
			id := e.bookFile("Название", name, content)
			if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			m := sent()[0]
			if m.msg.Filename != "Название.epub" || m.msg.ContentType != "application/epub+zip" || m.msg.Size != int64(len(m.data)) {
				t.Errorf("%+v", m.msg)
			}
			if !strings.Contains(epubText(t, m.data), "Текст книги") {
				t.Error("no text in the EPUB")
			}
		})
	}
}

func TestKindlePrefersEPUBAndRefusesWhatItCannotTake(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	both := e.book("Два формата", ".fb2", ".epub")
	if w := e.do("POST", "/api/books/"+itoa(both)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := sent()[0].msg.Filename; got != "Два формата.epub" {
		t.Errorf("sent %q", got)
	}
	mobi := e.bookFile("Только MOBI", "m.mobi", []byte("x"))
	if e.canSend(mobi, c) {
		t.Error("a button for a MOBI")
	}
	if w := e.do("POST", "/api/books/"+itoa(mobi)+"/kindle", "", c); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestKindleNeedsAccountAndAddress(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	id := e.bookFile("Книга", "a.epub", []byte("x"))
	c := e.kindleProfile("")
	if e.canSend(id, c) {
		t.Error("a button without a mail account")
	}
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != http.StatusConflict {
		t.Errorf("no account: %d", w.Code)
	}
	e.configureMail()
	_, other := e.profile(`{"name":"Без Kindle"}`)
	if e.canSend(id, other) {
		t.Error("a button without an address")
	}
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", other); w.Code != http.StatusConflict {
		t.Errorf("no address: %d", w.Code)
	}
	if len(sent()) != 0 {
		t.Error("something was sent")
	}
}

func TestKindleRespectsAccessToTheBook(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile(`,"isKid":true,"maxRating":"6+"`)
	id := e.bookFile("Взрослая", "a.epub", []byte("x"))
	e.mustExec(`UPDATE items SET rating='18+' WHERE id=?`, id)
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code == 204 {
		t.Fatalf("a kid sent an 18+ book: %d", w.Code)
	}
	if len(sent()) != 0 {
		t.Error("sent")
	}
}

func TestKindleOnlyBooks(t *testing.T) {
	e := newKindleEnv(t)
	stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	movie, _ := e.movie("Фильм", "", "x")
	if w := e.do("POST", "/api/books/"+itoa(movie)+"/kindle", "", c); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestKindleOver50MB(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	id := e.bookFile("Большая", "big.epub", []byte("x"))
	if err := os.Truncate(filepath.Join(e.media, "big.epub"), kindleMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d", w.Code)
	}
	if len(sent()) != 0 {
		t.Error("sent")
	}
}

func TestKindleErrorsAreExplained(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		text string
	}{
		{mailer.ErrAuth, 502, "логин или пароль"},
		{mailer.ErrRecipient, 502, "адрес"},
		{mailer.ErrTooLarge, 413, "почтовый сервер"},
		{mailer.ErrBlocked, 502, "небезопасное"},
		{mailer.ErrNoTLS, 502, "шифрование"},
		{errors.New("dial tcp secret-host"), 502, "попробуйте ещё раз"},
	} {
		e := newKindleEnv(t)
		stubKindle(t, tc.err)
		e.configureMail()
		c := e.kindleProfile("")
		id := e.bookFile("Книга", "a.epub", []byte("x"))
		w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.text) {
			t.Errorf("%v: %d %s", tc.err, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "secret-host") {
			t.Error("an internal error reached the client")
		}
	}
}

func TestKindleBrokenFB2(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	id := e.bookFile("Битая", "a.fb2", []byte("not a book"))
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(sent()) != 0 {
		t.Error("sent")
	}
}

func TestMailPasswordNeverLeavesTheServer(t *testing.T) {
	e := newKindleEnv(t)
	put := e.putSettings(`"mail":{"host":"smtp.gmail.com","port":587,"username":"me@example.com","password":"abcd efgh ijkl","from":"me@example.com"}`)
	if put.Code != 200 || strings.Contains(put.Body.String(), "abcd") {
		t.Fatalf("%d %s", put.Code, put.Body)
	}
	get := e.do("GET", "/api/settings", "", e.admin)
	if strings.Contains(get.Body.String(), "abcd") || !strings.Contains(get.Body.String(), `"mailPasswordSet":true`) {
		t.Fatalf("%s", get.Body)
	}
	stored := func() MailSettings {
		s, err := LoadAppSettings(context.Background(), e.db, AppSettings{})
		if err != nil {
			t.Fatal(err)
		}
		return s.Mail
	}
	if got := stored().Password; got != "abcdefghijkl" {
		t.Errorf("a Google password keeps its display spaces: %q", got)
	}
	// what a client sends back (no password) keeps it
	if w := e.putSettings(`"remoteQuality":"720"`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if stored().Password == "" {
		t.Error("another setting lost the password")
	}
	if w := e.putSettings(`"mail":{"host":"smtp.gmail.com","port":465,"username":"me@example.com","from":"me@example.com","password":""}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if s := stored(); s.Password == "" || s.Port != 465 {
		t.Errorf("%+v", s)
	}
	// no login: no account
	if w := e.putSettings(`"mail":{"host":"","username":"","from":""}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if s := stored(); s.Password != "" || s.Configured() {
		t.Errorf("%+v", s)
	}
}

func TestMailSettingsAdminOnlyAndValidated(t *testing.T) {
	e := newKindleEnv(t)
	_, c := e.profile(`{"name":"Взрослый"}`)
	if w := e.do("GET", "/api/settings", "", c); w.Code != 403 {
		t.Errorf("GET: %d", w.Code)
	}
	for _, tc := range []struct{ body, msg string }{
		{`"mail":{"host":"smtp gmail"}`, "почтового сервера"},
		{`"mail":{"host":"https://smtp.gmail.com"}`, "почтового сервера"},
		{`"mail":{"port":70000}`, "Порт"},
		{`"mail":{"from":"Имя <me@example.com>"}`, "отправителя"},
		{`"mail":{"from":"нет собаки"}`, "отправителя"},
		{`"mail":{"from":"reader_x1@kindle.com"}`, "в профиле"},
		{`"mail":{"username":"a\r\nb"}`, "перевод строки"},
	} {
		w := e.putSettings(tc.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%s: %d %s", tc.body, w.Code, w.Body)
		}
	}
}

func TestKindleAddressValidatedAndPrivate(t *testing.T) {
	e := newKindleEnv(t)
	id, c := e.profile(`{"name":"Читатель"}`)
	_, other := e.profile(`{"name":"Другой"}`)
	path := "/api/users/" + itoa(id)
	for _, bad := range []string{"me@gmail.com", "x@kindle.com.evil.org", "Имя <x@kindle.com>", "x@kindle.com, y@evil.org", "нет"} {
		b, _ := json.Marshal(map[string]string{"kindleEmail": bad})
		if w := e.do("PATCH", path, string(b), e.admin); w.Code != 400 {
			t.Errorf("%q: %d", bad, w.Code)
		}
	}
	if w := e.do("PATCH", path, `{"kindleEmail":"Reader.X_1aB@kindle.com"}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), "Reader.X_1aB@kindle.com") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// a profile cannot point the server's mailbox anywhere itself
	if w := e.do("PATCH", path, `{"kindleEmail":"x@kindle.com"}`, c); w.Code != 403 {
		t.Errorf("self: %d", w.Code)
	}
	if w := e.do("GET", "/api/profiles", "", nil); strings.Contains(w.Body.String(), "Reader.X") {
		t.Error("/api/profiles shows the address")
	}
	if w := e.do("GET", "/api/users", "", other); strings.Contains(w.Body.String(), "Reader.X") {
		t.Error("another profile sees the address")
	}
	if w := e.do("PATCH", path, `{"kindleEmail":""}`, e.admin); w.Code != 200 || strings.Contains(w.Body.String(), "Reader.X") {
		t.Errorf("clear: %d %s", w.Code, w.Body)
	}
}

func (e *libEnv) readMedia(name string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.media, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *libEnv) sentToKindle(id int64, c *http.Cookie) bool {
	e.t.Helper()
	w := e.do("GET", "/api/books/"+itoa(id), "", c)
	var d struct {
		Sent bool `json:"sentToKindle"`
	}
	json.Unmarshal(w.Body.Bytes(), &d)
	return d.Sent
}

func TestKindleRemembersWhatWasSent(t *testing.T) {
	e := newKindleEnv(t)
	stubKindle(t, nil)
	e.configureMail()
	uid, c := e.profile(`{"name":"Первый"}`)
	e.do("PATCH", "/api/users/"+itoa(uid), `{"kindleEmail":"reader_x1@kindle.com"}`, e.admin)
	other := e.kindleProfile("")
	id := e.bookFile("Книга", "a.epub", []byte("x"))
	if e.sentToKindle(id, c) {
		t.Fatal("sent before sending")
	}
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !e.sentToKindle(id, c) {
		t.Error("forgotten")
	}
	if e.sentToKindle(id, other) {
		t.Error("another profile's book counts as sent")
	}
	// sending again is allowed
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
	// a new address starts over
	e.do("PATCH", "/api/users/"+itoa(uid), `{"kindleEmail":"reader_x2@kindle.com"}`, e.admin)
	if e.sentToKindle(id, c) {
		t.Error("counts as sent to a new address")
	}
}

func TestKindleFailureIsNotRemembered(t *testing.T) {
	e := newKindleEnv(t)
	stubKindle(t, mailer.ErrAuth)
	e.configureMail()
	c := e.kindleProfile("")
	id := e.bookFile("Книга", "a.epub", []byte("x"))
	e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c)
	if e.sentToKindle(id, c) {
		t.Error("a failed send counts as sent")
	}
}
