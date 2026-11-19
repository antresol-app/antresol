package library

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

// RatingAllowed applies the rule of Visible to a root item: may a profile
// limited to maxRating ("" = no limit) see it? Video without a recognised
// rating is hidden from limited profiles; music and books without one are
// shown. This file has no Repo, so an addon can copy it unchanged and
// gate its routes by the X-Antresol-User maxRating with it.
func RatingAllowed(maxRating string, root Item) bool {
	if maxRating == "" {
		return true
	}
	rating := root.judged() // 18+ from an adults-only folder
	if IsUnratedKind(root.Kind) && rating == "" {
		return true
	}
	max, ok := RatingRank(maxRating)
	if !ok {
		return false
	}
	rank, ok := RatingRank(rating)
	return ok && rank <= max
}

// adultRating is what a folder marked «Только для взрослых» rates its
// titles (Item.Adult; RatingOf is the SQL of the same rule).
const adultRating = "18+"

// judged is the rating the rules see for it: 18+ when it is from an
// adults-only folder, its own otherwise.
func (it Item) judged() string {
	if it.Adult {
		return adultRating
	}
	return it.Rating
}
