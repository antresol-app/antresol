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
