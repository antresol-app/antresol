package mediaimport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Jellyfin over its HTTP API: what readJellyfin reads from jellyfin.db,
// from /Users and /Items?userId=… (the user's played, started and
// favourite items with their UserData, Path and ProviderIds). The rows are
// the database's, so the import matches and writes them the same way.

const jfAuthHint = "Jellyfin не принял ключ или пароль — API-ключ создают в Jellyfin: «Панель управления → API-ключи»"

// jfAuthorization is the MediaBrowser header Jellyfin takes a token in.
func jfAuthorization(token string) string {
	h := `MediaBrowser Client="Antresol", Device="Antresol", DeviceId="antresol-migrate", Version="1"`
	if token != "" {
		h += `, Token="` + token + `"`
	}
	return h
}

// jellyfinClient is a client signed in to Jellyfin: by the kept token, the
// API key, else the user name and password (AuthenticateByName); the
// token is returned to be kept.
func jellyfinClient(ctx context.Context, r Remote, hc *http.Client) (*apiClient, string, error) {
	if err := checkRemoteURL("Jellyfin", r.URL); err != nil {
		return nil, "", err
	}
	c := newAPIClient("Jellyfin", r.URL, hc, jfAuthHint)
	token := r.token
	switch {
	case token != "":
	case r.APIKey != "":
		token = r.APIKey
	case r.Username != "":
		c.headers["Authorization"] = jfAuthorization("")
		var out struct {
			AccessToken string `json:"AccessToken"`
		}
		err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", nil, map[string]string{"Username": r.Username, "Pw": r.Password}, &out)
		if isAuthErr(err) {
			return nil, "", remoteErr(err, "Jellyfin не принял имя пользователя или пароль")
		}
		if err != nil {
			return nil, "", err
		}
		if out.AccessToken == "" {
			return nil, "", remoteErr(nil, "По адресу %s отвечает не %s", c.shown(), "Jellyfin")
		}
		token = out.AccessToken
	default:
		return nil, "", remoteErr(nil, "Укажите API-ключ Jellyfin или имя пользователя и пароль")
	}
	c.headers["Authorization"] = jfAuthorization(token)
	return c, token, nil
}

type jfAPIUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type jfAPIItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	Path              string            `json:"Path"`
	SeriesName        string            `json:"SeriesName"`
	SeriesID          string            `json:"SeriesId"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	IndexNumber       *int64            `json:"IndexNumber"`
	ParentIndexNumber *int64            `json:"ParentIndexNumber"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	UserData          struct {
		PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
		Played                bool   `json:"Played"`
		IsFavorite            bool   `json:"IsFavorite"`
		LastPlayedDate        string `json:"LastPlayedDate"`
	} `json:"UserData"`
}

type jfAPIPage struct {
	Items            []jfAPIItem `json:"Items"`
	TotalRecordCount int         `json:"TotalRecordCount"`
}

// jfPage is how many items one /Items answer holds.
const jfPage = 500

// jfFolderTypes are the types whose «played» Jellyfin works out from their
// children (a series, a season, a folder): not a mark of their own.
var jfFolderTypes = map[string]bool{
	"Series": true, "Season": true, "BoxSet": true, "Folder": true, "CollectionFolder": true, "UserView": true,
	"MusicAlbum": true, "MusicArtist": true, "Playlist": true, "PhotoAlbum": true, "AggregateFolder": true,
}

// jfFullType is the database's Type of an API type.
func jfFullType(t string) string {
	switch t {
	case "Movie":
		return jfTypeMovie
	case "Episode":
		return jfTypeEpisode
	case "Series":
		return jfTypeSeries
	}
	return t
}

func jfTmdb(p map[string]string) int64 {
	for k, v := range p {
		if strings.EqualFold(k, "tmdb") {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// jfListUsers lists every user (an API key, an administrator's login); a
// login without the right to see the others is its own user (/Users/Me).
func jfListUsers(ctx context.Context, c *apiClient) ([]jfAPIUser, error) {
	var users []jfAPIUser
	err := c.get(ctx, "/Users", nil, &users)
	if isAuthErr(err) {
		var me jfAPIUser
		if err := c.get(ctx, "/Users/Me", nil, &me); err != nil {
			return nil, err
		}
		return []jfAPIUser{me}, nil
	}
	return users, err
}

// jfItems reads every item of user passing filter (IsPlayed, IsResumable,
// IsFavorite), page by page.
func jfItems(ctx context.Context, c *apiClient, user, filter string, extra url.Values) ([]jfAPIItem, error) {
	var out []jfAPIItem
	for start := 0; ; start += jfPage {
		q := url.Values{"userId": {user}, "Recursive": {"true"}, "Fields": {"Path,ProviderIds"}, "EnableUserData": {"true"},
			"EnableImages": {"false"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(jfPage)}}
		if filter != "" {
			q.Set("Filters", filter)
		}
		for k, v := range extra {
			q[k] = v
		}
		var page jfAPIPage
		if err := c.get(ctx, "/Items", q, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if len(page.Items) < jfPage || start+len(page.Items) >= page.TotalRecordCount {
			return out, nil
		}
	}
}

// readJellyfinAPI is readJellyfin over the API.
func readJellyfinAPI(ctx context.Context, c *apiClient) (jfData, error) {
	out := jfData{tmdb: map[string]int64{}, others: map[string]int{}}
	users, err := jfListUsers(ctx, c)
	if err != nil {
		return out, err
	}
	needSeries := map[string]string{} // series id → a user who may see it
	for _, u := range users {
		seen := map[string]bool{}
		fav := map[string]bool{}
		for _, filter := range []string{"IsPlayed", "IsResumable", "IsFavorite"} {
			items, err := jfItems(ctx, c, u.ID, filter, nil)
			if err != nil {
				return out, err
			}
			for _, it := range items {
				short := it.Type
				r := jfRow{itemID: it.ID, userID: u.ID, userName: u.Name, typ: jfFullType(it.Type), name: it.Name, path: it.Path,
					seriesName: it.SeriesName, seriesID: it.SeriesID, runTicks: it.RunTimeTicks, posTicks: it.UserData.PlaybackPositionTicks,
					played: it.UserData.Played, favorite: it.UserData.IsFavorite}
				if it.IndexNumber != nil {
					r.index = sql.NullInt64{Int64: *it.IndexNumber, Valid: true}
				}
				if it.ParentIndexNumber != nil {
					r.parentIndex = sql.NullInt64{Int64: *it.ParentIndexNumber, Valid: true}
				}
				r.at, _ = parseSourceTime(it.UserData.LastPlayedDate)
				if n := jfTmdb(it.ProviderIDs); n > 0 {
					out.tmdb[it.ID] = n
				}
				video := r.typ == jfTypeMovie || r.typ == jfTypeEpisode
				watched := r.played || r.posTicks > 0
				switch {
				case filter != "IsFavorite" && video && watched:
					if !seen[it.ID] {
						seen[it.ID] = true
						out.progress = append(out.progress, r)
						if r.typ == jfTypeEpisode && r.seriesID != "" {
							needSeries[r.seriesID] = u.ID
						}
					}
				case filter == "IsFavorite" && (r.typ == jfTypeMovie || r.typ == jfTypeSeries) && r.favorite:
					if !fav[it.ID] {
						fav[it.ID] = true
						out.favorites = append(out.favorites, r)
					}
				case filter == "IsFavorite" && r.favorite, filter != "IsFavorite" && !video && watched && !jfFolderTypes[short]:
					if strings.HasSuffix(short, "LiveTvChannel") || short == "TvChannel" {
						continue
					}
					key := filter + "\x00" + it.ID
					if filter == "IsResumable" {
						key = "IsPlayed\x00" + it.ID // one mark per item, as in the database
					}
					if seen[key] {
						continue
					}
					seen[key] = true
					if ru, ok := jfOtherKinds[short]; ok {
						short = ru
					}
					out.others[short]++
				}
			}
		}
	}
	// the episodes' series: their TMDB ids match an episode the path misses
	for id := range needSeries {
		if _, ok := out.tmdb[id]; ok {
			delete(needSeries, id)
		}
	}
	if err := jfSeriesTmdb(ctx, c, needSeries, out.tmdb); err != nil {
		return out, err
	}
	var ch jfAPIPage
	if err := c.get(ctx, "/LiveTv/Channels", url.Values{"Limit": {"0"}}, &ch); err == nil {
		out.channels = ch.TotalRecordCount
	} else if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, nil
}

// jfSeriesTmdb adds the TMDB ids of the series in need (id → a user who
// may see it), fifty per request.
func jfSeriesTmdb(ctx context.Context, c *apiClient, need map[string]string, tmdb map[string]int64) error {
	byUser := map[string][]string{}
	for id, u := range need {
		byUser[u] = append(byUser[u], id)
	}
	for u, ids := range byUser {
		for len(ids) > 0 {
			n := min(50, len(ids))
			items, err := jfItems(ctx, c, u, "", url.Values{"Ids": {strings.Join(ids[:n], ",")}})
			if err != nil {
				var re *RemoteError
				if errors.As(err, &re) {
					return nil // the paths still match; only the TMDB fallback is lost
				}
				return err
			}
			for _, it := range items {
				if n := jfTmdb(it.ProviderIDs); n > 0 {
					tmdb[it.ID] = n
				}
			}
			ids = ids[n:]
		}
	}
	return nil
}
