package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/library"
)

// The channel editor's tags: what the library has to pick by (countries,
// original languages, themes), a live preview of a channel's rules, and a
// channel read from a sentence («Опишите канал»).

// tagCount is a tag the library has and how many titles carry it.
type tagCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// visibleUnits: the library's movies and series this profile may see.
func (a *channelsAPI) visibleUnits(r *http.Request, u auth.User) ([]channels.Unit, error) {
	units, err := a.svc.Builder.Catalog.Units(r.Context())
	if err != nil {
		return nil, err
	}
	limit := u.RatingLimit()
	out := units[:0:0]
	for _, x := range units {
		if library.RatingAllowed(limit, library.Item{Kind: x.Kind, Rating: x.Rating}) {
			out = append(out, x)
		}
	}
	return out, nil
}

// tagOptions: the countries, original languages and themes of the units,
// the most common first (themes: the 120 most common, carried by two or more).
func tagOptions(units []channels.Unit) (countries, languages, keywords []tagCount, yearMin, yearMax int) {
	cs, ls, ks := map[string]int{}, map[string]int{}, map[string]int{}
	for _, u := range units {
		for _, c := range u.Countries {
			cs[strings.ToUpper(c)]++
		}
		if u.OrigLang != "" {
			ls[u.OrigLang]++
		}
		for _, k := range u.Keywords {
			ks[k]++
		}
		if u.Year >= 1880 {
			if yearMin == 0 || u.Year < yearMin {
				yearMin = u.Year
			}
			if u.Year > yearMax {
				yearMax = u.Year
			}
		}
	}
	list := func(m map[string]int, min, max int) []tagCount {
		out := []tagCount{}
		for k, n := range m {
			if n >= min {
				out = append(out, tagCount{k, n})
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Key < out[j].Key
		})
		if max > 0 && len(out) > max {
			out = out[:max]
		}
		return out
	}
	return list(cs, 1, 0), list(ls, 1, 0), list(ks, 2, 120), yearMin, yearMax
}

type previewTitle struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"`
}

// POST /api/channels/preview {rules}: what a channel of these rules would
// play now — how many movies and series, and the first of them (a stable
// sample: the newest years first).
func (a *channelsAPI) preview(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var body struct {
		Rules channels.Rules `json:"rules"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "Опишите, что показывать")
		return
	}
	rules := body.Rules
	rules.Picks, rules.Titles = nil, nil // a list of its own is seen as it is
	if !u.IsAdmin {
		rules.LibraryUser = 0
		if body.Rules.LibraryUser == u.ID {
			rules.LibraryUser = u.ID
		}
	}
	units, err := a.visibleUnits(r, u)
	if err != nil {
		internalErr(w, r, "channel preview", err)
		return
	}
	pool, err := a.svc.Builder.Catalog.PoolOf(r.Context(), units, rules)
	if err != nil {
		internalErr(w, r, "channel preview", err)
		return
	}
	movies, series, episodes := 0, 0, 0
	for _, x := range pool.Units {
		if x.Kind == library.KindSeries {
			series++
			episodes += len(pool.Progs[x.ID])
		} else {
			movies++
		}
	}
	sample := append([]channels.Unit(nil), pool.Units...)
	sort.SliceStable(sample, func(i, j int) bool {
		pi, pj := sample[i].Poster != "", sample[j].Poster != ""
		if pi != pj {
			return pi
		}
		return sample[i].Year > sample[j].Year
	})
	if len(sample) > 12 {
		sample = sample[:12]
	}
	items := make([]previewTitle, 0, len(sample))
	for _, x := range sample {
		items = append(items, previewTitle{ID: x.ID, Kind: x.Kind, Title: x.Title, Year: x.Year, Poster: library.ImageURL(x.ID, "poster", x.Poster)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies, "series": series, "episodes": episodes, "items": items})
}

// POST /api/channels/suggest {text}: a channel read from a sentence — its
// name and its rules as tags, for the editor to show before anything is
// saved. The words are read here; a language model (VAULT_LLM_URL) helps
// when the server has one.
func (a *channelsAPI) suggest(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeErr(w, r, http.StatusBadRequest, "Опишите, что показывать")
		return
	}
	units, err := a.visibleUnits(r, u)
	if err != nil {
		internalErr(w, r, "channel suggest", err)
		return
	}
	writeJSON(w, http.StatusOK, a.svc.Suggest(r.Context(), body.Text, channels.VocabularyOf(units)))
}
