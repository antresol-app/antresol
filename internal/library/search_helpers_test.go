package library

import "context"

// Search is SearchKinds over movies and series (what the tests mean by a
// plain search), within no rating limit.
func (r *Repo) Search(ctx context.Context, text string, limit int) ([]Item, error) {
	return r.SearchRated(ctx, text, limit, "")
}

// SearchRated is Search restricted to items within maxRating.
func (r *Repo) SearchRated(ctx context.Context, text string, limit int, maxRating string) ([]Item, error) {
	return r.SearchKinds(ctx, text, []string{KindMovie, KindSeries}, limit, maxRating)
}
