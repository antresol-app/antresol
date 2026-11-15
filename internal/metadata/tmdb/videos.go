package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Video is a TMDB video of a movie or show: a YouTube trailer or teaser.
type Video struct {
	Name     string `json:"name"`
	Site     string `json:"site"`
	Key      string `json:"key"`
	Type     string `json:"type"` // Trailer | Teaser
	Lang     string `json:"lang"` // ISO 639-1 ("" = none)
	Official bool   `json:"official"`
	// Published: TMDB's published_at (RFC 3339, "" = unknown).
	Published string `json:"published,omitempty"`
}

const (
	// videosScope is the tmdb_related scope of a title's videos
	// (videos-movie | videos-tv: the ids of the two kinds overlap).
	videosScope = "videos-"
	videosTTL   = 24 * time.Hour
	// videosLang: the cache keeps every language of a title's videos in
	// one entry; the API orders them for the viewer.
	videosLang = "all"
	// videoLanguages: the languages of videos asked for (TMDB answers only
	// these; "null" is a video without one).
	videoLanguages = "ru,en,uk,de,fr,es,it,ja,ko,zh,pt,pl,null"
)

// Videos are the YouTube trailers and teasers of a movie or show ("movie"
// | "tv"), in TMDB's order; false: none known (TMDB failing or slow with
// nothing cached). Cached a day.
func (s *Related) Videos(ctx context.Context, kind string, id int) ([]Video, bool) {
	if kind != "movie" && kind != "tv" {
		return nil, false
	}
	var out struct {
		Videos []Video `json:"videos"`
	}
	ok := s.get(ctx, videosScope+kind, id, videosLang, &out, func(ctx context.Context) (any, error) {
		v, err := s.Client.videos(ctx, kind, id)
		return struct {
			Videos []Video `json:"videos"`
		}{v}, err
	})
	return out.Videos, ok
}

// videos is /{kind}/{id}/videos: the YouTube trailers and teasers.
func (c *Client) videos(ctx context.Context, kind string, id int) ([]Video, error) {
	var raw struct {
		Results []struct {
			Name      string `json:"name"`
			Site      string `json:"site"`
			Key       string `json:"key"`
			Type      string `json:"type"`
			Lang      string `json:"iso_639_1"`
			Official  bool   `json:"official"`
			Published string `json:"published_at"`
		} `json:"results"`
	}
	q := url.Values{"include_video_language": {videoLanguages}}
	if err := c.get(ctx, fmt.Sprintf("/%s/%d/videos", kind, id), q, &raw); err != nil {
		return nil, err
	}
	out := []Video{}
	for _, v := range raw.Results {
		if v.Site != "YouTube" || v.Key == "" || (v.Type != "Trailer" && v.Type != "Teaser") {
			continue
		}
		out = append(out, Video{Name: v.Name, Site: v.Site, Key: v.Key, Type: v.Type, Lang: v.Lang,
			Official: v.Official, Published: v.Published})
	}
	return out, nil
}
