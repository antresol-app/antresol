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
	// DefaultMinRuntime: a generated channel of every kind (a category, a
	// series, a franchise, a profile's library) needs at least this much to
	// play, or it repeats itself within a couple of days: a channel of three
	// or six films is not worth a number.
	DefaultMinRuntime = 48 * time.Hour
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

// Generator makes and keeps the generated channels.
type Generator struct {
	Store   *Store
	Catalog Catalog
	// MinRuntime: what a generated channel of any kind needs to play (0:
	// DefaultMinRuntime).
	MinRuntime time.Duration

	sync sync.Mutex // one Sync at a time (a refresh and an admin's request overlap)
}

// planner is one Plan's view of the library: the movies and series that have
// something to play, their programmes, and the channels made so far.
type planner struct {
	movies, series, all []Unit // all: the movies, then the series
	progs               map[int64][]Prog
	minRuntime          time.Duration
	out                 []Channel
}

// playMs is how long title id plays: all its programmes.
func (p *planner) playMs(id int64) int64 {
	var ms int64
	for _, pr := range p.progs[id] {
		for _, part := range pr.Parts {
			ms += part.DurationMs
		}
	}
	return ms
}

func (p *planner) runtime(us []Unit) time.Duration {
	var ms int64
	for _, u := range us {
		ms += p.playMs(u.ID)
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *planner) enough(us []Unit) bool {
	return len(us) >= MinTitles && p.runtime(us) >= p.minRuntime
}

func (p *planner) add(key, name, cat string, r Rules) {
	p.out = append(p.out, Channel{Name: name, Category: cat, AutoKey: key, Rules: r, Enabled: true})
}

// Plan returns the generated channels the library has enough for.
func (g *Generator) Plan(ctx context.Context) ([]Channel, error) {
	units, err := g.Catalog.Units(ctx)
	if err != nil {
		return nil, err
	}
	progs, err := g.Catalog.Progs(ctx, units)
	if err != nil {
		return nil, err
	}
	p := &planner{progs: progs, minRuntime: g.MinRuntime}
	if p.minRuntime == 0 {
		p.minRuntime = DefaultMinRuntime
	}
	for _, u := range units {
		switch {
		case len(progs[u.ID]) == 0:
		case u.Kind == library.KindMovie:
			p.movies = append(p.movies, u)
		default:
			p.series = append(p.series, u)
		}
	}
	p.all = append(append([]Unit(nil), p.movies...), p.series...)
	p.planGeneral()
	p.planGenres()
	p.planDecades()
	// franchises in their order (the background pass's, franchise_pass.go)
	inFranchise, err := g.planFranchises(ctx, p)
	if err != nil {
		return nil, err
	}
	kids := Rules{Genres: kidsGenres, MaxRating: "6+", SkipSegments: true}
	if p.enough(Match(p.all, kids)) {
		p.add("kids", "Детский", CatKids, kids)
	}
	docs := Rules{Genres: []string{docsGenre}}
	if p.enough(Match(p.all, docs)) {
		p.add("docs", "Документальное", CatDocs, docs)
	}
	p.planMarathons(inFranchise)
	if err := g.planOwned(ctx, p); err != nil {
		return nil, err
	}
	return p.out, nil
}

// planGeneral: all films, all series (animated ones apart).
func (p *planner) planGeneral() {
	if p.enough(p.movies) {
		p.add("movies", "Все фильмы", CatGeneral, Rules{Kinds: []string{library.KindMovie}})
	}
	// animated series get their own channel and stay out of «Все сериалы»
	toons := Rules{Kinds: []string{library.KindSeries}, Genres: []string{animationGenre}, SkipSegments: true}
	hasToons := p.enough(Match(p.series, toons))
	all := Rules{Kinds: []string{library.KindSeries}, SkipSegments: true}
	if hasToons {
		all.NotGenres = []string{animationGenre}
	}
	if p.enough(Match(p.series, all)) {
		p.add("series", "Все сериалы", CatGeneral, all)
	}
	if hasToons {
		p.add("toons", "Мультсериалы", CatGeneral, toons)
	}
}

// planGenres: a channel of each genre of movies, most titles first.
func (p *planner) planGenres() {
	count := map[string]int{}
	for _, u := range p.movies {
		for _, gk := range dedupe(u.Genres) {
			count[gk]++
		}
	}
	var genres []string
	for gk, n := range count {
		if n >= MinTitles && genreChannel[gk] != "" && p.enough(Match(p.movies, Rules{Genres: []string{gk}})) {
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
		p.add("genre:"+gk, genreChannel[gk], CatGenres, Rules{Kinds: []string{library.KindMovie}, Genres: []string{gk}})
	}
}

// planDecades: a channel of each decade of movies.
func (p *planner) planDecades() {
	decades := map[int]int{}
	for _, u := range p.movies {
		if u.Year >= 1900 {
			decades[u.Year/10*10]++
		}
	}
	var ds []int
	for d, n := range decades {
		if n >= MinTitles && p.enough(Match(p.movies, Rules{YearFrom: d, YearTo: d + 9})) {
			ds = append(ds, d)
		}
	}
	sort.Ints(ds)
	for _, d := range ds {
		p.add("decade:"+strconv.Itoa(d), DecadeName(d), CatDecades,
			Rules{Kinds: []string{library.KindMovie}, YearFrom: d, YearTo: d + 9})
	}
}

// planMarathons: a series' own channel, most episodes first.
func (p *planner) planMarathons(inFranchise map[int64]bool) {
	long := append([]Unit(nil), p.series...)
	sort.SliceStable(long, func(i, j int) bool { return len(p.progs[long[i].ID]) > len(p.progs[long[j].ID]) })
	n := 0
	for _, u := range long {
		if n == MaxSeries || len(p.progs[u.ID]) < MinEpisodes {
			break
		}
		if p.runtime([]Unit{u}) < p.minRuntime {
			continue // a short series repeats within days: no channel of its own
		}
		if inFranchise[u.ID] {
			continue // its franchise's channel plays it
		}
		n++
		name := fitFranchiseName(u.Title) // a title the channel store would refuse is cut to fit
		p.add("series:"+strconv.FormatInt(u.ID, 10), name, CatSeries, Rules{Titles: []int64{u.ID}, Order: OrderOrdered, SkipSegments: true})
	}
}

// planOwned: each profile's own library.
func (g *Generator) planOwned(ctx context.Context, p *planner) error {
	owners, err := g.owners(ctx)
	if err != nil {
		return err
	}
	playable := map[int64]Unit{}
	for _, u := range p.all {
		playable[u.ID] = u
	}
	for _, o := range owners {
		var mine []Unit
		for _, id := range o.items {
			if u, ok := playable[id]; ok {
				mine = append(mine, u)
			}
		}
		if p.enough(mine) {
			p.out = append(p.out, Channel{Name: "Моя медиатека", Category: CatMine, AutoKey: "mine:" + strconv.FormatInt(o.uid, 10),
				UserID: o.uid, Rules: Rules{LibraryUser: o.uid}, Enabled: true})
		}
	}
	return nil
}

// planFranchises adds a channel of each franchise with at least
// MinFranchiseTitles playable titles and p's minimum runtime: its titles in
// the effective order (an admin's order kept), played in order, episodes
// without their openings and credits. It returns the titles those channels
// play: a series of a franchise gets no channel of its own.
func (g *Generator) planFranchises(ctx context.Context, p *planner) (map[int64]bool, error) {
	fs, err := g.Store.Franchises(ctx)
	if err != nil {
		return nil, err
	}
	covered := map[int64]bool{}
	playable := map[int64]bool{}
	for _, u := range p.all {
		playable[u.ID] = true
	}
	for _, f := range fs {
		var picks []Pick
		var ms int64
		for _, id := range f.Effective() {
			if playable[id] {
				picks = append(picks, Pick{Kind: PickTitle, ID: id})
				ms += p.playMs(id)
			}
		}
		if len(picks) < MinFranchiseTitles || time.Duration(ms)*time.Millisecond < p.minRuntime {
			continue
		}
		name := fitFranchiseName(f.Name)
		if name == "" { // a name the channel store would refuse
			name = franchiseFallbackName(f.Key)
		}
		p.add("franchise:"+f.Key, name, CatFranchises, Rules{Picks: picks, Order: OrderOrdered, SkipSegments: true})
		for _, pk := range picks {
			covered[pk.ID] = true
		}
	}
	return covered, nil
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

// dedupe is s trimmed, without empty entries and with each once (nil when
// none is left).
func dedupe(s []string) []string {
	var out []string
	for _, x := range s {
		if x = strings.TrimSpace(x); x != "" && !contains(out, x) {
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
	Created, Updated, Dropped int
}

// Sync makes the planned generated channels that are missing, brings the
// ones nobody edited up to date (and enables them), and drops the unedited
// ones the library no longer has enough for (a switched-off channel nobody
// asked for is clutter: it comes back by itself once the library has
// enough). Channels an admin edited are left alone, and so are the ones an
// owner deleted (channel_removed): their keys are skipped until they are
// given back.
func (g *Generator) Sync(ctx context.Context) (SyncResult, error) {
	g.sync.Lock()
	defer g.sync.Unlock()
	return g.syncLocked(ctx)
}

// Reset rebuilds the generated set: every deleted generated channel is given
// back (channel_removed is cleared), and Sync then makes what the library has
// enough for and drops the rest. Channels an admin made or edited stay.
func (g *Generator) Reset(ctx context.Context) (SyncResult, error) {
	g.sync.Lock()
	defer g.sync.Unlock()
	if _, err := g.Store.DB.ExecContext(ctx, `DELETE FROM channel_removed`); err != nil {
		return SyncResult{}, err
	}
	return g.syncLocked(ctx)
}

// skipKey logs a channel the store refused and lets the pass go on: one
// channel must not stop the others (nil); a pass that was cancelled stops
// (err).
func skipKey(ctx context.Context, verb, key string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("channels: %s %s: %w", verb, key, err)
	}
	log.Printf("channels: %s %s: %v", verb, key, err)
	return nil
}

func (g *Generator) syncLocked(ctx context.Context) (SyncResult, error) {
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
			p.Number = n
			if _, err := g.Store.Create(ctx, p); err != nil {
				if errors.Is(err, ErrRemoved) { // deleted while this pass ran
					continue
				}
				if err := skipKey(ctx, "create", p.AutoKey, err); err != nil {
					return res, err
				}
				continue
			}
			res.Created++
		case c.Edited:
		case !c.Enabled || c.Name != p.Name || c.Category != p.Category || !sameRules(c.Rules, p.Rules):
			c.Name, c.Category, c.Rules, c.Enabled = p.Name, p.Category, p.Rules, true
			if _, err := g.Store.Update(ctx, c); err != nil {
				if errors.Is(err, ErrNotFound) { // deleted while this pass ran
					continue
				}
				if err := skipKey(ctx, "update", p.AutoKey, err); err != nil {
					return res, err
				}
				continue
			}
			res.Updated++
		}
	}
	for key, c := range have {
		if !want[key] && !c.Edited {
			if err := g.Store.Drop(ctx, c.ID); err != nil {
				if errors.Is(err, ErrNotFound) { // deleted while this pass ran
					continue
				}
				return res, err
			}
			res.Dropped++
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
