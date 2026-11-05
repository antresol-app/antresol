package library

import (
	"regexp"
	"strconv"
	"strings"
)

// Music folder and file names: what an album folder, a disc folder and a
// track file name say when the tags are missing.

var (
	// reDiscDir matches a disc subfolder of an album ("CD1", "Disc 2", "Диск 1").
	reDiscDir = regexp.MustCompile(`(?i)^(cd|disc|disk|диск)\s*(\d+)`)
	// reDiscSuffix is a disc marker at the end of an album title ("The Wall
	// (Disc 1)", "Album CD2"): the keyword must start a word ("Abcd 2" is a
	// title, not disc 2).
	reDiscSuffix = regexp.MustCompile(`(?i)(?:^|[\s\(\[\-_.,])[\(\[]?(cd|disc|disk|диск)\s*(\d+)[\)\]]?$`)

	// reFeat cuts guests off a track artist ("Eminem feat. Dido", "Кино при
	// участии ...", "A & B"), leaving the primary artist: a counting key
	// only, never a stored name ("Simon & Garfunkel" is one artist).
	reFeat = regexp.MustCompile(`(?i)\s*[\(\[]?\s*(?:\bfeat\b\.?|\bft\b\.?|\bfeaturing\b|при\s+участии|\s&\s).*$`)
	// reGuest finds an explicit guest marker (feat./ft./featuring/при участии).
	reGuest = regexp.MustCompile(`(?i)(?:\bfeat\b\.?|\bft\b\.?|\bfeaturing\b|при\s+участии)`)

	reLeadGroup  = regexp.MustCompile(`^\s*[\(\[]([^\)\]]*)[\)\]]\s*`)
	reBrackets   = regexp.MustCompile(`\s*\[[^\]]*\]`)
	reParenYear  = regexp.MustCompile(`\s*\(((?:19|20)\d\d)\)`)
	reYearPart   = regexp.MustCompile(`^((?:19|20)\d\d)(?:\s*[,.;].*)?$`)
	reFormatTail = regexp.MustCompile(`(?i)\s*,\s*(?:flac|mp3|ape|wv|wavpack|alac|aac|ogg|opus|lossless|hi-?res|\d+\s*kbps|\d+\s*bit|image|tracks).*$`)
	reDashSep    = regexp.MustCompile(`\s+[-–—]\s+`)
	reYearOnly   = regexp.MustCompile(`^(?:19|20)\d\d$`)

	reDiscTrack = regexp.MustCompile(`^(\d{1,2})-(\d{2,3})[\s._]+(.+)$`)
	reTrackNo   = regexp.MustCompile(`^(\d{1,3})(?:\s*[-.)_]\s*|\s+)(.+)$`)
	reNumOnly   = regexp.MustCompile(`^\s*(\d{1,3})\s*$`)
	// reTagTrackNo: «01. Title», «3 - Title» in a tag's title (a dot or dash is required)
	reTagTrackNo = regexp.MustCompile(`^(\d{1,3})\s*[-.]\s+(.+)$`)
)

// discFolder returns the disc number of a disc folder name, or 0.
func discFolder(name string) int {
	m := reDiscDir.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// stripDiscSuffix removes a trailing disc marker from an album title and
// returns the bare title and the disc number (0 when there was none).
func stripDiscSuffix(album string) (string, int) {
	album = strings.TrimSpace(album)
	m := reDiscSuffix.FindStringSubmatchIndex(album)
	if m == nil || m[0] == 0 {
		return album, 0
	}
	n, _ := strconv.Atoi(album[m[4]:m[5]])
	return strings.TrimSpace(strings.TrimRight(album[:m[0]], " -–—_.,([")), n
}

// primaryArtist returns a track artist without its guests, for counting
// distinct artists (compilation check); it is never stored as a name.
func primaryArtist(artist string) string {
	if p := strings.TrimSpace(reFeat.ReplaceAllString(artist, "")); p != "" {
		return p
	}
	return strings.TrimSpace(artist)
}

// parseAlbumFolder reads artist, album and year from an album folder name:
// "Кино - Группа крови (1988) [FLAC]", "(Rock) Кино - Группа крови - 1988,
// FLAC (image+.cue), lossless", "Pink Floyd - 1979 - The Wall",
// "The Wall (1979)", "The Wall".
func parseAlbumFolder(name string) (artist, album string, year int) {
	s := strings.TrimSpace(name)
	// a leading "(Rock)" / "[Genre]" group; a leading "(1988)" is the year
	if m := reLeadGroup.FindStringSubmatch(s); m != nil {
		if reYearOnly.MatchString(strings.TrimSpace(m[1])) {
			year, _ = strconv.Atoi(strings.TrimSpace(m[1]))
		}
		s = s[len(m[0]):]
	}
	for _, m := range reBrackets.FindAllString(s, -1) {
		in := strings.Trim(strings.TrimSpace(m), "[]")
		if year == 0 && reYearOnly.MatchString(in) {
			year, _ = strconv.Atoi(in)
		}
	}
	s = reBrackets.ReplaceAllString(s, "")
	if m := reParenYear.FindStringSubmatch(s); m != nil {
		if year == 0 {
			year, _ = strconv.Atoi(m[1])
		}
		s = reParenYear.ReplaceAllString(s, "")
	}
	var parts []string
	for _, p := range reDashSep.Split(s, -1) {
		p = strings.TrimSpace(p)
		if m := reYearPart.FindStringSubmatch(p); m != nil {
			if year == 0 {
				year, _ = strconv.Atoi(m[1])
			}
			continue
		}
		p = strings.TrimSpace(reFormatTail.ReplaceAllString(p, ""))
		if p != "" {
			parts = append(parts, p)
		}
	}
	switch len(parts) {
	case 0:
		return "", "", year
	case 1:
		return "", parts[0], year
	}
	return parts[0], strings.Join(parts[1:], " - "), year
}

// parseTrackFile reads the track number and title from a file name without
// its extension: "01 - Группа крови", "01. Группа крови", "1-01 Title"
// (disc-track), "Группа крови".
func parseTrackFile(stem string) (no int, title string) {
	_, no, title = parseTrackFileDisc(stem)
	return no, title
}

// cleanTagTitle drops a track number a tag carried into the title («01.
// Ain't My Bitch», «03 - Cure»): only when it is that track's number (no) and
// set off by a dot or a dash, so «1979» or «99 Problems» stay as they are.
func cleanTagTitle(title string, no int) string {
	if no <= 0 {
		return title
	}
	m := reTagTrackNo.FindStringSubmatch(title)
	if m == nil {
		return title
	}
	if n, _ := strconv.Atoi(m[1]); n != no || strings.TrimSpace(m[2]) == "" {
		return title
	}
	return strings.TrimSpace(m[2])
}

// parseTrackFileDisc is parseTrackFile that also returns the disc number of
// a "1-01 Title" name (0 otherwise).
func parseTrackFileDisc(stem string) (disc, no int, title string) {
	s := strings.TrimSpace(stem)
	clean := func(t string) string { return strings.Trim(strings.TrimSpace(t), " -._") }
	if m := reDiscTrack.FindStringSubmatch(s); m != nil {
		disc, _ = strconv.Atoi(m[1])
		no, _ = strconv.Atoi(m[2])
		return disc, no, clean(m[3])
	}
	if m := reTrackNo.FindStringSubmatch(s); m != nil {
		no, _ = strconv.Atoi(m[1])
		return 0, no, clean(m[2])
	}
	if m := reNumOnly.FindStringSubmatch(s); m != nil {
		no, _ = strconv.Atoi(m[1])
		return 0, no, ""
	}
	return 0, 0, clean(s)
}
