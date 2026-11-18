package addons

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/library"
)

// ChannelExtras are «ТВ» channels' titles outside the library from an
// addon (channels.Extras over the channels.* hooks). The first addon in
// id order that is on, running, declares the four hooks and that the
// channel's audience may use (Manifest.OpenTo) answers; its titles are
// namespaced "addon:<id>:<key>", so a title is resolved by the addon that
// gave it. The core does not trust an addon's rating: each candidate is
// rated with the library's own item when the library has the title, else
// with the addon's, and one above the channel's MaxRating, or unrated
// while a limit is set, is dropped. A stream is read by ffmpeg through
// the Relay, never from the addon's socket directly.
type ChannelExtras struct {
	H     *Hooks
	Relay *Relay
	// Users gives the profile of a personal channel (nil: a personal
	// channel gets no titles outside the library).
	Users func(ctx context.Context, id int64) (auth.User, error)
	// Library rates the candidates the library has (nil: the addon's
	// ratings alone).
	Library ChannelLibrary
	Log     *slog.Logger

	now func() time.Time // test clock (nil: time.Now)

	mu    sync.Mutex
	ready map[string]readyAnswer // addon id → its last channels.ready
}

// ChannelLibrary is what ChannelExtras reads of the library
// (*library.Repo).
type ChannelLibrary interface {
	ItemIDsByTMDB(ctx context.Context, kind string, tmdbIDs []int64) (map[int64]int64, error)
	ItemsByID(ctx context.Context, ids []int64) (map[int64]library.Item, error)
}

type readyAnswer struct {
	ready bool
	until time.Time
}

// channelsReadyFor: a channels.ready answer (or failure) is kept this
// long; any status change of the addon forgets it.
const channelsReadyFor = 60 * time.Second

const (
	maxChannelKey     = 200
	maxChannelHeaders = 16
)

var _ channels.Extras = (*ChannelExtras)(nil)

// channelKeyPrefix starts the key of every title outside the library an
// addon gave ("addon:<id>:<key>").
const channelKeyPrefix = "addon:"

// IsChannelExtraKey: key (channels.ExtraTitle.Key, channels.Info.ExtraKey)
// is a title an addon gave. Kids never get one, on any channel.
func IsChannelExtraKey(key string) bool { return strings.HasPrefix(key, channelKeyPrefix) }

// NewChannelExtras is the adapter over h, its streams read through
// relay; it listens to s (nil: none) to forget an addon's ready answer
// and streams when its status changes.
func NewChannelExtras(h *Hooks, s *Supervisor, relay *Relay) *ChannelExtras {
	x := &ChannelExtras{H: h, Relay: relay}
	if s != nil {
		s.OnStatus(x.StatusChanged)
	}
	return x
}

// StatusChanged forgets addon id's ready answer and, unless it runs, its
// relay streams. A Supervisor.OnStatus listener: it takes only its own
// locks.
func (x *ChannelExtras) StatusChanged(id string, st Status) {
	x.mu.Lock()
	delete(x.ready, id)
	x.mu.Unlock()
	if st != StatusRunning && x.Relay != nil {
		x.Relay.Forget(id)
	}
}

func (x *ChannelExtras) clock() time.Time {
	if x.now != nil {
		return x.now()
	}
	return time.Now()
}

func (x *ChannelExtras) log() *slog.Logger {
	if x.Log != nil {
		return x.Log
	}
	return slog.Default()
}

// errNoChannelAddon: no addon answers for this channel now.
var errNoChannelAddon = errors.New("addons: no addon answers channels.* now")

// provider is the addon that answers for an audience (admin: an admin's
// personal channel or screen): the first in id order that is on,
// running, declares channels.* and is open to it. declared tells whether
// one would be there but is not running.
func (x *ChannelExtras) provider(ctx context.Context, admin bool) (in Installed, declared, ok bool) {
	list, err := x.H.T.List(ctx)
	if err != nil {
		x.log().Warn("addon channels: list", "err", err)
		return Installed{}, true, false
	}
	slices.SortFunc(list, func(a, b Installed) int { return strings.Compare(a.ID, b.ID) })
	for _, a := range list {
		if !a.Enabled || !a.Manifest.AnswersChannels() || !a.Manifest.OpenTo(admin) {
			continue
		}
		declared = true
		if st, _ := x.H.T.Serving(a.ID); st == StatusRunning {
			return a, true, true
		}
	}
	return Installed{}, declared, false
}

// audience is whose a call is: the screen's profile (Ready) or the
// channel's (Candidates, Resolve: channels.WithChannel). A shared channel,
// or one nobody names, is every profile's: only an addon open to adults
// answers. ok false: no titles outside the library (a kid, a profile that
// cannot be read).
func (x *ChannelExtras) audience(ctx context.Context) (admin, ok bool) {
	c, has := channels.ChannelFrom(ctx)
	if !has || c.UserID == 0 {
		return false, true
	}
	if x.Users == nil {
		return false, false
	}
	u, err := x.Users(ctx, c.UserID)
	if err != nil {
		if !errors.Is(err, auth.ErrNotFound) {
			x.log().Warn("addon channels: channel profile", "channel", c.ID, "err", err)
		}
		return false, false
	}
	return u.IsAdmin, !u.IsKid
}

// readyOf is in's channels.ready answer, kept channelsReadyFor; a failed
// call counts as not ready and is kept as long.
func (x *ChannelExtras) readyOf(ctx context.Context, in Installed) bool {
	now := x.clock()
	x.mu.Lock()
	a, ok := x.ready[in.ID]
	x.mu.Unlock()
	if ok && now.Before(a.until) {
		return a.ready
	}
	var ans addonkit.ChannelReady
	if err := x.H.Call(ctx, in.ID, "channels.ready", nil, nil, &ans); err != nil {
		if ctx.Err() != nil {
			return false // the caller went away: nothing learnt
		}
		if !errors.Is(err, errCooling) {
			x.log().Warn("addon channels: ready", "addon", in.ID, "err", err)
		}
		ans.Ready = false
	}
	x.mu.Lock()
	if x.ready == nil {
		x.ready = map[string]readyAnswer{}
	}
	x.ready[in.ID] = readyAnswer{ready: ans.Ready, until: now.Add(channelsReadyFor)}
	x.mu.Unlock()
	return ans.Ready
}

// Ready: an addon answers channels.* for the screen's profile (never a
// kid) and says it is ready.
func (x *ChannelExtras) Ready(ctx context.Context) bool {
	admin := false
	if u, ok := auth.UserFrom(ctx); ok {
		if u.IsKid {
			return false
		}
		admin = u.IsAdmin
	}
	in, _, ok := x.provider(ctx, admin)
	return ok && x.readyOf(ctx, in)
}

// Candidates are up to limit titles of the answering addon for rules
// with extras on (channels.ExtrasOn), rated by the core.
func (x *ChannelExtras) Candidates(ctx context.Context, rules channels.Rules, limit int) ([]channels.ExtraTitle, error) {
	if limit <= 0 || !channels.ExtrasOn(rules) {
		return nil, nil
	}
	admin, ok := x.audience(ctx)
	if !ok {
		return nil, nil
	}
	in, declared, ok := x.provider(ctx, admin)
	if !ok {
		if declared {
			return nil, errNoChannelAddon // asked again in an hour
		}
		return nil, nil
	}
	if !x.readyOf(ctx, in) {
		return nil, errNoChannelAddon
	}
	body := addonkit.ChannelCandidates{Limit: limit, Rules: addonkit.ChannelRules{Genres: rules.Genres, YearFrom: rules.YearFrom,
		YearTo: rules.YearTo, Countries: rules.Countries, Languages: rules.Languages, Keywords: rules.Keywords, MaxRating: rules.MaxRating}}
	var ans addonkit.ChannelCandidatesAnswer
	if err := x.H.Call(ctx, in.ID, "channels.candidates", nil, body, &ans); err != nil {
		return nil, err
	}
	var ts []addonkit.ChannelTitle
	for _, t := range ans.Titles {
		if len(ts) >= limit {
			break
		}
		if t.Kind == "" {
			t.Kind = library.KindMovie
		}
		if !validChannelKey(t.Key) || strings.TrimSpace(t.Title) == "" || t.RuntimeMs <= 0 ||
			(t.Kind != library.KindMovie && t.Kind != library.KindEpisode) {
			continue
		}
		ts = append(ts, t)
	}
	if len(ts) == 0 {
		return nil, nil
	}
	rated, err := x.libraryRatings(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("addons: channel candidates' ratings: %w", err) // the gate cannot be applied
	}
	var out []channels.ExtraTitle
	for _, t := range ts {
		item := library.Item{Kind: t.Kind, Rating: t.Rating}
		if it, ok := rated[ratedKind(t.Kind)][t.TMDBID]; ok && t.TMDBID > 0 {
			item.Rating = it.Rating
		}
		if !library.RatingAllowed(rules.MaxRating, library.Item{Kind: library.KindMovie, Rating: item.Rating}) {
			continue
		}
		out = append(out, channels.ExtraTitle{Key: channelKeyPrefix + in.ID + ":" + t.Key, Kind: t.Kind, TMDBID: int(t.TMDBID),
			Title: t.Title, Year: t.Year, Overview: t.Overview, Rating: item.Rating, Genres: t.Genres, Poster: t.Poster,
			Backdrop: t.Backdrop, RuntimeMs: t.RuntimeMs, Season: max(t.Season, 0), Episode: max(t.Episode, 0)})
	}
	return out, nil
}

// ratedKind is the library kind whose item rates a title of kind: an
// episode's is its series'.
func ratedKind(kind string) string {
	if kind == library.KindEpisode {
		return library.KindSeries
	}
	return library.KindMovie
}

// libraryRatings are the library's items of ts' titles, per rated kind
// and TMDB id (empty without a Library).
func (x *ChannelExtras) libraryRatings(ctx context.Context, ts []addonkit.ChannelTitle) (map[string]map[int64]library.Item, error) {
	out := map[string]map[int64]library.Item{}
	if x.Library == nil {
		return out, nil
	}
	byKind := map[string][]int64{}
	for _, t := range ts {
		if t.TMDBID > 0 {
			k := ratedKind(t.Kind)
			byKind[k] = append(byKind[k], t.TMDBID)
		}
	}
	idOf := map[string]map[int64]int64{}
	var ids []int64
	for kind, tmdb := range byKind {
		m, err := x.Library.ItemIDsByTMDB(ctx, kind, tmdb)
		if err != nil {
			return nil, err
		}
		idOf[kind] = m
		for _, id := range m {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := x.Library.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for kind, m := range idOf {
		out[kind] = map[int64]library.Item{}
		for tmdb, id := range m {
			if it, ok := items[id]; ok {
				out[kind][tmdb] = it
			}
		}
	}
	return out, nil
}

func validChannelKey(k string) bool {
	return k != "" && len(k) <= maxChannelKey && !strings.ContainsFunc(k, unicode.IsControl)
}

// headerName: an HTTP header name (a token).
var headerName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,64}$")

// reservedHeaders are the request headers an addon's stream may not set:
// the relay's own and HTTP's.
var reservedHeaders = map[string]bool{"Host": true, "Range": true, "If-Range": true, "Content-Length": true, "Connection": true,
	"Transfer-Encoding": true, "Upgrade": true, "Te": true, "Trailer": true, "Keep-Alive": true, "Proxy-Connection": true}

func validStreamHeaders(h map[string]string) bool {
	if len(h) > maxChannelHeaders {
		return false
	}
	for k, v := range h {
		if !headerName.MatchString(k) || reservedHeaders[http.CanonicalHeaderKey(k)] || strings.HasPrefix(strings.ToLower(k), "x-antresol-") ||
			len(v) > 1024 || strings.ContainsAny(v, "\r\n\x00") {
			return false
		}
	}
	return true
}

// Resolve asks the addon that gave t (its key's namespace) for a stream
// for channel ch and opens it on the relay. Its Release forgets the
// relay's stream and fires channels.release.
func (x *ChannelExtras) Resolve(ctx context.Context, ch int64, t channels.ExtraTitle) (channels.Stream, error) {
	rest, ok := strings.CutPrefix(t.Key, channelKeyPrefix)
	id, key, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 || id == "" || key == "" {
		return channels.Stream{}, fmt.Errorf("addons: %q is no addon's title", t.Key)
	}
	admin, ok := x.audience(ctx)
	if u, has := auth.UserFrom(ctx); has && u.IsKid {
		ok = false // a kid's resolve, should one ever come: never
	}
	if !ok {
		return channels.Stream{}, errors.New("addons: no titles outside the library for this channel")
	}
	in, err := x.H.T.Get(ctx, id)
	if err != nil {
		return channels.Stream{}, err
	}
	if !in.Enabled || !in.Manifest.AnswersChannels() || !in.Manifest.OpenTo(admin) {
		return channels.Stream{}, fmt.Errorf("addons: addon %s does not answer for this channel", id)
	}
	if x.Relay == nil {
		return channels.Stream{}, errors.New("addons: no relay")
	}
	body := addonkit.ChannelResolve{ChannelID: ch, Title: addonkit.ChannelTitle{Key: key, Kind: t.Kind, TMDBID: int64(t.TMDBID),
		Title: t.Title, Year: t.Year, Overview: t.Overview, Rating: t.Rating, Genres: t.Genres, Poster: t.Poster,
		Backdrop: t.Backdrop, RuntimeMs: t.RuntimeMs, Season: t.Season, Episode: t.Episode}}
	var ans addonkit.ChannelStream
	if err := x.H.Call(ctx, id, "channels.resolve", nil, body, &ans); err != nil {
		return channels.Stream{}, err
	}
	if ans.Path == "" {
		return channels.Stream{}, fmt.Errorf("addons: addon %s gave no stream", id)
	}
	if !validStreamHeaders(ans.Headers) {
		return channels.Stream{}, fmt.Errorf("addons: addon %s: bad stream headers", id)
	}
	u, token, rel, err := x.Relay.open(id, ans.Path, ans.Headers)
	if err != nil {
		return channels.Stream{}, err
	}
	path := ans.Path
	var once sync.Once
	release := func() {
		once.Do(func() {
			rel()
			go x.release(id, token, path)
		})
	}
	return channels.Stream{URL: u, Release: release}, nil
}

// release fires channels.release (async: its own 5 s deadline).
func (x *ChannelExtras) release(id, token, path string) {
	err := x.H.Call(context.Background(), id, "channels.release", nil, addonkit.ChannelRelease{Token: token, Path: path}, nil)
	if err != nil && !errors.Is(err, errCooling) {
		x.log().Info("addon channels: release", "addon", id, "err", err)
	}
}
