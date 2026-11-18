package addonkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is what the core passes to the addon's process (contract: "The
// process").
type Env struct {
	ID       string // ANTRESOL_ADDON_ID
	DataDir  string // ANTRESOL_ADDON_DATA_DIR: the addon's own files, kept across versions
	Socket   string // ANTRESOL_ADDON_SOCKET: serve HTTP here
	CoreURL  string // ANTRESOL_CORE_URL
	Token    string // ANTRESOL_ADDON_TOKEN: a fresh one on every start
	Locale   string // ANTRESOL_LOCALE: ru or en
	Contract int    // ANTRESOL_CONTRACT
}

// FromEnv reads the process environment. It fails when the program was
// not started by an Antresol server (no socket, no contract number).
func FromEnv() (Env, error) {
	e := Env{
		ID:      os.Getenv("ANTRESOL_ADDON_ID"),
		DataDir: os.Getenv("ANTRESOL_ADDON_DATA_DIR"),
		Socket:  os.Getenv("ANTRESOL_ADDON_SOCKET"),
		CoreURL: os.Getenv("ANTRESOL_CORE_URL"),
		Token:   os.Getenv("ANTRESOL_ADDON_TOKEN"),
		Locale:  os.Getenv("ANTRESOL_LOCALE"),
	}
	if e.Socket == "" {
		return Env{}, errors.New("addonkit: no ANTRESOL_ADDON_SOCKET: this program is an Antresol addon, the Antresol server starts it")
	}
	n, err := strconv.Atoi(os.Getenv("ANTRESOL_CONTRACT"))
	if err != nil {
		return Env{}, fmt.Errorf("addonkit: ANTRESOL_CONTRACT %q is not a number", os.Getenv("ANTRESOL_CONTRACT"))
	}
	e.Contract = n
	return e, nil
}

// Prefix is the addon's own variables' prefix: ANTRESOL_ plus the id
// upper-cased with - as _ (my-addon: ANTRESOL_MY_ADDON_). The server
// passes the variables with it through to this addon only.
func (e Env) Prefix() string {
	return "ANTRESOL_" + strings.ToUpper(strings.ReplaceAll(e.ID, "-", "_")) + "_"
}

// Var is the addon's own variable Prefix()+name ("" when unset).
func (e Env) Var(name string) string { return os.Getenv(e.Prefix() + name) }

// Timeouts of the core API calls. A caller's earlier deadline wins.
const (
	DefaultTimeout = 30 * time.Second
	PathsTimeout   = 5 * time.Minute  // FilePaths: the whole library, streamed
	ImportTimeout  = 30 * time.Minute // Import: a full library scan
)

// maxTMDBIDs is the core's limit of ids per /library/tmdb/{kind} call
// (and per list of a /watched call, and of a /progress call).
const maxTMDBIDs = 500

// APIError is an answer of the core API other than 2xx.
type APIError struct {
	Status  int
	Message string // the answer's {"error"}, else its status text
}

func (e *APIError) Error() string {
	return fmt.Sprintf("antresol core API: %d %s", e.Status, e.Message)
}

// IsNotFound: err is the core's 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// Client calls the core API v1 (ANTRESOL_CORE_URL/api/addon-core/v1) with
// the addon's token.
type Client struct {
	base  string
	token string
	http  *http.Client

	observe func(context.Context) // tests: the context of each request
}

// NewClient is a client of the core e names.
func NewClient(e Env) *Client {
	return &Client{
		base:  strings.TrimSuffix(e.CoreURL, "/") + "/api/addon-core/v1",
		token: e.Token,
		http: &http.Client{
			// a redirect is the core's answer, never followed (the token
			// would go along)
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// do sends method path (below base; already escaped) with body as JSON
// and decodes a 2xx answer into out. timeout bounds the call.
func (c *Client) do(ctx context.Context, timeout time.Duration, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if c.observe != nil {
		c.observe(ctx)
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode}
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			ae.Message = e.Error
		} else {
			ae.Message = http.StatusText(resp.StatusCode)
		}
		return ae
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("antresol core API: %s %s: %w", method, path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, DefaultTimeout, http.MethodGet, path, nil, out)
}

// found turns the core's 404 into ok=false.
func found(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// query is "?" + v encoded, or "" when v is empty.
func query(v url.Values) string {
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// Info is GET /info: the addon's id and version, the core's contract,
// tools, public address and library folders.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var i Info
	err := c.get(ctx, "/info", &i)
	return i, err
}

// Event publishes addon.<id>.<name> with data to the app (permission
// "events"). A name starting with "admin." reaches admins only; kids get
// none.
func (c *Client) Event(ctx context.Context, name string, data any) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, "/events", map[string]any{"name": name, "data": data}, nil)
}

// Item is GET /library/items/{id} (library.read); a missing one is an
// *APIError with Status 404 (IsNotFound).
func (c *Client) Item(ctx context.Context, id int64) (Item, error) {
	var it Item
	err := c.get(ctx, fmt.Sprintf("/library/items/%d", id), &it)
	return it, err
}

// ItemFiles is GET /library/items/{id}/files: the item's own files, then,
// for a series or a season, its episodes' (with Season and Episode).
func (c *Client) ItemFiles(ctx context.Context, id int64) ([]File, error) {
	var fs []File
	err := c.get(ctx, fmt.Sprintf("/library/items/%d/files", id), &fs)
	return fs, err
}

// ItemByTMDB is the library's movie or series ("movie", "series") with
// this TMDB id; ok is false when the library has none.
func (c *Client) ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (Item, bool, error) {
	var it Item
	ok, err := found(c.get(ctx, fmt.Sprintf("/library/tmdb/%s/%d", url.PathEscape(kind), tmdbID), &it))
	return it, ok, err
}

// ItemIDsByTMDB maps the TMDB ids the library has to their item ids. Any
// number of ids: they go in calls of at most 500.
func (c *Client) ItemIDsByTMDB(ctx context.Context, kind string, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for len(ids) > 0 {
		n := min(len(ids), maxTMDBIDs)
		parts := make([]string, n)
		for i, id := range ids[:n] {
			parts[i] = strconv.FormatInt(id, 10)
		}
		ids = ids[n:]
		var m map[string]int64
		if err := c.get(ctx, "/library/tmdb/"+url.PathEscape(kind)+query(url.Values{"ids": {strings.Join(parts, ",")}}), &m); err != nil {
			return nil, err
		}
		for k, v := range m {
			tmdb, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("antresol core API: tmdb id %q", k)
			}
			out[tmdb] = v
		}
	}
	return out, nil
}

// Watched is, of the item ids (movies and other leaves) and series ids,
// the ones profile userID watched (a series: every episode). Any number of
// ids: they go in calls of at most 500 per list (permission library.read).
func (c *Client) Watched(ctx context.Context, userID int64, items, series []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for len(items) > 0 || len(series) > 0 {
		ni, ns := min(len(items), maxTMDBIDs), min(len(series), maxTMDBIDs)
		v := url.Values{"userId": {strconv.FormatInt(userID, 10)}}
		set(v, "items", joinIDs(items[:ni]))
		set(v, "series", joinIDs(series[:ns]))
		items, series = items[ni:], series[ns:]
		var body struct {
			Watched map[string]bool `json:"watched"`
		}
		if err := c.get(ctx, "/watched"+query(v), &body); err != nil {
			return nil, err
		}
		for k, on := range body.Watched {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("antresol core API: watched id %q", k)
			}
			if on {
				out[id] = true
			}
		}
	}
	return out, nil
}

// joinIDs is ids, comma-separated.
func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// Progress is, of the item ids, profile userID's progress in the movies
// and episodes it may see, by item id (permission progress). Any number of
// ids: they go in calls of at most 500. An id with no progress, or that the
// profile may not see (over its rating limit, or no movie or episode), is
// not in the answer. An unknown or a kid profile is a 400 *APIError.
func (c *Client) Progress(ctx context.Context, userID int64, itemIDs []int64) (map[int64]ProgressEntry, error) {
	out := map[int64]ProgressEntry{}
	for len(itemIDs) > 0 {
		n := min(len(itemIDs), maxTMDBIDs)
		v := url.Values{"userId": {strconv.FormatInt(userID, 10)}, "items": {joinIDs(itemIDs[:n])}}
		itemIDs = itemIDs[n:]
		var body struct {
			Progress map[string]ProgressEntry `json:"progress"`
		}
		if err := c.get(ctx, "/progress"+query(v), &body); err != nil {
			return nil, err
		}
		for k, e := range body.Progress {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("antresol core API: progress id %q", k)
			}
			out[id] = e
		}
	}
	return out, nil
}

// SaveProgress records profile p.UserID's position in a movie or an
// episode (PUT /progress, permission progress). A refusal comes back as an
// *APIError with the core's message: 404 for an item the profile may not
// see (or that does not exist), 400 for a season or other non-video item, a
// kid or unknown profile, a negative position, or a time more than 5
// minutes ahead of the core's clock.
func (c *Client) SaveProgress(ctx context.Context, p ProgressSave) error {
	return c.do(ctx, DefaultTimeout, http.MethodPut, "/progress", p, nil)
}

// findItem GETs path?v: the item, or ok=false on 404.
func (c *Client) findItem(ctx context.Context, path string, v url.Values) (Item, bool, error) {
	var it Item
	ok, err := found(c.get(ctx, path+query(v), &it))
	return it, ok, err
}

// set adds k=val to v unless val is empty.
func set(v url.Values, k, val string) {
	if val != "" {
		v.Set(k, val)
	}
}

// FindTitle finds a movie or series by tmdbID, an album ("music") by
// creator (the artist) and title, a book or audiobook by title and,
// optionally, creator (the author).
func (c *Client) FindTitle(ctx context.Context, kind string, tmdbID int64, title, creator string) (Item, bool, error) {
	v := url.Values{"kind": {kind}}
	if tmdbID > 0 {
		v.Set("tmdbId", strconv.FormatInt(tmdbID, 10))
	}
	set(v, "title", title)
	set(v, "creator", creator)
	return c.findItem(ctx, "/library/find/title", v)
}

// FindAlbum finds an album by artist and album title.
func (c *Client) FindAlbum(ctx context.Context, artist, album string) (Item, bool, error) {
	return c.findItem(ctx, "/library/find/album", url.Values{"artist": {artist}, "album": {album}})
}

// FindAlbumByMBID finds an album by its MusicBrainz release group.
func (c *Client) FindAlbumByMBID(ctx context.Context, mbid string) (Item, bool, error) {
	return c.findItem(ctx, "/library/find/album", url.Values{"mbid": {mbid}})
}

// FindBook finds a book or audiobook ("book", "audiobook") by title and,
// optionally, author.
func (c *Client) FindBook(ctx context.Context, kind, author, title string) (Item, bool, error) {
	v := url.Values{"kind": {kind}, "title": {title}}
	set(v, "author", author)
	return c.findItem(ctx, "/library/find/book", v)
}

// HaveEpisodes is the set of [season, episode] of the series that have a
// file; empty when the library has no such series.
func (c *Client) HaveEpisodes(ctx context.Context, tmdbID int64) (map[[2]int]bool, error) {
	var pairs [][2]int
	ok, err := found(c.get(ctx, fmt.Sprintf("/library/series/%d/episodes", tmdbID), &pairs))
	out := make(map[[2]int]bool, len(pairs))
	if !ok {
		return out, err
	}
	for _, p := range pairs {
		out[p] = true
	}
	return out, nil
}

// EpisodeItems is the episodes of the series that have a file, with the
// library item of each, for profile userID (an adult one), by season and
// episode (permission library.read): the pairs of HaveEpisodes that the
// profile may see (the series over its rating limit has none), each with
// the id of the episode item holding its file, the id the player saves
// progress under (SaveProgress). Empty, not an error, when the library has
// no such series; an unknown or a kid profile is a 400 *APIError.
func (c *Client) EpisodeItems(ctx context.Context, userID, tmdbID int64) ([]EpisodeItem, error) {
	var body struct {
		Episodes []EpisodeItem `json:"episodes"`
	}
	v := url.Values{"userId": {strconv.FormatInt(userID, 10)}}
	ok, err := found(c.get(ctx, fmt.Sprintf("/library/series/%d/episode-items", tmdbID)+query(v), &body))
	if err != nil {
		return nil, err
	}
	if !ok || body.Episodes == nil {
		return []EpisodeItem{}, nil
	}
	return body.Episodes, nil
}

// SeriesFolder is the absolute folder directly under root (one of
// Info.LibraryRoots as given) that holds the series.
func (c *Client) SeriesFolder(ctx context.Context, root string, tmdbID int64) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	err := c.get(ctx, fmt.Sprintf("/library/series/%d/folder", tmdbID)+query(url.Values{"root": {root}}), &p)
	return p.Path, err
}

// SeriesAudio is the audio track titles of the series' episodes and, with
// the users.read permission, the voice each profile last chose.
func (c *Client) SeriesAudio(ctx context.Context, tmdbID int64) (SeriesAudio, error) {
	var sa SeriesAudio
	err := c.get(ctx, fmt.Sprintf("/library/series/%d/audio", tmdbID), &sa)
	return sa, err
}

// AlbumDir is the album's folder.
func (c *Client) AlbumDir(ctx context.Context, id int64) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	err := c.get(ctx, fmt.Sprintf("/library/albums/%d/dir", id), &p)
	return p.Path, err
}

// AlbumTracks is every track of every album in the album's folder, in
// album and playing order.
func (c *Client) AlbumTracks(ctx context.Context, id int64) ([]Track, error) {
	var ts []Track
	err := c.get(ctx, fmt.Sprintf("/library/albums/%d/tracks", id), &ts)
	return ts, err
}

// FilesUnder is every library file below dir, any depth, without Probe.
func (c *Client) FilesUnder(ctx context.Context, dir string) ([]File, error) {
	var fs []File
	err := c.get(ctx, "/library/files"+query(url.Values{"under": {dir}}), &fs)
	return fs, err
}

// FileByPath is the library file at path, with its Probe; ok is false
// when the library has none there.
func (c *Client) FileByPath(ctx context.Context, path string) (File, bool, error) {
	var fs []File
	if err := c.get(ctx, "/library/files"+query(url.Values{"path": {path}}), &fs); err != nil {
		return File{}, false, err
	}
	if len(fs) == 0 {
		return File{}, false, nil
	}
	return fs[0], true, nil
}

// FileByID is GET /library/files/{id}: the library file, with its probe;
// ok is false when the library has none with this id.
func (c *Client) FileByID(ctx context.Context, id int64) (File, bool, error) {
	var f File
	ok, err := found(c.get(ctx, fmt.Sprintf("/library/files/%d", id), &f))
	return f, ok, err
}

// FilePaths is the path of every library file, missing ones too, in path
// order.
func (c *Client) FilePaths(ctx context.Context) ([]string, error) {
	var ps []string
	err := c.do(ctx, PathsTimeout, http.MethodGet, "/library/paths", nil, &ps)
	return ps, err
}

// SearchTitles is the items of kind whose title, original title or alias
// equals title once normalised. limit is at most 50; 0 takes the core's
// default.
func (c *Client) SearchTitles(ctx context.Context, kind, title string, limit int) ([]Item, error) {
	v := url.Values{"kind": {kind}, "title": {title}}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	var items []Item
	err := c.get(ctx, "/library/search"+query(v), &items)
	return items, err
}

// refPath is /library/refs/<ref> with ref escaped as one segment.
func refPath(ref string) string { return "/library/refs/" + url.PathEscape(ref) }

// RefFiles is this addon's library files with ref, by path.
func (c *Client) RefFiles(ctx context.Context, ref string) ([]RefFile, error) {
	var rf []RefFile
	err := c.get(ctx, refPath(ref), &rf)
	return rf, err
}

// TracksOfRef is the tracks whose file carries this addon's ref.
func (c *Client) TracksOfRef(ctx context.Context, ref string) ([]Track, error) {
	var ts []Track
	err := c.get(ctx, "/library/tracks"+query(url.Values{"ref": {ref}}), &ts)
	return ts, err
}

// Users is the server's profiles (permission users.read).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var us []User
	err := c.get(ctx, "/users", &us)
	return us, err
}

// Studios is GET /settings/studios: the household's favourite studios
// (the Settings screen's «Любимые озвучки»), best first; empty when none.
func (c *Client) Studios(ctx context.Context) ([]string, error) {
	var body struct {
		Studios []string `json:"studios"`
	}
	if err := c.get(ctx, "/settings/studios", &body); err != nil {
		return nil, err
	}
	return nonNil(body.Studios), nil
}

// nonNil is s, or an empty slice for nil (a required JSON list).
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Import adds files or folders at paths to the library (library.write),
// giving them ref and the owner profile when set ("" and 0 for none). It
// scans the whole library: allow minutes (ImportTimeout).
func (c *Client) Import(ctx context.Context, paths []string, ref string, owner int64) (ImportResult, error) {
	body := struct {
		Paths       []string `json:"paths"`
		Ref         string   `json:"ref,omitempty"`
		OwnerUserID int64    `json:"ownerUserId,omitempty"`
	}{nonNil(paths), ref, owner}
	var res ImportResult
	err := c.do(ctx, ImportTimeout, http.MethodPost, "/library/import", body, &res)
	return res, err
}

// BindRef puts ref on exactly the library files at paths; this addon's
// other files with ref lose it. Empty paths clear the ref.
func (c *Client) BindRef(ctx context.Context, ref string, paths []string) (Bound, error) {
	var b Bound
	err := c.do(ctx, DefaultTimeout, http.MethodPut, refPath(ref), map[string][]string{"paths": nonNil(paths)}, &b)
	return b, err
}

// UnbindRef takes ref off this addon's files (the files stay).
func (c *Client) UnbindRef(ctx context.Context, ref string) error {
	return c.do(ctx, DefaultTimeout, http.MethodDelete, refPath(ref), nil, nil)
}

// BindRefs binds each path to its own ref, all at once.
func (c *Client) BindRefs(ctx context.Context, pairs []RefPath) (Bound, error) {
	var b Bound
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/library/refs", map[string][]RefPath{"refs": nonNil(pairs)}, &b)
	return b, err
}

// ClaimOwner makes profile userID an owner of the titles of this addon's
// files with ref, or, when ref is "", of the library files at paths.
func (c *Client) ClaimOwner(ctx context.Context, ref string, paths []string, userID int64) error {
	body := struct {
		Ref    string   `json:"ref,omitempty"`
		Paths  []string `json:"paths,omitempty"`
		UserID int64    `json:"userId"`
	}{Ref: ref, UserID: userID}
	if ref == "" {
		body.Paths = nonNil(paths)
	}
	return c.do(ctx, DefaultTimeout, http.MethodPost, "/library/owners", body, nil)
}

// CacheImage puts the https image at url into the server's image cache
// (permission images); the app shows it at /api/images/<name>.
func (c *Client) CacheImage(ctx context.Context, url string) (string, error) {
	var n struct {
		Name string `json:"name"`
	}
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/images", map[string]string{"url": url}, &n)
	return n.Name, err
}

// PutHolds replaces this addon's whole set of file holds (permission
// files.holds); empty clears it. Push the whole set after every change
// and on start.
func (c *Client) PutHolds(ctx context.Context, holds []Hold) error {
	return c.do(ctx, DefaultTimeout, http.MethodPut, "/files/holds", map[string][]Hold{"holds": nonNil(holds)}, nil)
}

// Holds is this addon's set of file holds as the server keeps it
// (permission files.holds): saved across the server's and the addon's
// restarts, paths in the file browser's form; empty (never nil) when it
// has none.
func (c *Client) Holds(ctx context.Context) ([]Hold, error) {
	var out struct {
		Holds []Hold `json:"holds"`
	}
	if err := c.get(ctx, "/files/holds", &out); err != nil {
		return nil, err
	}
	return nonNil(out.Holds), nil
}

// MetadataBase is the pass-through root of a metadata source ("tmdb",
// "musicbrainz", "openlibrary", "wikidata"; permission metadata): append
// the provider's own path and query.
func (c *Client) MetadataBase(source string) string {
	return c.base + "/metadata/" + url.PathEscape(source)
}

// MetadataHTTP is an HTTP client for MetadataBase URLs: it adds the
// addon's token to requests to the core (and to nothing else) and does
// not follow redirects (a provider's 3xx is the answer).
func (c *Client) MetadataHTTP() *http.Client {
	return &http.Client{
		Timeout:       60 * time.Second,
		Transport:     &bearer{base: c.base + "/", token: c.token, next: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// bearer adds the token to requests below base.
type bearer struct {
	base, token string
	next        http.RoundTripper
}

func (b *bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(r.URL.String(), b.base) {
		return b.next.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// RegisterInput registers an external video the addon serves on its socket
// for playback by in.UserID (POST /play/inputs). A 400 comes back as an
// *APIError with the core's message.
func (c *Client) RegisterInput(ctx context.Context, in InputRequest) (InputCreated, error) {
	in.Sidecars.Audio = nonNil(in.Sidecars.Audio) // [] rather than null
	in.Sidecars.Subtitles = nonNil(in.Sidecars.Subtitles)
	var out InputCreated
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/play/inputs", in, &out)
	return out, err
}

// DropInput forgets a registered input (DELETE /play/inputs/{id}); an
// unknown id is not an error.
func (c *Client) DropInput(ctx context.Context, id string) error {
	_, err := found(c.do(ctx, DefaultTimeout, http.MethodDelete, "/play/inputs/"+url.PathEscape(id), nil, nil))
	return err
}

// Notify queues n for the mail digests of its recipients and reports how
// many profiles it was queued for (those with an address and the topic
// on). A 400 comes back as an *APIError with the core's message.
func (c *Client) Notify(ctx context.Context, n NotifyRequest) (int, error) {
	var out struct {
		Queued int `json:"queued"`
	}
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/notify", n, &out)
	return out.Queued, err
}

// ApproveRequests approves the kids' requests of what an admin started
// getting (permission requests) and reports how many it approved; the
// kids are told. A 400 (AdminID is not an admin) comes back as an
// *APIError with the core's message.
func (c *Client) ApproveRequests(ctx context.Context, a ApproveRequest) (int, error) {
	var out struct {
		Approved int `json:"approved"`
	}
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/requests/approve", a, &out)
	return out.Approved, err
}

// EnsureTitle is POST /tracking/titles (permission tracking): the tracked
// title of kind ("series" | "movie") and TMDB id, made when the core has
// none (name and poster, a TMDB image path, are kept only then; either
// may be ""). Making a title marks it for nobody; KickTitle refreshes it.
func (c *Client) EnsureTitle(ctx context.Context, kind string, tmdbID int64, name, poster string) (TrackingTitle, error) {
	var t TrackingTitle
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/tracking/titles",
		map[string]any{"kind": kind, "tmdbId": tmdbID, "name": name, "poster": poster}, &t)
	return t, err
}

// KickTitle asks the core to refresh a tracked title soon (POST
// /tracking/titles/{id}/kick): its episode list and every kind's sources.
// An unknown id is an *APIError with Status 404 (IsNotFound).
func (c *Client) KickTitle(ctx context.Context, id int64) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, fmt.Sprintf("/tracking/titles/%d/kick", id), nil, nil)
}

// TitleByTMDB is the tracked title of kind and TMDB id (GET
// /tracking/titles?kind&tmdbId); ok is false when the core has none.
func (c *Client) TitleByTMDB(ctx context.Context, kind string, tmdbID int64) (TrackingTitle, bool, error) {
	var t TrackingTitle
	v := url.Values{"kind": {kind}, "tmdbId": {strconv.FormatInt(tmdbID, 10)}}
	ok, err := found(c.get(ctx, "/tracking/titles"+query(v), &t))
	return t, ok, err
}

// TitleByID is the tracked title with this id (GET /tracking/titles/{id});
// ok is false when the core has none.
func (c *Client) TitleByID(ctx context.Context, id int64) (TrackingTitle, bool, error) {
	var t TrackingTitle
	ok, err := found(c.get(ctx, fmt.Sprintf("/tracking/titles/%d", id), &t))
	return t, ok, err
}

// TitleEpisodes is a tracked title's episode list, by season and number
// (GET /tracking/titles/{id}/episodes); an unknown id is a 404 *APIError.
func (c *Client) TitleEpisodes(ctx context.Context, id int64) ([]TrackingEpisode, error) {
	var eps []TrackingEpisode
	err := c.get(ctx, fmt.Sprintf("/tracking/titles/%d/episodes", id), &eps)
	return nonNil(eps), err
}

// TitleSources is every source of a tracked title, of every kind, by
// season, episode, kind and ref (GET /tracking/titles/{id}/sources); an
// unknown id is a 404 *APIError.
func (c *Client) TitleSources(ctx context.Context, id int64) ([]TrackingSource, error) {
	var srcs []TrackingSource
	err := c.get(ctx, fmt.Sprintf("/tracking/titles/%d/sources", id), &srcs)
	return nonNil(srcs), err
}

// QueueFit asks the core to fit a voice-over track (POST /voiceover/fit)
// that is placed, or about to be, at path beside the library file
// fileID, measured against its track in origLang (ISO 639-1; "" when not
// known: the core asks the voiceover.origLang hook). The players leave
// the track out until it is fitted. A path that is not an audio file in
// the same folder as that file is a 400 *APIError.
func (c *Client) QueueFit(ctx context.Context, path string, fileID int64, origLang string) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, "/voiceover/fit",
		map[string]any{"path": path, "fileId": fileID, "origLang": origLang}, nil)
}

// FitState is the fit of the voice-over track at path (GET
// /voiceover/fit?path=); ok is false when the core has none queued.
func (c *Client) FitState(ctx context.Context, path string) (FitState, bool, error) {
	var f FitState
	ok, err := found(c.get(ctx, "/voiceover/fit"+query(url.Values{"path": {path}}), &f))
	return f, ok, err
}

// IsConflict: err is the core's 409 (an import album another claimer
// took, a job past the step asked for).
func IsConflict(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusConflict
}

// claimed turns the core's 409 into ok=false.
func claimed(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if IsConflict(err) {
		return false, nil
	}
	return false, err
}

func importGroupPath(id int64, op string) string { return fmt.Sprintf("/imports/groups/%d/%s", id, op) }

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// ImportActiveCount is how many albums of running import jobs are being
// searched or downloaded (GET /imports/active-count; permission imports).
func (c *Client) ImportActiveCount(ctx context.Context) (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	err := c.get(ctx, "/imports/active-count", &out)
	return out.Count, err
}

// ImportJobIDs are the ids of the import jobs in status, oldest first
// (GET /imports/jobs?status=).
func (c *Client) ImportJobIDs(ctx context.Context, status string) ([]int64, error) {
	var out struct {
		IDs []int64 `json:"ids"`
	}
	err := c.get(ctx, "/imports/jobs"+query(url.Values{"status": {status}}), &out)
	return nonNil(out.IDs), err
}

// ImportJob is one import job (GET /imports/jobs/{id}); ok is false when
// the core has none.
func (c *Client) ImportJob(ctx context.Context, id int64) (ImportJob, bool, error) {
	var j ImportJob
	ok, err := found(c.get(ctx, fmt.Sprintf("/imports/jobs/%d", id), &j))
	return j, ok, err
}

// ImportCandidates are the job's albums a queue may start, oldest first:
// waiting, waiting_space, waiting_download, and failed ones due for their
// automatic retry (failed once, at or before retryBefore) (GET
// /imports/jobs/{id}/candidates?retryBefore=).
func (c *Client) ImportCandidates(ctx context.Context, jobID int64, retryBefore time.Time) ([]ImportGroup, error) {
	var gs []ImportGroup
	v := url.Values{"retryBefore": {retryBefore.UTC().Format(time.RFC3339)}}
	err := c.get(ctx, fmt.Sprintf("/imports/jobs/%d/candidates", jobID)+query(v), &gs)
	return nonNil(gs), err
}

// ImportGroups are the job's albums in state ("": all), oldest first (GET
// /imports/jobs/{id}/groups?state=).
func (c *Client) ImportGroups(ctx context.Context, jobID int64, state string) ([]ImportGroup, error) {
	var gs []ImportGroup
	v := url.Values{}
	set(v, "state", state)
	err := c.get(ctx, fmt.Sprintf("/imports/jobs/%d/groups", jobID)+query(v), &gs)
	return nonNil(gs), err
}

// ImportStillDownloading: the job is still downloading, not cancelled
// meanwhile (GET /imports/jobs/{id}/still-downloading).
func (c *Client) ImportStillDownloading(ctx context.Context, jobID int64) (bool, error) {
	var out struct {
		Downloading bool `json:"downloading"`
	}
	err := c.get(ctx, fmt.Sprintf("/imports/jobs/%d/still-downloading", jobID), &out)
	return out.Downloading, err
}

// ImportGroupTracks are the album's songs the job imports, in these
// states (GET /imports/groups/{id}/tracks?state=…; none: no songs).
func (c *Client) ImportGroupTracks(ctx context.Context, groupID int64, states ...string) ([]ImportTrack, error) {
	if len(states) == 0 {
		return []ImportTrack{}, nil
	}
	var ts []ImportTrack
	err := c.get(ctx, importGroupPath(groupID, "tracks")+query(url.Values{"state": states}), &ts)
	return nonNil(ts), err
}

// MoveImportGroup moves an album from one state to another (POST
// /imports/groups/{id}/move): a compare-and-set, ok false when the album
// was not in from (another claimer took it, the core put a search back
// after a restart, a cancel dropped it). from is waiting, searching,
// waiting_space, waiting_download or failed; to is waiting, searching or
// waiting_space.
func (c *Client) MoveImportGroup(ctx context.Context, id int64, from, to string) (bool, error) {
	return claimed(c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "move"), map[string]any{"from": from, "to": to}, nil))
}

// WaitImportGroup records the download key the album waits for (named
// name, done bytes so far): it is waiting_download (POST …/wait).
func (c *Client) WaitImportGroup(ctx context.Context, id int64, key, name string, done int64) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "wait"),
		map[string]any{"key": key, "name": name, "done": done}, nil)
}

// GiveUpImportWait ends a waiting_download album's wait: it waits for its
// own search, which leaves key out (POST …/give-up-wait).
func (c *Client) GiveUpImportWait(ctx context.Context, id int64, key string) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "give-up-wait"), map[string]any{"key": key}, nil)
}

// StartImportGroup records a started download key (bytes) of a searching
// album: its songs downloading are on the way, missing ones are not in
// the release (POST …/start). ok false (the core's 409): the album was no
// longer searching — another claimer, a cancel, or a core restart — and
// the caller removes the download it began.
func (c *Client) StartImportGroup(ctx context.Context, id int64, key string, bytes int64, downloading, missing []int64) (bool, error) {
	return claimed(c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "start"),
		map[string]any{"key": key, "bytes": bytes, "downloading": nonNilIDs(downloading), "missing": nonNilIDs(missing)}, nil))
}

// SaveImportClock stores the clock of the album's download key (POST
// …/clock; only while it downloads that one).
func (c *Client) SaveImportClock(ctx context.Context, id int64, key string, clock ImportRunClock) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "clock"), map[string]any{"key": key, "clock": clock}, nil)
}

// ImportGivenUp lists the album's download key as given up, dead or slow,
// before it is removed (POST …/given-up).
func (c *Client) ImportGivenUp(ctx context.Context, id int64, key string, slow bool) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "given-up"), map[string]any{"key": key, "slow": slow}, nil)
}

// RequeueImportGroup puts an album downloading key back in the queue; when
// key is its maxDead-th dead download it fails with msg instead (POST
// …/requeue).
func (c *Client) RequeueImportGroup(ctx context.Context, id int64, key, msg string, maxDead int) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "requeue"),
		map[string]any{"key": key, "msg": msg, "maxDead": maxDead}, nil)
}

// ImportNoFormat settles an album whose releases are all of another
// format: its songs trackIDs are not found (POST …/no-format).
func (c *Client) ImportNoFormat(ctx context.Context, id int64, trackIDs []int64, msg string) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "no-format"),
		map[string]any{"trackIds": nonNilIDs(trackIDs), "msg": msg}, nil)
}

// FailImportGroup ends an album with msg: its songs trackIDs (and any
// that joined it) fail. retry: retried automatically once; else only the
// admin's «Повторить» is left (POST …/fail).
func (c *Client) FailImportGroup(ctx context.Context, id int64, trackIDs []int64, msg string, retry bool) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "fail"),
		map[string]any{"trackIds": nonNilIDs(trackIDs), "msg": msg, "retry": retry}, nil)
}

// FinishImportGroup ends a download placed in the library: done maps each
// song to its library track, failed were not among the placed files;
// album is the library album (0: unknown) (POST …/finish).
func (c *Client) FinishImportGroup(ctx context.Context, id int64, done map[int64]int64, failed []int64, album int64) error {
	if done == nil {
		done = map[int64]int64{}
	}
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "finish"),
		map[string]any{"done": done, "failed": nonNilIDs(failed), "album": album}, nil)
}

// ImportHave links the album's songs the library has (song → library
// track) and remembers the library album (0: none), which becomes the
// importer's too; done: the album is complete (POST …/have).
func (c *Client) ImportHave(ctx context.Context, id int64, have map[int64]int64, album int64, done bool) error {
	if have == nil {
		have = map[int64]int64{}
	}
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "have"),
		map[string]any{"have": have, "album": album, "done": done}, nil)
}

// ImportNotFound ends an album no release holds: its songs trackIDs are
// not found (POST …/not-found).
func (c *Client) ImportNotFound(ctx context.Context, id int64, trackIDs []int64, msg string) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "not-found"),
		map[string]any{"trackIds": nonNilIDs(trackIDs), "msg": msg}, nil)
}

// ImportProgress tells the core how many bytes of a downloading album are
// on disk (POST …/progress); the core keeps it in memory until the album
// leaves downloading. Send it every 10 s or so while it downloads.
func (c *Client) ImportProgress(ctx context.Context, id int64, done int64) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, importGroupPath(id, "progress"), map[string]any{"done": done}, nil)
}

// RebuildImport rebuilds the job's playlists and stars from what it has
// brought so far (POST /imports/jobs/{id}/rebuild).
func (c *Client) RebuildImport(ctx context.Context, jobID int64) error {
	return c.do(ctx, DefaultTimeout, http.MethodPost, fmt.Sprintf("/imports/jobs/%d/rebuild", jobID), nil, nil)
}

// ImportOpen reports whether the profile has an import job of the
// service in preview, matching or downloading (GET /imports/open).
func (c *Client) ImportOpen(ctx context.Context, userID int64, service string) (bool, error) {
	var out struct {
		Open bool `json:"open"`
	}
	v := url.Values{"userId": {strconv.FormatInt(userID, 10)}, "service": {service}}
	err := c.get(ctx, "/imports/open"+query(v), &out)
	return out.Open, err
}

// CreateReadingJob makes an empty import job of the profile and service
// that the addon fills (FillImportJob) or fails (FailImportJob) once its
// read ends (POST /imports/jobs).
func (c *Client) CreateReadingJob(ctx context.Context, userID int64, service string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/imports/jobs",
		map[string]any{"userId": userID, "service": service, "reading": true}, &out)
	return out.ID, err
}

// FillImportJob stores what a reading job read: it moves to preview (PUT
// /imports/jobs/{id}/snapshot, at most 32 MiB). A job no longer reading
// is a 409 *APIError (IsConflict).
func (c *Client) FillImportJob(ctx context.Context, id int64, snap ImportSnapshot) error {
	return c.do(ctx, DefaultTimeout, http.MethodPut, fmt.Sprintf("/imports/jobs/%d/snapshot", id), map[string]any{"snapshot": snap}, nil)
}

// FailImportJob fails a reading job with errText (POST
// /imports/jobs/{id}/fail); ok false when it was no longer reading
// (cancelled or deleted meanwhile).
func (c *Client) FailImportJob(ctx context.Context, id int64, errText string) (bool, error) {
	return claimed(c.do(ctx, DefaultTimeout, http.MethodPost, fmt.Sprintf("/imports/jobs/%d/fail", id), map[string]any{"error": errText}, nil))
}

// FailReadingJobs fails every job still reading with errText and returns
// their ids (POST /imports/jobs/fail-reading). In contract 1 that is every
// reading job, the server's own reads too (an uploaded Takeout being
// named): an addon fails the jobs it lost one by one with FailImportJob
// and does not call this at its start (contract: Imports, reading jobs).
func (c *Client) FailReadingJobs(ctx context.Context, errText string) ([]int64, error) {
	var out struct {
		IDs []int64 `json:"ids"`
	}
	err := c.do(ctx, DefaultTimeout, http.MethodPost, "/imports/jobs/fail-reading", map[string]any{"error": errText}, &out)
	return nonNil(out.IDs), err
}

// DeleteImportJob removes an import job that is not matching or
// downloading (DELETE /imports/jobs/{id}; a running one is a 409).
func (c *Client) DeleteImportJob(ctx context.Context, id int64) error {
	return c.do(ctx, DefaultTimeout, http.MethodDelete, fmt.Sprintf("/imports/jobs/%d", id), nil, nil)
}

// ImportReads are the ids of the profile's jobs still reading (GET
// /imports/reads?userId=).
func (c *Client) ImportReads(ctx context.Context, userID int64) ([]int64, error) {
	var out struct {
		IDs []int64 `json:"ids"`
	}
	err := c.get(ctx, "/imports/reads"+query(url.Values{"userId": {strconv.FormatInt(userID, 10)}}), &out)
	return nonNil(out.IDs), err
}
