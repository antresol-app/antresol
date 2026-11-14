package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"
)

// Pairings sign a device without a keyboard (a TV) in by QR code, as Quick
// Connect does: the device starts a request and shows a short code (and a
// QR of a page with it), a profile that is already signed in elsewhere (the
// phone app, a browser) approves it, and the device, polling with a secret
// only it knows, then gets a native token of the approved profile, issued by
// Sessions.CreateDevice exactly like POST /api/auth/token.
//
// A code works once and for PairingTTL; only hashes of the code and the
// secret are stored. Approval only marks the row: the token is minted on
// the device's next poll, so no token is ever stored or sent anywhere but
// to the device.
type Pairings struct {
	db       *sql.DB
	sessions *Sessions
	Now      func() time.Time
}

// NewPairings keeps the requests in db and issues tokens through sessions.
func NewPairings(db *sql.DB, sessions *Sessions) *Pairings {
	return &Pairings{db: db, sessions: sessions, Now: time.Now}
}

const (
	// PairingTTL is how long a code can be approved.
	PairingTTL = 5 * time.Minute
	// pairingPickup is how long an approved device has to fetch its token
	// at least (its request may be about to expire when it is approved).
	pairingPickup = time.Minute
	// pairingKeep: dead rows are deleted this long after they expire.
	pairingKeep = time.Hour
	// PairingMaxPerClient caps the open requests of one client address;
	// PairingMaxOpen the open requests of the whole server.
	PairingMaxPerClient = 3
	PairingMaxOpen      = 100

	// PairingCodeLen characters of pairingAlphabet: 31^6 ≈ 8.9·10^8 codes.
	PairingCodeLen = 6
	// pairingAlphabet has no 0/O, 1/I/L: a code is read off a TV.
	pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// Pairing statuses.
const (
	PairPending  = "pending"
	PairApproved = "approved"
	PairDenied   = "denied"
	PairDone     = "done"
	PairExpired  = "expired" // reported, never stored
)

var (
	// ErrPairingBusy: too many open requests (for the client or the server).
	ErrPairingBusy = errors.New("auth: too many open pairing requests")
	// ErrPairingGone: the code is unknown, used or expired.
	ErrPairingGone = errors.New("auth: pairing code unknown, used or expired")
)

// PairingStart is what the device gets: the code it shows and the secret
// it polls with.
type PairingStart struct {
	Code      string
	Secret    string
	ExpiresAt time.Time
}

// PairingInfo describes an open request to the profile that approves it.
type PairingInfo struct {
	ID        int64
	Device    DeviceInfo
	Remote    bool // the device asked from outside the home network
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PairingPoll is the device's view of its request. Token and User are set
// once, for the poll that picks up an approved request (Status PairDone).
type PairingPoll struct {
	Status    string // PairPending, PairDone, PairDenied, PairExpired
	ExpiresAt time.Time
	Token     string
	User      User
}

func (p *Pairings) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// NormalizePairingCode is a typed or scanned code as stored: upper case,
// without spaces and dashes; "" when it cannot be a code.
func NormalizePairingCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(s)))
	if len(s) != PairingCodeLen {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune(pairingAlphabet, c) {
			return ""
		}
	}
	return s
}

func newPairingCode() (string, error) {
	b := make([]byte, PairingCodeLen)
	n := big.NewInt(int64(len(pairingAlphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b[i] = pairingAlphabet[k.Int64()]
	}
	return string(b), nil
}

// Start opens a request for device d from client (Network.ClientKey;
// remote: not from the home network). An open request of the same device
// id ends: a TV that starts again replaces its code. ErrPairingBusy when
// the client or the server has too many open requests.
func (p *Pairings) Start(ctx context.Context, d DeviceInfo, client string, remote bool) (PairingStart, error) {
	now := p.now()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return PairingStart{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_pairings WHERE expires_at < ?`, ts(now.Add(-pairingKeep))); err != nil {
		return PairingStart{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_pairings WHERE device_id=? AND status=?`, d.ID, PairPending); err != nil {
		return PairingStart{}, err
	}
	var mine, all int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(client=?),0) FROM device_pairings WHERE status=? AND expires_at > ?`,
		client, PairPending, ts(now)).Scan(&all, &mine); err != nil {
		return PairingStart{}, err
	}
	if mine >= PairingMaxPerClient || all >= PairingMaxOpen {
		return PairingStart{}, ErrPairingBusy
	}
	secret, err := newToken()
	if err != nil {
		return PairingStart{}, err
	}
	exp := now.Add(PairingTTL)
	for range 5 { // a code still held by a live row: draw again
		code, err := newPairingCode()
		if err != nil {
			return PairingStart{}, err
		}
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_pairings WHERE code_hash=?`, TokenHash(code)).Scan(&taken); err != nil {
			return PairingStart{}, err
		}
		if taken > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_pairings(code_hash, secret_hash, device_id, device_name, device_model, app_version, client, remote, status, created_at, expires_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			TokenHash(code), TokenHash(secret), d.ID, d.Name, d.Model, d.Version, client, remote, PairPending, ts(now), ts(exp)); err != nil {
			return PairingStart{}, err
		}
		if err := tx.Commit(); err != nil {
			return PairingStart{}, err
		}
		return PairingStart{Code: code, Secret: secret, ExpiresAt: exp}, nil
	}
	return PairingStart{}, errors.New("auth: no free pairing code")
}

// Lookup returns the open (pending, unexpired) request of code;
// ErrPairingGone for any other.
func (p *Pairings) Lookup(ctx context.Context, code string) (PairingInfo, error) {
	code = NormalizePairingCode(code)
	if code == "" {
		return PairingInfo{}, ErrPairingGone
	}
	var in PairingInfo
	var created, exp string
	err := p.db.QueryRowContext(ctx, `SELECT id, device_id, device_name, device_model, app_version, remote, created_at, expires_at
		FROM device_pairings WHERE code_hash=? AND status=? AND expires_at > ?`, TokenHash(code), PairPending, ts(p.now())).
		Scan(&in.ID, &in.Device.ID, &in.Device.Name, &in.Device.Model, &in.Device.Version, &in.Remote, &created, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingInfo{}, ErrPairingGone
	}
	if err != nil {
		return PairingInfo{}, err
	}
	in.CreatedAt, _ = time.Parse(time.RFC3339, created)
	in.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
	return in, nil
}

// Approve lets the request of code sign in as userID (approvedBy is the
// profile that approved it); ErrPairingGone when it is not open. The
// policy (who may approve for whom) is the caller's.
func (p *Pairings) Approve(ctx context.Context, code string, userID, approvedBy int64) error {
	return p.decide(ctx, code, PairApproved, userID, approvedBy)
}

// Deny ends the request of code; ErrPairingGone when it is not open.
func (p *Pairings) Deny(ctx context.Context, code string, deniedBy int64) error {
	return p.decide(ctx, code, PairDenied, 0, deniedBy)
}

func (p *Pairings) decide(ctx context.Context, code, status string, userID, by int64) error {
	code = NormalizePairingCode(code)
	if code == "" {
		return ErrPairingGone
	}
	now := p.now()
	var uid any
	if userID > 0 {
		uid = userID
	}
	// an approved device gets at least pairingPickup to fetch its token
	res, err := p.db.ExecContext(ctx, `UPDATE device_pairings SET status=?, user_id=?, approved_by=?, decided_at=?,
		expires_at = MAX(expires_at, ?) WHERE code_hash=? AND status=? AND expires_at > ?`,
		status, uid, by, ts(now), ts(now.Add(pairingPickup)), TokenHash(code), PairPending, ts(now))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPairingGone
	}
	return nil
}

// Poll is the device asking about its request by its secret. An approved
// request is picked up once: that poll mints the device token (Status
// PairDone with Token and User) and every later one reports PairExpired.
// An unknown secret is ErrPairingGone.
func (p *Pairings) Poll(ctx context.Context, secret string) (PairingPoll, error) {
	if secret == "" {
		return PairingPoll{}, ErrPairingGone
	}
	h := TokenHash(secret)
	var id int64
	var status, exp string
	var uid sql.NullInt64
	var d DeviceInfo
	err := p.db.QueryRowContext(ctx, `SELECT id, status, expires_at, user_id, device_id, device_name, device_model, app_version FROM device_pairings WHERE secret_hash=?`, h).
		Scan(&id, &status, &exp, &uid, &d.ID, &d.Name, &d.Model, &d.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingPoll{}, ErrPairingGone
	}
	if err != nil {
		return PairingPoll{}, err
	}
	expires, _ := time.Parse(time.RFC3339, exp)
	now := p.now()
	if !expires.After(now) || status == PairDone {
		return PairingPoll{Status: PairExpired, ExpiresAt: expires}, nil
	}
	switch status {
	case PairPending, PairDenied:
		return PairingPoll{Status: status, ExpiresAt: expires}, nil
	case PairApproved:
	default:
		return PairingPoll{Status: PairExpired, ExpiresAt: expires}, nil
	}
	if !uid.Valid {
		return PairingPoll{Status: PairExpired, ExpiresAt: expires}, nil
	}
	// claim it first: two polls at once must not both get a token
	res, err := p.db.ExecContext(ctx, `UPDATE device_pairings SET status=? WHERE id=? AND status=?`, PairDone, id, PairApproved)
	if err != nil {
		return PairingPoll{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return PairingPoll{Status: PairExpired, ExpiresAt: expires}, nil
	}
	u, err := scan(p.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, uid.Int64))
	if err == nil {
		var tok string
		if tok, err = p.sessions.CreateDevice(ctx, u.ID, d); err == nil {
			return PairingPoll{Status: PairDone, ExpiresAt: expires, Token: tok, User: u}, nil
		}
	}
	if errors.Is(err, ErrNotFound) { // the profile is gone
		return PairingPoll{Status: PairExpired, ExpiresAt: expires}, nil
	}
	// hand it back: the next poll tries again
	if _, rerr := p.db.ExecContext(ctx, `UPDATE device_pairings SET status=? WHERE id=? AND status=?`, PairApproved, id, PairDone); rerr != nil {
		return PairingPoll{}, errors.Join(err, rerr)
	}
	return PairingPoll{}, err
}
