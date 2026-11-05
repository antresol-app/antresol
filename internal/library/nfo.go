package library

import (
	"bytes"
	"encoding/xml"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// NFO is the subset of a Kodi .nfo (movie, tvshow, episodedetails) the scanner uses.
type NFO struct {
	Title    string
	Original string
	Year     int
	Plot     string
	Genres   []string
	Rating   string
	TmdbID   int64
	Season   int
	Episode  int
	ExtIDs   map[string]string // meta_json keys tvdb_id, imdb_id (tmdb goes to TmdbID)
}

type nfoXML struct {
	Title    string   `xml:"title"`
	Original string   `xml:"originaltitle"`
	Year     string   `xml:"year"`
	Premiere string   `xml:"premiered"`
	Plot     string   `xml:"plot"`
	Genre    []string `xml:"genre"`
	Mpaa     string   `xml:"mpaa"`
	TmdbID   string   `xml:"tmdbid"`
	UniqueID []struct {
		Type string `xml:"type,attr"`
		Val  string `xml:",chardata"`
	} `xml:"uniqueid"`
	TvdbID  string `xml:"tvdbid"`
	ImdbID  string `xml:"imdbid"`
	ID      string `xml:"id"`
	Season  string `xml:"season"`
	Episode string `xml:"episode"`
}

var (
	reTmdbURL = regexp.MustCompile(`themoviedb\.org/(?:movie|tv)/(\d+)`)
	reYear4   = regexp.MustCompile(`^\s*((?:19|20)\d\d)`)
)

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// ParseNFO parses Kodi nfo bytes. It tolerates trailing junk (Kodi often
// appends a themoviedb URL line) and returns ok=false when nothing useful
// was found.
func ParseNFO(data []byte) (NFO, bool) {
	var x nfoXML
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	_ = dec.Decode(&x) // partial results are fine
	n := NFO{
		Title:    strings.TrimSpace(x.Title),
		Original: strings.TrimSpace(x.Original),
		Plot:     strings.TrimSpace(x.Plot),
		Rating:   strings.TrimSpace(x.Mpaa),
		TmdbID:   atoi64(x.TmdbID),
		Season:   int(atoi64(x.Season)),
		Episode:  int(atoi64(x.Episode)),
	}
	seenGenre := map[string]bool{}
	for _, g := range x.Genre {
		// one <genre> per genre, or "Drama / Comedy" in one
		for _, p := range strings.Split(g, "/") {
			if p = strings.TrimSpace(p); p != "" && !seenGenre[strings.ToLower(p)] {
				seenGenre[strings.ToLower(p)] = true
				n.Genres = append(n.Genres, p)
			}
		}
	}
	n.Year = int(atoi64(x.Year))
	if n.Year == 0 {
		if m := reYear4.FindStringSubmatch(x.Premiere); m != nil {
			n.Year = int(atoi64(m[1]))
		}
	}
	ext := map[string]string{}
	put := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" && v != "0" && ext[k] == "" {
			ext[k] = v
		}
	}
	for _, u := range x.UniqueID {
		switch t := strings.ToLower(strings.TrimSpace(u.Type)); t {
		case "tmdb":
			if n.TmdbID == 0 {
				n.TmdbID = atoi64(u.Val)
			}
		case "tvdb", "imdb":
			put(t+"_id", u.Val)
		}
	}
	put("tvdb_id", x.TvdbID)
	put("imdb_id", x.ImdbID)
	if strings.HasPrefix(strings.TrimSpace(x.ID), "tt") {
		put("imdb_id", x.ID)
	}
	if len(ext) > 0 {
		n.ExtIDs = ext
	}
	if n.TmdbID == 0 {
		if m := reTmdbURL.FindSubmatch(data); m != nil {
			n.TmdbID = atoi64(string(m[1]))
		}
	}
	if strings.EqualFold(n.Original, n.Title) {
		n.Original = ""
	}
	return n, n.Title != "" || n.TmdbID != 0 || n.Episode != 0
}

// readNFO reads and parses the nfo file at path; ok=false if missing/unusable.
func readNFO(path string) (NFO, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return NFO{}, false
	}
	return ParseNFO(b)
}
