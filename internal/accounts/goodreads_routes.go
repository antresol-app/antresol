package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const goodreadsProvider = "goodreads"

// ErrBadImport: an uploaded file is not what the import reads.
var ErrBadImport = errors.New("accounts: not a library export")

// maxGoodreadsCSV bounds an uploaded Goodreads export (reviews make a long
// library a few megabytes). It travels as a JSON string, which quotes and line
// breaks make longer: the request body may be up to three times that.
const maxGoodreadsCSV = 24 << 20

// ShelfSummary counts a profile's Goodreads books: all of them, by shelf,
// and how many of them are in the library.
type ShelfSummary struct {
	Total, Read, Reading, ToRead, InLibrary int
}

// Goodreads is the read-only Goodreads side of the API (the goodreads
// package implements it). Goodreads has no API a new application can use:
// a linked profile's public shelf widget is read, and a library export
// (CSV) is imported.
type Goodreads interface {
	// Check reads the profile's public shelf once: an unknown or non-public
	// one is ErrRemoteNotFound.
	Check(ctx context.Context, id int64) error
	Link(ctx context.Context, uid, id int64, name string) error
	Pull(ctx context.Context, uid int64) error
	// Import reads a library export (ErrBadImport for another file) into the
	// profile's books.
	Import(ctx context.Context, uid int64, csv io.Reader) (ShelfSummary, error)
	// Forget removes the link and the books read from Goodreads (marks stay).
	Forget(ctx context.Context, uid int64) error
	Summary(ctx context.Context, uid int64) (ShelfSummary, error)
	// ParseProfile reads a profile address or id (ok false: not one).
	ParseProfile(s string) (id int64, name string, ok bool)
}

type goodreadsJSON struct {
	Linked    bool    `json:"linked"`
	Nickname  string  `json:"nickname"`
	Status    string  `json:"status"`
	LastError string  `json:"lastError"`
	LastSync  *string `json:"lastSync"`
	Syncing   bool    `json:"syncing"`
	Total     int     `json:"total"`
	Read      int     `json:"read"`
	Reading   int     `json:"reading"`
	ToRead    int     `json:"toRead"`
	InLibrary int     `json:"inLibrary"`
}

func (a *API) goodreadsState(ctx context.Context, uid int64) (goodreadsJSON, error) {
	out := goodreadsJSON{Status: StatusOK, Syncing: a.Sync.Runs().Active(uid, goodreadsProvider)}
	sum, err := a.Goodreads.Summary(ctx, uid)
	if err != nil {
		return out, err
	}
	out.Total, out.Read, out.Reading, out.ToRead, out.InLibrary = sum.Total, sum.Read, sum.Reading, sum.ToRead, sum.InLibrary
	l, ok, err := a.Sync.Accounts.Link(ctx, uid, goodreadsProvider)
	if err != nil || !ok {
		return out, err
	}
	out.Linked, out.Nickname, out.Status = true, l.Nickname, l.Status
	out.LastError = a.localize(ctx, l.LastError)
	if !l.LastSync.IsZero() {
		s := l.LastSync.UTC().Format(time.RFC3339)
		out.LastSync = &s
	}
	return out, nil
}

func (a *API) putGoodreads(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if a.Goodreads == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Profile string `json:"profile"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Profile) == "" {
		a.fail(w, r, http.StatusBadRequest, "Укажите адрес профиля на Goodreads", nil)
		return
	}
	id, name, ok := a.Goodreads.ParseProfile(body.Profile)
	if !ok {
		a.fail(w, r, http.StatusBadRequest, "Не удалось найти номер профиля в адресе: нужен вид goodreads.com/user/show/12345", nil)
		return
	}
	ctx := r.Context()
	switch err := a.Goodreads.Check(ctx, id); {
	case errors.Is(err, ErrRemoteNotFound):
		a.fail(w, r, http.StatusNotFound, "Профиль Goodreads не найден или скрыт", nil)
		return
	case err != nil:
		a.fail(w, r, http.StatusBadGateway, "Goodreads не отвечает, попробуйте позже", err)
		return
	}
	if err := a.Goodreads.Link(ctx, v.ID, id, name); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	// with a sync running already none starts: the periodic pull reads a link
	// with no last sync at once
	a.startRun(v.ID, goodreadsProvider, 5*time.Minute, func(ctx context.Context) {
		if err := a.Goodreads.Pull(ctx, v.ID); err != nil {
			a.log().Info("accounts: goodreads pull after link", "user", v.ID, "err", err)
		}
	})
	a.get(w, r)
}

func (a *API) deleteGoodreads(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if a.Goodreads == nil {
		http.NotFound(w, r)
		return
	}
	if err := a.Goodreads.Forget(r.Context(), v.ID); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) importGoodreads(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if a.Goodreads == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		CSV string `json:"csv"`
	}
	// a library export is the file's text in JSON: a request type the CSRF
	// rule already lets through only from the app's own pages
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*maxGoodreadsCSV)).Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.fail(w, r, http.StatusRequestEntityTooLarge, "Файл больше 24 МБ", nil)
			return
		}
		a.fail(w, r, http.StatusBadRequest, "Неверный запрос", nil)
		return
	}
	if len(body.CSV) > maxGoodreadsCSV {
		a.fail(w, r, http.StatusRequestEntityTooLarge, "Файл больше 24 МБ", nil)
		return
	}
	switch _, err := a.Goodreads.Import(r.Context(), v.ID, strings.NewReader(body.CSV)); {
	case errors.Is(err, ErrBadImport):
		a.fail(w, r, http.StatusUnprocessableEntity, "Это не экспорт библиотеки Goodreads: нужен файл из «Мои книги → Импорт и экспорт → Экспортировать библиотеку»", nil)
		return
	case err != nil:
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	a.get(w, r)
}
