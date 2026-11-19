package mediaimport

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Audiobookshelf over its HTTP API: what readABS reads from
// absdatabase.sqlite. An administrator's token lists the users (/api/users)
// and reads each one's mediaProgress (/api/users/{id}); another user's token
// reads its own (/api/me). The book of a position is /api/items/{id}: its
// folder or file (path) and its title.

const absAuthHint = "Audiobookshelf не принял API-токен — он в Audiobookshelf: «Настройки → Пользователи → пользователь → API-токен» (или «Настройки → API-ключи»)"

type absAPIProgress struct {
	LibraryItemID string  `json:"libraryItemId"`
	EpisodeID     string  `json:"episodeId"`
	MediaItemType string  `json:"mediaItemType"`
	Duration      float64 `json:"duration"`
	CurrentTime   float64 `json:"currentTime"`
	IsFinished    bool    `json:"isFinished"`
	EbookLocation string  `json:"ebookLocation"`
	EbookProgress float64 `json:"ebookProgress"`
	LastUpdate    int64   `json:"lastUpdate"` // epoch ms
}

type absAPIUser struct {
	ID            string           `json:"id"`
	Username      string           `json:"username"`
	MediaProgress []absAPIProgress `json:"mediaProgress"`
}

type absAPIItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Media     struct {
		Metadata struct {
			Title string `json:"title"`
		} `json:"metadata"`
	} `json:"media"`
}

// readABSAPI is readABS and readABSOthers over the API.
func readABSAPI(ctx context.Context, r Remote, hc *http.Client) ([]absRow, map[string]int, error) {
	if err := checkRemoteURL("Audiobookshelf", r.URL); err != nil {
		return nil, nil, err
	}
	if r.APIKey == "" {
		return nil, nil, remoteErr(nil, "Укажите API-токен %s", "Audiobookshelf")
	}
	c := newAPIClient("Audiobookshelf", r.URL, hc, absAuthHint)
	c.headers["Authorization"] = "Bearer " + r.APIKey
	var me absAPIUser
	if err := c.get(ctx, "/api/me", nil, &me); err != nil {
		return nil, nil, err
	}
	if me.ID == "" {
		return nil, nil, remoteErr(nil, "По адресу %s отвечает не %s", c.shown(), "Audiobookshelf")
	}
	users := []absAPIUser{me}
	var list struct {
		Users []absAPIUser `json:"users"`
	}
	err := c.get(ctx, "/api/users", nil, &list)
	switch {
	case err == nil:
		users = users[:0]
		for _, u := range list.Users {
			if u.ID == me.ID {
				users = append(users, me)
				continue
			}
			var full absAPIUser
			if err := c.get(ctx, "/api/users/"+url.PathEscape(u.ID), nil, &full); err != nil {
				return nil, nil, err
			}
			users = append(users, full)
		}
	case !isAuthErr(err):
		return nil, nil, err
	}
	items := map[string]*absAPIItem{} // nil: gone from the library
	var out []absRow
	others := map[string]int{}
	for _, u := range users {
		for _, p := range u.MediaProgress {
			if p.EpisodeID != "" || (p.MediaItemType != "" && p.MediaItemType != "book") {
				typ := p.MediaItemType
				if typ == "" || typ == "podcastEpisode" {
					typ = "подкасты"
				}
				others[typ]++
				continue
			}
			it, ok := items[p.LibraryItemID]
			if !ok {
				var v absAPIItem
				err := c.get(ctx, "/api/items/"+url.PathEscape(p.LibraryItemID), nil, &v)
				switch {
				case err == nil:
					it = &v
				case !isNotFound(err):
					return nil, nil, err
				}
				items[p.LibraryItemID] = it
			}
			if it == nil {
				continue // the database's join drops it too
			}
			row := absRow{userID: u.ID, userName: u.Username, duration: p.Duration, current: p.CurrentTime, finished: p.IsFinished,
				ebookLoc: p.EbookLocation, ebookProgress: p.EbookProgress, itemPath: it.Path, itemTitle: it.Media.Metadata.Title, at: neverTime}
			if p.LastUpdate > 0 {
				row.at = time.UnixMilli(p.LastUpdate).UTC()
			}
			out = append(out, row)
		}
	}
	return out, others, nil
}
