package library

import "context"

// ItemsByTMDB finds the top-level items of these TMDB ids, per kind (movie,
// series), in one query per kind and one for the items; ids the library
// does not have are left out.
func (r *Repo) ItemsByTMDB(ctx context.Context, byKind map[string][]int64) (map[string]map[int64]Item, error) {
	out := map[string]map[int64]Item{}
	idOf := map[string]map[int64]int64{}
	var ids []int64
	for kind, tmdb := range byKind {
		m, err := r.ItemIDsByTMDB(ctx, kind, tmdb)
		if err != nil {
			return nil, err
		}
		idOf[kind] = m
		for _, id := range m {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := r.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for kind, m := range idOf {
		out[kind] = map[int64]Item{}
		for tmdb, id := range m {
			if it, ok := items[id]; ok {
				out[kind][tmdb] = it
			}
		}
	}
	return out, nil
}
