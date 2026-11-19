package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/go-chi/chi/v5"
)

// addonRuntime is what the addon routes need of *addons.Manager.
type addonRuntime interface {
	List(ctx context.Context) ([]addons.Installed, error)
	Get(ctx context.Context, id string) (addons.Installed, error)
	Status(id string) addons.Status
	// Serving: the status and, only while running, the socket, in one read.
	Serving(id string) (addons.Status, string)
	WebDir(id, version string) string
	LogTail(id string, n int) []string
	// IDForToken refuses the tokens of addons that are off or not running.
	IDForToken(ctx context.Context, token string) (string, error)
	Preview(ctx context.Context, url string) (addons.Manifest, error)
	// Install and Update take the version the admin previewed.
	Install(ctx context.Context, url, version string) (addons.Installed, error)
	Update(ctx context.Context, id, version string) (addons.Installed, error)
	SetEnabled(ctx context.Context, id string, on bool) error
	Remove(ctx context.Context, id string, deleteData bool) error
	UpdateAvailable(ctx context.Context, id string) (string, bool)
	// Pinned: installed by the deployment (VAULT_ADDONS_PIN); install,
	// update and remove of it answer 409.
	Pinned(id string) bool
	// PinFailed: pinned, and the pin could not be applied at boot (the
	// addon stays stopped; switching it on answers 409). FailedPins lists
	// those pins, installed or not.
	PinFailed(id string) bool
	FailedPins() []addons.Pin
}

const (
	msgAddonNotRunning = "Расширение не запущено"
	msgAddonStarting   = "Расширение запускается"
	msgAddonNoAnswer   = "Расширение не ответило"
	msgAddonNotFound   = "Расширение не найдено"
	msgAddonToken      = "Нужен токен расширения"
	msgAddonPermission = "У расширения нет такого разрешения"
	msgAddonKid        = "Недоступно в детском профиле"
	msgAddonForbidden  = "Недостаточно прав"
	msgAddonBadPath    = "Не удалось разобрать запрос"
	msgAddonNoVersion  = "Не указана версия расширения"
	msgAddonFailed     = "Не удалось выполнить действие с расширением"
	// addonProxyTimeout: how long the addon may take to start answering
	// (the body may then stream as long as it likes).
	addonProxyTimeout = 60 * time.Second
)

type addonsAPI struct {
	rt    addonRuntime
	bus   *events.Bus
	proxy *httputil.ReverseProxy
	// pools: the proxy's kept connections, one pool per addon process
	pools addonPools
	info  addonkit.Info // the server's part of /info (no id, version)
	// the core API's library and users (nil: those routes answer 503)
	lib    *library.Repo
	jobs   *library.Jobs
	users  *auth.Store
	refs   *addons.RefStore
	images *metadata.Images
	// prog: the profiles' watched marks (/watched; nil: 503)
	prog *progress.Store
	// db and settings: the profiles' interface languages (/users)
	db       *sql.DB
	settings AppSettings
	// importMu: one library import at a time (each scans the library)
	importMu sync.Mutex
	// meta: the metadata pass-through's sources (/metadata/…)
	meta AddonMetadata
	// holds: the addons' file holds (/files/holds; nil: 503)
	holds *addons.Holds
	// inputs: the addons' registered videos (/play/inputs; nil: 503)
	inputs *addons.Inputs
	// notify: the addons' letters (/notify; nil: 503)
	notify *notify.Service
	// requests: kids' requests (/requests/approve; nil: 503)
	requests *kidRequests
	// trackStore: the tracked titles (/tracking/…; nil: 503); trackKick
	// asks for a title's refresh (the service's Kick; tests record it)
	trackStore *tracking.Store
	trackKick  func(id int64)
	// fitKick asks the fit worker for a pass (/voiceover/fit; nil: no
	// worker, 503; tests record it)
	fitKick func()
	// fileDirs: the file browser's root folders as configured (/info
	// reports them resolved: fileRoots)
	fileDirs []string
	// imports: «Импорт медиатеки» (/imports/…; nil: 503)
	imports *playlistimport.Service
	// maxHeight is the user header's remote quality cap for a request
	// (remoteMaxHeight; nil: none)
	maxHeight func(*http.Request) int
}

func newAddonsAPI(deps Deps) *addonsAPI {
	a := &addonsAPI{rt: deps.Addons, bus: deps.Events, lib: deps.Library, jobs: deps.Jobs, users: deps.Users,
		refs: deps.AddonRefs, images: deps.Images, db: deps.DB, settings: deps.SettingsDefaults, meta: deps.Metadata, holds: deps.AddonHolds,
		inputs: deps.AddonInputs, notify: deps.Notify, imports: deps.PlaylistImport}
	a.maxHeight = func(r *http.Request) int { return remoteMaxHeight(&deps, r) }
	if deps.Tracking != nil && deps.Tracking.Store != nil {
		a.trackStore, a.trackKick = deps.Tracking.Store, deps.Tracking.Kick
	}
	if deps.VoiceOverFit != nil {
		a.fitKick = deps.VoiceOverFit.Kick
	}
	if a.prog = deps.Progress; a.prog == nil && deps.DB != nil {
		a.prog = progress.New(deps.DB) // the same marks: tests and servers without the player routes
	}
	a.info = addonkit.Info{Contract: addons.ContractVersion, FFmpeg: deps.FFmpeg, FFprobe: deps.FFprobe, PublicURL: deps.PublicURL}
	if filepath.IsAbs(deps.MediaRoot) {
		a.info.MediaRoot = filepath.Clean(deps.MediaRoot)
	}
	if deps.Files != nil && deps.Files.Sandbox != nil {
		for _, r := range deps.Files.Sandbox.Roots() {
			a.fileDirs = append(a.fileDirs, r.Dir)
		}
	}
	if deps.Jobs != nil && deps.Jobs.Scanner != nil {
		sc := deps.Jobs.Scanner
		a.info.LibraryRoots = addonkit.LibraryRoots{Movies: sc.Movies, Series: sc.Series, Music: sc.Music,
			Ebooks: sc.Ebooks, Audiobooks: sc.Audiobooks, Comics: sc.Comics}
		a.info.LibraryFolders = map[string][]string{}
		for _, k := range []string{library.KindFolderMovies, library.KindFolderSeries, library.KindFolderMusic,
			library.KindFolderEbooks, library.KindFolderAudiobooks, library.KindFolderComics} {
			if dirs := sc.Roots(k); len(dirs) > 0 {
				a.info.LibraryFolders[k] = dirs
			}
		}
	}
	a.proxy = &httputil.ReverseProxy{
		Rewrite:        a.rewrite,
		ModifyResponse: addonResponse,
		ErrorHandler:   a.proxyError,
		FlushInterval:  -1,
		Transport:      addonTransport{shared: newAddonSockTransport(true)},
	}
	if src, ok := deps.Addons.(addonStatusSource); ok {
		src.OnStatus(a.pools.statusChanged)
	}
	return a
}

// addonStatusSource is the runtime's status feed (the Manager has one;
// a runtime without it only gets its pools replaced by the proxy itself).
type addonStatusSource interface {
	OnStatus(func(id string, st addons.Status))
}

// newAddonSockTransport dials the socket the request's context names
// (addonSockKey). With keep-alives, it keeps up to addonIdlePerSocket idle
// connections: segment after segment of a stream then skips the dial.
func newAddonSockTransport(noKeepAlive bool) *http.Transport {
	return &http.Transport{
		DisableKeepAlives:     noKeepAlive,
		MaxIdleConnsPerHost:   addonIdlePerSocket,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: addonProxyTimeout,
		DisableCompression:    true, // what the addon sends passes as it is
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			sock, _ := ctx.Value(addonSockKey{}).(string)
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
		},
	}
}

// addonIdlePerSocket: idle connections kept to one addon process.
const addonIdlePerSocket = 16

// addonTransport sends a proxied request through the pool of the addon
// process serveProxy picked (addonPoolKey). Without one it uses shared,
// which keeps no connection.
type addonTransport struct{ shared *http.Transport }

type addonPoolKey struct{}

func (t addonTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if p, ok := r.Context().Value(addonPoolKey{}).(*http.Transport); ok {
		return p.RoundTrip(r)
	}
	return t.shared.RoundTrip(r)
}

// addonPools: one keep-alive Transport per addon id and process. A
// process is told apart by its socket (a new one at every start: <id>.<n>
// .sock), so no connection is ever reused across addons or across an
// addon's restart. A pool is closed (CloseIdleConnections) and dropped
// when its addon stops or restarts: by the runtime's status feed, and by
// serveProxy when it sees another socket or none.
type addonPools struct {
	mu sync.Mutex
	m  map[string]addonPool
}

type addonPool struct {
	sock string
	tr   *http.Transport
}

// get is id's pool for sock, replacing (and closing) a pool for an older
// process of id.
func (p *addonPools) get(id, sock string) *http.Transport {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.m[id]; ok {
		if cur.sock == sock {
			return cur.tr
		}
		cur.tr.CloseIdleConnections()
	}
	if p.m == nil {
		p.m = map[string]addonPool{}
	}
	tr := newAddonSockTransport(false)
	p.m[id] = addonPool{sock: sock, tr: tr}
	return tr
}

// drop closes and forgets id's pool.
func (p *addonPools) drop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.m[id]; ok {
		cur.tr.CloseIdleConnections()
		delete(p.m, id)
	}
}

// statusChanged: any status but running ends the addon's process (or is
// a restart in progress), so its pool goes. Called under the
// supervisor's lock: it only takes p.mu.
func (p *addonPools) statusChanged(id string, st addons.Status) {
	if st != addons.StatusRunning {
		p.drop(id)
	}
}

// mountAddons: the session routes (after guard.Require). The routes that
// manage addons are for admins. The web list, the bundles and the proxy
// are for admins and, for an addon whose manifest says access "adults",
// other adult profiles too, except the proxy's x/admin/*; kids get none.
func mountAddons(r chi.Router, a *addonsAPI) {
	r.Group(func(r chi.Router) {
		r.Use(addonsNoKids)
		r.Get("/api/addons/web", a.web) // lists what the profile may load
		r.With(a.addonAccess(true)).Handle("/api/addons/{id}/x/*", http.HandlerFunc(a.serveProxy))
		r.With(a.addonAccess(false)).Get("/addons/{id}/ui/*", a.static)
	})
	r.Group(func(r chi.Router) {
		r.Use(addonsAdminOnly)
		r.Get("/api/addons", a.list)
		r.Post("/api/addons/preview", a.preview)
		r.Post("/api/addons", a.install)
		r.Post("/api/addons/{id}/enable", a.setEnabled(true))
		r.Post("/api/addons/{id}/disable", a.setEnabled(false))
		r.Post("/api/addons/{id}/update", a.update)
		r.Delete("/api/addons/{id}", a.remove)
		r.Get("/api/addons/{id}/log", a.log)
	})
}

// addonsAdminOnly is auth.RequireAdmin that tells kids why.
func addonsAdminOnly(next http.Handler) http.Handler {
	admin := auth.RequireAdmin(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userOf(r).IsKid {
			writeErr(w, r, http.StatusForbidden, msgAddonKid)
			return
		}
		admin.ServeHTTP(w, r)
	})
}

// addonsNoKids: kids get no addon, with the reason.
func addonsNoKids(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userOf(r).IsKid {
			writeErr(w, r, http.StatusForbidden, msgAddonKid)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// addonAccess admits admins to every addon and other (adult) profiles to
// an addon with access "adults". With proxy set, a path the addon could
// read otherwise than the core is refused for everyone (addonProxyPath),
// and x/admin/* stays for admins whatever the addon says.
func (a *addonsAPI) addonAccess(proxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var rest string
			if proxy {
				var ok bool
				if rest, ok = addonProxyPath(r); !ok {
					writeErr(w, r, http.StatusBadRequest, msgAddonBadPath)
					return
				}
			}
			if userOf(r).IsAdmin {
				next.ServeHTTP(w, r)
				return
			}
			if proxy && addonAdminPath(rest) {
				writeErr(w, r, http.StatusForbidden, msgAddonForbidden)
				return
			}
			in, err := a.rt.Get(r.Context(), chi.URLParam(r, "id"))
			if err != nil || !in.Manifest.ForAdults() {
				writeErr(w, r, http.StatusForbidden, msgAddonForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// addonProxyPath is the proxied path after x/, decoded once: exactly what
// rewrite forwards. ok is false for a path that is not canonical (empty or
// dot segments, other than one trailing slash), still holds '%', '\' or
// ';', holds a control byte (below 0x20, or 0x7f) or is not valid UTF-8:
// the addon might resolve such a path otherwise than the checks here do.
func addonProxyPath(r *http.Request) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(r.URL.Path, "/api/addons/"+chi.URLParam(r, "id")+"/x/")
	if !ok || strings.ContainsAny(rest, `%\;`) || !utf8.ValidString(rest) {
		return "", false
	}
	for i := 0; i < len(rest); i++ {
		if c := rest[i]; c < 0x20 || c == 0x7f {
			return "", false
		}
	}
	p := "/" + rest
	if c := path.Clean(p); c != p && (c == "/" || c+"/" != p) {
		return "", false
	}
	return rest, true
}

// addonAdminPath: whether the canonical proxied path (addonProxyPath) is
// admin or under admin/, in any case. The first segment is compared with
// spaces and dots trimmed from both ends ("admin ", "admin.", " admin"):
// some frameworks trim them before routing.
func addonAdminPath(rest string) bool {
	first, _, _ := strings.Cut(rest, "/")
	first = strings.TrimFunc(first, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	return strings.EqualFold(first, "admin")
}

// mountAddonCore: the API addons call with their token (no session).
func mountAddonCore(r chi.Router, a *addonsAPI) {
	r.Route("/api/addon-core/v1", func(r chi.Router) {
		r.Use(a.requireToken)
		r.Get("/info", a.coreInfo)
		r.Post("/events", a.coreEvent)
		mountAddonCoreLibrary(r, a)
		mountAddonCoreTracking(r, a)
		mountAddonCoreWrite(r, a)
		mountAddonCoreMetadata(r, a)
		mountAddonCoreHolds(r, a)
		mountAddonCorePlay(r, a)
		mountAddonCoreNotify(r, a)
		mountAddonCoreRequests(r, a)
		mountAddonCoreVoiceover(r, a)
		mountAddonCoreImports(r, a)
		mountAddonCoreProgress(r, a)
		r.NotFound(http.NotFoundHandler().ServeHTTP)
	})
}

// addonProxyRoute: /api/addons/{id}/x/… (streamed, never compressed here).
func addonProxyRoute(p string) bool {
	rest, ok := strings.CutPrefix(p, "/api/addons/")
	if !ok {
		return false
	}
	_, rest, ok = strings.Cut(rest, "/")
	return ok && strings.HasPrefix(rest, "x/")
}

// addonErr answers with the user message err carries; any other error is
// logged and answered with a generic one.
func addonErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, addons.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, msgAddonNotFound)
		return
	}
	for _, e := range []error{addons.ErrPinned, addons.ErrPinNotApplied} {
		if errors.Is(err, e) {
			writeErr(w, r, http.StatusConflict, e.Error())
			return
		}
	}
	if msg, ok := addons.UserMessage(err); ok {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	log.Printf("api: addons %s %s: %v", r.Method, r.URL.Path, err)
	writeErr(w, r, http.StatusInternalServerError, msgAddonFailed)
}

func localText(lang string, t addons.Text) string {
	if lang == i18n.EN && t.EN != "" {
		return t.EN
	}
	if t.RU != "" {
		return t.RU
	}
	return t.EN
}

type addonItem struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Version     string        `json:"version"`
	Source      string        `json:"source"`
	Enabled     bool          `json:"enabled"`
	Status      addons.Status `json:"status"`
	Update      *string       `json:"update"` // a newer release's version, or null
	// Pinned: installed by the deployment; the admin cannot update or remove it
	Pinned bool `json:"pinned"`
	// PinFailed: its pin could not be applied at boot (see the server log);
	// it stays stopped and cannot be switched on
	PinFailed bool `json:"pinFailed"`
	// Helpers: the helper services its manifest declares ([] when none)
	Helpers []addonHelper `json:"helpers"`
}

// addonHelper is a declared helper service as the app shows it: where the
// addon looks for it and the block that adds it to compose.yml.
type addonHelper struct {
	addons.Helper
	Address string `json:"address"`
	Compose string `json:"compose"`
}

func helperViews(hs []addons.Helper) []addonHelper {
	out := make([]addonHelper, 0, len(hs))
	for _, h := range hs {
		out = append(out, addonHelper{Helper: h, Address: h.Address(), Compose: h.Compose()})
	}
	return out
}

func (a *addonsAPI) item(ctx context.Context, in addons.Installed, withUpdate bool) addonItem {
	lang := i18n.FromContext(ctx)
	it := addonItem{ID: in.ID, Name: localText(lang, in.Manifest.Name), Description: localText(lang, in.Manifest.Description),
		Version: in.Version, Source: in.Source, Enabled: in.Enabled, Status: a.rt.Status(in.ID), Pinned: a.rt.Pinned(in.ID), PinFailed: a.rt.PinFailed(in.ID),
		Helpers: helperViews(in.Manifest.Helpers)}
	if withUpdate && !it.Pinned {
		if v, ok := a.rt.UpdateAvailable(ctx, in.ID); ok {
			it.Update = &v
		}
	}
	return it
}

func (a *addonsAPI) list(w http.ResponseWriter, r *http.Request) {
	list, err := a.rt.List(r.Context())
	if err != nil {
		log.Printf("api: addons: %v", err)
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	// GitHub may be slow or down: the list does not wait for it long
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := make([]addonItem, 0, len(list))
	have := map[string]bool{}
	for _, in := range list {
		out = append(out, a.item(ctx, in, true))
		have[in.ID] = true
	}
	// a pin that failed on a first install has no row: listed all the same
	for _, p := range a.rt.FailedPins() {
		if !have[p.ID] {
			out = append(out, addonItem{ID: p.ID, Name: p.ID, Version: p.Version, Source: addons.LocalPrefix + p.ID,
				Status: addons.StatusStopped, Pinned: true, PinFailed: true, Helpers: []addonHelper{}})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type addonURL struct {
	URL string `json:"url"`
}

func (a *addonsAPI) preview(w http.ResponseWriter, r *http.Request) {
	var body addonURL
	if !decode(w, r, &body) {
		return
	}
	m, err := a.rt.Preview(r.Context(), body.URL)
	if err != nil {
		addonErr(w, r, err)
		return
	}
	lang := i18n.FromContext(r.Context())
	var installed string
	if in, err := a.rt.Get(r.Context(), m.ID); err == nil {
		installed = in.Version
	}
	perms := m.Permissions
	if perms == nil {
		perms = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": m.ID, "name": localText(lang, m.Name), "description": localText(lang, m.Description),
		"version": m.Version, "author": m.Author, "homepage": m.Homepage, "permissions": perms,
		"hasWeb": m.Web != nil, "installed": installed, "helpers": helperViews(m.Helpers),
	})
}

func (a *addonsAPI) install(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL     string `json:"url"`
		Version string `json:"version"` // the previewed release
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Version == "" {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoVersion)
		return
	}
	in, err := a.rt.Install(r.Context(), body.URL, body.Version)
	if err != nil {
		addonErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.item(r.Context(), in, false))
}

func (a *addonsAPI) update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"` // the previewed release
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Version == "" {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoVersion)
		return
	}
	in, err := a.rt.Update(r.Context(), chi.URLParam(r, "id"), body.Version)
	if err != nil {
		addonErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.item(r.Context(), in, false))
}

func (a *addonsAPI) setEnabled(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := a.rt.SetEnabled(r.Context(), id, on); err != nil {
			addonErr(w, r, err)
			return
		}
		in, err := a.rt.Get(r.Context(), id)
		if err != nil {
			addonErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, a.item(r.Context(), in, false))
	}
}

func (a *addonsAPI) remove(w http.ResponseWriter, r *http.Request) {
	if err := a.rt.Remove(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("data") == "1"); err != nil {
		addonErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *addonsAPI) log(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("n"))
	if err != nil || n <= 0 {
		n = 200
	}
	lines := a.rt.LogTail(chi.URLParam(r, "id"), n)
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

type addonWeb struct {
	ID    string `json:"id"`
	Entry string `json:"entry"`
	// Style is the bundle's stylesheet (index.css, from its CSS modules),
	// versioned like Entry; absent when the bundle has none.
	Style string `json:"style,omitempty"`
}

// web lists the bundles the app loads: enabled, running addons with a web
// part; for a profile that is not an admin, only those with access
// "adults".
func (a *addonsAPI) web(w http.ResponseWriter, r *http.Request) {
	out := []addonWeb{}
	list, err := a.rt.List(r.Context())
	if err != nil {
		log.Printf("api: addons: %v", err)
	}
	admin := userOf(r).IsAdmin
	for _, in := range list {
		if !in.Manifest.OpenTo(admin) {
			continue
		}
		if in.Enabled && in.Manifest.Web != nil && a.rt.Status(in.ID) == addons.StatusRunning {
			// ?v=: a new version is a new module URL, so an open tab
			// importing it again after an update runs the new code
			v := "?v=" + url.QueryEscape(in.Version)
			web := addonWeb{ID: in.ID, Entry: "/addons/" + in.ID + "/ui/index.js" + v}
			if st, err := os.Stat(filepath.Join(a.rt.WebDir(in.ID, in.Version), "index.css")); err == nil && st.Mode().IsRegular() {
				web.Style = "/addons/" + in.ID + "/ui/index.css" + v
			}
			out = append(out, web)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// static serves an enabled addon's web bundle. No directory listings;
// os.Root keeps every path inside the bundle.
func (a *addonsAPI) static(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	in, err := a.rt.Get(r.Context(), id)
	if err != nil || !in.Enabled || in.Manifest.Web == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/addons/"+id+"/ui/")
	if !fs.ValidPath(name) || name == "." {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(a.rt.WebDir(id, in.Version))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

type addonSockKey struct{}

func (a *addonsAPI) serveProxy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st, sock := a.rt.Serving(id)
	if sock == "" {
		a.pools.drop(id) // not running: its process's pool is done
	}
	if st == addons.StatusStarting {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonStarting)
		return
	}
	if sock == "" {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNotRunning)
		return
	}
	ctx := context.WithValue(r.Context(), addonSockKey{}, sock)
	ctx = context.WithValue(ctx, addonPoolKey{}, a.pools.get(id, sock))
	a.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// addonUser is the X-Antresol-User header: who is asking. MaxRating is
// the profile's rating limit as the core's own gate applies it
// (auth.User.RatingLimit, compared by library.RatingAllowed); left out when
// the profile has none. MaxHeight is the remote quality cap
// (remoteMaxHeight); left out at home and when it is «Оригинал».
type addonUser struct {
	ID        int64  `json:"id"`
	UID       string `json:"uid"`
	Name      string `json:"name"`
	IsAdmin   bool   `json:"isAdmin"`
	IsKid     bool   `json:"isKid"`
	Lang      string `json:"lang"`
	MaxRating string `json:"maxRating,omitempty"`
	MaxHeight int    `json:"maxHeight,omitempty"`
	// History: the separate history the request writes (addonHistory)
	History *addonkit.History `json:"history,omitempty"`
}

// rewrite: /api/addons/{id}/x/<rest> → /api/<rest> on the addon's socket.
// The browser's credentials and any X-Antresol-* it sent stay here; the
// addon learns the user from X-Antresol-User alone.
func (a *addonsAPI) rewrite(pr *httputil.ProxyRequest) {
	in := pr.In
	prefix := "/api/addons/" + chi.URLParam(in, "id") + "/x/"
	out := &url.URL{Scheme: "http", Host: "addon", Path: "/api/" + strings.TrimPrefix(in.URL.Path, prefix), RawQuery: in.URL.RawQuery}
	if in.URL.RawPath != "" {
		out.RawPath = "/api/" + strings.TrimPrefix(in.URL.EscapedPath(), prefix)
	}
	pr.Out.URL = out
	pr.Out.Host = "addon"
	h := pr.Out.Header
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Antresol-") {
			delete(h, k)
		}
	}
	h.Del("Cookie")
	h.Del("Authorization")
	u := userOf(in)
	b, _ := json.Marshal(addonUser{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid, Lang: i18n.FromContext(in.Context()),
		MaxRating: ratingLimit(u), MaxHeight: a.maxHeight(in), History: addonHistory(in.Context())})
	h.Set("X-Antresol-User", addons.ASCIIJSON(b))
}

// addonHistory is the separate history of the request for an addon
// (addonkit.User.History): the managed account auth.HistoryOf names, nil
// for the profile's own. The addon records what this request watches
// there, as the core does (auth.HistoryID), and the profile's limits stay
// the header's.
func addonHistory(ctx context.Context) *addonkit.History {
	h := auth.HistoryOf(ctx)
	if h.ID == 0 {
		return nil
	}
	return &addonkit.History{ID: h.ID, UID: h.UID, Name: h.Name}
}

// addonResponse: an addon sets no cookies on the server's origin, cannot
// wipe its site data, open it to other origins or widen a service
// worker's scope, and what it sends (other than JSON and event streams)
// never runs as a page of it.
func addonResponse(resp *http.Response) error {
	h := resp.Header
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Access-Control-") {
			delete(h, k)
		}
	}
	h.Del("Set-Cookie")
	h.Del("Clear-Site-Data")
	h.Del("Service-Worker-Allowed")
	h.Set("X-Content-Type-Options", "nosniff")
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	if mt != "application/json" && !strings.HasSuffix(mt, "+json") && mt != "text/event-stream" {
		h.Set("Content-Security-Policy", "sandbox")
	}
	return nil
}

// proxyError: an addon that is gone (never reached, or disabled, removed
// or crashed while the request was in flight) is "not running"; one that
// is still up and failed is "did not answer".
func (a *addonsAPI) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return // the client went away
	}
	var op *net.OpError
	if _, sock := a.rt.Serving(chi.URLParam(r, "id")); (errors.As(err, &op) && op.Op == "dial") || sock == "" {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNotRunning)
		return
	}
	log.Printf("api: addon proxy %s: %v", r.URL.Path, err)
	writeErr(w, r, http.StatusBadGateway, msgAddonNoAnswer)
}

type addonIDKey struct{}

// requireToken admits an addon by its Bearer token. Session cookies are
// dropped: these routes act as the addon, never as a user.
func (a *addonsAPI) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		h := r.Header.Get("Authorization")
		var tok string
		if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			tok = strings.TrimSpace(h[7:])
		}
		if tok == "" {
			writeErr(w, r, http.StatusUnauthorized, msgAddonToken)
			return
		}
		id, err := a.rt.IDForToken(r.Context(), tok)
		if errors.Is(err, addons.ErrNotFound) {
			writeErr(w, r, http.StatusUnauthorized, msgAddonToken)
			return
		}
		if err != nil {
			log.Printf("api: addon token: %v", err)
			writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), addonIDKey{}, id)))
	})
}

// callingAddon is the addon of a core API request; with perm set, it must
// have that permission (else 403 and ok false).
func (a *addonsAPI) callingAddon(w http.ResponseWriter, r *http.Request, perm string) (addons.Installed, bool) {
	id, _ := r.Context().Value(addonIDKey{}).(string)
	in, err := a.rt.Get(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusUnauthorized, msgAddonToken)
		return addons.Installed{}, false
	}
	if perm != "" && !slices.Contains(in.Manifest.Permissions, perm) {
		writeErr(w, r, http.StatusForbidden, msgAddonPermission)
		return addons.Installed{}, false
	}
	return in, true
}

func (a *addonsAPI) coreInfo(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "")
	if !ok {
		return
	}
	info := a.info
	info.ID, info.Version = in.ID, in.Version
	info.FileRoots = a.fileRoots()
	info.MissingRoots = a.missingRoots()
	writeJSON(w, http.StatusOK, info)
}

// fileRoots are the file browser's roots, admin-only ones included, with
// their symlinks resolved as the holds check resolves them (EvalSymlinks,
// like files.realRoots), each once, in the browser's order. A root not on
// disk (yet) is left out; it is resolved again on every call.
func (a *addonsAPI) fileRoots() []string {
	out := []string{}
	for _, d := range a.fileDirs {
		real, err := filepath.EvalSymlinks(d)
		if err != nil || slices.Contains(out, real) {
			continue
		}
		out = append(out, real)
	}
	return out
}

// missingRoots are the file browser's roots not on disk (their symlinks
// do not resolve), as configured, each once, in the browser's order: holds
// under them are kept and accepted, and protect the path once it is there.
func (a *addonsAPI) missingRoots() []string {
	out := []string{}
	for _, d := range a.fileDirs {
		if _, err := filepath.EvalSymlinks(d); err == nil || slices.Contains(out, d) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// addonEventBody caps a POST /events body, above the general 64 KiB: an
// addon's download list sent every 2 s is about 0.9-1.4 KB a row, so
// 64 KiB holds only 45-72 rows; 1 MiB holds 700 and more.
const addonEventBody = 1 << 20

// msgAddonEventTooBig answers a POST /events body over addonEventBody.
const msgAddonEventTooBig = "Событие больше 1 МБ"

// coreEvent publishes addon.<id>.<name> to the app's event stream; a name
// starting with "admin." reaches admins only (eventVisible).
func (a *addonsAPI) coreEvent(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "events")
	if !ok {
		return
	}
	var body struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, addonEventBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, r, http.StatusRequestEntityTooLarge, msgAddonEventTooBig)
			return
		}
		writeErr(w, r, http.StatusBadRequest, "Не удалось разобрать запрос")
		return
	}
	typ, err := addons.EventType(in.ID, body.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if a.bus == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "События недоступны")
		return
	}
	a.bus.Publish(events.Event{Type: typ, Data: body.Data})
	w.WriteHeader(http.StatusNoContent)
}
