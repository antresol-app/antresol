package api

import (
	"context"
	"sync/atomic"
)

// RequestsSwitch is the admin's «Запросы детей» setting as the server reads
// it: Open is Deps.RequestsOpen. Kids' requests are the core's own feature,
// open unless the admin turned them off (the zero value is open); no addon
// is needed, and the server's catalog (a TMDB key) is what Deps.Requests
// decides. Apply, called with every settings load and save, sets it.
type RequestsSwitch struct{ off atomic.Bool }

// Apply takes «Запросы детей» from the settings in force.
func (s *RequestsSwitch) Apply(a AppSettings) { s.off.Store(!a.KidRequests) }

// Open says whether kids' requests are on.
func (s *RequestsSwitch) Open(context.Context) bool { return !s.off.Load() }
