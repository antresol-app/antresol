package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/library/fb2epub"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/go-chi/chi/v5"
)

// MailSettings are «Отправка на Kindle»: the SMTP account books leave from.
// Password never leaves the server: settingsAPI.load drops it from answers.
type MailSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
}

const defaultMailPort = 587

// Configured: enough is set to try sending.
func (m MailSettings) Configured() bool {
	if m.Host == "" || m.From == "" {
		return false
	}
	return m.Username == "" || m.Password != ""
}

// normalized trims what was typed; a port and a sender follow from the rest.
func (m MailSettings) normalized() MailSettings {
	m.Host = strings.TrimSpace(m.Host)
	m.Username = strings.TrimSpace(m.Username)
	m.From = strings.TrimSpace(m.From)
	if m.Port == 0 {
		m.Port = defaultMailPort
	}
	if m.From == "" && strings.Contains(m.Username, "@") {
		m.From = m.Username
	}
	// Google shows an app password in groups, with spaces that are not part
	// of it; another provider's password may really contain them.
	if h := strings.ToLower(m.Host); strings.HasSuffix(h, "gmail.com") || strings.HasSuffix(h, "googlemail.com") {
		m.Password = strings.ReplaceAll(m.Password, " ", "")
	}
	return m
}

// validate returns a Russian message for a malformed value; a form still
// being filled in (empty fields) is fine.
func (m MailSettings) validate() string {
	switch {
	case strings.ContainsAny(m.Host, " \t\r\n/:@"):
		return "Укажите адрес почтового сервера, например smtp.gmail.com"
	case m.Port < 1 || m.Port > 65535:
		return "Порт почтового сервера — число от 1 до 65535"
	case strings.ContainsAny(m.Username, "\r\n") || strings.ContainsAny(m.Password, "\r\n"):
		return "Логин и пароль почты не могут содержать перевод строки"
	case m.From != "" && !plainAddress(m.From):
		return "Укажите адрес отправителя, например name@gmail.com"
	case m.From != "" && validKindleEmail(m.From):
		return "Адрес отправителя — это ваша почта. Адрес Kindle указывается в профиле читателя"
	}
	return ""
}

// plainAddress: an e-mail address alone, without a display name.
func plainAddress(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// validKindleEmail: only Amazon's own domains. The server signs in to a real
// mailbox, and this button must not turn it into a mail relay to anyone.
func validKindleEmail(s string) bool {
	if !plainAddress(s) {
		return false
	}
	d := strings.ToLower(s[strings.LastIndexByte(s, '@')+1:])
	return d == "kindle.com" || d == "free.kindle.com"
}

// kindleMaxBytes: Amazon takes a document of at most 50 MB.
const kindleMaxBytes = 50 << 20

// kindleMailTarget: a book larger than this goes with its pictures shrunk.
// Gmail takes 25 MB in a message, and base64 and its line breaks add 37%.
var kindleMailTarget = 17 << 20 // a variable for tests

// kindleMaxRead bounds an EPUB read into memory: it may still shrink to fit.
const kindleMaxRead = 256 << 20

// kindleMaxFB2 bounds an FB2 before conversion: its pictures are base64, and
// the EPUB limit is checked afterwards.
const kindleMaxFB2 = 256 << 20

// kindleFormats: what Send to Kindle takes as is or after conversion, best
// first (it refuses MOBI and AZW3).
var kindleFormats = []string{"EPUB", "FB2", "PDF"}

var errKindleTooLarge = errors.New("larger than Kindle takes")

// kindleFile is the file of a book to send (false: none Kindle takes).
func kindleFile(files []library.File) (library.File, bool) {
	for _, want := range kindleFormats {
		for _, f := range files {
			if bookFormat(f.Path) == want {
				return f, true
			}
		}
	}
	return library.File{}, false
}

// kindleName is the attachment's name: the title, which is also what a PDF
// is called on the Kindle.
func kindleName(title, format string) string {
	t := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	if t == "" {
		t = "book"
	}
	return t + "." + strings.ToLower(format)
}

// capWriter fails past its limit, so a conversion stops at what cannot be sent.
type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errKindleTooLarge
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

// fb2Source opens the FB2 text of a .fb2 or the first .fb2 of a .fb2.zip.
func fb2Source(path string) (io.ReadCloser, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".zip") {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if st.Size() > kindleMaxFB2 {
			f.Close()
			return nil, errKindleTooLarge
		}
		return f, nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", fb2epub.ErrNotFB2, err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(f.Name), ".fb2") {
			continue
		}
		if f.UncompressedSize64 > kindleMaxFB2 {
			zr.Close()
			return nil, errKindleTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			zr.Close()
			return nil, fmt.Errorf("%w: %v", fb2epub.ErrNotFB2, err)
		}
		// the declared size can lie: the limit also holds for what is read
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(rc, kindleMaxFB2), closers{rc, zr}}, nil
	}
	zr.Close()
	return nil, fmt.Errorf("%w: no .fb2 inside", fb2epub.ErrNotFB2)
}

type closers []io.Closer

func (c closers) Close() error {
	var first error
	for _, x := range c {
		if err := x.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// kindleAttachment is the file as Kindle takes it; its reader is closed by
// the returned function.
func kindleAttachment(f library.File, title string) (r io.Reader, size int64, name, ctype string, done func(), err error) {
	format := bookFormat(f.Path)
	if format == "FB2" {
		src, err := fb2Source(f.Path)
		if err != nil {
			return nil, 0, "", "", nil, err
		}
		defer src.Close()
		var buf bytes.Buffer
		if err := fb2epub.Convert(src, &capWriter{w: &buf, left: kindleMaxRead}, fb2epub.Options{Title: title}); err != nil {
			return nil, 0, "", "", nil, err
		}
		book, err := kindleFits(buf.Bytes(), title)
		if err != nil {
			return nil, 0, "", "", nil, err
		}
		return bytes.NewReader(book), int64(len(book)), kindleName(title, "EPUB"), "application/epub+zip", func() {}, nil
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, 0, "", "", nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, "", "", nil, err
	}
	limit := int64(kindleMaxRead)
	if format == "PDF" {
		limit = kindleMaxBytes
	}
	if st.Size() > limit {
		file.Close()
		return nil, 0, "", "", nil, errKindleTooLarge
	}
	if format == "PDF" {
		return file, st.Size(), kindleName(title, format), "application/pdf", func() { file.Close() }, nil
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, "", "", nil, err
	}
	// a book added before it was cleaned on the way in, or a read-only folder
	book, err := kindleFits(library.CleanEPUB(data), title)
	if err != nil {
		return nil, 0, "", "", nil, err
	}
	return bytes.NewReader(book), int64(len(book)), kindleName(title, format), "application/epub+zip", func() {}, nil
}

// kindleFits is the EPUB as mail takes it, its pictures shrunk when it is too
// large. Only this copy changes: the library keeps the full pictures for
// reading on other screens. What stays over Gmail's limit still goes, since
// another provider may take it, and a refusal is then explained.
func kindleFits(book []byte, title string) ([]byte, error) {
	if len(book) > kindleMailTarget {
		before := len(book)
		small, ok := library.ShrinkEPUB(book, kindleMailTarget)
		log.Printf("kindle: «%s»: pictures shrunk %d -> %d bytes (fits: %v)", title, before, len(small), ok)
		book = small
	}
	if len(book) > kindleMaxBytes {
		return nil, errKindleTooLarge
	}
	return book, nil
}

// kindleReady: this profile has an address, the server an account.
func (h *lib) kindleReady(ctx context.Context, address string) (MailSettings, bool) {
	if address == "" {
		return MailSettings{}, false
	}
	s, err := LoadAppSettings(ctx, h.DB, h.SettingsDefaults)
	if err != nil {
		log.Printf("kindle: settings: %v", err)
		return MailSettings{}, false
	}
	return s.Mail, s.Mail.Configured()
}

// kindleSend: a variable for tests.
var kindleSend = mailer.Send

// kindleSlot: one conversion and upload at a time; books live in memory
// meanwhile.
var kindleSlot = make(chan struct{}, 1)

// kindleWait is how long POST waits for its job: a slow upload goes on after
// the answer (a proxy gives up on a silent request after a minute).
var kindleWait = 45 * time.Second

const kindleJobTimeout = 10 * time.Minute

// sendToKindle is POST /api/books/{id}/kindle: 204 sent, 202 still sending.
func (h *lib) sendToKindle(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindBook)
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	cfg, ready := h.kindleReady(ctx, u.KindleEmail)
	if !ready {
		writeErr(w, r, http.StatusConflict, "Отправка на Kindle не настроена")
		return
	}
	files, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "kindle files", err)
		return
	}
	f, ok := kindleFile(files)
	if !ok {
		writeErr(w, r, http.StatusUnsupportedMediaType, "У этой книги нет формата, который принимает Kindle")
		return
	}
	to, title, userID := u.KindleEmail, it.Title, u.ID
	// a second click, or a retry after a timeout, joins the running job
	key := strconv.FormatInt(u.ID, 10) + "/" + strconv.FormatInt(it.ID, 10)
	_, err = h.kindleJobs.do(ctx, key, kindleWait, kindleJobTimeout, func(job context.Context) (struct{}, error) {
		select {
		case kindleSlot <- struct{}{}:
			defer func() { <-kindleSlot }()
		case <-job.Done():
			return struct{}{}, job.Err()
		}
		err := deliverToKindle(job, cfg, to, f, title)
		if err != nil {
			log.Printf("kindle: book %d: %v", it.ID, err)
			return struct{}{}, err
		}
		if merr := h.Library.MarkSentToKindle(job, userID, it.ID, to); merr != nil {
			log.Printf("kindle: remember book %d: %v", it.ID, merr) // it did go
		}
		return struct{}{}, nil
	})
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errStillWorking), errors.Is(err, context.Canceled):
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "sending"})
	case errors.Is(err, errKindleTooLarge):
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Книга больше 50 МБ — Kindle такую не примет")
	case errors.Is(err, mailer.ErrTooLarge):
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Книга больше, чем принимает почтовый сервер")
	case errors.Is(err, fb2epub.ErrNotFB2):
		writeErr(w, r, http.StatusUnprocessableEntity, "Не удалось прочитать файл книги")
	case errors.Is(err, mailer.ErrBlocked):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер заблокировал письмо как небезопасное: дело в содержимом файла, а не в размере")
	case errors.Is(err, mailer.ErrAuth):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер не принял логин или пароль — проверьте настройки")
	case errors.Is(err, mailer.ErrRecipient):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер не принял адрес отправителя или Kindle")
	case errors.Is(err, mailer.ErrNoTLS):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер не поддерживает шифрование — такой сервер не используем")
	default:
		writeErr(w, r, http.StatusBadGateway, "Не удалось отправить книгу, попробуйте ещё раз")
	}
}

func deliverToKindle(ctx context.Context, cfg MailSettings, to string, f library.File, title string) error {
	body, size, name, ctype, done, err := kindleAttachment(f, title)
	if err != nil {
		return err
	}
	defer done()
	return kindleSend(ctx, mailer.Config{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: cfg.Password, From: cfg.From},
		mailer.Message{To: to, Subject: title, Body: title, Filename: name, ContentType: ctype, Attachment: body, Size: size})
}

func mountKindle(r chi.Router, h *lib) {
	r.Post("/api/books/{id}/kindle", h.sendToKindle)
}
