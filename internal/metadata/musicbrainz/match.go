package musicbrainz

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/release"
)

// The library import matches a streaming service's song to a recording:
// MatchCandidates searches the recordings (through the client's limiter,
// like every request) and lists them per official release group; the
// importer's rules pick one.

// TrackQuery is a song as a streaming service names it.
type TrackQuery struct {
	ISRC, Artist, Title, Album string
	DurationMs                 int64
}

// Credit is one artist of a credit.
type Credit struct{ Name, ID string }

// TrackCandidate is one recording on one official release group.
type TrackCandidate struct {
	RecordingID, Title string
	Artist, ArtistID   string   // the recording's credit, its first artist
	Credits            []Credit // every credited artist
	// Group is the release group (types from the release's release-group
	// subobject; Artist and ArtistID from the release's credit; Year from
	// its earliest release of the recording); MatchedTrack = the title.
	Group      ReleaseGroup
	DurationMs int64 // 0 unknown
	// Live: a live take (a version word «live», «концерт»… in its title or
	// disambiguation) or a group of type Live.
	Live           bool
	Disambiguation string // the recording's («live, 1993-07-04: …», «demo»)
	Date           string // the earliest release date on the group ("" unknown)
	Releases       int    // the recording's releases, a popularity hint
}

// MatchCandidates searches the song: by ISRC when given (one request),
// else by title and artist, narrowed by the album first (when no recording
// of it has the title, a second request goes without the album). The
// candidates are in release date order, undated last.
func (c *Client) MatchCandidates(ctx context.Context, q TrackQuery) ([]TrackCandidate, error) {
	if isrc := strings.TrimSpace(q.ISRC); isrc != "" {
		recs, err := c.searchRecordings(ctx, "isrc:"+luceneEscape(strings.ToUpper(isrc)))
		if err != nil {
			return nil, err
		}
		return trackCandidates(recs), nil
	}
	title := StripEditions(q.Title)
	base := "recording:" + phrase(title) + " AND artist:" + phrase(q.Artist)
	if album := StripEditions(q.Album); album != "" {
		recs, err := c.searchRecordings(ctx, base+" AND release:"+phrase(album))
		if err != nil {
			return nil, err
		}
		cs := trackCandidates(recs)
		for _, x := range cs {
			if Norm(StripEditions(x.Title)) == Norm(title) {
				return cs, nil
			}
		}
	}
	recs, err := c.searchRecordings(ctx, base)
	if err != nil {
		return nil, err
	}
	return trackCandidates(recs), nil
}

// trackCandidates lists each recording once per official release group.
func trackCandidates(recs []rawRecording) []TrackCandidate {
	var out []TrackCandidate
	for _, r := range recs {
		credits := make([]Credit, 0, len(r.ArtistCredit))
		for _, a := range r.ArtistCredit {
			name := a.Name
			if name == "" {
				name = a.Artist.Name
			}
			credits = append(credits, Credit{name, a.Artist.ID})
		}
		live := isLive(r.Title + " " + r.Disambiguation)
		at := map[string]int{} // release group → index in out
		for _, rel := range r.Releases {
			rg := rel.ReleaseGroup
			if rg.ID == "" || rel.Status != "Official" {
				continue
			}
			if i, ok := at[rg.ID]; ok {
				if earlier(rel.Date, out[i].Date) {
					out[i].Date = rel.Date
					out[i].Group.Year = year(rel.Date)
				}
				continue
			}
			g := ReleaseGroup{ID: rg.ID, Title: rg.Title, Artist: rel.ArtistCredit.text(), ArtistID: rel.ArtistCredit.firstID(),
				Year: year(rel.Date), PrimaryType: rg.PrimaryType, SecondaryTypes: rg.SecondaryTypes, MatchedTrack: r.Title}
			if g.Artist == "" {
				g.Artist, g.ArtistID = r.ArtistCredit.text(), r.ArtistCredit.firstID()
			}
			at[rg.ID] = len(out)
			out = append(out, TrackCandidate{RecordingID: r.ID, Title: r.Title, Artist: r.ArtistCredit.text(),
				ArtistID: r.ArtistCredit.firstID(), Credits: credits, Group: g, DurationMs: int64(r.Length),
				Live: live || slices.Contains(rg.SecondaryTypes, "Live"), Disambiguation: r.Disambiguation,
				Date: rel.Date, Releases: len(r.Releases)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return earlier(out[i].Date, out[j].Date) })
	return out
}

// earlier: date a comes before b; an unknown date comes last.
func earlier(a, b string) bool {
	if a == "" || b == "" {
		return a != "" && b == ""
	}
	return a < b
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

// Norm folds a name for comparison: lower case, letters and digits only,
// single spaces.
func Norm(s string) string { return norm(s) }

// StripEditions removes edition suffixes from a song or album title,
// last first. A suffix that names a version stays whole, edition words
// or not («(Live - 2021 Remaster)», « - Acoustic / Remastered»): the
// matcher must see it. (release.StripEditions, shared with the library
// scanner.)
func StripEditions(s string) string { return release.StripEditions(s) }

func lastSuffix(s string) (head, tail string, ok bool) { return release.LastSuffix(s) }

// VersionTagged: a suffix of the title («(…)», «[…]», « - …», any of
// them, edition words or not) names a version («(Live at Wembley)»,
// « - Live / Remastered 2021»). A version word in the name itself
// («Live and Let Die») does not count.
func VersionTagged(title string) bool {
	s := strings.TrimSpace(title)
	for {
		head, tail, ok := lastSuffix(s)
		if !ok || head == s {
			return false
		}
		if hasWord(tail, versionWords) {
			return true
		}
		s = head
	}
}

// versionWords mark a recording that is not the studio song.
var versionWords = release.VersionWords

var liveWords = []string{"live", "концерт", "вживую", "живое"}

// VersionWords lists the version words (live, karaoke, remix, «концерт»…)
// in s, whole words only, in the list's order.
func VersionWords(s string) []string {
	var out []string
	words := strings.Fields(norm(s))
	for _, v := range versionWords {
		if slices.Contains(words, v) {
			out = append(out, v)
		}
	}
	return out
}

func isLive(s string) bool { return hasWord(s, liveWords) }

func hasWord(s string, list []string) bool {
	for _, w := range strings.Fields(norm(s)) {
		if slices.Contains(list, w) {
			return true
		}
	}
	return false
}
