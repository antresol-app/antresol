package auth

import (
	"context"
	"time"
)

// ProfileDevice is an app or a browser of a profile, as an admin's device
// list shows it.
type ProfileDevice struct {
	UserID   int64
	Profile  string // the profile's name
	DeviceID string // an app's; "" for a browser
	Name     string
	Model    string
	LastSeen time.Time // zero when unreadable
}

// readSeen is a stored last_seen (RFC 3339): zero when it does not read.
func readSeen(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// NativeDevices lists every profile's native apps by device id (a profile's
// device once, its latest session), the most recently seen first.
func (s *Sessions) NativeDevices(ctx context.Context) ([]ProfileDevice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.user_id, u.name, s.device_id, s.device_name, s.device_model, s.last_seen
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.kind=? AND s.device_id<>'' ORDER BY s.last_seen DESC`, KindNative)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileDevice
	seen := map[[2]any]bool{}
	for rows.Next() {
		var d ProfileDevice
		var seenAt string
		if err := rows.Scan(&d.UserID, &d.Profile, &d.DeviceID, &d.Name, &d.Model, &seenAt); err != nil {
			return nil, err
		}
		if k := [2]any{d.UserID, d.DeviceID}; !seen[k] {
			seen[k] = true
			d.LastSeen = readSeen(seenAt)
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// RecentBrowsers lists the browsers of every profile seen since, one row
// per profile, browser (Name) and system (Model), the most recently seen
// first.
func (s *Sessions) RecentBrowsers(ctx context.Context, since time.Time) ([]ProfileDevice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.user_id, u.name, s.device_name, s.device_model, MAX(s.last_seen)
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.kind=? AND s.last_seen>=?
		GROUP BY s.user_id, s.device_name, s.device_model ORDER BY MAX(s.last_seen) DESC`, KindWeb, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileDevice
	for rows.Next() {
		var d ProfileDevice
		var seenAt string
		if err := rows.Scan(&d.UserID, &d.Profile, &d.Name, &d.Model, &seenAt); err != nil {
			return nil, err
		}
		d.LastSeen = readSeen(seenAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ProtoToken is an app token of one protocol, with its profile's name.
type ProtoToken struct {
	Token    string
	UserID   int64
	Profile  string
	Device   string // the name the device gave
	LastSeen time.Time
	// SetID is the channel set the token is assigned (app_tokens.set_id,
	// the external IPTV's); 0: none.
	SetID int64
}

// ByProto lists the tokens of proto, the most recently seen first.
func (t *AppTokens) ByProto(ctx context.Context, proto string) ([]ProtoToken, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT t.token, t.user_id, u.name, t.device, t.last_seen, COALESCE(t.set_id, 0)
		FROM app_tokens t JOIN users u ON u.id=t.user_id WHERE t.proto=? ORDER BY t.last_seen DESC`, proto)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProtoToken
	for rows.Next() {
		var k ProtoToken
		var seenAt string
		if err := rows.Scan(&k.Token, &k.UserID, &k.Profile, &k.Device, &seenAt, &k.SetID); err != nil {
			return nil, err
		}
		k.LastSeen = readSeen(seenAt)
		out = append(out, k)
	}
	return out, rows.Err()
}
