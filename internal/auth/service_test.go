package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceProfileCreate(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	admin, _ := s.CreateFirstAdmin(ctx, "Папа", "secret-pass")

	u, pw, err := s.CreateService(ctx, "  playback-check ")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Service || u.Name != "playback-check" || u.IsAdmin || u.IsKid || u.OwnerID != 0 || !u.HasPassword || len(pw) < 32 {
		t.Fatalf("service %+v, password of %d characters", u, len(pw))
	}
	// it signs in with that password and with nothing else
	if got, err := s.Verify(ctx, u.ID, pw); err != nil || got.ID != u.ID || !got.Service {
		t.Fatalf("Verify: %+v %v", got, err)
	}
	if _, err := s.Verify(ctx, u.ID, ""); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("Verify without a password: %v", err)
	}
	if _, err := s.VerifyPasswordless(ctx, u.ID); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("VerifyPasswordless: %v", err)
	}
	// a name is taken once, by a person's profile too
	if _, _, err := s.CreateService(ctx, "playback-check"); !errors.Is(err, ErrExists) {
		t.Fatalf("a second one: %v", err)
	}
	if _, _, err := s.CreateService(ctx, admin.Name); !errors.Is(err, ErrExists) {
		t.Fatalf("a person's name: %v", err)
	}
	if _, _, err := s.CreateService(ctx, "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty name: %v", err)
	}
}

// A service profile is in no list of people and is no one's account.
func TestServiceProfileIsInNoListOfPeople(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	if _, _, err := s.CreateService(ctx, "machine"); err != nil {
		t.Fatal(err)
	}
	// only a service profile exists: the server still needs its first administrator
	if need, err := s.NeedsSetup(ctx); err != nil || !need {
		t.Fatalf("NeedsSetup: %v %v", need, err)
	}
	admin, err := s.CreateFirstAdmin(ctx, "Папа", "secret-pass")
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	mom, _ := s.CreateUser(ctx, NewUser{Name: "Мама"})
	svc, err := s.serviceByName(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}

	for name, list := range map[string]func(context.Context) ([]User, error){"List": s.List, "ListAll": s.ListAll} {
		got, err := list(ctx)
		if err != nil || len(got) != 2 || got[0].ID != admin.ID || got[1].ID != mom.ID {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if got, err := s.ListService(ctx); err != nil || len(got) != 1 || got[0].ID != svc.ID || !got[0].Service {
		t.Errorf("ListService: %+v %v", got, err)
	}
	// the name a Subsonic or OPDS client signs in with, and the one an admin looks up
	if _, err := s.ByName(ctx, "machine"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ByName: %v", err)
	}

	// its sign-ins are in no list of devices
	if _, err := ss.CreateBrowser(ctx, svc.ID, "Chrome", "Linux"); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateDevice(ctx, svc.ID, DeviceInfo{ID: "d1", Name: "Phone", Model: "x", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateBrowser(ctx, mom.ID, "Safari", "Mac"); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateDevice(ctx, mom.ID, DeviceInfo{ID: "d2", Name: "Tablet", Model: "y", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	all, err := ss.AllDevices(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("AllDevices: %d %v", len(all), err)
	}
	for _, d := range all {
		if d.UserID == svc.ID {
			t.Errorf("AllDevices lists the service profile: %+v", d)
		}
	}
	if got, err := ss.NativeDevices(ctx); err != nil || len(got) != 1 || got[0].UserID != mom.ID {
		t.Errorf("NativeDevices: %+v %v", got, err)
	}
	if got, err := ss.RecentBrowsers(ctx, time.Now().Add(-time.Hour)); err != nil || len(got) != 1 || got[0].UserID != mom.ID {
		t.Errorf("RecentBrowsers: %+v %v", got, err)
	}
}

func TestServiceProfileProtoTokensAreNotListed(t *testing.T) {
	ctx := context.Background()
	s, _, d := newStoresDB(t)
	svc, _, _ := s.CreateService(ctx, "machine")
	mom, _ := s.CreateUser(ctx, NewUser{Name: "Мама"})
	tokens := NewAppTokens(d)
	for _, id := range []int64{svc.ID, mom.ID} {
		if _, err := tokens.Issue(ctx, id, "proto", Device{ID: "dev", Name: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := tokens.ByProto(ctx, "proto")
	if err != nil || len(got) != 1 || got[0].UserID != mom.ID {
		t.Fatalf("ByProto: %+v %v", got, err)
	}
}

// The people's profile API cannot change or remove it: only the command.
func TestServiceProfileIsChangedOnlyByTheCommand(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	if _, err := s.CreateFirstAdmin(ctx, "Папа", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	svc, pw, _ := s.CreateService(ctx, "machine")

	name := "other"
	if err := s.Update(ctx, svc.ID, UserPatch{Name: &name}); !errors.Is(err, ErrService) {
		t.Errorf("Update: %v", err)
	}
	admin := true
	if err := s.Update(ctx, svc.ID, UserPatch{IsAdmin: &admin}); !errors.Is(err, ErrService) {
		t.Errorf("Update to admin: %v", err)
	}
	if err := s.Delete(ctx, svc.ID); !errors.Is(err, ErrService) {
		t.Errorf("Delete: %v", err)
	}
	if got, err := s.Get(ctx, svc.ID); err != nil || got.Name != "machine" || got.IsAdmin {
		t.Fatalf("after: %+v %v", got, err)
	}
	// the command's removal never takes a person
	if err := s.DeleteService(ctx, "Папа"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteService of a person: %v", err)
	}
	if _, _, err := s.ResetServicePassword(ctx, "Папа"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResetServicePassword of a person: %v", err)
	}

	// a new password: the old one and the sessions end
	if _, err := ss.CreateBrowser(ctx, svc.ID, "curl", "Linux"); err != nil {
		t.Fatal(err)
	}
	u, pw2, err := s.ResetServicePassword(ctx, "machine")
	if err != nil || u.ID != svc.ID || pw2 == pw {
		t.Fatalf("ResetServicePassword: %+v %q %v", u, pw2, err)
	}
	if _, err := s.Verify(ctx, svc.ID, pw); !errors.Is(err, ErrBadPassword) {
		t.Errorf("the old password: %v", err)
	}
	if _, err := s.Verify(ctx, svc.ID, pw2); err != nil {
		t.Errorf("the new password: %v", err)
	}
	var left int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, svc.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("sessions left: %d %v", left, err)
	}

	if err := s.DeleteService(ctx, "machine"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if _, err := s.Get(ctx, svc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after DeleteService: %v", err)
	}
}
