// Package accounts stores external-account links per profile (OAuth tokens
// encrypted at rest), the remote list twins and the outbox of pending
// pushes.
package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Link is a profile's connection to an external provider.
type Link struct {
	UserID                               int64
	Provider                             string
	ExternalID                           int64
	Nickname                             string
	ExpiresAt                            time.Time
	Scope, WriteState, Status, LastError string
	LastSync, LinkedAt                   time.Time
	// FirstPullDone: a pull has imported the remote list (first-link rule ran).
	FirstPullDone bool
	// Unmapped: remote entries the last pull could not map to a title.
	Unmapped int
}

// Tokens are the decrypted OAuth tokens of a link.
type Tokens struct {
	Access, Refresh string
	Expires         time.Time
}

// Rate is one remote list entry (target) of a title; a title may have
// several (cours mapped to TMDB seasons, see TMDBSeason/TMDBOffset).
type Rate struct {
	UserID                 int64
	Provider               string
	TargetID               int64
	TitleID                int64
	TMDBSeason, TMDBOffset int
	RateID                 int64
	AnimeEpisodes          int    // the remote title's episodes, 0 = unknown
	AnimeStatus            string // the remote title's airing status (anons | ongoing | released), "" = unknown
	RemoteStatus           string
	// RemoteEpisodes: the entry's episode count as of the last pull or our
	// last write of it; LocalEpisodes: Antresol's count in the cour's window
	// when we last wrote an episode count to it.
	RemoteEpisodes, LocalEpisodes int
	RemoteUpdatedAt, PushedAt     time.Time
	// MarkAt: the title's mark time as of its last pull win or push (the
	// same on every cour of the title; zero = never synced)
	MarkAt time.Time
}

// OutboxItem is a pending push of one title.
type OutboxItem struct {
	ID, UserID, TitleID int64
	Provider, Op        string
	CreatedAt           time.Time
	Attempts            int
	LastError           string
	NextAt              time.Time // a failed item waits until then
	Seq                 int64     // bumped by every Enqueue of the title
}

// Store persists links; Key is the 32-byte token encryption key.
type Store struct {
	DB  *sql.DB
	Key []byte
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

const linkCols = `user_id, provider, external_id, nickname, expires_at, scope, write_state, status, last_error, last_sync_at, linked_at, first_pull_done, unmapped`

func scanLink(r interface{ Scan(...any) error }) (Link, error) {
	var l Link
	var exp, sync, linked int64
	err := r.Scan(&l.UserID, &l.Provider, &l.ExternalID, &l.Nickname, &exp, &l.Scope, &l.WriteState, &l.Status, &l.LastError, &sync, &linked, &l.FirstPullDone, &l.Unmapped)
	l.ExpiresAt, l.LastSync, l.LinkedAt = fromUnix(exp), fromUnix(sync), fromUnix(linked)
	return l, err
}

// Link returns the profile's link to provider.
func (s *Store) Link(ctx context.Context, uid int64, provider string) (Link, bool, error) {
	l, err := scanLink(s.DB.QueryRowContext(ctx,
		`SELECT `+linkCols+` FROM account_links WHERE user_id=? AND provider=?`, uid, provider))
	if err == sql.ErrNoRows {
		return Link{}, false, nil
	}
	return l, err == nil, err
}

// Links returns every profile's link to provider.
func (s *Store) Links(ctx context.Context, provider string) ([]Link, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+linkCols+` FROM account_links WHERE provider=? ORDER BY user_id`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetNickname creates the link when missing and records the remote
// account's nickname and id.
func (s *Store) SetNickname(ctx context.Context, uid int64, provider, nick string, externalID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO account_links(user_id, provider, nickname, external_id, linked_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(user_id, provider) DO UPDATE SET nickname=excluded.nickname, external_id=excluded.external_id`,
		uid, provider, nick, externalID, time.Now().Unix())
	return err
}

func aad(uid int64, provider, field string) string {
	return strconv.FormatInt(uid, 10) + "|" + provider + "|" + field
}

// SetTokens stores the tokens encrypted, creating the link when missing.
// An empty Access is an error (a partial Tokens must not wipe a stored
// token); an empty Refresh is allowed and stored as NULL.
func (s *Store) SetTokens(ctx context.Context, uid int64, provider string, t Tokens) error {
	if t.Access == "" {
		return errors.New("accounts: empty access token")
	}
	a, err := seal(s.Key, t.Access, aad(uid, provider, "access"))
	if err != nil {
		return err
	}
	r, err := seal(s.Key, t.Refresh, aad(uid, provider, "refresh"))
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO account_links(user_id, provider, access_enc, refresh_enc, expires_at, linked_at) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(user_id, provider) DO UPDATE SET access_enc=excluded.access_enc, refresh_enc=excluded.refresh_enc, expires_at=excluded.expires_at`,
		uid, provider, a, r, unix(t.Expires), time.Now().Unix())
	return err
}

// Tokens returns the decrypted tokens; a wrong key is an error.
func (s *Store) Tokens(ctx context.Context, uid int64, provider string) (Tokens, bool, error) {
	var a, r []byte
	var exp int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT access_enc, refresh_enc, expires_at FROM account_links WHERE user_id=? AND provider=?`,
		uid, provider).Scan(&a, &r, &exp)
	if err == sql.ErrNoRows {
		return Tokens{}, false, nil
	}
	if err != nil {
		return Tokens{}, false, err
	}
	var t Tokens
	if t.Access, err = open(s.Key, a, aad(uid, provider, "access")); err != nil {
		return Tokens{}, false, err
	}
	if t.Refresh, err = open(s.Key, r, aad(uid, provider, "refresh")); err != nil {
		return Tokens{}, false, err
	}
	t.Expires = fromUnix(exp)
	return t, true, nil
}

// ClearTokens forgets the link's OAuth tokens (the link itself stays).
func (s *Store) ClearTokens(ctx context.Context, uid int64, provider string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_links SET access_enc=NULL, refresh_enc=NULL, expires_at=0 WHERE user_id=? AND provider=?`, uid, provider)
	return err
}

// SetWriteState records the profile's write-back choice.
func (s *Store) SetWriteState(ctx context.Context, uid int64, provider, state string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_links SET write_state=? WHERE user_id=? AND provider=?`, state, uid, provider)
	return err
}

// SetFirstPullDone records that the first-link import ran.
func (s *Store) SetFirstPullDone(ctx context.Context, uid int64, provider string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_links SET first_pull_done=1 WHERE user_id=? AND provider=?`, uid, provider)
	return err
}

// SetUnmapped records how many remote entries the last pull could not map.
func (s *Store) SetUnmapped(ctx context.Context, uid int64, provider string, n int) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_links SET unmapped=? WHERE user_id=? AND provider=?`, n, uid, provider)
	return err
}

// SetStatus records the sync outcome.
func (s *Store) SetStatus(ctx context.Context, uid int64, provider, status, lastErr string, syncedAt time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_links SET status=?, last_error=?, last_sync_at=? WHERE user_id=? AND provider=?`,
		status, lastErr, unix(syncedAt), uid, provider)
	return err
}

// Unlink removes the link with its rates and pending outbox.
func (s *Store) Unlink(ctx context.Context, uid int64, provider string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM account_outbox WHERE user_id=? AND provider=?`,
		`DELETE FROM account_rates WHERE user_id=? AND provider=?`,
		`DELETE FROM account_links WHERE user_id=? AND provider=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, uid, provider); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rate returns the remote entry of target (the provider's title id).
func (s *Store) Rate(ctx context.Context, uid int64, provider string, targetID int64) (Rate, bool, error) {
	rs, err := s.rates(ctx, `AND target_id=?`, uid, provider, targetID)
	if err != nil || len(rs) == 0 {
		return Rate{}, false, err
	}
	return rs[0], true, nil
}

// TitleRates lists a title's remote entries (its cours), in season and
// offset order.
func (s *Store) TitleRates(ctx context.Context, uid int64, provider string, titleID int64) ([]Rate, error) {
	return s.rates(ctx, `AND title_id=?`, uid, provider, titleID)
}

// Rates returns all remote entries of a profile at provider.
func (s *Store) Rates(ctx context.Context, uid int64, provider string) ([]Rate, error) {
	return s.rates(ctx, ``, uid, provider)
}

func (s *Store) rates(ctx context.Context, extra string, args ...any) ([]Rate, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT user_id, provider, target_id, title_id, tmdb_season, tmdb_offset, rate_id, anime_episodes, anime_status, remote_status, remote_episodes, local_episodes, remote_updated_at, pushed_at, mark_at
		 FROM account_rates WHERE user_id=? AND provider=? `+extra+` ORDER BY title_id, tmdb_season, tmdb_offset, target_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rate
	for rows.Next() {
		var r Rate
		var ru, pu, ma int64
		if err := rows.Scan(&r.UserID, &r.Provider, &r.TargetID, &r.TitleID, &r.TMDBSeason, &r.TMDBOffset, &r.RateID, &r.AnimeEpisodes, &r.AnimeStatus, &r.RemoteStatus, &r.RemoteEpisodes, &r.LocalEpisodes, &ru, &pu, &ma); err != nil {
			return nil, err
		}
		r.RemoteUpdatedAt, r.PushedAt, r.MarkAt = fromUnix(ru), fromUnix(pu), fromUnix(ma)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRate stores or replaces a remote entry (keyed by its target).
func (s *Store) SetRate(ctx context.Context, r Rate) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO account_rates(user_id, provider, target_id, title_id, tmdb_season, tmdb_offset, rate_id, anime_episodes, anime_status, remote_status, remote_episodes, local_episodes, remote_updated_at, pushed_at, mark_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(user_id, provider, target_id) DO UPDATE SET title_id=excluded.title_id,
		   tmdb_season=excluded.tmdb_season, tmdb_offset=excluded.tmdb_offset, rate_id=excluded.rate_id,
		   anime_episodes=excluded.anime_episodes, anime_status=excluded.anime_status, remote_status=excluded.remote_status,
		   remote_episodes=excluded.remote_episodes, local_episodes=excluded.local_episodes, remote_updated_at=excluded.remote_updated_at, pushed_at=excluded.pushed_at,
		   mark_at=excluded.mark_at`,
		r.UserID, r.Provider, r.TargetID, r.TitleID, r.TMDBSeason, r.TMDBOffset, r.RateID, r.AnimeEpisodes, r.AnimeStatus, r.RemoteStatus,
		r.RemoteEpisodes, r.LocalEpisodes, unix(r.RemoteUpdatedAt), unix(r.PushedAt), unix(r.MarkAt))
	return err
}

// SetTitleMarkAt records the mark time a pull win or push synced on every
// cour of a title.
func (s *Store) SetTitleMarkAt(ctx context.Context, uid int64, provider string, titleID int64, at time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_rates SET mark_at=? WHERE user_id=? AND provider=? AND title_id=?`, unix(at), uid, provider, titleID)
	return err
}

// DeleteRate forgets a remote entry.
func (s *Store) DeleteRate(ctx context.Context, uid int64, provider string, targetID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM account_rates WHERE user_id=? AND provider=? AND target_id=?`, uid, provider, targetID)
	return err
}

// Enqueue records the latest intent for a title (op: upsert | delete),
// replacing any pending one and resetting its retry state.
func (s *Store) Enqueue(ctx context.Context, uid int64, provider string, titleID int64, op string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO account_outbox(user_id, provider, title_id, op, created_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(user_id, provider, title_id) DO UPDATE SET op=excluded.op, created_at=excluded.created_at,
		   attempts=0, last_error='', next_at=0, seq=account_outbox.seq+1`,
		uid, provider, titleID, op, now.Unix())
	return err
}

// Outbox lists pending pushes, oldest first.
func (s *Store) Outbox(ctx context.Context, uid int64, provider string) ([]OutboxItem, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, provider, title_id, op, created_at, attempts, last_error, next_at, seq
		 FROM account_outbox WHERE user_id=? AND provider=? ORDER BY created_at, id`, uid, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var c, next int64
		if err := rows.Scan(&it.ID, &it.UserID, &it.Provider, &it.TitleID, &it.Op, &c, &it.Attempts, &it.LastError, &next, &it.Seq); err != nil {
			return nil, err
		}
		it.CreatedAt, it.NextAt = fromUnix(c), fromUnix(next)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Done removes a pushed item.
func (s *Store) Done(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM account_outbox WHERE id=?`, id)
	return err
}

// DoneAt removes a pushed item unless it was queued again (a newer
// intent: seq changed) while it was being pushed.
func (s *Store) DoneAt(ctx context.Context, id, seq int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM account_outbox WHERE id=? AND seq=?`, id, seq)
	return err
}

// Failed records a failed attempt; the item waits until retryAt.
func (s *Store) Failed(ctx context.Context, id int64, errMsg string, retryAt time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE account_outbox SET attempts=attempts+1, last_error=?, next_at=? WHERE id=?`, errMsg, unix(retryAt), id)
	return err
}

// ClearOutbox drops every pending push of the profile at provider.
func (s *Store) ClearOutbox(ctx context.Context, uid int64, provider string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM account_outbox WHERE user_id=? AND provider=?`, uid, provider)
	return err
}

// ClientSettings returns the provider's OAuth client id and decrypted
// secret; ok is false when none are stored.
func (s *Store) ClientSettings(ctx context.Context, provider string) (clientID, secret string, ok bool, err error) {
	var enc []byte
	err = s.DB.QueryRowContext(ctx,
		`SELECT client_id, secret_enc FROM account_settings WHERE provider=?`, provider).Scan(&clientID, &enc)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if secret, err = open(s.Key, enc, aad(0, provider, "secret")); err != nil {
		return "", "", false, err
	}
	return clientID, secret, true, nil
}

// SetClientSettings stores the provider's OAuth client id and secret
// (encrypted).
func (s *Store) SetClientSettings(ctx context.Context, provider, clientID, secret string) error {
	enc, err := seal(s.Key, secret, aad(0, provider, "secret"))
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO account_settings(provider, client_id, secret_enc, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(provider) DO UPDATE SET client_id=excluded.client_id, secret_enc=excluded.secret_enc, updated_at=excluded.updated_at`,
		provider, clientID, enc, time.Now().Unix())
	return err
}
