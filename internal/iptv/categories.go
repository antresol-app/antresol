package iptv

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

// Categories of external channels for a viewer's filter: a big playlist's
// own group-titles are hundreds of spellings («RU | Новости», «Новостные
// HD»), so beside them each channel gets one to three categories of one
// fixed list, made by the AI helper's light model from its name and
// group, and cached by its base name (iptv_channel_ai) — one answer for
// every source and variant of it. The same answer carries its language
// (languages.go). Without a model the group-titles and the sources still
// filter.

// Category is a code of the fixed list and its name (a catalog key).
type Category struct{ Code, Name string }

// CategoryList: the codes the model may answer, in the filter's order.
var CategoryList = []Category{
	{"news", "Новости"},
	{"sport", "Спорт"},
	{"movies", "Кино"},
	{"series", "Сериалы"},
	{"kids", "Детские"},
	{"music", "Музыка"},
	{"education", "Познавательные"},
	{"documentary", "Документальные"},
	{"entertainment", "Развлекательные"},
	{"humor", "Юмор"},
	{"lifestyle", "Дом и стиль жизни"},
	{"travel", "Путешествия"},
	{"business", "Бизнес"},
	{"religion", "Религия"},
	{"regional", "Региональные"},
	{"general", "Общие"},
	{"shopping", "Магазины"},
	{AdultCode, "Для взрослых"},
}

// CategoryName is a code's name (a catalog key; "" for an unknown code).
func CategoryName(code string) string {
	for _, c := range CategoryList {
		if c.Code == code {
			return c.Name
		}
	}
	return ""
}

// AIInfo is what the model made of a channel.
type AIInfo struct {
	Cats []string
	Lang string
}

// aiKey: the cache key of a channel's name (its base, VariantName).
func aiKey(name string) string {
	base, _ := VariantName(name)
	return base
}

// aiCache: iptv_channel_ai in memory (loaded once, written through).
type aiCache struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]AIInfo
	// failedAt: the last round the model failed (the next waits aiRetry).
	failedAt time.Time
	running  bool
}

const (
	// aiBatch: names per question; aiRound: the most a round asks (the
	// rest waits for the next tick, so a new big playlist costs a few
	// minutes, not one long burst).
	aiBatch = 80
	aiRound = 25
	aiRetry = time.Hour
)

func (s *Service) aiInfo(ctx context.Context) (map[string]AIInfo, error) {
	c := &s.ai
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.m, nil
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT name_key, cats_json, lang FROM iptv_channel_ai`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]AIInfo{}
	for rows.Next() {
		var k, cats string
		var x AIInfo
		if err := rows.Scan(&k, &cats, &x.Lang); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cats), &x.Cats)
		m[k] = x
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	c.m, c.loaded = m, true
	return m, nil
}

// aiItem is a channel the model is asked about.
type aiItem struct{ key, name, group, tvgID string }

// Categorize asks the light model about the shown external channels it
// has no answer for yet, at most aiRound questions; a failure waits
// aiRetry. Run calls it every Tick; one round at a time.
func (s *Service) Categorize(ctx context.Context) {
	if s.Model == nil {
		return
	}
	c := &s.ai
	c.mu.Lock()
	if c.running || (!c.failedAt.IsZero() && s.now().Sub(c.failedAt) < aiRetry) {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()
	l := s.Model(ctx, channels.TierLight)
	if l == nil {
		return
	}
	known, err := s.aiInfo(ctx)
	if err != nil {
		log.Printf("iptv: ai cache: %v", err)
		return
	}
	todo, err := s.aiTodo(ctx, known)
	if err != nil {
		log.Printf("iptv: ai categories: %v", err)
		return
	}
	langs := false
	for i := 0; i < aiRound && len(todo) > 0 && ctx.Err() == nil; i++ {
		part := todo[:min(aiBatch, len(todo))]
		todo = todo[len(part):]
		got, err := askCategories(ctx, l, part)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("iptv: ai categories: %v", err)
				c.mu.Lock()
				c.failedAt = s.now()
				c.mu.Unlock()
			}
			return
		}
		if err := s.storeAI(ctx, got); err != nil {
			log.Printf("iptv: ai cache: %v", err)
			return
		}
		langs = langs || anyLangs(got)
	}
	if langs && s.langFilters(ctx) {
		// the answers' languages: a source's language filter hides more
		s.srcMu.Lock()
		err := s.Sync(ctx)
		s.srcMu.Unlock()
		if err != nil && ctx.Err() == nil {
			log.Printf("iptv: sync: %v", err)
		}
	}
}

func anyLangs(m map[string]AIInfo) bool {
	for _, x := range m {
		if x.Lang != "" {
			return true
		}
	}
	return false
}

// langFilters: a source has a language filter.
func (s *Service) langFilters(ctx context.Context) bool {
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		return false
	}
	for _, src := range srcs {
		if len(src.Langs) > 0 {
			return true
		}
	}
	return false
}

// aiTodo: the shown external channels (by their row's name) without an
// answer, one per key.
func (s *Service) aiTodo(ctx context.Context, known map[string]AIInfo) ([]aiItem, error) {
	ms, err := s.Store.AllMembers(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.externalRows(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []aiItem
	for _, m := range ms {
		if !m.Present || !m.Shown || m.ChannelID == 0 {
			continue
		}
		r, ok := rows[m.ChannelID]
		if !ok || !r.Enabled {
			continue
		}
		k := aiKey(r.Name)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := known[k]; ok {
			continue
		}
		out = append(out, aiItem{key: k, name: r.Name, group: m.Group, tvgID: m.TvgID})
	}
	return out, nil
}

func (s *Service) storeAI(ctx context.Context, got map[string]AIInfo) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	for k, x := range got {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iptv_channel_ai (name_key, cats_json, lang, made_ms) VALUES (?,?,?,?)
			ON CONFLICT(name_key) DO UPDATE SET cats_json=excluded.cats_json, lang=excluded.lang, made_ms=excluded.made_ms`,
			k, jsonText(x.Cats), x.Lang, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c := &s.ai
	c.mu.Lock()
	for k, x := range got {
		c.m[k] = x
	}
	c.mu.Unlock()
	return nil
}

const aiCategoriesSystem = `You sort TV channels of an IPTV playlist. For each numbered channel (its name, its playlist group, its tvg-id) answer:
"c": one to three category codes from this list only: %s;
"l": the ISO 639-1 code of the language it broadcasts in ("" when you cannot tell).
Answer one JSON object only: {"1":{"c":["news"],"l":"ru"},"2":{...}}, a key for every number.`

// askCategories asks the model about items; an item it gave nothing
// usable for is stored as known-empty (it is not asked again).
func askCategories(ctx context.Context, l *channels.LLM, items []aiItem) (map[string]AIInfo, error) {
	codes := make([]string, len(CategoryList))
	for i, c := range CategoryList {
		codes[i] = c.Code
	}
	var b strings.Builder
	for i, it := range items {
		fmt.Fprintf(&b, "%d. %s", i+1, it.name)
		if g := strings.TrimSpace(it.group); g != "" {
			fmt.Fprintf(&b, " | group: %s", g)
		}
		if id := strings.TrimSpace(it.tvgID); id != "" {
			fmt.Fprintf(&b, " | tvg-id: %s", id)
		}
		b.WriteByte('\n')
	}
	raw, err := l.AskJSON(ctx, fmt.Sprintf(aiCategoriesSystem, strings.Join(codes, ", ")), b.String())
	if err != nil {
		return nil, err
	}
	var ans map[string]struct {
		C []string `json:"c"`
		L string   `json:"l"`
	}
	if err := json.Unmarshal([]byte(raw), &ans); err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	out := make(map[string]AIInfo, len(items))
	for i, it := range items {
		a := ans[fmt.Sprint(i+1)]
		x := AIInfo{Cats: []string{}}
		for _, c := range a.C {
			c = strings.ToLower(strings.TrimSpace(c))
			if CategoryName(c) != "" && !slices.Contains(x.Cats, c) && len(x.Cats) < 3 {
				x.Cats = append(x.Cats, c)
			}
		}
		if lang := strings.ToLower(strings.TrimSpace(a.L)); len(lang) == 2 {
			x.Lang = lang
		}
		out[it.key] = x
	}
	return out, nil
}
