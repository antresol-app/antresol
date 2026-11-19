package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/version"
)

// The MCP server's tools. Each needs a scope (read, act, admin: the key's,
// see auth.APIKey); tools/list shows only what the key may run. A tool
// calls the /api routes (toolCall.call), so the profile's own limits
// (rating, kids, «Моя медиатека») apply as they do in the apps. There is
// no tool that deletes anything.

// mcpDownloaderHook: the download tools (discover_titles, download_title)
// work through the addon that fulfils the kids' requests (it declares this
// hook and downloads what an admin approves): its own adults' routes
// /api/discover and /api/acquire, through the addon proxy
// (/api/addons/{id}/x/…). No such addon on: the tools are not listed.
const mcpDownloaderHook = "requests.title"

type mcpTool struct {
	name, title, description string
	scope                    string // auth.ScopeRead | ScopeAct | ScopeAdmin
	downloads                bool   // needs the downloading addon (mcpDownloaderHook)
	idempotent               bool
	schema                   map[string]any
	run                      func(c *toolCall, a mcpArgs) (any, error)
}

func (t mcpTool) describe() map[string]any {
	schema := t.schema
	if schema == nil {
		schema = obj(nil)
	}
	return map[string]any{
		"name": t.name, "title": t.title, "description": t.description, "inputSchema": schema,
		"annotations": map[string]any{
			"title":           t.title,
			"readOnlyHint":    t.scope == auth.ScopeRead,
			"destructiveHint": false,
			"idempotentHint":  t.idempotent || t.scope == auth.ScopeRead,
			"openWorldHint":   t.downloads,
		},
	}
}

// obj is a JSON Schema object of props, with required names.
func obj(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}

// tools: what the request's key may run, in this order.
func (m *mcpServer) tools(r *http.Request) []mcpTool {
	key, _ := auth.APIKeyFrom(r.Context())
	u := userOf(r)
	var out []mcpTool
	for _, t := range mcpTools {
		switch t.scope {
		case auth.ScopeAct:
			if key.Scope == auth.ScopeRead {
				continue
			}
		case auth.ScopeAdmin:
			if key.Scope != auth.ScopeAdmin || !u.IsAdmin {
				continue
			}
		}
		if t.downloads && m.downloader(r.Context()) == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

var mcpTools = []mcpTool{
	{
		name: "search_library", title: "Search the library", scope: auth.ScopeRead,
		description: "Search the server's library (films, series, music, books, audiobooks, comics) by title or name. Returns sections of matching items with their ids.",
		schema:      obj(map[string]any{"query": prop("string", "What to look for: a title, an artist, an author")}, "query"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			q := a.str("query")
			if q == "" {
				return nil, errors.New("query is required")
			}
			var out map[string]any
			if err := c.call(http.MethodGet, "/api/search?q="+url.QueryEscape(q), nil, &out); err != nil {
				return nil, err
			}
			for k, v := range out {
				if l, ok := v.([]any); ok && len(l) == 0 {
					delete(out, k)
				}
			}
			return slim(out), nil
		},
	},
	{
		name: "get_item", title: "Item details", scope: auth.ScopeRead,
		description: "Details of one library item by its id: overview, year, genres, cast, seasons and episodes of a series, the profile's progress.",
		schema:      obj(map[string]any{"id": prop("integer", "The item's id (from search_library and the other tools)")}, "id"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			id, ok := a.num("id")
			if !ok || id <= 0 {
				return nil, errors.New("id is required")
			}
			var out any
			if err := c.call(http.MethodGet, fmt.Sprintf("/api/items/%d", id), nil, &out); err != nil {
				return nil, err
			}
			return slim(out), nil
		},
	},
	{
		name: "continue_watching", title: "Continue watching", scope: auth.ScopeRead,
		description: "What the profile is in the middle of watching (with progress 0..1), and new episodes of the titles it follows.",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			return homeShelves(c, "continue", "tracked-new")
		},
	},
	{
		name: "recently_added", title: "Recently added", scope: auth.ScopeRead,
		description: "Films and series episodes recently added to the library.",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			return homeShelves(c, "recent-movies", "recent-series")
		},
	},
	{
		name: "tv_now", title: "On TV now", scope: auth.ScopeRead,
		description: "The server's TV channels with what is on now and what is next (start and end times).",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			var out struct {
				Channels []map[string]any `json:"channels"`
			}
			if err := c.call(http.MethodGet, "/api/channels", nil, &out); err != nil {
				return nil, err
			}
			list := []any{}
			for _, ch := range out.Channels {
				row := map[string]any{"number": ch["number"], "name": ch["name"]}
				for _, k := range []string{"now", "next"} {
					if p, ok := ch[k].(map[string]any); ok {
						row[k] = pick(p, "title", "subtitle", "year", "start", "end", "itemId")
					}
				}
				list = append(list, row)
			}
			return map[string]any{"channels": list}, nil
		},
	},
	{
		name: "upcoming_episodes", title: "Upcoming episodes", scope: auth.ScopeRead,
		description: "The calendar of the episodes and releases coming out for the titles the profile follows, day by day.",
		schema:      obj(map[string]any{"days": prop("integer", "How many days ahead, from today (1-42, default 14)")}),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			days, ok := a.num("days")
			if !ok || days <= 0 {
				days = 14
			}
			days = min(days, calendarMaxDays)
			from := time.Now()
			to := from.AddDate(0, 0, int(days))
			var out any
			path := "/api/tracking/calendar?from=" + from.Format(time.DateOnly) + "&to=" + to.Format(time.DateOnly)
			if err := c.call(http.MethodGet, path, nil, &out); err != nil {
				return nil, err
			}
			return slim(out), nil
		},
	},
	{
		name: "server_status", title: "Server status", scope: auth.ScopeRead,
		description: "The server's version, its addons, the media disk's usage and the library scan's state.",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			out := map[string]any{"version": version.Version}
			var disk any
			if err := c.call(http.MethodGet, "/api/disk", nil, &disk); err == nil {
				out["disk"] = disk
			}
			var scan any
			if err := c.call(http.MethodGet, "/api/library/status", nil, &scan); err == nil {
				out["libraryScan"] = slim(scan)
			}
			var list []map[string]any
			path := "/api/addons/web"
			if userOf(c.r).IsAdmin {
				path = "/api/addons"
			}
			if err := c.call(http.MethodGet, path, nil, &list); err == nil {
				addons := []any{}
				for _, a := range list {
					addons = append(addons, pick(a, "id", "name", "version", "status", "enabled", "update"))
				}
				out["addons"] = slim(addons)
			}
			return out, nil
		},
	},
	{
		name: "discover_titles", title: "Find titles to download", scope: auth.ScopeRead, downloads: true,
		description: "Look up films and series that are not necessarily in the library yet (by the catalog), to download them with download_title. Returns tmdbId, kind, year and whether the title is already in the library.",
		schema:      obj(map[string]any{"query": prop("string", "The title to look for")}, "query"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			q := a.str("query")
			if q == "" {
				return nil, errors.New("query is required")
			}
			var list []map[string]any
			if err := c.call(http.MethodGet, "/api/addons/"+c.m.downloader(c.r.Context())+"/x/discover?q="+url.QueryEscape(q), nil, &list); err != nil {
				return nil, err
			}
			out := []any{}
			for _, it := range list {
				row := pick(it, "kind", "tmdbId", "title", "subtitle", "year", "inLibrary", "libraryItemId", "overview")
				if s, ok := row["overview"].(string); ok && len([]rune(s)) > 300 {
					row["overview"] = string([]rune(s)[:300]) + "…"
				}
				out = append(out, row)
			}
			return out, nil
		},
	},
	{
		name: "set_watched", title: "Mark watched", scope: auth.ScopeAct, idempotent: true,
		description: "Mark a library item (a film, an episode, a season or a whole series) watched or not watched for the profile.",
		schema: obj(map[string]any{
			"id":      prop("integer", "The item's id"),
			"watched": prop("boolean", "true: watched (default), false: not watched"),
		}, "id"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			id, ok := a.num("id")
			if !ok || id <= 0 {
				return nil, errors.New("id is required")
			}
			w := a.boolean("watched", true)
			if err := c.call(http.MethodPost, fmt.Sprintf("/api/items/%d/watched", id), map[string]any{"watched": w}, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "watched": w}, nil
		},
	},
	{
		name: "download_title", title: "Download a title", scope: auth.ScopeAct, downloads: true,
		description: "Download a film or a series season to the library: the server picks the best release itself. Take tmdbId and kind from discover_titles.",
		schema: obj(map[string]any{
			"kind":    enum("movie or series", "movie", "series"),
			"tmdbId":  prop("integer", "The title's TMDB id (from discover_titles)"),
			"season":  prop("integer", "For a series: the season to download"),
			"quality": enum("The wanted quality (default: the server's choice)", "720p", "1080p", "2160p"),
			"title":   prop("string", "The title's name (helps the release search)"),
			"year":    prop("integer", "The title's year"),
		}, "kind", "tmdbId"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			kind := a.str("kind")
			if kind != "movie" && kind != "series" {
				return nil, errors.New("kind must be movie or series")
			}
			id, ok := a.num("tmdbId")
			if !ok || id <= 0 {
				return nil, errors.New("tmdbId is required")
			}
			in := map[string]any{"kind": kind, "tmdbId": id, "mode": "download"}
			if s, ok := a.num("season"); ok && s > 0 {
				in["season"] = s
			}
			if q := a.str("quality"); q != "" {
				in["quality"] = q
			}
			if t := a.str("title"); t != "" {
				in["title"] = t
			}
			if y, ok := a.num("year"); ok && y > 0 {
				in["year"] = y
			}
			var out map[string]any
			if err := c.call(http.MethodPost, "/api/addons/"+c.m.downloader(c.r.Context())+"/x/acquire", in, &out); err != nil {
				return nil, err
			}
			return pick(out, "status", "existing", "note"), nil
		},
	},
	{
		name: "scan_library", title: "Scan the library", scope: auth.ScopeAdmin, idempotent: true,
		description: "Start a scan of the library's folders for new and changed files (admin).",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			var out any
			if err := c.call(http.MethodPost, "/api/library/scan", nil, &out); err != nil {
				return nil, err
			}
			return slim(out), nil
		},
	},
	{
		name: "restart_addon", title: "Restart an addon", scope: auth.ScopeAdmin, idempotent: true,
		description: "Restart one of the server's addons by its id (see server_status) (admin).",
		schema:      obj(map[string]any{"id": prop("string", "The addon's id")}, "id"),
		run: func(c *toolCall, a mcpArgs) (any, error) {
			id := a.str("id")
			if id == "" || strings.ContainsAny(id, "/?#%") {
				return nil, errors.New("id is required")
			}
			if err := c.call(http.MethodPost, "/api/addons/"+id+"/disable", nil, nil); err != nil {
				return nil, err
			}
			if err := c.call(http.MethodPost, "/api/addons/"+id+"/enable", nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "restarted": true}, nil
		},
	},
	{
		name: "list_profiles", title: "List profiles", scope: auth.ScopeAdmin,
		description: "The server's profiles: names, which are admins and which are kids' (admin).",
		run: func(c *toolCall, _ mcpArgs) (any, error) {
			var list []map[string]any
			if err := c.call(http.MethodGet, "/api/users", nil, &list); err != nil {
				return nil, err
			}
			out := []any{}
			for _, u := range list {
				out = append(out, pick(u, "id", "name", "isAdmin", "isKid", "hasPassword", "maxRating"))
			}
			return out, nil
		},
	},
}

// homeShelves: the cards of /api/home's shelves ids, by id.
func homeShelves(c *toolCall, ids ...string) (any, error) {
	var home struct {
		Shelves []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Cards []any  `json:"cards"`
		} `json:"shelves"`
	}
	if err := c.call(http.MethodGet, "/api/home", nil, &home); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, s := range home.Shelves {
		for _, id := range ids {
			if s.ID == id {
				out[id] = map[string]any{"title": s.Title, "items": slim(s.Cards)}
			}
		}
	}
	return out, nil
}

// pick keeps the named keys of m that are there.
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

// slimDrop: the fields a model has no use for (pictures, links for the
// web app's own pages, colours, internals).
var slimDrop = map[string]bool{
	"poster": true, "backdrop": true, "logo": true, "thumb": true, "photo": true, "avatar": true,
	"image": true, "images": true, "badges": true, "why": true, "href": true, "frames": true,
	"trickplay": true, "logoUrl": true, "streamUrl": true, "still": true, "profile": true,
	"posterUrl": true, "backdropUrl": true, "art": true, "color": true, "colors": true,
}

// slimMaxList: the longest list a tool returns.
const slimMaxList = 60

// slim drops slimDrop, empty values and the tails of long lists.
func slim(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if slimDrop[k] || strings.HasSuffix(k, "Url") || strings.HasSuffix(k, "URL") {
				continue
			}
			x = slim(x)
			switch y := x.(type) {
			case nil:
				continue
			case string:
				if y == "" {
					continue
				}
			case bool:
				if !y {
					continue
				}
			case []any:
				if len(y) == 0 {
					continue
				}
			case map[string]any:
				if len(y) == 0 {
					continue
				}
			}
			out[k] = x
		}
		return out
	case []any:
		n := min(len(t), slimMaxList)
		out := make([]any, 0, n)
		for _, x := range t[:n] {
			out = append(out, slim(x))
		}
		return out
	}
	return v
}
