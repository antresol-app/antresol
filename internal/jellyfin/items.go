package jellyfin

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/library"
)

// mountLibrary registers the library routes (behind requireToken).
func (s *Server) mountLibrary(r chi.Router) {
	r.Get("/items", s.listItems)
	r.Get("/users/{userId}/items", s.listItems)
	r.Get("/items/latest", s.latest)
	r.Get("/users/{userId}/items/latest", s.latest)
	r.Get("/useritems/resume", s.resume)
	r.Get("/users/{userId}/items/resume", s.resume)
	r.Get("/items/filters", s.itemFilters)
	r.Get("/items/{id}", s.getItem)
	r.Get("/users/{userId}/items/{id}", s.getItem)
	r.Get("/genres", s.genres)
	r.Get("/persons", s.emptyResult)
	r.Get("/studios", s.emptyResult)
	r.Get("/items/{id}/similar", s.similar)
	r.Get("/items/{id}/intros", s.emptyResult)
	r.Get("/mediasegments/{id}", s.emptyResult)
	r.Get("/items/{id}/specialfeatures", s.emptyArray)
	r.Get("/items/{id}/localtrailers", s.emptyArray)
	r.Get("/shows/{id}/seasons", s.seasons)
	r.Get("/shows/{id}/episodes", s.episodes)
	r.Get("/shows/nextup", s.nextUp)
}

func (s *Server) emptyResult(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, QueryResult[BaseItemDto]{Items: []BaseItemDto{}})
}

func (s *Server) emptyArray(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte("[]"))
}

// typeSet reads includeItemTypes/excludeItemTypes as Vault kinds. known
// is false when the client named types, none of which Vault has
// (BoxSet, MusicAlbum…): such a list is empty.
func typeSet(r *http.Request, key string) (set map[string]bool, named bool) {
	set = map[string]bool{}
	for _, t := range qList(r, key) {
		named = true
		if k := kinds[strings.ToLower(t)]; k != "" {
			set[k] = true
		}
	}
	return set, named
}

// viewKinds are the kinds a library view lists (recursive: with the
// seasons and episodes of the series).
func viewKinds(view int64, recursive bool) []string {
	switch view {
	case viewNums["movies"]:
		return []string{library.KindMovie}
	case viewNums["tvshows"]:
		if recursive {
			return []string{library.KindSeries, library.KindSeason, library.KindEpisode}
		}
		return []string{library.KindSeries}
	}
	return nil
}

// listKind lists all items of a kind within the profile's limit (in
// SQL for movies and series, which carry the rating; seasons and
// episodes take their series' rating: Visible decides).
func (c *conv) listKind(kind, sort string, limit int) ([]library.Item, error) {
	q := library.ListQuery{Kind: kind, Sort: sort, Limit: limit}
	if !library.HasRatedParent(kind) {
		q.MaxRating = c.limit
	}
	items, _, err := c.s.Library.List(c.ctx, q)
	return items, err
}

// keepVisible drops the items the profile may not see and kinds not served.
func (c *conv) keepVisible(items []library.Item) ([]library.Item, error) {
	out := items[:0:0]
	for _, it := range items {
		if jfTypes[it.Kind] == "" {
			continue
		}
		ok, err := c.visible(it)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// searchLimit bounds the matches /Items?searchTerm filters and pages.
const searchLimit = 200

// collect gathers the candidates of /Items: by ids, by search, under a
// parent (a view, a series, a season) or the whole library. ordered: the
// order is meaningful (ids, children) when no sortBy is given.
func (c *conv) collect(r *http.Request) (items []library.Item, ordered bool, err error) {
	include, named := typeSet(r, "includeItemTypes")
	if named && len(include) == 0 {
		return nil, false, nil
	}
	recursive := qBool(r, "recursive", false) || qBool(r, "isRecursive", false)
	pick := func(def ...string) []string {
		if len(include) == 0 {
			return def
		}
		var out []string
		for _, k := range []string{library.KindMovie, library.KindSeries, library.KindSeason, library.KindEpisode} {
			if include[k] {
				out = append(out, k)
			}
		}
		return out
	}

	if ids := qList(r, "ids"); len(ids) > 0 {
		for _, v := range ids {
			k, id, ok := ParseID(v)
			if !ok || k != kindItem {
				continue
			}
			it, err := c.item(id)
			if errors.Is(err, library.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, false, err
			}
			items = append(items, it)
		}
		return items, true, nil
	}

	if term := strings.TrimSpace(qv(r, "searchTerm")); term != "" {
		var ks []string
		for _, k := range pick(library.KindMovie, library.KindSeries) {
			if k == library.KindMovie || k == library.KindSeries {
				ks = append(ks, k)
			}
		}
		items, err := c.s.Library.SearchKinds(c.ctx, term, ks, searchLimit, c.limit)
		return items, true, err
	}

	var ks []string
	if p := qv(r, "parentId"); p != "" {
		k, id, ok := ParseID(p)
		switch {
		case !ok:
			return nil, false, nil
		case k == kindView:
			vk := viewKinds(id, recursive)
			if len(vk) == 0 {
				return nil, false, nil
			}
			if len(include) == 0 {
				ks = vk[:1]
			} else {
				for _, k := range pick() {
					if slices.Contains(vk, k) {
						ks = append(ks, k)
					}
				}
			}
		case k == kindItem:
			parent, ok, err := c.visibleItem(id)
			if err != nil || !ok {
				return nil, false, err
			}
			items, err := c.descendants(parent, recursive)
			return items, true, err
		default:
			return nil, false, nil
		}
	} else {
		ks = pick(library.KindMovie, library.KindSeries)
	}
	for _, k := range ks {
		got, err := c.listKind(k, "title", 0)
		if err != nil {
			return nil, false, err
		}
		items = append(items, got...)
	}
	return items, false, nil
}

// descendants are a series' seasons (recursive: and their episodes) or a
// season's episodes, in order.
func (c *conv) descendants(parent library.Item, recursive bool) ([]library.Item, error) {
	if parent.Kind != library.KindSeries && parent.Kind != library.KindSeason {
		return nil, nil
	}
	children, err := c.s.Library.Children(c.ctx, parent.ID)
	if err != nil || !recursive || parent.Kind != library.KindSeries {
		return children, err
	}
	var out []library.Item
	for _, se := range children {
		out = append(out, se)
		eps, err := c.s.Library.Children(c.ctx, se.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, eps...)
	}
	return out, nil
}

// filter applies excludeItemTypes, includeItemTypes, filters=…, the
// isPlayed/isFavorite flags, genres and years.
func (c *conv) filter(r *http.Request, items []library.Item) ([]library.Item, error) {
	include, _ := typeSet(r, "includeItemTypes")
	exclude, _ := typeSet(r, "excludeItemTypes")
	flags := map[string]bool{}
	for _, f := range qList(r, "filters") {
		flags[strings.ToLower(f)] = true
	}
	if v := qv(r, "isPlayed"); v != "" {
		flags["isplayed"], flags["isunplayed"] = qBool(r, "isPlayed", false), !qBool(r, "isPlayed", false)
	}
	if qBool(r, "isFavorite", false) {
		flags["isfavorite"] = true
	}
	genres := map[string]bool{}
	for _, g := range strings.Split(qv(r, "genres"), "|") {
		if g = strings.TrimSpace(g); g != "" {
			genres[strings.ToLower(g)] = true
		}
	}
	years := map[int]bool{}
	for _, y := range qList(r, "years") {
		if n, err := strconv.Atoi(y); err == nil {
			years[n] = true
		}
	}
	out := items[:0:0]
	for _, it := range items {
		ok, err := c.pass(it, include, exclude, flags, genres, years)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

func (c *conv) pass(it library.Item, include, exclude, flags map[string]bool, genres map[string]bool, years map[int]bool) (bool, error) {
	if exclude[it.Kind] || len(include) > 0 && !include[it.Kind] {
		return false, nil
	}
	folder := it.Kind == library.KindSeries || it.Kind == library.KindSeason
	if flags["isfolder"] && !folder || flags["isnotfolder"] && folder {
		return false, nil
	}
	if len(years) > 0 && !years[it.Year] {
		return false, nil
	}
	if len(genres) > 0 && !slices.ContainsFunc(metaOf(it).Genres, func(g string) bool { return genres[strings.ToLower(g)] }) {
		return false, nil
	}
	if flags["isplayed"] || flags["isunplayed"] {
		p, err := c.played(it)
		if err != nil {
			return false, err
		}
		if flags["isplayed"] && !p || flags["isunplayed"] && p {
			return false, nil
		}
	}
	if flags["isresumable"] {
		if folder {
			return false, nil
		}
		e, err := c.entry(it.ID)
		if err != nil || !resumable(e) {
			return false, err
		}
	}
	if flags["isfavorite"] {
		f, err := c.favorite(it.ID)
		if err != nil || !f {
			return false, err
		}
	}
	return true, nil
}

// sortKey is the SortName order: seasons and episodes by number.
func sortKey(it library.Item) string {
	t := it.SortTitle
	if t == "" {
		t = strings.ToLower(it.Title)
	}
	if it.Kind == library.KindSeason || it.Kind == library.KindEpisode {
		return strconv.Itoa(10000+it.SeasonNo) + strconv.Itoa(10000+it.EpisodeNo) + t
	}
	return t
}

// sortItems orders by sortBy (comma list; unknown keys: by name) and
// sortOrder. With no sortBy an ordered list stays as it is.
func (c *conv) sortItems(r *http.Request, items []library.Item, ordered bool) error {
	by := qList(r, "sortBy")
	if len(by) == 0 {
		if ordered {
			return nil
		}
		by = []string{"SortName"}
	}
	orders := qList(r, "sortOrder")
	desc := func(i int) bool {
		if len(orders) == 0 {
			return false
		}
		return strings.EqualFold(orders[min(i, len(orders)-1)], "Descending")
	}
	if strings.EqualFold(by[0], "Random") {
		rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
		return nil
	}
	played := map[int64]int64{}
	for _, b := range by {
		if strings.EqualFold(b, "DatePlayed") {
			for _, it := range items {
				e, err := c.entry(it.ID)
				if err != nil {
					return err
				}
				if e != nil {
					played[it.ID] = e.UpdatedAt.UnixNano()
				}
			}
		}
	}
	cmp := func(a, b library.Item, key string) int {
		switch strings.ToLower(key) {
		case "datecreated":
			return a.AddedAt.Compare(b.AddedAt)
		case "productionyear", "premieredate":
			return a.Year - b.Year
		case "dateplayed":
			switch x, y := played[a.ID], played[b.ID]; {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
		return strings.Compare(sortKey(a), sortKey(b))
	}
	sort.SliceStable(items, func(i, j int) bool {
		for k, key := range by {
			n := cmp(items[i], items[j], key)
			if desc(k) {
				n = -n
			}
			if n != 0 {
				return n < 0
			}
		}
		return sortKey(items[i]) < sortKey(items[j])
	})
	return nil
}

// page cuts startIndex/limit out of items (limit ≤ 0: all).
func page[T any](r *http.Request, items []T) ([]T, int) {
	start := max(qInt(r, "startIndex", 0), 0)
	if start > len(items) {
		start = len(items)
	}
	items = items[start:]
	if n := qInt(r, "limit", 0); n > 0 && n < len(items) {
		items = items[:n]
	}
	return items, start
}

// result converts a filtered, sorted list and sends its page.
func (s *Server) result(w http.ResponseWriter, r *http.Request, c *conv, items []library.Item) {
	total := len(items)
	pg, start := page(r, items)
	out, err := c.dtos(pg)
	if err != nil {
		s.internal(w, r, "items", err)
		return
	}
	writeJSON(w, QueryResult[BaseItemDto]{Items: out, TotalRecordCount: total, StartIndex: start})
}

// listItems serves /Items and /Users/{u}/Items. TotalRecordCount counts
// every visible item after all filters, before the page.
func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), fieldsOf(r))
	items, ordered, err := c.collect(r)
	if err == nil {
		items, err = c.keepVisible(items)
	}
	if err == nil {
		items, err = c.filter(r, items)
	}
	if err == nil {
		err = c.sortItems(r, items, ordered)
	}
	if err != nil {
		s.internal(w, r, "items", err)
		return
	}
	s.result(w, r, c, items)
}

// getItem serves /Items/{id}: a view, or the full item (media sources,
// streams, path, provider ids, user data whatever fields says). A hidden
// item is 404 like a missing one.
func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	if ok && k == kindView {
		for name, n := range viewNums {
			if n == id {
				writeJSON(w, s.viewDto(r.Context(), name))
				return
			}
		}
	}
	if !ok || k != kindItem {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	c := s.conv(r.Context(), userOf(r), fieldSet{fieldFull: true})
	it, ok, err := c.visibleItem(id)
	if err != nil {
		s.internal(w, r, "item", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	d, err := c.dto(it)
	if err != nil {
		s.internal(w, r, "item", err)
		return
	}
	writeJSON(w, d)
}

// resume serves the Resume shelf: movies and episodes started and not
// finished (the IsResumable rule), newest first.
func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	c := s.conv(r.Context(), u, fieldsOf(r))
	ks := []string{library.KindMovie, library.KindEpisode}
	if include, named := typeSet(r, "includeItemTypes"); named {
		ks = slices.DeleteFunc(ks, func(k string) bool { return !include[k] })
	}
	if k, id, ok := ParseID(qv(r, "parentId")); ok && k == kindView {
		vk := viewKinds(id, true)
		ks = slices.DeleteFunc(ks, func(k string) bool { return !slices.Contains(vk, k) })
	}
	var items []library.Item
	if len(ks) > 0 {
		entries, err := s.Progress.InProgress(r.Context(), u.ID, ks, 0)
		if err != nil {
			s.internal(w, r, "resume", err)
			return
		}
		for _, e := range entries {
			if !resumable(&e) {
				continue
			}
			it, ok, err := c.visibleItem(e.ItemID)
			if err != nil {
				s.internal(w, r, "resume", err)
				return
			}
			if ok {
				c.prog[e.ItemID] = &e
				items = append(items, it)
			}
		}
	}
	s.result(w, r, c, items)
}

// latest serves «Недавно добавлено»: a bare array; series for tvshows
// (by their newest episode), movies for movies.
func (s *Server) latest(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), fieldsOf(r))
	limit := qInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	kind := ""
	if k, id, ok := ParseID(qv(r, "parentId")); ok && k == kindView {
		vk := viewKinds(id, false)
		if len(vk) == 0 {
			writeJSON(w, []BaseItemDto{})
			return
		}
		kind = vk[0]
	} else if include, named := typeSet(r, "includeItemTypes"); named {
		switch {
		case include[library.KindMovie] && !include[library.KindSeries] && !include[library.KindEpisode]:
			kind = library.KindMovie
		case !include[library.KindMovie]:
			kind = library.KindSeries
		}
	}
	var items []library.Item
	var err error
	switch kind {
	case library.KindSeries:
		items, err = s.Library.RecentSeries(r.Context(), limit, c.limit)
	case "": // movies and series
		items, _, err = s.Library.List(r.Context(), library.ListQuery{Sort: "added", Limit: limit, MaxRating: c.limit, NoTotal: true})
	default:
		items, err = c.listKind(kind, "added", limit)
	}
	if err == nil {
		items, err = c.keepVisible(items)
	}
	var out []BaseItemDto
	if err == nil {
		out, err = c.dtos(items)
	}
	if err != nil {
		s.internal(w, r, "latest", err)
		return
	}
	writeJSON(w, out)
}

// browsable lists the visible movies and series (a view: its kind only).
func (c *conv) browsable(r *http.Request) ([]library.Item, error) {
	ks := []string{library.KindMovie, library.KindSeries}
	if k, id, ok := ParseID(qv(r, "parentId")); ok && k == kindView {
		ks = viewKinds(id, false)
	}
	var items []library.Item
	for _, k := range ks {
		got, err := c.listKind(k, "title", 0)
		if err != nil {
			return nil, err
		}
		items = append(items, got...)
	}
	return c.keepVisible(items)
}

// itemFilters serves /Items/Filters: genres, ratings and years of the
// visible library.
func (s *Server) itemFilters(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), nil)
	items, err := c.browsable(r)
	if err != nil {
		s.internal(w, r, "filters", err)
		return
	}
	genres, ratings, years := map[string]bool{}, map[string]bool{}, map[int]bool{}
	for _, it := range items {
		for _, g := range metaOf(it).Genres {
			genres[g] = true
		}
		if it.Rating != "" {
			ratings[it.Rating] = true
		}
		if it.Year > 0 {
			years[it.Year] = true
		}
	}
	out := struct {
		Genres          []string `json:"Genres"`
		Tags            []string `json:"Tags"`
		OfficialRatings []string `json:"OfficialRatings"`
		Years           []int    `json:"Years"`
	}{sortedKeys(genres), []string{}, sortedKeys(ratings), []int{}}
	for y := range years {
		out.Years = append(out.Years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out.Years)))
	writeJSON(w, out)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// genres serves /Genres: the genres (meta_json.genres) of the visible
// library.
func (s *Server) genres(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), nil)
	items, err := c.browsable(r)
	if err != nil {
		s.internal(w, r, "genres", err)
		return
	}
	seen := map[string]bool{}
	for _, it := range items {
		for _, g := range metaOf(it).Genres {
			seen[g] = true
		}
	}
	out := []BaseItemDto{}
	for _, g := range sortedKeys(seen) {
		out = append(out, BaseItemDto{Name: g, Id: genreID(g), ServerId: s.sid(), Type: "Genre", ImageTags: map[string]string{}})
	}
	total := len(out)
	pg, start := page(r, out)
	writeJSON(w, QueryResult[BaseItemDto]{Items: pg, TotalRecordCount: total, StartIndex: start})
}
