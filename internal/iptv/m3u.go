package iptv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// MaxEntries is the most entries a playlist may have.
const MaxEntries = 50000

var (
	// ErrNotM3U: the answer is not a playlist (an HTML login page, JSON…).
	ErrNotM3U = errors.New("iptv: not an M3U playlist")
	// ErrEmpty: the playlist has no entries (or the answer is empty).
	ErrEmpty = errors.New("iptv: empty playlist")
	// ErrTooManyEntries: more entries than the cap.
	ErrTooManyEntries = errors.New("iptv: too many playlist entries")
)

// Entry is a playlist entry.
type Entry struct {
	// Name: the #EXTINF title (after the attributes).
	Name, TvgID, TvgName, Logo, Group string
	URL                               string
	// UserAgent, Referrer: what #EXTVLCOPT, #KODIPROP or a Kodi "|"
	// suffix of the URL ask the player to send.
	UserAgent, Referrer string
}

// Playlist is a parsed M3U.
type Playlist struct {
	// EPGURLs: url-tvg / x-tvg-url of #EXTM3U, in order, without repeats.
	EPGURLs []string
	Entries []Entry
}

// ParseM3U reads an extended M3U (any attribute order and quoting, CRLF,
// a BOM). It fails with ErrNotM3U when the first non-empty line is not
// #EXTM3U, ErrEmpty without entries and ErrTooManyEntries past maxEntries.
func ParseM3U(r io.Reader, maxEntries int) (Playlist, error) {
	var p Playlist
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	started := false
	var cur Entry
	var groupHint string
	seenEPG := map[string]bool{}
	addEPG := func(v string) {
		for _, u := range strings.Split(v, ",") {
			u = strings.TrimSpace(u)
			if u != "" && !seenEPG[u] {
				seenEPG[u] = true
				p.EPGURLs = append(p.EPGURLs, u)
			}
		}
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !started {
			line = strings.TrimPrefix(line, "\ufeff")
			if line == "" {
				continue
			}
			if !hasPrefixFold(line, "#EXTM3U") {
				return p, ErrNotM3U
			}
			started = true
			for _, kv := range attrs(line[len("#EXTM3U"):]) {
				switch kv[0] {
				case "url-tvg", "x-tvg-url":
					addEPG(kv[1])
				}
			}
			continue
		}
		switch {
		case line == "":
		case hasPrefixFold(line, "#EXTINF:"):
			cur = Entry{}
			groupHint = ""
			head, name := splitInf(line[len("#EXTINF:"):])
			cur.Name = strings.TrimSpace(name)
			for _, kv := range attrs(head) {
				switch kv[0] {
				case "tvg-id":
					cur.TvgID = kv[1]
				case "tvg-name":
					cur.TvgName = kv[1]
				case "tvg-logo":
					cur.Logo = kv[1]
				case "group-title":
					cur.Group = kv[1]
				}
			}
		case hasPrefixFold(line, "#EXTGRP:"):
			groupHint = strings.TrimSpace(line[len("#EXTGRP:"):])
		case hasPrefixFold(line, "#EXTVLCOPT:"):
			k, v, _ := strings.Cut(line[len("#EXTVLCOPT:"):], "=")
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "http-user-agent":
				cur.UserAgent = strings.TrimSpace(v)
			case "http-referrer", "http-referer":
				cur.Referrer = strings.TrimSpace(v)
			}
		case hasPrefixFold(line, "#KODIPROP:"):
			k, v, _ := strings.Cut(line[len("#KODIPROP:"):], "=")
			if strings.EqualFold(strings.TrimSpace(k), "inputstream.adaptive.stream_headers") {
				headers(&cur, v)
			}
		case strings.HasPrefix(line, "#"):
			// Other directives (#EXTM3U repeats, #EXT-X-…, comments).
		default:
			u, hs, _ := strings.Cut(line, "|")
			if hs != "" {
				headers(&cur, hs)
			}
			cur.URL = strings.TrimSpace(u)
			if cur.Group == "" {
				cur.Group = groupHint
			}
			if cur.Name == "" {
				cur.Name = cur.TvgName
			}
			if cur.Name == "" {
				cur.Name = strings.TrimSuffix(path.Base(cur.URL), path.Ext(cur.URL))
			}
			if len(p.Entries) >= maxEntries {
				return p, fmt.Errorf("%w (over %d)", ErrTooManyEntries, maxEntries)
			}
			p.Entries = append(p.Entries, cur)
			cur = Entry{}
			groupHint = ""
		}
	}
	if err := sc.Err(); err != nil {
		return p, fmt.Errorf("iptv: reading playlist: %w", err)
	}
	if len(p.Entries) == 0 {
		return p, ErrEmpty
	}
	return p, nil
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// splitInf splits "#EXTINF:" text into the duration+attributes and the
// title: at the first comma outside quotes.
func splitInf(s string) (head, name string) {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			// A quote opens only a value (right after '=').
			if i > 0 && s[i-1] == '=' {
				q = c
			}
		case c == ',':
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// attrs reads key=value pairs ("…", '…' or bare), keys lower case.
func attrs(s string) [][2]string {
	var out [][2]string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		k0 := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := strings.ToLower(s[k0:i])
		if i >= len(s) || s[i] != '=' {
			continue // a bare word (the duration)
		}
		i++
		var val string
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			v0 := i
			for i < len(s) && s[i] != q {
				i++
			}
			val = s[v0:i]
			if i < len(s) {
				i++
			}
		} else {
			v0 := i
			for i < len(s) && s[i] != ' ' && s[i] != '\t' {
				i++
			}
			val = s[v0:i]
		}
		if key != "" {
			out = append(out, [2]string{key, strings.TrimSpace(val)})
		}
	}
	return out
}

// headers reads "User-Agent=…&Referer=…" (values may be URL-encoded).
func headers(e *Entry, s string) {
	for _, kv := range strings.Split(s, "&") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			v = uv
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "user-agent":
			e.UserAgent = strings.TrimSpace(v)
		case "referer", "referrer":
			e.Referrer = strings.TrimSpace(v)
		}
	}
}
