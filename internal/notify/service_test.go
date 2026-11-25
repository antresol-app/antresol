package notify

import "testing"

// A service profile gets no letters, whatever an address and topics were
// set on it (here straight in the table, as the topics are set with
// rights it has not).
func TestServiceProfileIsNeverNotified(t *testing.T) {
	f := newFixture(t)
	admin := f.user(t, "Аня", true, false)
	svc, _, err := f.users.CreateService(f.ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	f.subscribe(t, admin, "anya@example.com", Prefs{Episodes: true, Available: true, Requests: true})
	f.subscribe(t, svc, "machine@example.com", Prefs{})
	if _, err := f.db.Exec(`UPDATE notify_prefs SET episodes=1, available=1, requests=1 WHERE user_id=?`, svc.ID); err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{Episodes, Available, Requests} {
		got, err := f.svc.wanting(f.ctx, topic)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got[svc.ID]; ok {
			t.Errorf("%s: the service profile is wanting a letter", topic)
		}
		if _, ok := got[admin.ID]; !ok && topic != Requests {
			t.Errorf("%s: the person is not wanting one (%v)", topic, got)
		}
	}
}
