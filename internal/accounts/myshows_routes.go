package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const myShowsProvider = "myshows"

// MyShows is the read-only MyShows side of the API (the myshows package
// implements it). Check reads the login's public list once: an unknown login
// is ErrRemoteNotFound, a private profile ErrRemoteForbidden.
type MyShows interface {
	Check(ctx context.Context, login string) error
	Pull(ctx context.Context, uid int64) error
}

type myShowsJSON struct {
	Linked    bool    `json:"linked"`
	Nickname  string  `json:"nickname"`
	Status    string  `json:"status"`
	LastError string  `json:"lastError"`
	LastSync  *string `json:"lastSync"`
	Unmapped  int     `json:"unmapped"`
}

func (a *API) myShowsState(ctx context.Context, uid int64) (myShowsJSON, error) {
	out := myShowsJSON{Status: StatusOK}
	l, ok, err := a.Sync.Accounts.Link(ctx, uid, myShowsProvider)
	if err != nil || !ok {
		return out, err
	}
	out.Linked, out.Nickname, out.Status, out.Unmapped = true, l.Nickname, l.Status, l.Unmapped
	out.LastError = a.localize(ctx, l.LastError)
	if !l.LastSync.IsZero() {
		s := l.LastSync.UTC().Format(time.RFC3339)
		out.LastSync = &s
	}
	return out, nil
}

func (a *API) putMyShows(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if a.MyShows == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Nickname string `json:"nickname"`
	}
	login := ""
	if err := decode(r, &body); err == nil {
		login = strings.TrimSpace(body.Nickname)
	}
	if login == "" {
		a.fail(w, r, http.StatusBadRequest, "Укажите логин на MyShows", nil)
		return
	}
	ctx := r.Context()
	switch err := a.MyShows.Check(ctx, login); {
	case errors.Is(err, ErrRemoteNotFound):
		a.fail(w, r, http.StatusNotFound, "Пользователь MyShows не найден", nil)
		return
	case errors.Is(err, ErrRemoteForbidden):
		a.fail(w, r, http.StatusUnprocessableEntity, "Профиль MyShows скрыт", nil)
		return
	case err != nil:
		a.fail(w, r, http.StatusBadGateway, "MyShows не отвечает, попробуйте позже", err)
		return
	}
	// under the profile's sync lock: a pull in flight does not see the
	// link change halfway
	lk := a.Sync.userLock(v.ID)
	lk.Lock()
	err := a.Sync.Accounts.SetNickname(ctx, v.ID, myShowsProvider, login, 0)
	lk.Unlock()
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	a.background(func() {
		// a first read of a long list takes minutes (a request a second)
		pctx, cancel := context.WithTimeout(context.Background(), linkPullTimeout)
		defer cancel()
		if err := a.MyShows.Pull(pctx, v.ID); err != nil {
			a.log().Info("accounts: myshows pull after link", "user", v.ID, "err", err)
		}
	})
	a.get(w, r)
}

// linkPullTimeout bounds the pull right after linking: only a safety net, a
// list of hundreds of shows takes ten minutes or so.
const linkPullTimeout = 3 * time.Hour

func (a *API) unlinkMyShows(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if a.MyShows == nil {
		http.NotFound(w, r)
		return
	}
	lk := a.Sync.userLock(v.ID)
	lk.Lock()
	err := a.Sync.Accounts.Unlink(r.Context(), v.ID, myShowsProvider)
	lk.Unlock()
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
