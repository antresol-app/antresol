package channels

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
)

// Generated channels: how much a channel needs to be made, and where the
// numbers of each category start.
const (
	MinTitles   = 6  // movies or series of a genre, a decade, kids…
	MinEpisodes = 8  // a series' own channel
	MaxSeries   = 30 // series channels at most (most episodes first)
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

// planned is a generated channel the library has content for.
type planned struct {
	Channel
	from int // the old number ranges (kept for the plan's order only)
}

// Generator makes and keeps the generated channels.
type Generator struct {
	Store   *Store
	Catalog Catalog
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
	var out []planned
	add := func(key, name, cat string, from int, r Rules) {
		out = append(out, planned{Channel: Channel{Name: name, Category: cat, AutoKey: key, Rules: r, Enabled: true}, from: from})
	}
	if len(movies) >= MinTitles {
		add("movies", "Все фильмы", CatGeneral, 1, Rules{Kinds: []string{library.KindMovie}})
	}
	if len(series) >= MinTitles {
		add("series", "Все сериалы", CatGeneral, 1, Rules{Kinds: []string{library.KindSeries}})
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
		if n >= MinTitles && genreChannel[gk] != "" {
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
		if n >= MinTitles {
			ds = append(ds, d)
		}
	}
	sort.Ints(ds)
	for _, d := range ds {
		add("decade:"+strconv.Itoa(d), DecadeName(d), CatDecades, 40,
			Rules{Kinds: []string{library.KindMovie}, YearFrom: d, YearTo: d + 9})
	}
	kids := Rules{Genres: kidsGenres, MaxRating: "6+"}
	if len(Match(append(append([]Unit{}, movies...), series...), kids)) >= MinTitles {
		add("kids", "Детский", CatKids, 50, kids)
	}
	docs := Rules{Genres: []string{docsGenre}}
	if len(Match(append(append([]Unit{}, movies...), series...), docs)) >= MinTitles {
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
		n++
		add("series:"+strconv.FormatInt(u.ID, 10), u.Title, CatSeries, 100, Rules{Titles: []int64{u.ID}, Order: OrderOrdered})
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
// edited (or deleted: disabled) are left alone.
func (g *Generator) Sync(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	plan, err := g.Plan(ctx)
	if err != nil {
		return res, err
	}
	all, err := g.Store.All(ctx)
	if err != nil {
		return res, err
	}
	have := map[string]Channel{}
	for _, c := range all {
		if c.AutoKey != "" && !c.External() { // external rows are internal/iptv's
			have[c.AutoKey] = c
		}
	}
	want := map[string]bool{}
	for _, p := range plan {
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
				return res, fmt.Errorf("channels: create %s: %w", p.AutoKey, err)
			}
			res.Created++
		case c.Edited:
		case !c.Enabled || c.Name != p.Name || c.Category != p.Category || !sameRules(c.Rules, p.Rules):
			c.Name, c.Category, c.Rules, c.Enabled = p.Name, p.Category, p.Rules, true
			if _, err := g.Store.Update(ctx, c); err != nil {
				return res, fmt.Errorf("channels: update %s: %w", p.AutoKey, err)
			}
			res.Updated++
		}
	}
	for key, c := range have {
		if !want[key] && !c.Edited && c.Enabled {
			c.Enabled = false
			if _, err := g.Store.Update(ctx, c); err != nil {
				return res, err
			}
			res.Disabled++
		}
	}
	return res, nil
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
