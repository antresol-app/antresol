package addons

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// Limits of addon inputs (external videos an addon serves over its socket).
const (
	InputTTL            = 6 * time.Hour // since the input was last used
	MaxInputsPerAddon   = 200           // the next one evicts the addon's least recently used
	MaxAudioSidecars    = 32
	MaxSubtitleSidecars = 64
	MaxInputMeta        = 4 << 10

	maxInputPath = 1024
	maxInputName = 512
	maxInputLang = 35 // a BCP 47 tag fits
)

// ErrInputStarting: the addon is still starting (503 «Расширение запускается»).
var ErrInputStarting = errors.New("addon is starting")

// ErrInputNotRunning: the addon is not running (503 «Расширение не запущено»).
var ErrInputNotRunning = errors.New("addon not running")

// Input is a registered external video: a main file plus sidecars, all
// routes on the socket of the process that registered it.
type Input struct {
	ID, AddonID, Socket string
	Req                 addonkit.InputRequest // read-only
	LastUsed            time.Time
}

// Part is relay part n: 0 the main file, 1…A the audio sidecars in request
// order, A+1…A+S the subtitle sidecars (size 0: unknown).
func (in Input) Part(n int) (path, name string, size int64, ok bool) {
	audio, subs := in.Req.Sidecars.Audio, in.Req.Sidecars.Subtitles
	switch {
	case n == 0:
		return in.Req.Path, in.Req.Name, in.Req.Size, true
	case n >= 1 && n <= len(audio):
		a := audio[n-1]
		return a.Path, a.Name, a.Size, true
	case n > len(audio) && n <= len(audio)+len(subs):
		s := subs[n-1-len(audio)]
		return s.Path, s.Name, 0, true
	}
	return "", "", 0, false
}

// Inputs is the in-memory registry of addon inputs. An input lives while
// it is used within InputTTL and the process that registered it is still
// serving: every spawn gets a new socket name, so a different socket from
// Serving means that process is gone. Expiry is lazy, no goroutine.
type Inputs struct {
	Serving func(id string) (Status, string) // *Manager.Serving
	Now     func() time.Time                 // nil → time.Now

	mu  sync.Mutex
	m   map[string]*inputEntry
	seq uint64 // use counter: LRU order without relying on clock resolution
}

type inputEntry struct {
	in   Input
	used uint64
}

func (s *Inputs) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func badInput(msg string) error { return userError(msg) }

// ValidateInput checks an addon's input request; refusals are user errors
// «Неверное описание видео: …».
func ValidateInput(req addonkit.InputRequest) error {
	if req.UserID <= 0 {
		return badInput("Неверное описание видео: неверный профиль")
	}
	if req.Size <= 0 {
		return badInput("Неверное описание видео: неверный размер")
	}
	if len(req.Sidecars.Audio) > MaxAudioSidecars {
		return badInput("Неверное описание видео: слишком много звуковых дорожек")
	}
	if len(req.Sidecars.Subtitles) > MaxSubtitleSidecars {
		return badInput("Неверное описание видео: слишком много субтитров")
	}
	names := make(map[string]bool, 1+len(req.Sidecars.Audio)+len(req.Sidecars.Subtitles))
	part := func(p, name string) error {
		if !validInputPath(p) {
			return badInput("Неверное описание видео: неверный путь")
		}
		if !validInputName(name) {
			return badInput("Неверное описание видео: неверное имя файла")
		}
		if names[name] {
			return badInput("Неверное описание видео: имя файла повторяется")
		}
		names[name] = true
		return nil
	}
	if err := part(req.Path, req.Name); err != nil {
		return err
	}
	for _, a := range req.Sidecars.Audio {
		if a.Size <= 0 {
			return badInput("Неверное описание видео: неверный размер")
		}
		if err := part(a.Path, a.Name); err != nil {
			return err
		}
	}
	for _, sub := range req.Sidecars.Subtitles {
		if err := part(sub.Path, sub.Name); err != nil {
			return err
		}
		if len(sub.Lang) > maxInputLang || hasControl(sub.Lang) {
			return badInput("Неверное описание видео: неверный язык субтитров")
		}
		if subtitles.Codecs[strings.ToLower(path.Ext(sub.Name))] == "" {
			return badInput("Неверное описание видео: неподдерживаемый формат субтитров")
		}
	}
	if len(req.Meta) > MaxInputMeta {
		return badInput("Неверное описание видео: слишком большие данные расширения")
	}
	if len(req.Meta) > 0 {
		m := bytes.TrimSpace(req.Meta)
		if len(m) == 0 || m[0] != '{' || !json.Valid(m) {
			return badInput("Неверное описание видео: данные расширения не JSON-объект")
		}
	}
	if t := req.Title; t != nil {
		if (t.Kind != "movie" && t.Kind != "series") || t.TMDBID <= 0 || t.Season < 0 || t.Episode < 0 {
			return badInput("Неверное описание видео: неверное название")
		}
	}
	return nil
}

// validInputPath: an absolute route on the addon's socket, written in Go's
// canonical escaping (u.RawPath empty: what url.URL.EscapedPath would give
// back for its decoded path), so the path the relay sends is the one
// checked here. That refuses any needless escape (%41, %2F) and any byte
// that must be escaped but is not: the path is ASCII, and non-ASCII, a
// space and the like are written as uppercase %XX; the query is printable
// ASCII without spaces too. No other host, no dot
// segments or backslashes, never /health or /hooks/*.
func validInputPath(p string) bool {
	if p == "" || p[0] != '/' || len(p) > maxInputPath || strings.ContainsAny(p, "\\#") {
		return false
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || !plainQuery(u.RawQuery) {
		return false
	}
	d := u.Path
	if d == "" || d[0] != '/' || d != path.Clean(d) || strings.Contains(d, "//") ||
		strings.Contains(d, "\\") || strings.ContainsRune(d, 0) {
		return false
	}
	for _, seg := range strings.Split(d, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return d != "/health" && d != "/hooks" && !strings.HasPrefix(d, "/hooks/")
}

// plainQuery: a query of printable ASCII without spaces (sent as it is on
// the request line).
func plainQuery(q string) bool {
	return !strings.ContainsFunc(q, func(r rune) bool { return r <= 0x20 || r >= 0x7f })
}

// validInputName: a clean slash-separated relative path without control
// characters.
func validInputName(n string) bool {
	if n == "" || len(n) > maxInputName || hasControl(n) || n[0] == '/' || n != path.Clean(n) {
		return false
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

// hasControl: an ASCII control character (below 0x20, or 0x7f) in s.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func newInputID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func cloneInputReq(r addonkit.InputRequest) addonkit.InputRequest {
	if r.Title != nil {
		t := *r.Title
		r.Title = &t
	}
	r.Sidecars.Audio = append([]addonkit.AudioSidecar(nil), r.Sidecars.Audio...)
	r.Sidecars.Subtitles = append([]addonkit.SubtitleSidecar(nil), r.Sidecars.Subtitles...)
	if r.Meta != nil {
		r.Meta = append(json.RawMessage(nil), r.Meta...)
	}
	return r
}

// Add registers an input of a running addon, bound to its current socket.
func (s *Inputs) Add(addonID string, req addonkit.InputRequest) (Input, error) {
	if err := ValidateInput(req); err != nil {
		return Input{}, err
	}
	st, sock := s.Serving(addonID)
	if st == StatusStarting {
		return Input{}, ErrInputStarting
	}
	if st != StatusRunning || sock == "" {
		return Input{}, ErrInputNotRunning
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*inputEntry{}
	}
	var id string
	for id == "" || s.m[id] != nil {
		var err error
		if id, err = newInputID(); err != nil {
			return Input{}, err
		}
	}
	// Sweep: expired entries of every addon, and this addon's entries of
	// an earlier process; then evict its least recently used over the limit.
	var lru *inputEntry
	own := 0
	for k, e := range s.m {
		if now.Sub(e.in.LastUsed) > InputTTL || (e.in.AddonID == addonID && e.in.Socket != sock) {
			delete(s.m, k)
			continue
		}
		if e.in.AddonID == addonID {
			own++
			if lru == nil || e.used < lru.used {
				lru = e
			}
		}
	}
	if own >= MaxInputsPerAddon && lru != nil {
		delete(s.m, lru.in.ID)
	}
	s.seq++
	e := &inputEntry{in: Input{ID: id, AddonID: addonID, Socket: sock, Req: cloneInputReq(req), LastUsed: now}, used: s.seq}
	s.m[id] = e
	out := e.in
	out.Req = cloneInputReq(out.Req)
	return out, nil
}

// Get is a live input (touching it), a deep copy the caller may change;
// false when it is unknown, expired or its process no longer serves (a
// dead entry is deleted).
func (s *Inputs) Get(id string) (Input, bool) {
	s.mu.Lock()
	e := s.m[id]
	s.mu.Unlock()
	if e == nil {
		return Input{}, false
	}
	// Serving is asked without our lock held.
	st, sock := s.Serving(e.in.AddonID)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[id] != e {
		return Input{}, false
	}
	if now.Sub(e.in.LastUsed) > InputTTL || st != StatusRunning || sock != e.in.Socket {
		delete(s.m, id)
		return Input{}, false
	}
	e.in.LastUsed = now
	s.seq++
	e.used = s.seq
	out := e.in
	out.Req = cloneInputReq(out.Req)
	return out, true
}

// Drop removes one of addonID's own inputs.
func (s *Inputs) Drop(addonID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[id]
	if e == nil || e.in.AddonID != addonID {
		return false
	}
	delete(s.m, id)
	return true
}
