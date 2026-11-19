package channels

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

// FranchiseSource is TMDB's collections (*tmdb.Related through an
// adapter: cached in tmdb_related, the «Похожее» block's own fetch).
type FranchiseSource interface {
	// MovieCollection is the collection of a TMDB movie (id 0: none); ok
	// is false when TMDB did not answer in time (asked again later).
	MovieCollection(ctx context.Context, tmdbID int) (id int, name string, ok bool)
	// CollectionParts are a collection's name and its movies' TMDB ids in
	// release order; ok is false when TMDB did not answer in time.
	CollectionParts(ctx context.Context, id int) (name string, parts []int, ok bool)
	// Language is the TMDB language the names are in ("ru-RU").
	Language() string
}

// FranchiseFacts is Wikidata's franchise facts by TMDB id
// (*wikidata.Client); optional.
type FranchiseFacts interface {
	TitleFacts(ctx context.Context, kind string, tmdbIDs []int, lang string) (map[int]wikidata.TitleFacts, error)
}

// Limits of a pass.
const (
	franchiseAlgo        = "3" // a new value recomputes every library once (2: franchises from Wikidata's anchors; 3: Wikidata's facts actually read, see wikidata.claimsBatch)
	franchiseEvery       = 6 * time.Hour
	franchisePassTimeout = 15 * time.Minute
	franchiseAskTimeout  = 120 * time.Second
	franchiseWikiTimeout = 90 * time.Second // a batch of 50 titles: the search, their claims and their anchors' (a few dozen requests)
	franchiseWikiTTL     = 30 * 24 * time.Hour
	maxFranchiseAsks     = 40 // clusters the model is asked about in a pass
	maxFranchiseMembers  = 60
)

// Franchises works the library's franchises out in the background and
// keeps them in the franchises table: on start, on Kick (after a scan),
// every franchiseEvery, only when the library's titles (or the model)
// changed since the last complete pass, or when forced.
type Franchises struct {
	Store *Store
	TMDB  FranchiseSource // nil: no franchises
	Wiki  FranchiseFacts  // nil: no Wikidata evidence
	Model func(context.Context) *LLM
	// Changed is called after a pass that wrote the table.
	Changed func()

	mu      sync.Mutex
	running atomic.Bool
	once    sync.Once
	kick    chan bool // true: forced
}

func (f *Franchises) init() { f.once.Do(func() { f.kick = make(chan bool, 1) }) }

// Kick asks for a pass soon (force: even when nothing changed).
func (f *Franchises) Kick(force bool) {
	if f == nil {
		return
	}
	f.init()
	select {
	case f.kick <- force:
	default:
		if force { // a plain kick is waiting: make it a forced one
			select {
			case <-f.kick:
			default:
			}
			select {
			case f.kick <- true:
			default:
			}
		}
	}
}

// Run passes at once, then on Kick and every franchiseEvery, until ctx ends.
func (f *Franchises) Run(ctx context.Context) {
	f.init()
	t := time.NewTicker(franchiseEvery)
	defer t.Stop()
	force := false
	for {
		ran, err := f.Pass(ctx, force)
		if err != nil && ctx.Err() == nil {
			log.Printf("channels: franchises: %v", err)
		}
		if ran && f.Changed != nil {
			f.Changed()
		}
		if err == nil { // a forced pass that failed is not forgotten
			force = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case force = <-f.kick:
		}
	}
}

// FranchiseStatus is the last pass, for the admin.
type FranchiseStatus struct {
	Running bool      `json:"running"`
	At      time.Time `json:"at,omitzero"`
	Model   string    `json:"model,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Incomplete: TMDB did not answer for some titles (tried again later).
	Incomplete bool `json:"incomplete,omitempty"`
}

// Status is the last pass.
func (f *Franchises) Status(ctx context.Context) FranchiseStatus {
	st := FranchiseStatus{Running: f.running.Load(), Model: f.Store.franchiseState(ctx, "model"),
		Error: f.Store.franchiseState(ctx, "error"), Incomplete: f.Store.franchiseState(ctx, "incomplete") == "1"}
	if at, _ := strconv.ParseInt(f.Store.franchiseState(ctx, "at"), 10, 64); at > 0 {
		st.At = time.Unix(at, 0)
	}
	return st
}

// libTitle is a library movie or series with a TMDB id.
type libTitle struct {
	ID    int64
	Kind  string
	TMDB  int
	Title string
	Year  int
}

// titles are the library's movies and series with a TMDB id, one edition
// of each (the lowest item id).
func (f *Franchises) titles(ctx context.Context) ([]libTitle, error) {
	rows, err := f.Store.DB.QueryContext(ctx, `SELECT id, kind, tmdb_id, title, year FROM items
	  WHERE kind IN ('movie','series') AND missing_since IS NULL AND tmdb_id > 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []libTitle
	seen := map[string]bool{}
	for rows.Next() {
		var t libTitle
		if err := rows.Scan(&t.ID, &t.Kind, &t.TMDB, &t.Title, &t.Year); err != nil {
			return nil, err
		}
		k := t.Kind + "/" + strconv.Itoa(t.TMDB)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out, rows.Err()
}

func modelID(l *LLM) string {
	if l == nil {
		return ""
	}
	return l.URL + "|" + l.Model
}

// franchiseFingerprint is what a pass is made for: the titles (an edition
// replaced by one of the same TMDB id, a title renamed or re-dated count),
// the metadata language and the model.
func franchiseFingerprint(titles []libTitle, lang, model string) string {
	keys := make([]string, 0, len(titles))
	for _, t := range titles {
		keys = append(keys, fmt.Sprintf("%s/%d/%d/%d/%s", t.Kind, t.TMDB, t.ID, t.Year, t.Title))
	}
	slices.Sort(keys)
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n", franchiseAlgo, lang, model)
	for _, k := range keys {
		h.Write([]byte(k + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// retryDue: the model failed on a cluster and its backoff is over.
func (f *Franchises) retryDue(ctx context.Context) bool {
	at, _ := strconv.ParseInt(f.Store.franchiseState(ctx, "retry"), 10, 64)
	return at > 0 && f.Store.now().Unix() >= at
}

// Pass works the franchises out unless the library and the model are as
// the last complete pass found them and no failed ask is due again (force:
// anyway, failed asks included); ran: it did.
func (f *Franchises) Pass(ctx context.Context, force bool) (ran bool, err error) {
	if f == nil || f.TMDB == nil {
		return false, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	titles, err := f.titles(ctx)
	if err != nil {
		return false, err
	}
	var model *LLM
	if f.Model != nil {
		model = f.Model(ctx)
	}
	fp := franchiseFingerprint(titles, f.TMDB.Language(), modelID(model))
	if !force && f.Store.franchiseState(ctx, "fingerprint") == fp && !f.retryDue(ctx) {
		return false, nil
	}
	f.running.Store(true)
	defer f.running.Store(false)
	pctx, cancel := context.WithTimeout(ctx, franchisePassTimeout)
	defer cancel()
	colls, complete := f.collections(pctx, titles)
	facts, wikiErr := f.facts(pctx, titles)
	groups := clusters(titles, colls, facts)
	var out []Franchise
	var passErr string
	var asks map[string]franchiseAsk
	if model == nil {
		for _, cl := range groups {
			out = append(out, cl.baseline(""))
		}
	} else {
		prev := map[string]Franchise{}
		if old, err := f.Store.Franchises(ctx); err == nil {
			for _, x := range old {
				prev[x.Key] = x
			}
		}
		out, passErr, asks = f.withAgent(pctx, model, titles, groups, facts, prev, f.Store.franchiseAsks(ctx), force, complete)
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if pctx.Err() != nil {
		complete = false // out of time: what was found is written, nothing is pruned
	}
	if err := f.Store.saveFranchises(ctx, out, complete, asks); err != nil {
		return false, err
	}
	retry := int64(0)
	for _, a := range asks {
		if retry == 0 || a.RetryAt < retry {
			retry = a.RetryAt
		}
	}
	state := map[string]string{"at": strconv.FormatInt(f.Store.now().Unix(), 10), "model": modelID(model), "error": passErr, "incomplete": "0", "retry": ""}
	if retry > 0 {
		state["retry"] = strconv.FormatInt(retry, 10)
	}
	if wikiErr != "" { // Wikidata failed: the pass is asked again next time, and the admin sees why
		state["error"] = strings.TrimPrefix(passErr+"; "+wikiErr, "; ")
	}
	if complete && wikiErr == "" {
		state["fingerprint"] = fp
	} else if complete {
		state["fingerprint"] = ""
	} else {
		state["incomplete"], state["fingerprint"] = "1", ""
	}
	return true, f.Store.setFranchiseState(ctx, state)
}

// coll is a TMDB collection with the library's movies of it in its order.
type coll struct {
	id    int
	name  string
	items []int64
	year  int // of its first title in the library
}

// collections are the TMDB collections of the library's movies (complete:
// TMDB answered for all of them), by id.
func (f *Franchises) collections(ctx context.Context, titles []libTitle) ([]coll, bool) {
	complete := true
	byColl := map[int][]libTitle{}
	names := map[int]string{}
	for _, t := range titles {
		if t.Kind != library.KindMovie {
			continue
		}
		if ctx.Err() != nil {
			return nil, false
		}
		id, name, ok := f.TMDB.MovieCollection(ctx, t.TMDB)
		if !ok {
			complete = false
			continue
		}
		if id > 0 {
			byColl[id] = append(byColl[id], t)
			names[id] = name
		}
	}
	var out []coll
	for id, ms := range byColl {
		pos := map[int]int{}
		name, parts, ok := f.TMDB.CollectionParts(ctx, id)
		if !ok {
			complete = false
		} else {
			for i, p := range parts {
				pos[p] = i + 1
			}
		}
		if name == "" {
			name = names[id]
		}
		slices.SortStableFunc(ms, func(a, b libTitle) int {
			pa, pb := pos[a.TMDB], pos[b.TMDB]
			switch {
			case pa != 0 && pb != 0:
				return cmp.Compare(pa, pb)
			case pa != 0:
				return -1
			case pb != 0:
				return 1
			}
			return cmp.Or(cmp.Compare(a.Year, b.Year), cmp.Compare(a.ID, b.ID))
		})
		cname := fitFranchiseName(FranchiseName(name))
		if cname == "" { // a name that is only «collection», or none: the first title's
			cname = fitFranchiseName(ms[0].Title)
		}
		c := coll{id: id, name: cname, year: ms[0].Year}
		for _, m := range ms {
			c.items = append(c.items, m.ID)
			if m.Year > 0 && (c.year == 0 || m.Year < c.year) {
				c.year = m.Year
			}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b coll) int { return cmp.Compare(a.id, b.id) })
	return out, complete
}

// FranchiseName is a collection's name without the word «collection»
// («Звёздные войны (Коллекция)» → «Звёздные войны»).
func FranchiseName(s string) string {
	s = strings.TrimSpace(s)
	for {
		l := strings.ToLower(s)
		cut := false
		for _, w := range []string{"(коллекция)", "коллекция", "(collection)", "collection", "- коллекция"} {
			if strings.HasSuffix(l, w) {
				s, cut = strings.TrimSpace(s[:len(s)-len(w)]), true
				break
			}
			if strings.HasPrefix(l, w+" ") {
				s, cut = strings.TrimSpace(s[len(w):]), true
				break
			}
		}
		s = strings.TrimRight(s, " -–—:,")
		if !cut {
			return s
		}
	}
}

// factsCurrent: a cached value of this FactsVersion (a miss, "", is one).
func factsCurrent(data string) bool {
	if data == "" {
		return true
	}
	var v struct {
		V int `json:"v"`
	}
	return json.Unmarshal([]byte(data), &v) == nil && v.V >= wikidata.FactsVersion
}

// facts are Wikidata's facts of the titles by item id, cached in
// wikidata_facts; Wikidata off or failing: what the cache has, expired or not.
func (f *Franchises) facts(ctx context.Context, titles []libTitle) (map[int64]wikidata.TitleFacts, string) {
	out := map[int64]wikidata.TitleFacts{}
	if f.Wiki == nil {
		return out, ""
	}
	wikiErr := ""
	lang, _, _ := strings.Cut(f.TMDB.Language(), "-")
	fresh := f.Store.now().Add(-franchiseWikiTTL).Unix()
	failed := false
	for _, kind := range []string{library.KindMovie, library.KindSeries} {
		wk := "movie"
		if kind == library.KindSeries {
			wk = "tv"
		}
		byTMDB := map[int]int64{}
		stale := map[int]string{} // expired rows: what Wikidata being down falls back to
		var missing []int
		for _, t := range titles {
			if t.Kind != kind {
				continue
			}
			byTMDB[t.TMDB] = t.ID
			var data string
			var at int64
			err := f.Store.DB.QueryRowContext(ctx, `SELECT data, fetched_at FROM wikidata_facts WHERE kind=? AND tmdb_id=?`, wk, t.TMDB).Scan(&data, &at)
			if err == nil && (at >= fresh && factsCurrent(data) || failed) {
				var x wikidata.TitleFacts
				if data != "" && json.Unmarshal([]byte(data), &x) == nil {
					out[t.ID] = x
				}
				continue
			}
			if err == nil {
				stale[t.TMDB] = data
			}
			missing = append(missing, t.TMDB)
		}
		useStale := func(ids []int) {
			for _, id := range ids {
				var x wikidata.TitleFacts
				if data := stale[id]; data != "" && json.Unmarshal([]byte(data), &x) == nil {
					out[byTMDB[id]] = x
				}
			}
		}
		for len(missing) > 0 && !failed {
			n := min(len(missing), 50)
			part := missing[:n]
			missing = missing[n:]
			wctx, cancel := context.WithTimeout(ctx, franchiseWikiTimeout)
			got, err := f.Wiki.TitleFacts(wctx, wk, part, lang)
			cancel()
			if err != nil {
				log.Printf("channels: franchises: wikidata: %v", err)
				wikiErr = "Wikidata: " + err.Error()
				failed = true // the rest of the pass goes without it
				useStale(part)
				useStale(missing)
				break
			}
			now := f.Store.now().Unix()
			for _, id := range part {
				data := ""
				if x, ok := got[id]; ok {
					b, _ := json.Marshal(x)
					data = string(b)
					out[byTMDB[id]] = x
				}
				if _, err := f.Store.DB.ExecContext(ctx, `INSERT INTO wikidata_facts (kind, tmdb_id, data, fetched_at) VALUES (?,?,?,?)
				  ON CONFLICT (kind, tmdb_id) DO UPDATE SET data=excluded.data, fetched_at=excluded.fetched_at`, wk, id, data, now); err != nil {
					log.Printf("channels: franchises: save wikidata: %v", err)
				}
			}
		}
	}
	return out, wikiErr
}
