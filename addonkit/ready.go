package addonkit

import (
	"net/http"
	"sync/atomic"
)

// Ready is the addon's readiness for GET /health. Until Set, Health
// answers {"ok": true, "ready": false}: the server shows the addon as
// starting and keeps asking every 2 s, its proxy routes answer 503 and
// its hooks count as down; the core API already accepts its token.
// An addon that is ready at once may call Set before serving.
type Ready struct{ ready atomic.Bool }

// Set marks the addon ready.
func (r *Ready) Set() { r.ready.Store(true) }

// Health is the GET /health handler.
func (r *Ready) Health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true, "ready": r.ready.Load()})
}
