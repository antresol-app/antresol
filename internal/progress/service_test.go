package progress

import (
	"testing"
	"time"
)

// A service profile plays like any other, but what it reports is dropped
// silently: no error, no row, nothing to continue, no watched time.
func TestServiceProfileWritesNoHistory(t *testing.T) {
	e := newEnv(t)
	person := e.user("person")
	svc, _, err := e.users.CreateService(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	film, book, audio := e.movie("Film"), e.movie("Book"), e.movie("Audio")
	at := time.Now().Add(-time.Minute)

	writes := map[string]func(uid int64) error{
		"PlayedAt":       func(uid int64) error { return e.s.PlayedAt(ctx, uid, film, 50_000, 100_000, at) },
		"PlayedAt again": func(uid int64) error { return e.s.PlayedAt(ctx, uid, film, 60_000, 100_000, at.Add(10*time.Second)) },
		"Save":           func(uid int64) error { return e.s.Save(ctx, uid, film, 70_000, 100_000) },
		"ListenedAt":     func(uid int64) error { return e.s.ListenedAt(ctx, uid, audio, 10_000, 100_000, at) },
		"SaveLocator":    func(uid int64) error { return e.s.SaveLocator(ctx, uid, book, "loc", 0.4, at) },
		"watched mark":   func(uid int64) error { return e.s.MarkWatchedAt(ctx, uid, film, true, at) },
		"unwatched mark": func(uid int64) error { return e.s.MarkWatchedAt(ctx, uid, audio, false, at) },
		"watchlist add":  func(uid int64) error { return e.s.AddToWatchlist(ctx, uid, film) },
		"watchlist drop": func(uid int64) error { return e.s.RemoveFromWatchlist(ctx, uid, audio) },
		"annotation save": func(uid int64) error {
			_, err := e.s.PutAnnotation(ctx, uid, book, Annotation{ID: "a1", Kind: KindNote, Locator: "loc", Note: "n", UpdatedAt: at})
			return err
		},
	}
	for name, w := range writes {
		if err := w(svc.ID); err != nil {
			t.Errorf("%s as a service profile failed: %v", name, err)
		}
		if err := w(person); err != nil {
			t.Errorf("%s as a person failed: %v", name, err)
		}
	}

	if _, err := e.s.Get(ctx, svc.ID, film); err != ErrNotFound {
		t.Errorf("Get: a service profile has progress: %v", err)
	}
	if got, err := e.s.ContinueWatching(ctx, svc.ID, 0); err != nil || len(got) != 0 {
		t.Errorf("ContinueWatching: %v %v", got, err)
	}
	if got, err := e.s.InProgress(ctx, svc.ID, nil, 0); err != nil || len(got) != 0 {
		t.Errorf("InProgress: %v %v", got, err)
	}
	if got, err := e.s.FinishedOf(ctx, svc.ID, nil, 0); err != nil || len(got) != 0 {
		t.Errorf("FinishedOf: %v %v", got, err)
	}
	if got, err := e.s.Watchlist(ctx, svc.ID); err != nil || len(got) != 0 {
		t.Errorf("Watchlist: %v %v", got, err)
	}
	if got, err := e.s.Annotations(ctx, svc.ID, book); err != nil || len(got) != 0 {
		t.Errorf("Annotations: %v %v", got, err)
	}
	var played int
	if err := e.s.db.QueryRow(`SELECT COUNT(*) FROM watch_time WHERE user_id=?`, svc.ID).Scan(&played); err != nil || played != 0 {
		t.Errorf("watch_time: %d rows, %v", played, err)
	}
	// the same writes by a person are kept: the silence is the profile's, not the store's
	if _, err := e.s.Get(ctx, person, film); err != nil {
		t.Errorf("a person's progress is gone: %v", err)
	}
	if got, err := e.s.Watchlist(ctx, person); err != nil || len(got) != 1 {
		t.Errorf("a person's watchlist: %v %v", got, err)
	}
}
