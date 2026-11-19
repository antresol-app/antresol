package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

// A managed account's app password binds an app of a third-party protocol
// to the account's history: the login is the person's (History set), the
// person's own password keeps the person's history.
func TestAppLoginWithAManagedAccountsPassword(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	papa, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tv, err := s.CreateManaged(ctx, papa.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	own, err := s.SetAppPassword(ctx, papa.ID)
	if err != nil {
		t.Fatal(err)
	}
	tvPass, err := s.SetManagedAppPassword(ctx, papa.ID, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	login := func(pw string) AppLoginOutcome {
		t.Helper()
		r := httptest.NewRequest("GET", "/", nil)
		out, err := AppLogin(ctx, s, &Network{}, NewThrottle(), r, "test", "papa", func(app string) bool { return app == pw })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := login(own); out.Result != LoginOK || out.User.ID != papa.ID || out.History.ID != 0 {
		t.Fatalf("own: %+v", out)
	}
	out := login(tvPass)
	if out.Result != LoginOK || out.User.ID != papa.ID || out.History.ID != tv.ID || out.History.Name != "Гостиная" || out.History.UID != tv.UID {
		t.Fatalf("managed: %+v", out)
	}
	if out := login("nope"); out.Result != LoginBad {
		t.Fatalf("bad: %+v", out)
	}
	// the person without an app password of their own still signs in with
	// the account's (and is not «no app password»)
	if err := s.ClearAppPassword(ctx, papa.ID); err != nil {
		t.Fatal(err)
	}
	if out := login(tvPass); out.Result != LoginOK || out.History.ID != tv.ID {
		t.Fatalf("managed only: %+v", out)
	}
	if err := s.ClearManagedAppPassword(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	if out := login(tvPass); out.Result != LoginNoAppPassword {
		t.Fatalf("none left: %+v", out)
	}
	// the managed account's name is never a login
	r := httptest.NewRequest("GET", "/", nil)
	if out, _ := AppLogin(ctx, s, &Network{}, NewThrottle(), r, "test", "Гостиная", func(string) bool { return true }); out.Result == LoginOK {
		t.Fatalf("signed in by the account's name: %+v", out)
	}
}

func TestManagedAppPasswordOnlyForItsOwner(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	papa, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	mama, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})
	tv, _ := s.CreateManaged(ctx, papa.ID, "Гостиная")
	if _, err := s.SetManagedAppPassword(ctx, mama.ID, tv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another person's account: %v", err)
	}
	if _, err := s.SetManagedAppPassword(ctx, papa.ID, papa.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a person: %v", err)
	}
	if _, _, err := s.ManagedAppPasswordInfo(ctx, mama.ID, tv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("info of another's: %v", err)
	}
	if err := s.ClearManagedAppPassword(ctx, 0, tv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear with no owner: %v", err)
	}
}

// A Jellyfin token got with a managed account's password carries the
// account; it ends with the account's password and with the account, not
// with the person's own app password. A login-password change or the
// admin's reset ends everything.
func TestAppTokenOfAManagedAccount(t *testing.T) {
	ctx := context.Background()
	s, _, d := newStoresDB(t)
	tokens := NewAppTokens(d)
	papa, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	mama, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})
	tv, _ := s.CreateManaged(ctx, papa.ID, "Гостиная")
	mamas, _ := s.CreateManaged(ctx, mama.ID, "Кухня")

	if _, err := tokens.Issue(ctx, papa.ID, "jellyfin", Device{ID: "x", History: History{ID: mamas.ID}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another person's account: %v", err)
	}
	issue := func(dev string) string {
		t.Helper()
		tok, err := tokens.Issue(ctx, papa.ID, "jellyfin", Device{ID: dev, History: History{ID: tv.ID}})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	alive := func(tok string) bool {
		_, _, err := tokens.Lookup(ctx, tok)
		return err == nil
	}
	if _, err := s.SetManagedAppPassword(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	tok := issue("tv")
	u, dev, err := tokens.Lookup(ctx, tok)
	if err != nil || u.ID != papa.ID || dev.History.ID != tv.ID || dev.History.Name != "Гостиная" || dev.History.UID != tv.UID {
		t.Fatalf("lookup %+v %+v %v", u, dev, err)
	}
	own, _ := tokens.Issue(ctx, papa.ID, "jellyfin", Device{ID: "phone"})
	if _, d2, _ := tokens.Lookup(ctx, own); d2.History.ID != 0 {
		t.Fatalf("own token %+v", d2)
	}

	// the person's own app password renewed: the TV's token stays
	if _, err := s.SetAppPassword(ctx, papa.ID); err != nil {
		t.Fatal(err)
	}
	if !alive(tok) || alive(own) {
		t.Fatalf("after own renew: tv %v own %v", alive(tok), alive(own))
	}
	// the account's password renewed: the TV's token ends
	if _, err := s.SetManagedAppPassword(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	if alive(tok) {
		t.Fatal("token outlived the account's new password")
	}
	// the account deleted: its tokens go with it
	tok = issue("tv")
	if err := s.DeleteManaged(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	if alive(tok) {
		t.Fatal("token outlived its account")
	}

	// the admin's reset ends the account's password and its tokens too
	tv, _ = s.CreateManaged(ctx, papa.ID, "Гостиная")
	if _, err := s.SetManagedAppPassword(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	tok = issue("tv")
	if err := s.ResetAppPasswords(ctx, papa.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.AppPasswordInfo(ctx, tv.ID); ok || alive(tok) {
		t.Fatalf("after reset: password %v token %v", ok, alive(tok))
	}
	if err := s.ResetAppPasswords(ctx, tv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset of a managed account: %v", err)
	}

	// a new login password ends the account's password as well
	if _, err := s.SetManagedAppPassword(ctx, papa.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	tok = issue("tv")
	pw := "secret2"
	if err := s.Update(ctx, papa.ID, UserPatch{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.AppPasswordInfo(ctx, tv.ID); ok || alive(tok) {
		t.Fatalf("after password change: password %v token %v", ok, alive(tok))
	}
}

func TestHistoryOfContext(t *testing.T) {
	ctx := WithUser(context.Background(), User{ID: 7})
	if HistoryID(ctx) != 7 || HistoryOf(ctx).ID != 0 {
		t.Fatal("own history")
	}
	hc := WithHistory(ctx, History{ID: 9, Name: "TV", UID: "u9"})
	if HistoryID(hc) != 9 || HistoryOf(hc).UID != "u9" {
		t.Fatalf("credential history %+v", HistoryOf(hc))
	}
	// the device's switch wins over nothing else being set
	sc := WithSession(ctx, Session{HistoryUserID: 11, HistoryName: "Box", HistoryUID: "u11"})
	if HistoryID(sc) != 11 || HistoryOf(sc).Name != "Box" {
		t.Fatalf("session history %+v", HistoryOf(sc))
	}
	if HistoryID(WithHistory(ctx, History{})) != 7 {
		t.Fatal("an empty history is the person's")
	}
}
