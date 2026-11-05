package auth

// KidDefaultRating limits kid profiles that have no explicit maxRating.
const KidDefaultRating = "12+"

// RatingLimit is the profile's effective maximum rating ("" = no limit),
// for the web API and Subsonic alike.
func (u User) RatingLimit() string {
	if u.MaxRating != nil && *u.MaxRating != "" {
		return *u.MaxRating
	}
	if u.IsKid {
		return KidDefaultRating
	}
	return ""
}
