package mediaimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Kavita over its HTTP API: what readKavita reads from kavita.db. A Kavita
// API key is a user's own and opens only that user's progress, so one key
// is typed per user (several, separated by spaces or commas). Each key signs
// in (/api/Plugin/authenticate); the series the user has started
// (/api/Series/all-v2, pagesRead > 0) are read volume by volume
// (/api/Series/volumes): every chapter's pagesRead and its first file.

const kvAuthHint = "Kavita не принял API-ключ — он в Kavita: «Настройки → Учётная запись → API-ключ»"

// kvKeys splits the typed keys.
func kvKeys(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
}

type kvAPISeries struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	PagesRead int64  `json:"pagesRead"`
}

type kvAPIChapter struct {
	ID                     int64  `json:"id"`
	Pages                  int64  `json:"pages"`
	PagesRead              int64  `json:"pagesRead"`
	LastReadingProgressUtc string `json:"lastReadingProgressUtc"`
	LastReadingProgress    string `json:"lastReadingProgress"`
	Files                  []struct {
		ID       int64  `json:"id"`
		FilePath string `json:"filePath"`
	} `json:"files"`
}

type kvAPIVolume struct {
	Chapters []kvAPIChapter `json:"chapters"`
}

const kvPage = 100

// kvUserID is the user's id in Kavita's token (its "nameid" claim), else
// the user name.
func kvUserID(token, name string) string {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var c struct {
				NameID json.RawMessage `json:"nameid"`
			}
			if json.Unmarshal(b, &c) == nil && len(c.NameID) > 0 {
				var s string
				if json.Unmarshal(c.NameID, &s) == nil && s != "" {
					return s
				}
				var n int64
				if json.Unmarshal(c.NameID, &n) == nil && n > 0 {
					return strconv.FormatInt(n, 10)
				}
			}
		}
	}
	return "name:" + name
}

// readKavitaAPI is readKavita over the API, for every key of r.
func readKavitaAPI(ctx context.Context, r Remote, hc *http.Client) ([]kvRow, error) {
	if err := checkRemoteURL("Kavita", r.URL); err != nil {
		return nil, err
	}
	keys := kvKeys(r.APIKey)
	if len(keys) == 0 {
		return nil, remoteErr(nil, "Укажите API-ключ %s", "Kavita")
	}
	var out []kvRow
	seenUser := map[string]bool{}
	for _, key := range keys {
		c := newAPIClient("Kavita", r.URL, hc, kvAuthHint)
		var me struct {
			Username string `json:"username"`
			Token    string `json:"token"`
		}
		q := url.Values{"apiKey": {key}, "pluginName": {"Antresol"}}
		if err := c.do(ctx, http.MethodPost, "/api/Plugin/authenticate", q, nil, &me); err != nil {
			return nil, err
		}
		if me.Token == "" {
			return nil, remoteErr(nil, "По адресу %s отвечает не %s", c.shown(), "Kavita")
		}
		uid := kvUserID(me.Token, me.Username)
		if seenUser[uid] {
			continue // the same user's key twice
		}
		seenUser[uid] = true
		c.headers["Authorization"] = "Bearer " + me.Token
		series, err := kvStartedSeries(ctx, c)
		if err != nil {
			return nil, err
		}
		for _, s := range series {
			var vols []kvAPIVolume
			if err := c.get(ctx, "/api/Series/volumes", url.Values{"seriesId": {strconv.FormatInt(s.ID, 10)}}, &vols); err != nil {
				return nil, err
			}
			for _, v := range vols {
				for _, ch := range v.Chapters {
					if ch.PagesRead <= 0 || len(ch.Files) == 0 {
						continue
					}
					first := ch.Files[0]
					for _, f := range ch.Files[1:] {
						if f.ID < first.ID {
							first = f
						}
					}
					row := kvRow{userID: uid, userName: me.Username, pagesRead: ch.PagesRead, pages: ch.Pages, filePath: first.FilePath}
					at := ch.LastReadingProgressUtc
					if at == "" {
						at = ch.LastReadingProgress
					}
					row.at, _ = parseSourceTime(at)
					out = append(out, row)
				}
			}
		}
	}
	return out, nil
}

// kvStartedSeries lists the series the signed-in user has read pages of:
// /api/Series/all-v2 (Kavita 0.7.14 and newer), else /api/Series/all.
func kvStartedSeries(ctx context.Context, c *apiClient) ([]kvAPISeries, error) {
	var out []kvAPISeries
	v2 := true
	for page := 1; ; page++ {
		q := url.Values{"PageNumber": {strconv.Itoa(page)}, "PageSize": {strconv.Itoa(kvPage)}}
		var list []kvAPISeries
		var err error
		if v2 {
			err = c.do(ctx, http.MethodPost, "/api/Series/all-v2", q, map[string]any{"statements": []any{}, "combination": 1, "limitTo": 0}, &list)
			if page == 1 && isNotFound(err) {
				v2, err = false, nil
			}
		}
		if !v2 {
			q.Set("libraryId", "0")
			err = c.do(ctx, http.MethodPost, "/api/Series/all", q, map[string]any{}, &list)
		}
		if err != nil {
			return nil, err
		}
		for _, s := range list {
			if s.PagesRead > 0 {
				out = append(out, s)
			}
		}
		if len(list) < kvPage {
			return out, nil
		}
	}
}
