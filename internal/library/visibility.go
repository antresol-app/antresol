package library

import (
	"context"
	"strings"
)

// HasRatedParent reports kinds whose age rating is the one of the top of
// their hierarchy: episode/season → series, track/album → artist,
// comic_volume → comic.
func HasRatedParent(kind string) bool {
	switch kind {
	case KindSeason, KindEpisode, KindTrack, KindAlbum, KindComicVolume:
		return true
	}
	return false
}

// Root walks up to the top of an item's hierarchy (or returns it as is).
func (r *Repo) Root(ctx context.Context, it Item) (Item, error) {
	for i := 0; i < 4 && HasRatedParent(it.Kind) && it.ParentID != 0; i++ {
		p, err := r.Item(ctx, it.ParentID)
		if err != nil {
			return it, err
		}
		it = p
	}
	return it, nil
}

// Visible is the one visibility rule of the web API and Subsonic: may a
// profile limited to maxRating ("" = no limit) see it? Children take the
// rating of their root. Video without a recognised rating is hidden from
// limited profiles; music and books without a rating are shown (they carry
// none: IsUnratedKind); a rating, when set, still applies.
func (r *Repo) Visible(ctx context.Context, maxRating string, it Item) (bool, error) {
	if maxRating == "" {
		return true, nil
	}
	root, err := r.Root(ctx, it)
	if err != nil {
		return false, err
	}
	return RatingAllowed(maxRating, root), nil
}

// RatingAllowed applies the rule of Visible to a root item.
func RatingAllowed(maxRating string, root Item) bool {
	if maxRating == "" {
		return true
	}
	if IsUnratedKind(root.Kind) && root.Rating == "" {
		return true
	}
	max, ok := RatingRank(maxRating)
	if !ok {
		return false
	}
	rank, ok := RatingRank(root.Rating)
	return ok && rank <= max
}

// MusicRootAllowed is the SQL form of RatingAllowed for a music root (an
// artist, or an album without one) whose rating is the expression root.
func MusicRootAllowed(root, maxRating string) (string, []any) {
	if maxRating == "" {
		return "1", nil
	}
	rank, ok := RatingRank(maxRating)
	if !ok {
		return "0", nil
	}
	args := make([]any, 0, rank+1)
	for i := 0; i <= rank; i++ {
		args = append(args, ruRatings[i])
	}
	return "(COALESCE(" + root + ",'') = '' OR " + root + " IN (?" + strings.Repeat(",?", rank) + "))", args
}

// musicRootJoins joins p1 (the parent) and p2 (the grandparent) of items
// "i"; musicRootExpr is then the rating of i's root for artist, album and
// track items (the walk of Root).
const (
	musicRootJoins = ` LEFT JOIN items p1 ON p1.id=i.parent_id LEFT JOIN items p2 ON p2.id=p1.parent_id`
	musicRootExpr  = `(CASE WHEN i.kind='track' AND p2.id IS NOT NULL THEN p2.rating
	  WHEN i.kind IN ('track','album') AND p1.id IS NOT NULL THEN p1.rating ELSE i.rating END)`
)
