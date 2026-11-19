package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
)

// «Франшизы»: the library's titles of one franchise (Star Wars, Harry
// Potter…) in the franchise's order, the content of a generated franchise
// channel. Worked out in the background (franchise_pass.go) from TMDB
// collections and Wikidata's franchise items, put in story order by the
// AI helper when there is a model; an admin's order
// (FranchiseManual) is kept across recomputes.

// Sources of a franchise.
const (
	FranchiseTMDB     = "tmdb"     // a TMDB collection, release order
	FranchiseWikidata = "wikidata" // titles sharing a Wikidata franchise or universe (and collections), release order
	FranchiseAI       = "ai"       // the evidence's titles in the model's story order
)

// Franchise is a row of franchises.
type Franchise struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Source string `json:"source"`
	// Items: the computed order (library item ids).
	Items []int64 `json:"items"`
	// Manual: the admin's order (nil: none).
	Manual    *FranchiseManual `json:"manual,omitempty"`
	InputHash string           `json:"-"`
	// Error: why the model's order is not used (its last answer).
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FranchiseManual is an admin's order of a franchise: the titles in this
// order, the hidden ones left out.
type FranchiseManual struct {
	Order  []int64 `json:"order"`
	Hidden []int64 `json:"hidden,omitempty"`
}

// Effective is the order a franchise plays: the admin's order merged with
// the computed one (MergeOrder), the hidden titles left out.
func (f Franchise) Effective() []int64 {
	if f.Manual == nil {
		return slices.Clone(f.Items)
	}
	out := MergeOrder(f.Manual.Order, f.Items)
	return slices.DeleteFunc(out, func(id int64) bool { return slices.Contains(f.Manual.Hidden, id) })
}

// MergeOrder keeps the ids of manual that are in computed, in manual's
// order, and puts every other id of computed right after its predecessor
// in computed (the first one first): a title a recompute adds lands where
// it belongs, the admin's order stays.
func MergeOrder(manual, computed []int64) []int64 {
	in := map[int64]bool{}
	for _, id := range computed {
		in[id] = true
	}
	var out []int64
	for _, id := range manual {
		if in[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return placeMissing(out, computed)
}

// placeMissing puts every id of computed that order lacks right after its
// predecessor in computed (the first one first).
func placeMissing(order, computed []int64) []int64 {
	out := slices.Clone(order)
	for i, id := range computed {
		if slices.Contains(out, id) {
			continue
		}
		at := 0
		if i > 0 {
			at = slices.Index(out, computed[i-1]) + 1 // the predecessor is placed by now
		}
		out = slices.Insert(out, at, id)
	}
	return out
}

// ErrFranchiseOrder: an order with a title that is not the franchise's.
var ErrFranchiseOrder = errors.New("channels: not a title of the franchise")

const franchiseCols = `key, name, source, items_json, manual_json, input_hash, error, updated_at`

func scanFranchise(r rowScanner) (Franchise, error) {
	var f Franchise
	var items, manual string
	var at int64
	if err := r.Scan(&f.Key, &f.Name, &f.Source, &items, &manual, &f.InputHash, &f.Error, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, ErrNotFound
		}
		return f, err
	}
	_ = json.Unmarshal([]byte(items), &f.Items)
	if manual != "" {
		var m FranchiseManual
		if json.Unmarshal([]byte(manual), &m) == nil {
			f.Manual = &m
		}
	}
	f.UpdatedAt = time.Unix(at, 0)
	return f, nil
}

// franchiseQuerier is a *sql.DB or a *sql.Tx.
type franchiseQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func readFranchises(ctx context.Context, q franchiseQuerier) ([]Franchise, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+franchiseCols+` FROM franchises ORDER BY name, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Franchise
	for rows.Next() {
		f, err := scanFranchise(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Franchises returns every franchise, by name. One with no titles is an
// admin's order kept for titles that may come back: it plays nothing.
func (s *Store) Franchises(ctx context.Context) ([]Franchise, error) {
	return readFranchises(ctx, s.DB)
}

// Franchise returns one franchise.
func (s *Store) Franchise(ctx context.Context, key string) (Franchise, error) {
	return scanFranchise(s.DB.QueryRowContext(ctx, `SELECT `+franchiseCols+` FROM franchises WHERE key=?`, key))
}

// SetFranchiseOrder sets (nil: drops) the admin's order of a franchise;
// every id must be one of its computed titles.
func (s *Store) SetFranchiseOrder(ctx context.Context, key string, m *FranchiseManual) (Franchise, error) {
	f, err := s.Franchise(ctx, key)
	if err != nil {
		return f, err
	}
	raw := ""
	if m != nil {
		clean := FranchiseManual{}
		for _, id := range m.Order {
			if !slices.Contains(f.Items, id) {
				return f, ErrFranchiseOrder
			}
			if !slices.Contains(clean.Order, id) {
				clean.Order = append(clean.Order, id)
			}
		}
		for _, id := range m.Hidden {
			if !slices.Contains(f.Items, id) {
				return f, ErrFranchiseOrder
			}
			if !slices.Contains(clean.Hidden, id) {
				clean.Hidden = append(clean.Hidden, id)
			}
		}
		b, _ := json.Marshal(clean)
		raw, m = string(b), &clean
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE franchises SET manual_json=? WHERE key=?`, raw, key); err != nil {
		return f, err
	}
	f.Manual = m
	return f, nil
}

// franchiseAsk is what the model's failures on a cluster of collections
// cost: it is not asked again before RetryAt, whatever else changed in the
// library (a success clears it).
type franchiseAsk struct {
	Model    string
	Failures int
	RetryAt  int64
	Error    string
}

// Retry backoff of a failed ask: 30 min, doubling, a day at most.
const (
	franchiseRetryBase = 30 * time.Minute
	franchiseRetryMax  = 24 * time.Hour
)

func franchiseBackoff(failures int) time.Duration {
	return min(franchiseRetryBase<<min(max(failures-1, 0), 10), franchiseRetryMax)
}

func (s *Store) franchiseAsks(ctx context.Context) map[string]franchiseAsk {
	out := map[string]franchiseAsk{}
	rows, err := s.DB.QueryContext(ctx, `SELECT key, model, failures, retry_at, error FROM franchise_asks`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var a franchiseAsk
		if rows.Scan(&k, &a.Model, &a.Failures, &a.RetryAt, &a.Error) == nil {
			out[k] = a
		}
	}
	return out
}

// reconcileFranchises says what a pass does with the stored rows it did not
// produce. A franchise changes its key when its collections merge into a
// cluster or a smaller collection id joins it, so a row whose titles (at
// least half of them) are now another franchise's is that franchise under
// its old key: its admin's order moves to the new one (adopt: new key →
// order; when that one has an order of its own, the old one is only
// shelved) and it goes (drop). Any other row goes unless it has an admin's
// order, which is shelved: kept without titles (plays nothing) for titles
// that may come back. An incomplete pass drops and shelves nothing but what
// is superseded: what TMDB did not answer for stays as it was.
func reconcileFranchises(old, list []Franchise, complete bool) (adopt map[string]*FranchiseManual, drop, shelve []string) {
	adopt = map[string]*FranchiseManual{}
	byKey := map[string]bool{}
	hasOrder := map[string]bool{}
	for _, f := range list {
		byKey[f.Key] = true
	}
	for _, o := range old {
		if byKey[o.Key] && o.Manual != nil {
			hasOrder[o.Key] = true
		}
	}
	rest := slices.DeleteFunc(slices.Clone(old), func(o Franchise) bool { return byKey[o.Key] })
	slices.SortFunc(rest, func(a, b Franchise) int { return strings.Compare(a.Key, b.Key) })
	for _, o := range rest {
		titles := o.Items
		if len(titles) == 0 && o.Manual != nil {
			titles = o.Manual.Order // shelved: the order remembers what it was of
		}
		var best *Franchise
		bestN := 0
		for i := range list {
			n := 0
			for _, id := range titles {
				if slices.Contains(list[i].Items, id) {
					n++
				}
			}
			if n > bestN || (n == bestN && n > 0 && list[i].Key < best.Key) {
				best, bestN = &list[i], n
			}
		}
		switch {
		case best != nil && bestN*2 >= len(titles):
			var m *FranchiseManual
			if o.Manual != nil && !hasOrder[best.Key] {
				m = o.Manual.within(best.Items)
			}
			switch {
			case o.Manual == nil || (m == nil && !hasOrder[best.Key]):
				drop = append(drop, o.Key)
			case m != nil:
				adopt[best.Key] = m
				hasOrder[best.Key] = true
				drop = append(drop, o.Key)
			case len(o.Items) > 0:
				shelve = append(shelve, o.Key)
			}
		case !complete:
		case o.Manual == nil:
			drop = append(drop, o.Key)
		case len(o.Items) > 0:
			shelve = append(shelve, o.Key)
		}
	}
	return adopt, drop, shelve
}

// within is the order limited to the ids of items (nil: nothing left).
func (m FranchiseManual) within(items []int64) *FranchiseManual {
	out := FranchiseManual{}
	for _, id := range m.Order {
		if slices.Contains(items, id) {
			out.Order = append(out.Order, id)
		}
	}
	for _, id := range m.Hidden {
		if slices.Contains(items, id) {
			out.Hidden = append(out.Hidden, id)
		}
	}
	if len(out.Order) == 0 && len(out.Hidden) == 0 {
		return nil
	}
	return &out
}

// saveFranchises writes the pass's franchises (their admin's orders kept,
// see reconcileFranchises for the rows it did not produce) and the model's
// failures (asks: what stays after the pass).
func (s *Store) saveFranchises(ctx context.Context, list []Franchise, complete bool, asks map[string]franchiseAsk) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	old, err := readFranchises(ctx, tx)
	if err != nil {
		return err
	}
	now := s.now().Unix()
	for _, f := range list {
		items, _ := json.Marshal(f.Items)
		if _, err := tx.ExecContext(ctx, `INSERT INTO franchises (key, name, source, items_json, input_hash, error, updated_at) VALUES (?,?,?,?,?,?,?)
		  ON CONFLICT (key) DO UPDATE SET name=excluded.name, source=excluded.source, items_json=excluded.items_json,
		  input_hash=excluded.input_hash, error=excluded.error, updated_at=excluded.updated_at`,
			f.Key, f.Name, f.Source, string(items), f.InputHash, f.Error, now); err != nil {
			return err
		}
	}
	adopt, drop, shelve := reconcileFranchises(old, list, complete)
	for key, m := range adopt {
		b, _ := json.Marshal(m)
		if _, err := tx.ExecContext(ctx, `UPDATE franchises SET manual_json=? WHERE key=?`, string(b), key); err != nil {
			return err
		}
	}
	for _, key := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM franchises WHERE key=?`, key); err != nil {
			return err
		}
	}
	for _, key := range shelve {
		if _, err := tx.ExecContext(ctx, `UPDATE franchises SET items_json='[]' WHERE key=?`, key); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM franchise_asks`); err != nil {
		return err
	}
	for k, a := range asks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO franchise_asks (key, model, failures, retry_at, error) VALUES (?,?,?,?,?)`,
			k, a.Model, a.Failures, a.RetryAt, a.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// maxFranchiseName is the longest channel name the store takes.
const maxFranchiseName = 80

// cleanName is a name without control characters and with its white space
// collapsed (a name comes from TMDB or the model: it ends up on every
// client).
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// fitFranchiseName is a name made fit for a channel: cleaned, then cut at a
// word boundary to the longest name a channel takes.
func fitFranchiseName(s string) string {
	s = cleanName(s)
	r := []rune(s)
	if len(r) <= maxFranchiseName {
		return s
	}
	cut := r[:maxFranchiseName]
	if r[maxFranchiseName] != ' ' {
		for i := len(cut) - 1; i > maxFranchiseName/2; i-- {
			if cut[i] == ' ' {
				cut = cut[:i]
				break
			}
		}
	}
	return strings.TrimRight(string(cut), " -–—:,")
}

// franchiseState reads a value of the last pass ("" = none).
func (s *Store) franchiseState(ctx context.Context, k string) string {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT v FROM franchise_state WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Store) setFranchiseState(ctx context.Context, kv map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO franchise_state (k, v) VALUES (?,?) ON CONFLICT (k) DO UPDATE SET v=excluded.v`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
