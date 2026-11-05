package api

import (
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
)

// TestEventVisibilityCore: request.changed reaches the admins and the kid
// it names; every other event reaches everyone (both editions).
func TestEventVisibilityCore(t *testing.T) {
	admin := auth.User{ID: 1, IsAdmin: true}
	kid7 := auth.User{ID: 7, IsKid: true}
	kid8 := auth.User{ID: 8, IsKid: true}
	data := map[string]int64{"id": 1, "userId": 7}
	for _, c := range []struct {
		name string
		u    auth.User
		typ  string
		data any
		want bool
	}{
		{"admin", admin, events.RequestChanged, data, true},
		{"kid it names", kid7, events.RequestChanged, data, true},
		{"other kid", kid8, events.RequestChanged, data, false},
		{"other kid, other event", kid8, events.LibraryUpdated, nil, true},
	} {
		if got := eventVisible(c.u, c.typ, c.data); got != c.want {
			t.Errorf("%s: eventVisible = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestAdminEventsCore: the events every edition keeps from other profiles.
func TestAdminEventsCore(t *testing.T) {
	for _, typ := range []string{events.DiskWarning, events.MigrateProgress, events.ImportProgress} {
		if !adminEvents[typ] {
			t.Errorf("%s is not admin-only", typ)
		}
	}
	if adminEvents[events.LibraryUpdated] || adminEvents[events.RequestChanged] {
		t.Error("library.updated and request.changed must reach non-admins")
	}
}
