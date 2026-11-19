// Package extsvc holds «Внешние сервисы»: the public services the server
// asks about titles already in its library (Wikidata, the IMDb ratings
// dataset, LRCLIB, the intro and credits databases), each on or off.
//
// What decides a service, strongest first: the environment (its VAULT_*
// variable, set to "off", "on" or an address: the setting is locked), then
// the admin's choice in «Настройки сервера → Внешние сервисы» or the
// first-run wizard (stored in the settings table), then the service's
// default (Default). Switches applies a new choice at once: the clients
// built at startup ask Gate before every request, so nothing restarts.
package extsvc

import "sync"

// ID names a service in the settings and the API.
type ID string

const (
	// Wikidata: franchises merged across TMDB collections, Rotten Tomatoes
	// scores, the other names of books. Sends TMDB/IMDb ids and book titles.
	Wikidata ID = "wikidata"
	// IMDb: the IMDb ratings dataset, downloaded once a day (sends nothing
	// about the library; IMDb's terms: personal, non-commercial use).
	IMDb ID = "imdb"
	// Lyrics: LRCLIB, the words of the library's songs (sends the artist,
	// title, album and length of a track).
	Lyrics ID = "lyrics"
	// Segments: TheIntroDB and IntroDB, where intros and credits are (sends
	// the TMDB/IMDb id of a film or series with the season and episode).
	Segments ID = "segments"
)

// All are the services in the order the settings list them.
var All = []ID{Wikidata, IMDb, Lyrics, Segments}

// Known reports whether id names a service.
func Known(id ID) bool {
	for _, x := range All {
		if x == id {
			return true
		}
	}
	return false
}

// Default is whether a service is on when nobody chose (docs/server-editions.md,
// «External services»): Wikidata and the intro databases carry facts about
// titles (CC0, ids) and are on; the IMDb dataset (licensed for personal,
// non-commercial use) and song words (the lyrics' own copyright) are content
// whose terms the person running the server accepts, so they wait for a yes
// (the wizard offers them checked).
func Default(id ID) bool { return id == Wikidata || id == Segments }

// Choice is what the admin chose: a nil entry was never chosen.
type Choice map[ID]*bool

// Chosen reports whether any service was chosen.
func (c Choice) Chosen() bool {
	for _, v := range c {
		if v != nil {
			return true
		}
	}
	return false
}

// Switches is the state of every service now. The zero value is not
// usable: New makes one.
type Switches struct {
	mu     sync.RWMutex
	env    map[ID]bool // the environment's word, which wins
	choice Choice
}

// New returns the switches with env (the services the environment sets)
// and nothing chosen yet.
func New(env map[ID]bool) *Switches {
	s := &Switches{env: map[ID]bool{}, choice: Choice{}}
	for id, on := range env {
		s.env[id] = on
	}
	return s
}

// Apply takes a new choice (the stored one at startup, a saved one later).
func (s *Switches) Apply(c Choice) {
	cp := Choice{}
	for id, v := range c {
		if v != nil && Known(id) {
			b := *v
			cp[id] = &b
		}
	}
	s.mu.Lock()
	s.choice = cp
	s.mu.Unlock()
}

// On reports whether the service is on now. A nil Switches is the
// defaults.
func (s *Switches) On(id ID) bool {
	if s == nil {
		return Default(id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if on, ok := s.env[id]; ok {
		return on
	}
	if v := s.choice[id]; v != nil {
		return *v
	}
	return Default(id)
}

// Env reports the environment's word on the service (set: locked).
func (s *Switches) Env(id ID) (on, set bool) {
	if s == nil {
		return false, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	on, set = s.env[id]
	return on, set
}

// Gate is On(id) as a func, what a client asks before a request.
func (s *Switches) Gate(id ID) func() bool { return func() bool { return s.On(id) } }
