package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Personal API keys («Ключи API и ИИ»): a profile's long-lived keys for
// scripts and AI assistants, sent as Authorization: Bearer on the /api
// routes and on the MCP server (/mcp). A key carries a scope that caps the
// profile's rights: ScopeRead lets in reads only (GET, HEAD), ScopeAct
// reads and actions, ScopeAdmin an admin's own rights. Without ScopeAdmin
// the request is never an admin's, whoever made the key. Kids and managed
// accounts have no keys. Only the key's SHA-256 is stored.
const (
	ScopeRead  = "read"
	ScopeAct   = "act"
	ScopeAdmin = "admin"
)

// APIKeyPrefix starts every key, so the guard tells a key from a session
// token and secret scanners recognise one.
const APIKeyPrefix = "antresol_"

var (
	// ErrKidKey: a kid's profile asked for a key.
	ErrKidKey = errors.New("auth: kids have no api keys")
	// ErrAdminScope: a profile that is not an admin asked for ScopeAdmin.
	ErrAdminScope = errors.New("auth: admin scope for a non-admin")
	// ErrTooManyKeys: the profile has maxKeysPerProfile keys already.
	ErrTooManyKeys = errors.New("auth: too many api keys")
)

const (
	maxKeysPerProfile = 25
	maxKeyName        = 80
	// keyLogKeep: the rows of api_key_log kept per profile.
	keyLogKeep = 500
	// keyTouchEvery: how often last_used_at is written for one key.
	keyTouchEvery = time.Minute
	// the per-key rate: keyBurst requests at once, then keyRate a second
	keyBurst = 60
	keyRate  = 2.0
)

// APIKey is a key as the list shows it (never the key itself).
type APIKey struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"-"`
	Name         string     `json:"name"`
	Scope        string     `json:"scope"`
	Hint         string     `json:"hint"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt"`
	LastUsedFrom string     `json:"lastUsedFrom,omitempty"`
}

// APIKeyEvent is a row of the keys' log.
type APIKeyEvent struct {
	At      time.Time `json:"at"`
	KeyName string    `json:"keyName"`
	Method  string    `json:"method"`
	Path    string    `json:"path"`
	Status  int       `json:"status"`
	Via     string    `json:"via,omitempty"`
	Client  string    `json:"client,omitempty"`
}

// IsAPIKey reports whether a bearer token is a personal API key.
func IsAPIKey(tok string) bool { return strings.HasPrefix(tok, APIKeyPrefix) }

// APIKeys stores the keys, checks them, limits their rate and logs what
// they did.
type APIKeys struct {
	db  *sql.DB
	Now func() time.Time

	mu      sync.Mutex
	touched map[int64]time.Time
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func NewAPIKeys(db *sql.DB) *APIKeys {
	return &APIKeys{db: db, Now: time.Now, touched: map[int64]time.Time{}, buckets: map[int64]*bucket{}}
}

func (k *APIKeys) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// ValidScope reports whether s is one of the scopes.
func ValidScope(s string) bool { return s == ScopeRead || s == ScopeAct || s == ScopeAdmin }

// Create makes a key for u and returns it in the clear (the only time).
func (k *APIKeys) Create(ctx context.Context, u User, name, scope string) (string, APIKey, error) {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxKeyName {
		name = string(r[:maxKeyName])
	}
	if name == "" || !ValidScope(scope) {
		return "", APIKey{}, ErrInvalid
	}
	if u.IsKid || u.OwnerID != 0 {
		return "", APIKey{}, ErrKidKey
	}
	if scope == ScopeAdmin && !u.IsAdmin {
		return "", APIKey{}, ErrAdminScope
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", APIKey{}, err
	}
	key := APIKeyPrefix + hex.EncodeToString(b)
	now := k.now()
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return "", APIKey{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE user_id=?`, u.ID).Scan(&n); err != nil {
		return "", APIKey{}, err
	}
	if n >= maxKeysPerProfile {
		return "", APIKey{}, ErrTooManyKeys
	}
	hint := key[len(key)-4:]
	res, err := tx.ExecContext(ctx, `INSERT INTO api_keys(user_id, name, scope, hash, hint, created_at) VALUES(?,?,?,?,?,?)`,
		u.ID, name, scope, TokenHash(key), hint, now.Format(time.RFC3339))
	if err != nil {
		return "", APIKey{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", APIKey{}, err
	}
	return key, APIKey{ID: id, UserID: u.ID, Name: name, Scope: scope, Hint: hint, CreatedAt: now}, nil
}

const keyCols = `id, user_id, name, scope, hint, created_at, COALESCE(last_used_at, ''), last_used_from`

func scanKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var a APIKey
	var created, used string
	if err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Scope, &a.Hint, &created, &used, &a.LastUsedFrom); err != nil {
		return APIKey{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	if t, err := time.Parse(time.RFC3339, used); err == nil {
		a.LastUsedAt = &t
	}
	return a, nil
}

// List returns the profile's keys, newest first.
func (k *APIKeys) List(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := k.db.QueryContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE user_id=? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		a, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Revoke deletes one of the profile's keys (ErrNotFound: none such).
func (k *APIKeys) Revoke(ctx context.Context, userID, id int64) error {
	res, err := k.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	k.mu.Lock()
	delete(k.touched, id)
	delete(k.buckets, id)
	k.mu.Unlock()
	return nil
}

// Lookup returns the key's profile (current state) and the key;
// ErrNotFound for an unknown or revoked key, or one whose profile has
// become a kid's.
func (k *APIKeys) Lookup(ctx context.Context, key string) (User, APIKey, error) {
	if !IsAPIKey(key) {
		return User{}, APIKey{}, ErrNotFound
	}
	a, err := scanKey(k.db.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE hash=?`, TokenHash(key)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, APIKey{}, ErrNotFound
	}
	if err != nil {
		return User{}, APIKey{}, err
	}
	u, err := scan(k.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, a.UserID))
	if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return User{}, APIKey{}, ErrNotFound
	}
	if err != nil {
		return User{}, APIKey{}, err
	}
	if u.IsKid || u.OwnerID != 0 {
		return User{}, APIKey{}, ErrNotFound
	}
	return u, a, nil
}

// Touch records that the key was used from addr (at most once per
// keyTouchEvery).
func (k *APIKeys) Touch(ctx context.Context, id int64, addr string) {
	now := k.now()
	k.mu.Lock()
	last := k.touched[id]
	if now.Sub(last) < keyTouchEvery {
		k.mu.Unlock()
		return
	}
	k.touched[id] = now
	k.mu.Unlock()
	_, _ = k.db.ExecContext(context.WithoutCancel(ctx), `UPDATE api_keys SET last_used_at=?, last_used_from=? WHERE id=?`,
		now.Format(time.RFC3339), addr, id)
}

// Allow takes one request from the key's bucket; false: over the rate,
// with how long until the next one is let in.
func (k *APIKeys) Allow(id int64) (bool, time.Duration) {
	now := k.now()
	k.mu.Lock()
	defer k.mu.Unlock()
	b := k.buckets[id]
	if b == nil {
		b = &bucket{tokens: keyBurst, at: now}
		k.buckets[id] = b
	}
	b.tokens = min(keyBurst, b.tokens+now.Sub(b.at).Seconds()*keyRate)
	b.at = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / keyRate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// Log records a key's request and trims the profile's log.
func (k *APIKeys) Log(ctx context.Context, a APIKey, e APIKeyEvent) {
	ctx = context.WithoutCancel(ctx)
	if e.At.IsZero() {
		e.At = k.now()
	}
	if len(e.Path) > 300 {
		e.Path = e.Path[:300]
	}
	if len(e.Client) > 120 {
		e.Client = e.Client[:120]
	}
	_, err := k.db.ExecContext(ctx, `INSERT INTO api_key_log(user_id, key_id, key_name, at, method, path, status, via, client) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.UserID, a.ID, a.Name, e.At.UTC().Format(time.RFC3339), e.Method, e.Path, e.Status, e.Via, e.Client)
	if err != nil {
		return
	}
	_, _ = k.db.ExecContext(ctx, `DELETE FROM api_key_log WHERE user_id=? AND id <= (SELECT id FROM api_key_log WHERE user_id=? ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		a.UserID, a.UserID, keyLogKeep)
}

// Events returns the profile's latest log rows, newest first.
func (k *APIKeys) Events(ctx context.Context, userID int64, limit int) ([]APIKeyEvent, error) {
	if limit <= 0 || limit > keyLogKeep {
		limit = 50
	}
	rows, err := k.db.QueryContext(ctx, `SELECT at, key_name, method, path, status, via, client FROM api_key_log WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyEvent{}
	for rows.Next() {
		var e APIKeyEvent
		var at string
		if err := rows.Scan(&at, &e.KeyName, &e.Method, &e.Path, &e.Status, &e.Via, &e.Client); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

type apiKeyCtx struct{}
type internalCallCtx struct{}

// APIKeyFrom returns the key a request was let in with.
func APIKeyFrom(ctx context.Context) (APIKey, bool) {
	a, ok := ctx.Value(apiKeyCtx{}).(APIKey)
	return a, ok
}

// WithInternalCall marks ctx as the server's own call on behalf of a key's
// request already let in (the MCP server calling the API for a tool): the
// guard checks the key and its scope again but does not count it against
// the rate. via names the tool, for the log.
func WithInternalCall(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, internalCallCtx{}, via)
}

func internalCall(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(internalCallCtx{}).(string)
	return v, ok
}

// MCPPath is where the MCP server listens: every scope may POST there (its
// tools check the scope on the routes they call).
const MCPPath = "/mcp"

// keyForbidden: what no API key may do, whatever its scope: manage
// credentials (other keys, app passwords, sign-ins and devices) or
// profiles. A key is for using the library, not for minting access.
func keyForbidden(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/me/api-keys") || strings.HasPrefix(p, "/api/auth/") || strings.Contains(p, "/app-password") {
		return true
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if write && strings.HasPrefix(p, "/api/me/devices") {
		return true
	}
	if write && (p == "/api/users" || (strings.HasPrefix(p, "/api/users/") && !strings.Contains(strings.TrimPrefix(p, "/api/users/"), "/"))) {
		return true
	}
	if write && strings.HasPrefix(p, "/api/me/managed") {
		return true
	}
	return false
}

// statusWriter keeps the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// serveKey is Require for a request with an API key.
func (g *Guard) serveKey(w http.ResponseWriter, r *http.Request, key string, next http.Handler) {
	if g.APIKeys == nil {
		jsonErr(w, r, http.StatusUnauthorized, "Нужно войти")
		return
	}
	ctx := r.Context()
	u, a, err := g.APIKeys.Lookup(ctx, key)
	if errors.Is(err, ErrNotFound) {
		jsonErr(w, r, http.StatusUnauthorized, "Ключ API не найден или отозван")
		return
	}
	if err != nil {
		jsonErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	if !u.HasPassword && !g.Network.IsTrusted(r) {
		w.Header().Set("X-Vault-Error", "remote-passwordless")
		jsonErr(w, r, http.StatusForbidden, MsgRemotePasswordless)
		return
	}
	via, internal := internalCall(ctx)
	client, _ := g.Network.ClientIP(r)
	addr := ""
	if client.IsValid() {
		addr = client.String()
	}
	if !internal {
		if ok, wait := g.APIKeys.Allow(a.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			jsonErr(w, r, http.StatusTooManyRequests, "Слишком много запросов с этим ключом, попробуйте чуть позже")
			return
		}
		g.APIKeys.Touch(ctx, a.ID, addr)
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	ev := APIKeyEvent{Method: r.Method, Path: r.URL.Path, Via: via, Client: r.UserAgent()}
	if internal {
		ev.Client = ""
	}
	if keyForbidden(r) {
		ev.Status = http.StatusForbidden
		g.APIKeys.Log(ctx, a, ev)
		jsonErr(w, r, http.StatusForbidden, "Ключом API этого сделать нельзя")
		return
	}
	if !read && a.Scope == ScopeRead && r.URL.Path != MCPPath {
		ev.Status = http.StatusForbidden
		g.APIKeys.Log(ctx, a, ev)
		jsonErr(w, r, http.StatusForbidden, "Этот ключ API только для чтения")
		return
	}
	// the scope caps the profile's rights: only an admin's admin key is an admin's
	u.IsAdmin = u.IsAdmin && a.Scope == ScopeAdmin
	ctx = WithUser(ctx, u)
	ctx = context.WithValue(ctx, apiKeyCtx{}, a)
	ctx = context.WithValue(ctx, tokenKey{}, "")
	if read || r.URL.Path == MCPPath {
		next.ServeHTTP(w, r.WithContext(ctx))
		return
	}
	sw := &statusWriter{ResponseWriter: w}
	next.ServeHTTP(sw, r.WithContext(ctx))
	ev.Status = sw.code
	if ev.Status == 0 {
		ev.Status = http.StatusOK
	}
	g.APIKeys.Log(ctx, a, ev)
}
