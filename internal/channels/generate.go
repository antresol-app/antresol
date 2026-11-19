package channels

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// Generated channels: how much a channel needs to be made, and where the
// numbers of each category start.
const (
	MinTitles   = 6  // movies or series of a genre, a decade, kids…
	MinEpisodes = 8  // a series' own channel
	MaxSeries   = 30 // series channels at most (most episodes first)
	// DefaultMinRuntime: a generated channel needs at least this much to
	// play, or it repeats itself within a couple of days.
	DefaultMinRuntime = 48 * time.Hour
	// DefaultFranchiseMinRuntime: what a franchise channel needs. A
	// franchise is a marathon whose loop is the point: 48 h would leave
	// almost none (the Harry Potter films are about 19 h); 5 h keeps a
	// trilogy and drops a lone pair of short films.
	DefaultFranchiseMinRuntime = 5 * time.Hour
	// MinFranchiseTitles: a franchise channel has at least these many titles.
	MinFranchiseTitles = 2
)

// Category names of generated channels (TiviMate's groups).
const (
	CatGeneral = "Кино и сериалы"
	CatGenres  = "Кино по жанрам"
	CatDecades = "Кино по десятилетиям"
	CatKids    = "Детям"
	CatDocs    = "Документальное"
	CatSeries  = "Сериалы нон-стоп"
	CatMine    = "Моё"
	// CatFranchises: a franchise's titles in its order (franchise.go)
	CatFranchises = "Франшизы"
)

// genreChannel names a genre's channel (keys: i18n.GenreKey).
var genreChannel = map[string]string{
	"боевик": "Боевики", "приключения": "Приключения", "мультфильм": "Мультфильмы", "комедия": "Комедии",
	"криминал": "Криминал", "драма": "Драмы", "семейный": "Семейное кино", "фэнтези": "Фэнтези",
	"история": "Исторические фильмы", "ужасы": "Ужасы", "музыка": "Музыкальные фильмы", "детектив": "Детективы",
	"мелодрама": "Мелодрамы", "фантастика": "Фантастика", "триллер": "Триллеры", "военный": "Военное кино",
	"вестерн": "Вестерны",
}

var kidsGenres = []string{"мультфильм", "семейный", "детский"}

const docsGenre = "документальный"

// animationGenre is the genre of animated films and series (i18n.GenreKey).
const animationGenre = "мультфильм"

// planned is a generated channel the library has content for.
type planned struct {
	Channel
	from int // the old number ranges (kept for the plan's order only)
}

// Generator makes and keeps the generated channels.
type Generator struct {
	Store   *Store
	Catalog Catalog
	// MinRuntime: what a generated channel needs to play (0: DefaultMinRuntime).
	MinRuntime time.Duration
	// FranchiseMinRuntime: what a franchise channel needs (0:
	// DefaultFranchiseMinRuntime).
	FranchiseMinRuntime time.Duration

	sync sync.Mutex // one Sync at a time (a refresh and an admin's request overlap)
}

// Plan returns the generated channels the library has enough for.
func (g *Generator) Plan(ctx context.Context) ([]planned, error) {
	units, err := g.Catalog.Units(ctx)
	if err != nil {
		return nil, err
	}
	progs, err := g.Catalog.Progs(ctx, units)
	if err != nil {
		return nil, err
	}
	var movies, series []Unit
	for _, u := range units {
		if len(progs[u.ID]) == 0 {
			continue
		}
		if u.Kind == library.KindMovie {
			movies = append(movies, u)
		} else {
			series = append(series, u)
		}
	}
	runtime := func(us []Unit) time.Duration {
		var ms int64
		for _, u := range us {
			for _, p := range progs[u.ID] {
				for _, part := range p.Parts {
					ms += part.DurationMs
				}
			}
		}
		return time.Duration(ms) * time.Millisecond
	}
	minRuntime := g.MinRuntime
	if minRuntime == 0 {
		minRuntime = DefaultMinRuntime
	}
	enough := func(us []Unit) bool { return len(us) >= MinTitles && runtime(us) >= minRuntime }
	var out []planned
	add := func(key, name, cat string, from int, r Rules) {
		out = append(out, planned{Channel: Channel{Name: name, Category: cat, AutoKey: key, Rules: r, Enabled: true}, from: from})
	}
	if enough(movies) {
		add("movies", "Все фильмы", CatGeneral, 1, Rules{Kinds: []string{library.KindMovie}})
	}
	// animated series get their own channel and stay out of «Все сериалы»
	toons := Rules{Kinds: []string{library.KindSeries}, Genres: []string{animationGenre}, SkipSegments: true}
	hasToons := enough(Match(series, toons))
	all := Rules{Kinds: []string{library.KindSeries}, SkipSegments: true}
	if hasToons {
		all.NotGenres = []string{animationGenre}
	}
	if enough(Match(series, all)) {
		add("series", "Все сериалы", CatGeneral, 1, all)
	}
	if hasToons {
		add("toons", "Мультсериалы", CatGeneral, 1, toons)
	}
	// genres of movies, most titles first
	count := map[string]int{}
	for _, u := range movies {
		for _, gk := range dedupe(u.Genres) {
			count[gk]++
		}
	}
	var genres []string
	for gk, n := range count {
		if n >= MinTitles && genreChannel[gk] != "" && enough(Match(movies, Rules{Genres: []string{gk}})) {
			genres = append(genres, gk)
		}
	}
	sort.Slice(genres, func(i, j int) bool {
		if count[genres[i]] != count[genres[j]] {
			return count[genres[i]] > count[genres[j]]
		}
		return genres[i] < genres[j]
	})
	for _, gk := range genres {
		add("genre:"+gk, genreChannel[gk], CatGenres, 10, Rules{Kinds: []string{library.KindMovie}, Genres: []string{gk}})
	}
	// decades of movies
	decades := map[int]int{}
	for _, u := range movies {
		if u.Year >= 1900 {
			decades[u.Year/10*10]++
		}
	}
	var ds []int
	for d, n := range decades {
		if n >= MinTitles && enough(Match(movies, Rules{YearFrom: d, YearTo: d + 9})) {
			ds = append(ds, d)
		}
	}
	sort.Ints(ds)
	for _, d := range ds {
		add("decade:"+strconv.Itoa(d), DecadeName(d), CatDecades, 40,
			Rules{Kinds: []string{library.KindMovie}, YearFrom: d, YearTo: d + 9})
	}
	// franchises in their order (the background pass's, franchise_pass.go)
	if err := g.planFranchises(ctx, append(append([]Unit{}, movies...), series...), progs, add); err != nil {
		return nil, err
	}
	kids := Rules{Genres: kidsGenres, MaxRating: "6+", SkipSegments: true}
	if enough(Match(append(append([]Unit{}, movies...), series...), kids)) {
		add("kids", "Детский", CatKids, 50, kids)
	}
	docs := Rules{Genres: []string{docsGenre}}
	if enough(Match(append(append([]Unit{}, movies...), series...), docs)) {
		add("docs", "Документальное", CatDocs, 60, docs)
	}
	// series marathons: most episodes first
	long := append([]Unit(nil), series...)
	sort.SliceStable(long, func(i, j int) bool { return len(progs[long[i].ID]) > len(progs[long[j].ID]) })
	n := 0
	for _, u := range long {
		if n == MaxSeries || len(progs[u.ID]) < MinEpisodes {
			break
		}
		if runtime([]Unit{u}) < minRuntime {
			continue // a short series repeats within days: no channel of its own
		}
		n++
		add("series:"+strconv.FormatInt(u.ID, 10), u.Title, CatSeries, 100, Rules{Titles: []int64{u.ID}, Order: OrderOrdered, SkipSegments: true})
	}
	// each profile's own library
	owners, err := g.owners(ctx)
	if err != nil {
		return nil, err
	}
	playable := map[int64]bool{}
	for _, u := range append(append([]Unit{}, movies...), series...) {
		playable[u.ID] = true
	}
	for _, o := range owners {
		k := 0
		for _, id := range o.items {
			if playable[id] {
				k++
			}
		}
		if k >= MinTitles {
			p := planned{Channel: Channel{Name: "Моя медиатека", Category: CatMine, AutoKey: "mine:" + strconv.FormatInt(o.uid, 10),
				UserID: o.uid, Rules: Rules{LibraryUser: o.uid}, Enabled: true}, from: 200}
			out = append(out, p)
		}
	}
	return out, nil
}

// planFranchises adds a channel of each franchise with at least
// MinFranchiseTitles playable titles and FranchiseMinRuntime of them: its
// titles in the effective order (an admin's order kept), played in order,
// episodes without their openings and credits.
func (g *Generator) planFranchises(ctx context.Context, units []Unit, progs map[int64][]Prog, add func(key, name, cat string, from int, r Rules)) error {
	fs, err := g.Store.Franchises(ctx)
	if err != nil {
		return err
	}
	min := g.FranchiseMinRuntime
	if min == 0 {
		min = DefaultFranchiseMinRuntime
	}
	playable := map[int64]bool{}
	for _, u := range units {
		playable[u.ID] = true
	}
	for _, f := range fs {
		var picks []Pick
		var ms int64
		for _, id := range f.Effective() {
			if !playable[id] {
				continue
			}
			picks = append(picks, Pick{Kind: PickTitle, ID: id})
			for _, p := range progs[id] {
				for _, part := range p.Parts {
					ms += part.DurationMs
				}
			}
		}
		if len(picks) < MinFranchiseTitles || time.Duration(ms)*time.Millisecond < min {
			continue
		}
		name := fitFranchiseName(f.Name)
		if name == "" { // a name the channel store would refuse
			name = franchiseFallbackName(f.Key)
		}
		add("franchise:"+f.Key, name, CatFranchises, 45, Rules{Picks: picks, Order: OrderOrdered, SkipSegments: true})
	}
	return nil
}

// franchiseFallbackName is the channel name of a franchise whose own name
// is unusable: «Франшиза 10» for key c10.
func franchiseFallbackName(key string) string {
	return "Франшиза " + strings.TrimPrefix(key, "c")
}

// DecadeName is the channel name of a decade: «Кино 80-х», «Кино 2000-х».
func DecadeName(d int) string {
	if d >= 2000 {
		return fmt.Sprintf("Кино %d-х", d)
	}
	return fmt.Sprintf("Кино %02d-х", d%100)
}

func dedupe(s []string) []string {
	var out []string
	for _, x := range s {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

type owner struct {
	uid   int64
	items []int64
}

func (g *Generator) owners(ctx context.Context) ([]owner, error) {
	rows, err := g.Store.DB.QueryContext(ctx, `SELECT o.user_id, o.item_id FROM item_owners o JOIN users u ON u.id=o.user_id
	  WHERE u.is_kid=0 ORDER BY o.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []owner
	for rows.Next() {
		var uid, id int64
		if err := rows.Scan(&uid, &id); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].uid != uid {
			out = append(out, owner{uid: uid})
		}
		out[len(out)-1].items = append(out[len(out)-1].items, id)
	}
	return out, rows.Err()
}

// SyncResult counts what Sync did.
type SyncResult struct {
	Created, Updated, Disabled int
}

// Sync makes the planned generated channels that are missing, brings the
// ones nobody edited up to date (and enables them), and disables the
// unedited ones the library no longer has content for. Channels an admin
// edited are left alone, and so are the ones an owner deleted
// (channel_removed): their keys are skipped until they are given back.
func (g *Generator) Sync(ctx context.Context) (SyncResult, error) {
	g.sync.Lock()
	defer g.sync.Unlock()
	var res SyncResult
	plan, err := g.Plan(ctx)
	if err != nil {
		return res, err
	}
	all, err := g.Store.All(ctx)
	if err != nil {
		return res, err
	}
	// read after the channels: a channel deleted in between is in neither
	removed, err := g.Store.RemovedKeys(ctx)
	if err != nil {
		return res, err
	}
	have := map[string]Channel{}
	for _, c := range all {
		if c.AutoKey != "" && !c.External() && !removed[c.AutoKey] { // external rows are internal/iptv's
			have[c.AutoKey] = c
		}
	}
	want := map[string]bool{}
	for _, p := range plan {
		if removed[p.AutoKey] {
			continue
		}
		want[p.AutoKey] = true
		c, ok := have[p.AutoKey]
		switch {
		case !ok:
			n, err := g.Store.NextNumber(ctx, ManualFrom) // one list: the first free number
			if err != nil {
				return res, err
			}
			p.Channel.Number = n
			if _, err := g.Store.Create(ctx, p.Channel); err != nil {
				if errors.Is(err, ErrRemoved) { // deleted while this pass ran
					continue
				}
				if keyKind(p.AutoKey) == "franchise" { // one franchise must not stop every channel
					log.Printf("channels: create %s: %v", p.AutoKey, err)
					continue
				}
				return res, fmt.Errorf("channels: create %s: %w", p.AutoKey, err)
			}
			res.Created++
		case c.Edited:
		case !c.Enabled || c.Name != p.Name || c.Category != p.Category || !sameRules(c.Rules, p.Rules):
			c.Name, c.Category, c.Rules, c.Enabled = p.Name, p.Category, p.Rules, true
			if _, err := g.Store.Update(ctx, c); err != nil {
				if errors.Is(err, ErrNotFound) { // deleted while this pass ran
					continue
				}
				if keyKind(p.AutoKey) == "franchise" {
					log.Printf("channels: update %s: %v", p.AutoKey, err)
					continue
				}
				return res, fmt.Errorf("channels: update %s: %w", p.AutoKey, err)
			}
			res.Updated++
		}
	}
	for key, c := range have {
		if !want[key] && !c.Edited && c.Enabled {
			c.Enabled = false
			if _, err := g.Store.Update(ctx, c); err != nil {
				if errors.Is(err, ErrNotFound) { // deleted while this pass ran
					continue
				}
				return res, err
			}
			res.Disabled++
		}
	}
	return res, nil
}

// Edits reports whether saving c over old is an edit the generator must
// leave alone: another name, group or rules, or the channel switched off
// (Sync would switch an unedited one on again). Its number and switching it
// on are not.
func Edits(old, c Channel) bool {
	return strings.TrimSpace(c.Name) != old.Name || strings.TrimSpace(c.Category) != old.Category ||
		!sameRules(old.Rules, c.Rules) || (old.Enabled && !c.Enabled)
}

func sameRules(a, b Rules) bool {
	na, nb := Channel{Name: "x", Rules: a}, Channel{Name: "x", Rules: b}
	_ = normalize(&na)
	_ = normalize(&nb)
	return fmt.Sprint(na.Rules) == fmt.Sprint(nb.Rules)
}

// keyKind is the kind of a generated channel's key ("genre", "series"…).
func keyKind(key string) string {
	k, _, _ := strings.Cut(key, ":")
	return k
}
