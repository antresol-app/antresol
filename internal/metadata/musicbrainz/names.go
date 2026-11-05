package musicbrainz

import (
	"context"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/release"
)

// Artist is a MusicBrainz artist.
type Artist struct{ ID, Name string }

// ArtistsNamed returns the artists (at most maxQueryArtists, of a high
// relevance) whose name, sort name or an alias spells name the way a
// streaming service romanizes it: diacritics dropped and Cyrillic
// transliterated (Bi-2 = Би-2, Alisa = Алиса), or every word with the
// consonants of the alias's (Cherny Kofe = Chernyi kofe). One request.
func (c *Client) ArtistsNamed(ctx context.Context, name string) ([]Artist, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	as, err := c.searchArtists(ctx, name)
	if err != nil {
		return nil, err
	}
	var out []Artist
	for _, a := range as {
		if len(out) == maxQueryArtists {
			break
		}
		if int(a.Score) < minArtistScore || a.ID == "" || a.ID == variousArtists {
			continue
		}
		names := []string{a.Name, a.SortName}
		for _, al := range a.Aliases {
			names = append(names, al.Name, al.SortName)
		}
		if slices.ContainsFunc(names, func(n string) bool { return SameName(n, name) }) {
			out = append(out, Artist{ID: a.ID, Name: a.Name})
		}
	}
	return out, nil
}

// SameName: two spellings of one name — equal with diacritics dropped,
// «&»/«and» left out and Cyrillic transliterated («Би-2» = «Bi-2»), or
// word by word of the same consonants («Chernyi kofe» = «Cherny Kofe»;
// at least 4 of them: a short name must be spelled alike).
func SameName(a, b string) bool {
	fa, fb := release.FoldLatin(a), release.FoldLatin(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	wa, wb := strings.Fields(fa), strings.Fields(fb)
	if len(wa) != len(wb) {
		return false
	}
	n := 0
	for i := range wa {
		ka, kb := release.Skeleton(wa[i], false), release.Skeleton(wb[i], false)
		if ka == "" || ka != kb {
			return false
		}
		n += len([]rune(ka))
	}
	return n >= 4
}
