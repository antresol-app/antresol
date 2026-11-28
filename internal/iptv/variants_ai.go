package iptv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/channels"
)

// Variants the rules are unsure of. The rules (variants.go) group what
// reads the same once the quality marks are cut off, and what shares a
// tvg-id; a big playlist also has look-alikes they cannot judge: a name in
// another script («Rossiya 1» beside «Россия 1»), a region with no brackets
// («Россия 1 Москва» — a regional feed, or «Россия 1 Владивосток», hours
// ahead: a timeshift), a channel of its own with a longer name («НТВ
// Мир»), a tvg-id the provider reused for two channels. Such entries form
// a cluster (planVariants), and the AI helper's medium model is asked, in
// the background, which of them are one channel (AskVariants). Its answer
// is kept by the hash of the cluster's content (iptv_variant_ai), so a
// playlist is asked about once per change; until it answers, and without
// an AI helper, the rules stand. The admin's word on an entry
// (Decision.Variant) wins over both.

// The admin's word on an entry's variants (Decision.Variant).
const (
	VariantOwn  = "own"
	VariantWith = "with:" // + the other entry's Ident
)

// How a group of variants was made (VariantInfo.How).
const (
	VariantByRules = "rules"
	VariantByAI    = "ai"
	VariantByAdmin = "admin"
)

// The kinds an AI verdict names (the schema's enum).
const (
	kindMain      = "main"
	kindQuality   = "quality"
	kindRegional  = "regional"
	kindTimeshift = "timeshift"
)

const (
	// maxCluster: a cluster with more entries is left to the rules (a
	// playlist's hundred regional feeds of one channel is no question for
	// one answer, and the rules group the bracketed ones already).
	maxCluster = 60
	// variantBatch: entries per question; variantRound: questions per
	// AskVariants (the rest waits for the next tick).
	variantBatch = 150
	variantRound = 20
	// fuzzyMin: how alike two names in different scripts must read (one
	// minus the edit distance over the longer, after transliteration).
	fuzzyMin = 0.7
)

// variantVerdict is the AI helper's word on one entry of a cluster.
type variantVerdict struct {
	Ident string `json:"i"`
	// Ch: the channel within the cluster (entries of one channel share
	// it); 0: no usable answer, the rules stand.
	Ch   int    `json:"c"`
	Kind string `json:"k"`
}

// variantCluster is a question: look-alike entries of one source, by
// playlist position, and the hash of their content.
type variantCluster struct {
	key string
	ms  []Member
}

// variantPlan is how a source's entries group.
type variantPlan struct {
	// groups: two or more entries each, best first.
	groups [][]Member
	// how: per grouped entry, how its group was made (VariantBy*).
	how map[int64]string
	// timeshift: entries the AI helper called a timeshift copy.
	timeshift map[int64]bool
	// keys: every cluster's key (answered or not); ask: the clusters
	// without an answer yet.
	keys []string
	ask  []variantCluster
}

// planVariants groups a source's present entries: the rules, then the AI
// helper's answers (by cluster key; nil: none), then the admin's word.
func planVariants(ms []Member, answers map[string][]variantVerdict) variantPlan {
	var pres []Member
	for _, m := range ms {
		if m.Present {
			pres = append(pres, m)
		}
	}
	n := len(pres)
	cand := make([]bool, n)
	bases := make([]string, n)
	ranks := make([]int, n)
	unsure := make([]bool, n)
	rules := newUnion(n)
	byBase := map[string]int{}
	for i, m := range pres {
		var regional bool
		bases[i], ranks[i], regional = variantParse(m.Name)
		if cand[i] = variantCandidate(m); !cand[i] {
			continue
		}
		unsure[i] = regional
		if j, ok := byBase[bases[i]]; ok {
			rules.join(i, j)
		} else {
			byBase[bases[i]] = i
		}
	}
	// ids: tvg-id and the guide channel (when it was matched, not only
	// guessed by a normalised name); a join of two different names by an
	// id alone is a guess the AI helper is asked about
	joinBy := func(key func(Member) string) {
		first := map[string]int{}
		names := map[string]map[string]bool{}
		for i, m := range pres {
			if k := key(m); cand[i] && k != "" {
				if names[k] == nil {
					names[k] = map[string]bool{}
				}
				names[k][bases[i]] = true
			}
		}
		for i, m := range pres {
			k := key(m)
			if !cand[i] || k == "" || len(names[k]) > maxIDShare {
				continue
			}
			if j, ok := first[k]; ok {
				if bases[i] != bases[j] {
					unsure[i], unsure[j] = true, true
				}
				rules.join(i, j)
			} else {
				first[k] = i
			}
		}
	}
	joinBy(func(m Member) string { return strings.ToLower(strings.TrimSpace(m.TvgID)) })
	joinBy(func(m Member) string {
		if m.EPGID == "" || m.EPGHow == "norm-name" {
			return ""
		}
		return m.EPGID
	})

	// clusters: the rules' groups, joined by look-alike names, around
	// what the rules are unsure of
	cl := rules.clone()
	linked := make([]bool, n)
	for _, l := range lookAlikes(byBase) {
		cl.join(l[0], l[1])
		linked[l[0]], linked[l[1]] = true, true
	}
	comp := map[int][]int{}
	for i := range pres {
		if cand[i] {
			r := cl.find(i)
			comp[r] = append(comp[r], i)
		}
	}
	label := make([]string, n)
	for i := range pres {
		if cand[i] {
			label[i] = fmt.Sprintf("r%d", rules.find(i))
		} else {
			label[i] = fmt.Sprintf("s%d", i)
		}
	}
	plan := variantPlan{how: map[int64]string{}, timeshift: map[int64]bool{}}
	byAI := map[string]bool{}
	roots := make([]int, 0, len(comp))
	for r := range comp {
		roots = append(roots, r)
	}
	sort.Ints(roots)
	for _, r := range roots {
		is := comp[r]
		ask := false
		for _, i := range is {
			ask = ask || unsure[i] || linked[i]
		}
		if !ask || len(is) < 2 || len(is) > maxCluster {
			continue
		}
		c := variantCluster{ms: make([]Member, len(is))}
		for k, i := range is {
			c.ms[k] = pres[i]
		}
		c.key = clusterKey(c.ms)
		plan.keys = append(plan.keys, c.key)
		vs, ok := answers[c.key]
		if !ok {
			plan.ask = append(plan.ask, c)
			continue
		}
		if !validVerdicts(c.ms, vs) {
			continue // the AI helper could not tell: the rules stand
		}
		by := map[string]variantVerdict{}
		for _, v := range vs {
			by[v.Ident] = v
		}
		for _, i := range is {
			v := by[pres[i].Ident]
			if v.Kind == kindTimeshift {
				label[i] = fmt.Sprintf("s%d", i)
				plan.timeshift[pres[i].ID] = true
				continue
			}
			if v.Kind == kindRegional && !regionalName(pres[i].Name) {
				ranks[i] -= 5 // a regional feed without brackets: the national one leads
			}
			label[i] = fmt.Sprintf("a%s:%d", c.key, v.Ch)
			byAI[label[i]] = true
		}
	}

	// the admin's word: a channel of its own, or a variant of another
	byIdent := map[string]int{}
	for i, m := range pres {
		byIdent[m.Ident] = i
	}
	byAdmin := map[string]bool{}
	word := func(i int) string {
		if pres[i].Manual == nil {
			return ""
		}
		return pres[i].Manual.Variant
	}
	for i := range pres {
		if word(i) == VariantOwn {
			label[i] = fmt.Sprintf("o%d", i)
		}
	}
	var resolve func(i int, seen map[int]bool) string
	resolve = func(i int, seen map[int]bool) string {
		w := word(i)
		if !strings.HasPrefix(w, VariantWith) || seen[i] {
			return label[i]
		}
		seen[i] = true
		j, ok := byIdent[strings.TrimPrefix(w, VariantWith)]
		if !ok || j == i {
			return label[i]
		}
		return resolve(j, seen)
	}
	final := make([]string, n)
	for i := range pres {
		final[i] = resolve(i, map[int]bool{})
		if word(i) != "" {
			byAdmin[final[i]] = true
		}
	}
	// groups, in the order of their first entry
	byLabel := map[string][]int{}
	var order []string
	for i := range pres {
		l := final[i]
		if _, ok := byLabel[l]; !ok {
			order = append(order, l)
		}
		byLabel[l] = append(byLabel[l], i)
	}
	for _, l := range order {
		is := byLabel[l]
		if len(is) < 2 {
			continue
		}
		sort.SliceStable(is, func(a, b int) bool {
			if ranks[is[a]] != ranks[is[b]] {
				return ranks[is[a]] > ranks[is[b]]
			}
			return pres[is[a]].Position < pres[is[b]].Position
		})
		how := VariantByRules
		switch {
		case byAdmin[l]:
			how = VariantByAdmin
		case byAI[l]:
			how = VariantByAI
		}
		g := make([]Member, len(is))
		for k, i := range is {
			g[k] = pres[i]
			plan.how[pres[i].ID] = how
		}
		plan.groups = append(plan.groups, g)
	}
	return plan
}

func regionalName(name string) bool { _, _, r := variantParse(name); return r }

// union is a union-find over n entries (the smaller index the root).
type union []int

func newUnion(n int) union {
	u := make(union, n)
	for i := range u {
		u[i] = i
	}
	return u
}

func (u union) find(i int) int {
	for u[i] != i {
		u[i] = u[u[i]]
		i = u[i]
	}
	return i
}

func (u union) join(a, b int) {
	if ra, rb := u.find(a), u.find(b); ra != rb {
		u[max(ra, rb)] = min(ra, rb)
	}
}

func (u union) clone() union { return append(union(nil), u...) }

// lookAlikes: pairs of entries (one per base name) whose names may be one
// channel the rules did not join: a base that is another's with words of
// letters after it («россия 1 москва» after «россия 1», not «россия 24»),
// and names in two scripts that read alike once transliterated («rossiya
// 1», «russia 1» beside «россия 1»), with the same numbers in them.
func lookAlikes(byBase map[string]int) [][2]int {
	var out [][2]int
	type fold struct {
		base, lat, digits string
		cyr               bool
	}
	var folds []fold
	for b, i := range byBase {
		w := strings.Fields(b)
		for k := 1; k < len(w); k++ {
			j, ok := byBase[strings.Join(w[:k], " ")]
			if ok && lettersOnly(w[k:]) {
				out = append(out, [2]int{min(i, j), max(i, j)})
			}
		}
		lat, cyr, latin := translit(b)
		if cyr != latin { // one script only: a mixed name is no pair
			folds = append(folds, fold{base: b, lat: lat, digits: digitsOf(b), cyr: cyr})
		}
	}
	sort.Slice(folds, func(a, b int) bool { return folds[a].base < folds[b].base })
	// one script against the other, within the same first letter
	byFirst := map[rune][]fold{}
	for _, f := range folds {
		if r := []rune(f.lat); len(r) > 0 {
			byFirst[r[0]] = append(byFirst[r[0]], f)
		}
	}
	for _, fs := range byFirst {
		for a := range fs {
			for b := a + 1; b < len(fs); b++ {
				x, y := fs[a], fs[b]
				if x.cyr == y.cyr || x.digits != y.digits {
					continue
				}
				if x.lat == y.lat || (len([]rune(x.lat)) >= 4 && len([]rune(y.lat)) >= 4 && alike(x.lat, y.lat) >= fuzzyMin) {
					a, b := byBase[x.base], byBase[y.base]
					out = append(out, [2]int{min(a, b), max(a, b)})
				}
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a][0] != out[b][0] {
			return out[a][0] < out[b][0]
		}
		return out[a][1] < out[b][1]
	})
	return out
}

func lettersOnly(ws []string) bool {
	for _, w := range ws {
		for _, r := range w {
			if !unicode.IsLetter(r) {
				return false
			}
		}
	}
	return len(ws) > 0
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") {
			b.WriteByte(' ')
		}
	}
	return strings.TrimSpace(b.String())
}

var cyrLat = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ж': "zh", 'з': "z", 'и': "i", 'й': "y",
	'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e",
	'ю': "yu", 'я': "ya", 'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g", 'ў': "u",
}

// translit: a base name in Latin letters, and whether it had Cyrillic and
// Latin letters.
func translit(s string) (lat string, cyr, latin bool) {
	var b strings.Builder
	for _, r := range s {
		if t, ok := cyrLat[r]; ok {
			b.WriteString(t)
			cyr = true
			continue
		}
		if r < unicode.MaxASCII && unicode.IsLetter(r) {
			latin = true
		}
		b.WriteRune(r)
	}
	return b.String(), cyr, latin
}

// alike: one minus the edit distance of a and b over the longer one.
func alike(a, b string) float64 {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			c := 1
			if x[i-1] == y[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(y)])/float64(max(len(x), len(y)))
}

// clusterKey: the hash of what the AI helper is told about the entries
// (their idents, names, groups, tvg-ids), in a fixed order.
func clusterKey(ms []Member) string {
	lines := make([]string, len(ms))
	for i, m := range ms {
		lines[i] = strings.Join([]string{m.Ident, m.Name, m.Group, m.TvgID}, "\x00")
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:16])
}

// validVerdicts: an answer for every entry of the cluster, each with a
// channel and a known kind.
func validVerdicts(ms []Member, vs []variantVerdict) bool {
	if len(vs) != len(ms) {
		return false
	}
	want := map[string]bool{}
	for _, m := range ms {
		want[m.Ident] = true
	}
	for _, v := range vs {
		if !want[v.Ident] || v.Ch <= 0 {
			return false
		}
		switch v.Kind {
		case kindMain, kindQuality, kindRegional, kindTimeshift:
		default:
			return false
		}
		delete(want, v.Ident)
	}
	return len(want) == 0
}

// ---- the AI helper ----

// variantCache: iptv_variant_ai in memory (loaded once, written through).
type variantCache struct {
	mu       sync.Mutex
	loaded   bool
	m        map[string][]variantVerdict
	failedAt time.Time
	running  bool
}

func (s *Service) variantAnswers(ctx context.Context) (map[string][]variantVerdict, error) {
	c := &s.variants
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.m, nil
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT cluster_key, answer_json FROM iptv_variant_ai`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string][]variantVerdict{}
	for rows.Next() {
		var k, a string
		if err := rows.Scan(&k, &a); err != nil {
			return nil, err
		}
		var vs []variantVerdict
		_ = json.Unmarshal([]byte(a), &vs)
		m[k] = vs
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	c.m, c.loaded = m, true
	return m, nil
}

// VariantInfo is an entry's group of variants (Variants).
type VariantInfo struct {
	// Members: the group, best first (the first leads when it is shown).
	Members []int64
	How     string
}

// Variants: how the source's present entries group now (member id →
// its group); an entry of no group is left out. Empty when the source
// does not merge variants.
func (s *Service) Variants(ctx context.Context, src Source, ms []Member) (map[int64]VariantInfo, error) {
	out := map[int64]VariantInfo{}
	if !src.PresetVariants {
		return out, nil
	}
	answers, err := s.variantAnswers(ctx)
	if err != nil {
		return nil, err
	}
	p := planVariants(ms, answers)
	for _, g := range p.groups {
		ids := make([]int64, len(g))
		for i, m := range g {
			ids[i] = m.ID
		}
		for _, m := range g {
			out[m.ID] = VariantInfo{Members: ids, How: p.how[m.ID]}
		}
	}
	return out, nil
}

// SetVariant stores the admin's word on an entry's variants: VariantOwn,
// VariantWith + another present entry's Ident of the same source, or ""
// (the rules and the AI helper decide), and brings the rows in step.
func (s *Service) SetVariant(ctx context.Context, m Member, word string) error {
	d := Decision{}
	if m.Manual != nil {
		d = *m.Manual
	}
	d.Variant = word
	var dp *Decision
	if d != (Decision{}) {
		dp = &d
	}
	if err := s.Store.SetManual(ctx, m.ID, dp); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// kickVariants runs AskVariants in the background (after a refresh: the
// import never waits for the model), when Run runs.
func (s *Service) kickVariants() {
	s.omu.Lock()
	base := s.base
	s.omu.Unlock()
	if base == nil || s.Model == nil {
		return
	}
	go s.AskVariants(base)
}

// AskVariants asks the medium model about the clusters of the sources
// that merge variants which have no answer yet, at most variantRound
// questions, stores the answers and brings the rows in step; a failure
// waits aiRetry. Answers no source's playlist asks any more are dropped.
// Run calls it every Tick, a refresh right after; one round at a time.
func (s *Service) AskVariants(ctx context.Context) {
	if s.Model == nil {
		return
	}
	c := &s.variants
	c.mu.Lock()
	if c.running || (!c.failedAt.IsZero() && s.now().Sub(c.failedAt) < aiRetry) {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()
	answers, err := s.variantAnswers(ctx)
	if err != nil {
		log.Printf("iptv: variants cache: %v", err)
		return
	}
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		log.Printf("iptv: variants: %v", err)
		return
	}
	// The members of every source are read only when a source asks for
	// variants and a model is there: a tick every 10 minutes must stay cheap.
	srcs = slices.DeleteFunc(srcs, func(src Source) bool { return !src.PresetVariants })
	if len(srcs) == 0 {
		if err := s.pruneVariants(ctx, nil); err != nil { // no playlist asks any more
			log.Printf("iptv: variants cache: %v", err)
		}
		return
	}
	l := s.Model(ctx, channels.FeatureIPTVVariants, channels.TierMedium)
	if l == nil {
		return
	}
	all, err := s.Store.AllMembers(ctx)
	if err != nil {
		log.Printf("iptv: variants: %v", err)
		return
	}
	bySrc := map[int64][]Member{}
	for _, m := range all {
		bySrc[m.SourceID] = append(bySrc[m.SourceID], m)
	}
	used := map[string]bool{}
	var todo []variantCluster
	for _, src := range srcs {
		p := planVariants(bySrc[src.ID], answers)
		for _, k := range p.keys {
			used[k] = true
		}
		for _, q := range p.ask {
			if !used["ask:"+q.key] { // the same cluster in two sources: once
				used["ask:"+q.key] = true
				todo = append(todo, q)
			}
		}
	}
	if err := s.pruneVariants(ctx, used); err != nil {
		log.Printf("iptv: variants cache: %v", err)
	}
	if len(todo) == 0 {
		return
	}
	got := 0
	for i := 0; i < variantRound && len(todo) > 0 && ctx.Err() == nil; i++ {
		part, size := 0, 0
		for part < len(todo) && (part == 0 || size+len(todo[part].ms) <= variantBatch) {
			size += len(todo[part].ms)
			part++
		}
		batch := todo[:part]
		todo = todo[part:]
		ans, err := askVariants(ctx, l, batch)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("iptv: ai variants: %v", err)
				c.mu.Lock()
				c.failedAt = s.now()
				c.mu.Unlock()
			}
			break
		}
		if err := s.storeVariants(ctx, ans, l.Model); err != nil {
			log.Printf("iptv: variants cache: %v", err)
			return
		}
		got += len(ans)
	}
	if got == 0 {
		return
	}
	s.srcMu.Lock()
	err = s.Sync(ctx)
	s.srcMu.Unlock()
	if err != nil && ctx.Err() == nil {
		log.Printf("iptv: sync: %v", err)
	}
}

func (s *Service) storeVariants(ctx context.Context, got map[string][]variantVerdict, model string) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	for k, vs := range got {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iptv_variant_ai (cluster_key, answer_json, model, made_ms) VALUES (?,?,?,?)
			ON CONFLICT(cluster_key) DO UPDATE SET answer_json=excluded.answer_json, model=excluded.model, made_ms=excluded.made_ms`,
			k, jsonText(vs), model, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c := &s.variants
	c.mu.Lock()
	for k, vs := range got {
		c.m[k] = vs
	}
	c.mu.Unlock()
	return nil
}

// pruneVariants drops the answers about clusters no playlist has now.
func (s *Service) pruneVariants(ctx context.Context, used map[string]bool) error {
	c := &s.variants
	c.mu.Lock()
	var gone []string
	for k := range c.m {
		if !used[k] {
			gone = append(gone, k)
		}
	}
	c.mu.Unlock()
	for _, k := range gone {
		if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM iptv_variant_ai WHERE cluster_key=?`, k); err != nil {
			return err
		}
		c.mu.Lock()
		delete(c.m, k)
		c.mu.Unlock()
	}
	return nil
}

const aiVariantsSystem = `You group the entries of an IPTV playlist into TV channels. Each numbered entry belongs to a cluster of entries whose names look alike; judge each cluster on its own.
Within a cluster give entries that are the same channel the same "channel" number (1, 2, 3…): another quality of it (SD, HD, FHD, 4K, 50 fps), the provider's original feed, its name in another script or language ("Rossiya 1" in Latin letters is the same channel as its Cyrillic name), or a regional feed of it that keeps its schedule (regional news inserts). A different channel with a similar name (a sister channel, an international or thematic version with its own schedule, a numbered sibling) gets a number of its own.
"kind": "main" for a channel's standard feed, "quality" for another quality or the original feed of it, "regional" for a regional feed of it, "timeshift" for a copy of a channel shown hours earlier or later (a "+2", a far-east or Siberian feed, an "Orbita" or "Dubl" copy, in Latin or Cyrillic letters).
Use the names, the playlist groups and the tvg-ids. Answer every entry, once.`

// aiVariantsSchema: the answer's JSON Schema (strict: every object closed,
// every property required).
var aiVariantsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"entries": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entry":   map[string]any{"type": "integer"},
					"channel": map[string]any{"type": "integer"},
					"kind":    map[string]any{"type": "string", "enum": []string{kindMain, kindQuality, kindRegional, kindTimeshift}},
				},
				"required":             []string{"entry", "channel", "kind"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"entries"},
	"additionalProperties": false,
}

// askVariants asks the model about the clusters: each cluster's verdicts
// (a cluster it answered unusably for is stored with no channel: the
// rules stand, it is not asked again). An answer that is not the
// schema's JSON at all is an error (asked again after aiRetry).
func askVariants(ctx context.Context, l *channels.LLM, cs []variantCluster) (map[string][]variantVerdict, error) {
	var b strings.Builder
	type at struct{ c, m int }
	var idx []at
	for ci, c := range cs {
		fmt.Fprintf(&b, "Cluster %d:\n", ci+1)
		for mi, m := range c.ms {
			idx = append(idx, at{ci, mi})
			fmt.Fprintf(&b, "%d. %s", len(idx), m.Name)
			if g := strings.TrimSpace(m.Group); g != "" {
				fmt.Fprintf(&b, " | group: %s", g)
			}
			if id := strings.TrimSpace(m.TvgID); id != "" {
				fmt.Fprintf(&b, " | tvg-id: %s", id)
			}
			b.WriteByte('\n')
		}
	}
	raw, err := l.AskSchema(ctx, aiVariantsSystem, b.String(), aiVariantsSchema)
	if err != nil {
		return nil, err
	}
	var ans struct {
		Entries []struct {
			Entry   int    `json:"entry"`
			Channel int    `json:"channel"`
			Kind    string `json:"kind"`
		} `json:"entries"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ans); err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	per := make([][]variantVerdict, len(cs))
	seen := map[int]bool{}
	for _, e := range ans.Entries {
		if e.Entry < 1 || e.Entry > len(idx) || seen[e.Entry] {
			continue
		}
		seen[e.Entry] = true
		p := idx[e.Entry-1]
		per[p.c] = append(per[p.c], variantVerdict{Ident: cs[p.c].ms[p.m].Ident, Ch: e.Channel, Kind: strings.ToLower(strings.TrimSpace(e.Kind))})
	}
	out := make(map[string][]variantVerdict, len(cs))
	for ci, c := range cs {
		vs := per[ci]
		if !validVerdicts(c.ms, vs) {
			vs = make([]variantVerdict, len(c.ms)) // no usable answer
			for i, m := range c.ms {
				vs[i] = variantVerdict{Ident: m.Ident}
			}
		}
		out[c.key] = vs
	}
	return out, nil
}
