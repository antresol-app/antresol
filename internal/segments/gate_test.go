package segments

import "testing"

// TestOnlineGate: «Внешние сервисы» off asks neither database.
func TestOnlineGate(t *testing.T) {
	on := false
	o := NewOnline("test")
	o.Gate = func() bool { return on }
	if o.Enabled(SourceTheIntroDB) || o.Enabled(SourceIntroDB) {
		t.Fatal("asked while off")
	}
	on = true
	if !o.Enabled(SourceTheIntroDB) || !o.Enabled(SourceIntroDB) {
		t.Fatal("not asked while on")
	}
}
