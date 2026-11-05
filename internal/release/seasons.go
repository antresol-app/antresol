package release

import (
	"regexp"
	"strconv"
)

var (
	// «S01-S03», «Сезоны: 1-3», «Seasons 1-3», and the compact «S1-9E1-134»
	// (seasons 1–9, episodes 1–134 in all)
	reSeasonRangeA = regexp.MustCompile(`(?i)(?:сезон\p{L}*\s*[:№]?\s*|\bseasons?\s*[:#]?\s*|\bS)(\d{1,2})\s*[-–]\s*S?(\d{1,2})(?:\b|E\d)`)
	reSeasonRangeB = regexp.MustCompile(`(?i)\b(\d{1,2})\s*[-–]\s*(\d{1,2})\s*(?:-?й\s*)?сезон`)
	// «S01E01-S09E10»: from an episode of one season to one of another
	reSeasonRangeC = regexp.MustCompile(`(?i)\bS(\d{1,2})E\d{1,4}\s*[-–]\s*S(\d{1,2})E\d{1,4}\b`)
	reEpisodeWord  = regexp.MustCompile(`(?i)^\s*(?:сери|эпизод|ep)`)
)

// SeasonRange returns the seasons a release named title covers
// (info.Season, info.Season when it names no range: 0, 0 for none).
func SeasonRange(title string) (lo, hi int) { return SeasonRangeOf(title, Parse(title)) }

// SeasonRangeOf is SeasonRange for a title already parsed into info.
func SeasonRangeOf(title string, info Info) (lo, hi int) {
	for _, re := range []*regexp.Regexp{reSeasonRangeC, reSeasonRangeA, reSeasonRangeB} {
		for _, m := range re.FindAllStringSubmatchIndex(title, -1) {
			// "сезон: 1-10 серии" is a range of episodes, not of seasons
			if reEpisodeWord.MatchString(title[m[1]:]) {
				continue
			}
			lo, _ := strconv.Atoi(title[m[2]:m[3]])
			hi, _ := strconv.Atoi(title[m[4]:m[5]])
			if lo > 0 && hi > lo {
				return lo, hi
			}
		}
	}
	return info.Season, info.Season
}
