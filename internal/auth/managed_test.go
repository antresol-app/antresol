package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestManagedAccountInheritsAndCannotSignIn(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	admin, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	kid, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true, MaxRating: "6+"})

	m, err := s.CreateManaged(ctx, kid.ID, "Детская")
	if err != nil {
		t.Fatal(err)
	}
	if m.OwnerID != kid.ID || !m.IsKid || m.MaxRating == nil || *m.MaxRating != "6+" || m.IsAdmin || m.HasPassword {
		t.Fatalf("managed %+v", m)
	}
	// the owner's limits change: the managed account follows
	r := "12+"
	if err := s.Update(ctx, kid.ID, UserPatch{MaxRating: &r}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, m.ID)
	if err != nil || got.MaxRating == nil || *got.MaxRating != "12+" || !got.IsKid {
		t.Fatalf("after owner change %+v %v", got, err)
	}
	// an admin's managed account is no admin
	am, err := s.CreateManaged(ctx, admin.ID, "Гостиная")
	if err != nil || am.IsAdmin {
		t.Fatalf("%+v %v", am, err)
	}

	list, _ := s.List(ctx)
	for _, u := range list {
		if u.OwnerID != 0 {
			t.Fatalf("List shows managed %+v", u)
		}
	}
	if _, err := s.ByName(ctx, "Гостиная"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ByName: %v", err)
	}
	if _, err := s.Verify(ctx, am.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := s.VerifyPasswordless(ctx, am.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VerifyPasswordless: %v", err)
	}
	pw := "secret12"
	if err := s.Update(ctx, am.ID, UserPatch{Password: &pw}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update: %v", err)
	}
	if _, err := s.SetAppPassword(ctx, am.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAppPassword: %v", err)
	}
	// a managed account has no managed accounts of its own
	if _, err := s.CreateManaged(ctx, am.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nested: %v", err)
	}
	if ok, _ := s.IsManaged(ctx, am.ID); !ok {
		t.Fatal("IsManaged")
	}
	if ok, _ := s.IsManaged(ctx, admin.ID); ok {
		t.Fatal("IsManaged person")
	}
}

func TestManagedAccountCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	b, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})

	m, err := s.CreateManaged(ctx, a.ID, "  Гостиная ")
	if err != nil || m.Name != "Гостиная" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := s.CreateManaged(ctx, a.ID, "Гостиная"); !errors.Is(err, ErrExists) {
		t.Fatalf("dup: %v", err)
	}
	if _, err := s.CreateManaged(ctx, a.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	// someone else's account is not found
	if err := s.RenameManaged(ctx, b.ID, m.ID, "Кухня"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename other: %v", err)
	}
	if err := s.RenameManaged(ctx, a.ID, m.ID, "Кухня"); err != nil {
		t.Fatal(err)
	}
	// a person's id is no managed account
	if err := s.RenameManaged(ctx, 0, b.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename person: %v", err)
	}
	mine, err := s.ManagedOf(ctx, a.ID)
	if err != nil || len(mine) != 1 || mine[0].Name != "Кухня" {
		t.Fatalf("%+v %v", mine, err)
	}
	all, err := s.AllManaged(ctx)
	if err != nil || len(all) != 1 || all[0].OwnerID != a.ID {
		t.Fatalf("%+v %v", all, err)
	}
	if err := s.DeleteManaged(ctx, b.ID, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete other: %v", err)
	}
	if err := s.DeleteManaged(ctx, 0, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete person via managed: %v", err)
	}
	if err := s.DeleteManaged(ctx, a.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}

	// the limit per person
	for i := 0; i < maxManaged; i++ {
		if _, err := s.CreateManaged(ctx, b.ID, fmt.Sprintf("TV %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateManaged(ctx, b.ID, "one more"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit: %v", err)
	}
	// deleting the person deletes theirs
	if err := s.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.AllManaged(ctx); len(all) != 0 {
		t.Fatalf("left after owner delete: %+v", all)
	}
}

func TestDeviceHistorySwitch(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	b, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})
	tv := DeviceInfo{ID: "tv-000001", Name: "Apple TV"}
	tok, _ := ss.CreateDevice(ctx, a.ID, tv)
	_, sess, err := ss.LookupSession(ctx, tok)
	if err != nil || sess.HistoryUserID != 0 {
		t.Fatalf("%+v %v", sess, err)
	}
	if got := HistoryID(WithSession(WithUser(ctx, a), sess)); got != a.ID {
		t.Fatalf("HistoryID off = %d", got)
	}
	m, _ := s.CreateManaged(ctx, a.ID, "Гостиная")
	other, _ := s.CreateManaged(ctx, b.ID, "Спальня")
	if err := ss.SetHistory(ctx, sess, other.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's account: %v", err)
	}
	if err := ss.SetHistory(ctx, sess, m.ID, true); err != nil {
		t.Fatal(err)
	}
	_, sess, _ = ss.LookupSession(ctx, tok)
	if sess.HistoryUserID != m.ID || sess.HistoryName != "Гостиная" {
		t.Fatalf("on: %+v", sess)
	}
	if got := HistoryID(WithSession(WithUser(ctx, a), sess)); got != m.ID {
		t.Fatalf("HistoryID on = %d", got)
	}
	// a new sign-in of the same install keeps the switch
	tok2, _ := ss.CreateDevice(ctx, a.ID, tv)
	_, sess2, _ := ss.LookupSession(ctx, tok2)
	if sess2.HistoryUserID != m.ID {
		t.Fatalf("after sign-in: %+v", sess2)
	}
	devs, _ := ss.Devices(ctx, a.ID)
	if len(devs) != 1 || devs[0].HistoryUserID != m.ID {
		t.Fatalf("devices %+v", devs)
	}
	using, err := ss.DevicesOfManaged(ctx, m.ID)
	if err != nil || len(using) != 1 || using[0].ID != sess2.ID {
		t.Fatalf("using %+v %v", using, err)
	}
	// «Оставить»: off, the account is remembered
	if err := ss.SetHistory(ctx, sess2, 0, false); err != nil {
		t.Fatal(err)
	}
	_, sess2, _ = ss.LookupSession(ctx, tok2)
	if sess2.HistoryUserID != 0 {
		t.Fatalf("off: %+v", sess2)
	}
	if id, _ := ss.LastHistory(ctx, sess2); id != m.ID {
		t.Fatalf("remembered %d", id)
	}
	if using, _ := ss.DevicesOfManaged(ctx, m.ID); len(using) != 0 {
		t.Fatalf("off still using %+v", using)
	}

	// a browser's switch goes with its session
	web, _ := ss.Create(ctx, a.ID)
	_, ws, _ := ss.LookupSession(ctx, web)
	if err := ss.SetHistory(ctx, ws, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := ss.Revoke(ctx, web); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM device_history WHERE device_key LIKE 's:%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("browser rows left %d", n)
	}
	// deleting the account sends the device back to the person's history
	if err := ss.SetHistory(ctx, sess2, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteManaged(ctx, a.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	_, sess2, err = ss.LookupSession(ctx, tok2)
	if err != nil || sess2.HistoryUserID != 0 {
		t.Fatalf("after delete %+v %v", sess2, err)
	}
}

// A managed account's name is unique per owner and never meets the names
// of the people's profiles: «Гостиная» may be a profile and an account.
func TestManagedNamesAreScopedToTheirOwner(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	b, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})

	m, err := s.CreateManaged(ctx, a.ID, "Гостиная")
	if err != nil || m.Name != "Гостиная" {
		t.Fatalf("%+v %v", m, err)
	}
	// a real profile of that name is allowed, and it is the one that signs in
	room, err := s.CreateUser(ctx, NewUser{Name: "Гостиная"})
	if err != nil || room.OwnerID != 0 {
		t.Fatalf("profile of the same name: %+v %v", room, err)
	}
	if got, err := s.ByName(ctx, "Гостиная"); err != nil || got.ID != room.ID {
		t.Fatalf("ByName: %+v %v", got, err)
	}
	// real profiles are still unique among themselves
	if _, err := s.CreateUser(ctx, NewUser{Name: "Гостиная"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate profile: %v", err)
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "Mama"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate profile: %v", err)
	}
	// another person's account may have the name, the same person's may not
	other, err := s.CreateManaged(ctx, b.ID, "Гостиная")
	if err != nil || other.Name != "Гостиная" {
		t.Fatalf("other owner: %+v %v", other, err)
	}
	if _, err := s.CreateManaged(ctx, a.ID, "Гостиная"); !errors.Is(err, ErrExists) {
		t.Fatalf("same owner: %v", err)
	}
	k, err := s.CreateManaged(ctx, a.ID, "Кухня")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenameManaged(ctx, a.ID, k.ID, "Гостиная"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename onto a taken name: %v", err)
	}
	if err := s.RenameManaged(ctx, a.ID, k.ID, "Спальня"); err != nil {
		t.Fatal(err)
	}
	// a managed account's name is what every reader sees
	mine, _ := s.ManagedOf(ctx, a.ID)
	if len(mine) != 2 || mine[0].Name != "Гостиная" || mine[1].Name != "Спальня" {
		t.Fatalf("ManagedOf %+v", mine)
	}
	if got, _ := s.Get(ctx, k.ID); got.Name != "Спальня" {
		t.Fatalf("Get %+v", got)
	}
	// a device's «base 2» naming is per owner too
	for i, want := range []string{"Apple TV", "Apple TV 2"} {
		got, err := s.CreateManagedNamed(ctx, a.ID, "Apple TV")
		if err != nil || got.Name != want {
			t.Fatalf("named %d: %+v %v", i, got, err)
		}
	}
	if got, err := s.CreateManagedNamed(ctx, b.ID, "Apple TV"); err != nil || got.Name != "Apple TV" {
		t.Fatalf("named, other owner: %+v %v", got, err)
	}
	// the device's history shows the account's name, not its key
	tok, _ := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "tv-000001", Name: "Apple TV"})
	_, sess, _ := ss.LookupSession(ctx, tok)
	if err := ss.SetHistory(ctx, sess, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, sess, _ = ss.LookupSession(ctx, tok); sess.HistoryName != "Гостиная" {
		t.Fatalf("history name %q", sess.HistoryName)
	}
}

// A managed account is never signed in: whatever path made a session or a
// token for one, creating and presenting it are refused.
func TestManagedAccountHasNoSessionsOrTokens(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	m, err := s.CreateManaged(ctx, a.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	tokens := NewAppTokens(d)

	// making one
	if _, err := ss.Create(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("browser session: %v", err)
	}
	if _, err := ss.CreateDevice(ctx, m.ID, DeviceInfo{ID: "tv-000001", Name: "TV"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("device token: %v", err)
	}
	if _, err := tokens.Issue(ctx, m.ID, "iptv", Device{ID: "x", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app token: %v", err)
	}
	var n int
	d.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions WHERE user_id=?) + (SELECT COUNT(*) FROM app_tokens WHERE user_id=?)`, m.ID, m.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows made for a managed account", n)
	}

	// presenting one that got into the tables some other way
	tok := "managed-session-token"
	res, err := d.Exec(`INSERT INTO sessions(token, user_id, kind, device_id, device_name) VALUES(?,?,?,?,?)`, TokenHash(tok), m.ID, KindNative, "tv-000002", "TV")
	if err != nil {
		t.Fatal(err)
	}
	rowid, _ := res.LastInsertId()
	if _, _, err := ss.LookupSession(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session of a managed account: %v", err)
	}
	if _, err := ss.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup: %v", err)
	}
	// a media key of that session
	mk := newKeys(t, ss)
	sess, err := ss.SessionByID(ctx, rowid)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := mk.Issue(sess, tok, time.Hour)
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("media key of a managed account: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO app_tokens(token, user_id, proto, device_id, device, client, version, created_at, last_seen) VALUES(?,?,?,?,?,?,?,?,?)`,
		"appt-managed", m.ID, "iptv", "x", "x", "x", "", now, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokens.Lookup(ctx, "appt-managed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app token of a managed account: %v", err)
	}
	if _, err := tokens.LookupProto(ctx, "appt-managed", "iptv"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LookupProto: %v", err)
	}
	// a person's still work
	if _, err := ss.Create(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	pt, err := tokens.Issue(ctx, a.ID, "iptv", Device{ID: "x", Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if u, _, err := tokens.Lookup(ctx, pt); err != nil || u.ID != a.ID {
		t.Fatalf("person's token: %+v %v", u, err)
	}
}

// A pairing approved for a managed account (whatever approved it) signs
// nobody in.
func TestPairingNeverSignsInAManagedAccount(t *testing.T) {
	ctx := context.Background()
	p, ss, a, _ := newPairingsTest(t)
	users := NewStore(p.db)
	m, err := users.CreateManaged(ctx, a.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	st, err := p.Start(ctx, tvDevice, "192.168.1.30", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Approve(ctx, st.Code, m.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := p.Poll(ctx, st.Secret)
	if err != nil || got.Status != PairExpired || got.Token != "" {
		t.Fatalf("poll: %+v %v", got, err)
	}
	if devs, _ := ss.Devices(ctx, m.ID); len(devs) != 0 {
		t.Fatalf("devices of a managed account: %+v", devs)
	}
}

// Two requests that switch a device on at once end with one account.
func TestSwitchOnTwiceAtOnceMakesOneAccount(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	tok, _ := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "tv-000001", Name: "Apple TV"})
	_, sess, err := ss.LookupSession(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	ids := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = ss.SwitchOn(ctx, sess, 0, "Apple TV")
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: %d %v (first %d)", i, ids[i], errs[i], ids[0])
		}
	}
	mine, _ := s.ManagedOf(ctx, a.ID)
	if len(mine) != 1 || mine[0].ID != ids[0] || mine[0].Name != "Apple TV" {
		t.Fatalf("accounts %+v", mine)
	}
	_, sess, _ = ss.LookupSession(ctx, tok)
	if sess.HistoryUserID != ids[0] {
		t.Fatalf("device writes into %d, want %d", sess.HistoryUserID, ids[0])
	}
	// off with «Оставить» and on again: the same account
	if err := ss.SetHistory(ctx, sess, 0, false); err != nil {
		t.Fatal(err)
	}
	if again, err := ss.SwitchOn(ctx, sess, 0, "Apple TV"); err != nil || again != ids[0] {
		t.Fatalf("again: %d %v", again, err)
	}
	// another person's account is refused and nothing is made
	b, _ := s.CreateUser(ctx, NewUser{Name: "Mama"})
	theirs, _ := s.CreateManaged(ctx, b.ID, "Спальня")
	if _, err := ss.SwitchOn(ctx, sess, theirs.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's: %v", err)
	}
}

func TestDetachManagedSwitchesEveryDeviceOff(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	m, _ := s.CreateManaged(ctx, a.ID, "Гостиная")
	var toks []string
	for _, id := range []string{"tv-000001", "tv-000002"} {
		tok, _ := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: id, Name: id})
		_, sess, _ := ss.LookupSession(ctx, tok)
		if err := ss.SetHistory(ctx, sess, m.ID, true); err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	if using, _ := ss.DevicesOfManaged(ctx, m.ID); len(using) != 2 {
		t.Fatalf("using %+v", using)
	}
	if err := ss.DetachManaged(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		_, sess, _ := ss.LookupSession(ctx, tok)
		if sess.HistoryUserID != 0 {
			t.Fatalf("still writes into the account: %+v", sess)
		}
		if id, _ := ss.LastHistory(ctx, sess); id != m.ID {
			t.Fatalf("the device forgot the account: %d", id)
		}
	}
}
