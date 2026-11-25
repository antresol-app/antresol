package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func newPairingsTest(t *testing.T) (*Pairings, *Sessions, User, *time.Time) {
	t.Helper()
	s, ss, d := newStoresDB(t)
	u, err := s.CreateFirstAdmin(context.Background(), "Papa", "secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p := NewPairings(d, ss)
	p.Now = func() time.Time { return now }
	return p, ss, u, &now
}

var tvDevice = DeviceInfo{ID: "tv-000001", Name: "Гостиная", Model: "Apple TV", Version: "2.0"}

func TestPairingHappyPath(t *testing.T) {
	ctx := context.Background()
	p, ss, u, _ := newPairingsTest(t)
	st, err := p.Start(ctx, tvDevice, "192.168.1.30", false)
	if err != nil {
		t.Fatal(err)
	}
	if NormalizePairingCode(st.Code) != st.Code || len(st.Secret) != 43 {
		t.Fatalf("start %+v", st)
	}
	if got, err := p.Poll(ctx, st.Secret); err != nil || got.Status != PairPending || got.Token != "" {
		t.Fatalf("poll pending %+v %v", got, err)
	}
	// typed by hand: lower case, a dash in the middle
	typed := strings.ToLower(st.Code[:3] + "-" + st.Code[3:])
	info, err := p.Lookup(ctx, typed)
	if err != nil || info.Device != tvDevice || info.Remote {
		t.Fatalf("lookup %+v %v", info, err)
	}
	if err := p.Approve(ctx, typed, u.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	// the code is used: no second approval, no lookup
	if err := p.Approve(ctx, st.Code, u.ID, u.ID); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("second approve %v", err)
	}
	if _, err := p.Lookup(ctx, st.Code); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("lookup after approve %v", err)
	}
	got, err := p.Poll(ctx, st.Secret)
	if err != nil || got.Status != PairDone || got.Token == "" || got.User.ID != u.ID {
		t.Fatalf("poll approved %+v %v", got, err)
	}
	// the same token as /api/auth/token: a native session of the device
	who, sess, err := ss.LookupSession(ctx, got.Token)
	if err != nil || who.ID != u.ID || sess.Kind != KindNative || sess.Device != tvDevice {
		t.Fatalf("session %+v %+v %v", who, sess, err)
	}
	// picked up once
	if again, err := p.Poll(ctx, st.Secret); err != nil || again.Status != PairExpired || again.Token != "" {
		t.Fatalf("second pickup %+v %v", again, err)
	}
}

func TestPairingDeny(t *testing.T) {
	ctx := context.Background()
	p, _, u, _ := newPairingsTest(t)
	st, _ := p.Start(ctx, tvDevice, "c", false)
	if err := p.Deny(ctx, st.Code, u.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Poll(ctx, st.Secret); got.Status != PairDenied {
		t.Fatalf("%+v", got)
	}
	if err := p.Approve(ctx, st.Code, u.ID, u.ID); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("approve after deny %v", err)
	}
}

func TestPairingExpiry(t *testing.T) {
	ctx := context.Background()
	p, _, u, now := newPairingsTest(t)
	st, _ := p.Start(ctx, tvDevice, "c", false)
	*now = now.Add(PairingTTL)
	if _, err := p.Lookup(ctx, st.Code); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("lookup %v", err)
	}
	if err := p.Approve(ctx, st.Code, u.ID, u.ID); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("approve %v", err)
	}
	if got, err := p.Poll(ctx, st.Secret); err != nil || got.Status != PairExpired {
		t.Fatalf("poll %+v %v", got, err)
	}
	// an hour after it expired the row is gone: the secret is unknown
	*now = now.Add(pairingKeep + time.Second)
	if _, err := p.Start(ctx, DeviceInfo{ID: "other-0001"}, "c", false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Poll(ctx, st.Secret); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("poll after cleanup %v", err)
	}
}

func TestPairingApprovedLateGetsPickupTime(t *testing.T) {
	ctx := context.Background()
	p, _, u, now := newPairingsTest(t)
	st, _ := p.Start(ctx, tvDevice, "c", false)
	*now = now.Add(PairingTTL - time.Second)
	if err := p.Approve(ctx, st.Code, u.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(30 * time.Second) // past the code's own expiry
	if got, err := p.Poll(ctx, st.Secret); err != nil || got.Status != PairDone || got.Token == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPairingCaps(t *testing.T) {
	ctx := context.Background()
	p, _, _, _ := newPairingsTest(t)
	for i := range PairingMaxPerClient {
		if _, err := p.Start(ctx, DeviceInfo{ID: "dev-0000" + string(rune('a'+i))}, "10.0.0.5", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Start(ctx, DeviceInfo{ID: "dev-0000z"}, "10.0.0.5", false); !errors.Is(err, ErrPairingBusy) {
		t.Fatalf("over the client cap: %v", err)
	}
	// the same device starting again replaces its own request
	if _, err := p.Start(ctx, DeviceInfo{ID: "dev-0000a"}, "10.0.0.5", false); err != nil {
		t.Fatalf("restart: %v", err)
	}
	// another client is not held up
	if _, err := p.Start(ctx, DeviceInfo{ID: "dev-0000y"}, "10.0.0.6", false); err != nil {
		t.Fatal(err)
	}
}

func TestPairingRestartEndsOldCode(t *testing.T) {
	ctx := context.Background()
	p, _, _, _ := newPairingsTest(t)
	first, _ := p.Start(ctx, tvDevice, "c", false)
	if _, err := p.Start(ctx, tvDevice, "c", false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Lookup(ctx, first.Code); !errors.Is(err, ErrPairingGone) {
		t.Fatalf("old code %v", err)
	}
}

func TestPairingConcurrentPickupOneToken(t *testing.T) {
	ctx := context.Background()
	p, _, u, _ := newPairingsTest(t)
	st, _ := p.Start(ctx, tvDevice, "c", false)
	if err := p.Approve(ctx, st.Code, u.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	tokens := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.Poll(ctx, st.Secret)
			if err != nil {
				t.Error(err)
				return
			}
			if got.Token != "" {
				mu.Lock()
				tokens++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if tokens != 1 {
		t.Fatalf("%d tokens", tokens)
	}
}

func TestPairingSecretsHashed(t *testing.T) {
	ctx := context.Background()
	p, _, _, _ := newPairingsTest(t)
	st, _ := p.Start(ctx, tvDevice, "c", true)
	var code, secret string
	var remote bool
	if err := p.db.QueryRow(`SELECT code_hash, secret_hash, remote FROM device_pairings`).Scan(&code, &secret, &remote); err != nil {
		t.Fatal(err)
	}
	if code != TokenHash(st.Code) || secret != TokenHash(st.Secret) || !remote {
		t.Fatalf("stored %q %q %v", code, secret, remote)
	}
}

func TestNormalizePairingCode(t *testing.T) {
	for in, want := range map[string]string{
		"abc-def": "ABCDEF", " AB CD EF ": "ABCDEF", "ABCDE": "", "ABCDEFG": "", "ABCDE0": "", "ABCDEI": "", "": "",
	} {
		if got := NormalizePairingCode(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
