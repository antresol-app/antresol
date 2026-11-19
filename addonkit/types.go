package addonkit

import (
	"encoding/json"
	"time"
)

// Item is a library item (movie, series, season, episode, album, ...).
type Item struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	ParentID      int64           `json:"parentId"`
	Title         string          `json:"title"`
	OriginalTitle string          `json:"originalTitle"`
	Year          int             `json:"year"`
	Rating        string          `json:"rating"`
	TMDBID        int64           `json:"tmdbId"`
	SeasonNo      int             `json:"seasonNo"`
	EpisodeNo     int             `json:"episodeNo"`
	Poster        string          `json:"poster"`
	AddedAt       time.Time       `json:"addedAt"`
	Meta          json.RawMessage `json:"meta,omitempty"`
	// Aliases are the item's other titles the library knows (its title and
	// original title left out), in the order it stores them: [] when none,
	// on GET /library/items/{id}, /library/tmdb/… and /library/search;
	// left out of the other answers, which do not read them (nil here).
	Aliases []string `json:"aliases,omitzero"`
}

// File is a media file attached to an item.
type File struct {
	ID         int64           `json:"id"`
	ItemID     int64           `json:"itemId"`
	Path       string          `json:"path"`
	Size       int64           `json:"size"`
	Mtime      int64           `json:"mtime"`
	DurationMs int64           `json:"durationMs"`
	Season     int             `json:"season"`
	Episode    int             `json:"episode"`
	Probe      json.RawMessage `json:"probe,omitempty"`
}

// Track is a music track: an item plus its album, artist and file.
type Track struct {
	Item
	AlbumID  int64  `json:"albumId"`
	ArtistID int64  `json:"artistId"`
	FileID   int64  `json:"fileId"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Disc     int    `json:"disc"`
	No       int    `json:"no"`
	StartMs  int64  `json:"startMs"`
	EndMs    *int64 `json:"endMs,omitempty"`
}

// User is the acting user; it is what the X-Antresol-User header carries.
type User struct {
	ID int64 `json:"id"`
	// UID is the profile's stable key: an id may be given to a new
	// profile once one is deleted, a uid never. Key per-profile rows by
	// it.
	UID     string `json:"uid"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
	IsKid   bool   `json:"isKid"`
	Lang    string `json:"lang"`
	// Languages is the profile's «Языки», its preferred audio languages in
	// order (its own list, else the household's); only GET /users fills
	// it ([] when there are none; nil in the X-Antresol-User header).
	Languages []string `json:"languages"`
	// MaxRating is the profile's rating limit ("0+" … "18+"; "" = none)
	// as the server's own gate applies it: the X-Antresol-User header and
	// GET /users carry it. A video title or item whose rating is unknown
	// or above it is hidden from the profile.
	MaxRating string `json:"maxRating,omitempty"`
	// MaxHeight is the height in pixels «Качество вне дома» caps playback
	// at (1080, 720, 480) for a request from outside the home network;
	// 0 (absent) at home, when that setting is «Оригинал», and on calls
	// not made for an app's request. Only the X-Antresol-User header
	// carries it. An addon serving video plays no rendition taller.
	MaxHeight int `json:"maxHeight,omitempty"`
	// History: the request writes a separate history («Отдельная история
	// на этом устройстве»: a shared TV, or an app signed in with a managed
	// account's app password). The profile above is still the one asking
	// (its limits, its settings, its rights); what it watches is recorded
	// for History instead. Absent: the profile's own history. Only the
	// X-Antresol-User header carries it. An addon that keeps a history
	// (positions, «Продолжить», what was watched) keys it by
	// User.HistoryUser().
	History *History `json:"history,omitempty"`
	// Histories: the profile's separate histories (managed accounts), so
	// that an addon keeping rows by History.UID knows which uids still
	// exist. Only GET /users fills it (HistoryProfiles lists them).
	Histories []History `json:"histories,omitempty"`
}

// History is a managed account: a separate history under a profile. It
// has no sign-in and no settings of its own; the profile's limits apply.
// Its id is accepted wherever the core API takes the userId of a history
// (GET/PUT /progress, GET /watched, episode-items).
type History struct {
	ID   int64  `json:"id"`
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// HistoryUser is u as the history it records: ID and UID of u.History
// when the request writes a separate one, else u unchanged. The rest (the
// rating limit, kid, language) stays u's: a separate history inherits its
// profile's limits.
func (u User) HistoryUser() User {
	if u.History == nil || u.History.ID <= 0 || u.History.UID == "" {
		u.History = nil
		return u
	}
	h := *u.History
	u.ID, u.UID, u.History, u.Histories = h.ID, h.UID, nil, nil
	return u
}

// HistoryProfiles is us (GET /users) with each profile's separate
// histories after it, as HistoryUser would give them: every id and uid an
// addon may have keyed a history by.
func HistoryProfiles(us []User) []User {
	out := make([]User, 0, len(us))
	for _, u := range us {
		out = append(out, u)
		for _, h := range u.Histories {
			hu := u
			hu.History = &h
			hu = hu.HistoryUser()
			hu.Name = h.Name
			out = append(out, hu)
		}
	}
	return out
}

// Text is a user-facing string in both interface languages.
type Text struct {
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Hold describes files an addon keeps under its own control.
type Hold struct {
	Key         string     `json:"key"`
	Root        string     `json:"root"`
	Files       []HoldFile `json:"files"`
	Active      bool       `json:"active"`
	ItemID      int64      `json:"itemId"`
	Busy        bool       `json:"busy"`
	Label       Text       `json:"label"`
	MoveWarning Text       `json:"moveWarning"`
	KeptWarning Text       `json:"keptWarning"`
}

// HoldFile is one file of a Hold.
type HoldFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Selected bool   `json:"selected"`
}

// RefPath pairs an addon-side reference with a path.
type RefPath struct {
	Ref  string `json:"ref"`
	Path string `json:"path"`
}

// RefFile is a library file resolved for a reference.
type RefFile struct {
	FileID int64  `json:"fileId"`
	ItemID int64  `json:"itemId"`
	Path   string `json:"path"`
}

// Bound is the outcome of binding paths to a reference.
type Bound struct {
	Bound   int      `json:"bound"`
	Missing []string `json:"missing"`
	Taken   []string `json:"taken"`
}

// ImportResult is the outcome of importing files into the library.
type ImportResult struct {
	Files   []ImportedFile `json:"files"`
	Missing []string       `json:"missing"`
	Taken   []string       `json:"taken"`
}

// ImportedFile is one imported file.
type ImportedFile struct {
	Path   string `json:"path"`
	FileID int64  `json:"fileId"`
	ItemID int64  `json:"itemId"`
}

// SeriesAudio lists the audio choices of a series.
type SeriesAudio struct {
	Files []EpisodeAudio `json:"files"`
	// Profiles is left out for an addon without the users.read permission.
	Profiles []ProfileVoice `json:"profiles,omitempty"`
}

// EpisodeAudio lists the audio track titles of one episode.
type EpisodeAudio struct {
	Season  int      `json:"season"`
	Episode int      `json:"episode"`
	Titles  []string `json:"titles"`
}

// ProfileVoice is the audio track a user last chose.
type ProfileVoice struct {
	UserID     int64     `json:"userId"`
	AudioTitle string    `json:"audioTitle"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// EpisodeItem is the library item of one episode of a series (EpisodeItems,
// GET /library/series/{tmdbId}/episode-items, permission library.read): the
// episode item that holds the file of season Season, episode Episode. Two
// episodes of one multi-episode file share its ItemID.
type EpisodeItem struct {
	Season  int   `json:"season"`
	Episode int   `json:"episode"`
	ItemID  int64 `json:"itemId"`
}

// ProgressEntry is a profile's watch progress of one movie or episode
// (Progress, GET /progress, permission progress): where it stopped, the
// duration known when it did, whether it counts as watched (from 92% of a
// known duration, or marked so), and when the position was recorded.
type ProgressEntry struct {
	PositionMs int64     `json:"positionMs"`
	DurationMs int64     `json:"durationMs"`
	Watched    bool      `json:"watched"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ProgressSave is a profile's position in one movie or episode
// (SaveProgress, PUT /progress, permission progress). At is when the
// position was reached (zero: now); the core ignores a position older than
// the one it keeps, and refuses one more than 5 minutes ahead of its clock.
// Played says the position is a player's report, so the time played since
// the last one counts in the profile's watch time; false for a position
// copied from elsewhere.
type ProgressSave struct {
	UserID     int64     `json:"userId"`
	ItemID     int64     `json:"itemId"`
	PositionMs int64     `json:"positionMs"`
	DurationMs int64     `json:"durationMs"`
	At         time.Time `json:"at,omitzero"`
	Played     bool      `json:"played"`
}

// Info describes the core to an addon.
type Info struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	Contract     int          `json:"contract"`
	FFmpeg       string       `json:"ffmpeg"`
	FFprobe      string       `json:"ffprobe"`
	PublicURL    string       `json:"publicUrl"`
	LibraryRoots LibraryRoots `json:"libraryRoots"`
	// MediaRoot is the server's media folder (absolute; "" when unset):
	// an addon's relative folders are taken against it.
	MediaRoot string `json:"mediaRoot"`
	// FileRoots are the file browser's root folders, admin-only ones
	// included, with their symlinks resolved (those not on disk are left
	// out). PUT /files/holds refuses a hold whose root, its symlinks
	// resolved, is not at or inside one of them, unless the root is at or
	// inside one of MissingRoots (then it is accepted as written).
	FileRoots []string `json:"fileRoots"`
	// MissingRoots are the file browser's roots not on disk right now (a
	// mount not up yet), as configured. Holds under them are accepted and
	// kept; they protect the path once it is there.
	MissingRoots []string `json:"missingRoots"`
}

// LibraryRoots are the library folders per content kind.
type LibraryRoots struct {
	Movies     string `json:"movies"`
	Series     string `json:"series"`
	Music      string `json:"music"`
	Ebooks     string `json:"ebooks"`
	Audiobooks string `json:"audiobooks"`
	Comics     string `json:"comics"`
}

// InputRequest registers an external video for playback (RegisterInput).
type InputRequest struct {
	UserID int64 `json:"userId"`
	// Path is a route on the addon's socket; it must support Range.
	Path string `json:"path"`
	// Name is the slash-separated relative path in the addon's collection.
	Name     string          `json:"name"`
	Size     int64           `json:"size"`
	ItemID   int64           `json:"itemId,omitempty"`
	Title    *InputTitle     `json:"title,omitempty"`
	Sidecars InputSidecars   `json:"sidecars"`
	Slow     bool            `json:"slow,omitempty"`
	Meta     json.RawMessage `json:"meta,omitempty"`
}

// InputTitle ties an input to a title (and an episode of a series).
type InputTitle struct {
	Kind    string `json:"kind"`
	TMDBID  int64  `json:"tmdbId"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

// InputSidecars are the extra audio and subtitle files of an input.
type InputSidecars struct {
	Audio     []AudioSidecar    `json:"audio"`
	Subtitles []SubtitleSidecar `json:"subtitles"`
}

// AudioSidecar is a separate audio file served by the addon.
type AudioSidecar struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// SubtitleSidecar is a subtitle file served by the addon.
type SubtitleSidecar struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Lang string `json:"lang"`
}

// InputCreated answers RegisterInput.
type InputCreated struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// PlayProgress is the play.progress hook payload.
type PlayProgress struct {
	InputID     string  `json:"inputId"`
	UserID      int64   `json:"userId"`
	PositionSec float64 `json:"positionSec"`
	DurationSec float64 `json:"durationSec"`
	Ended       bool    `json:"ended"`
}

// NotifyRequest is a notification for the profiles' mail digests (Notify,
// POST /notify, permission notify). Topic is one of the manifest's notify
// topics. Exactly one of UserIDs and Audience names the recipients; with
// neither, the topic's audience does. Audience ("admins" | "adults") may
// only narrow the topic's; a kid profile is never a recipient. Key names
// the thing (at most 200 characters): a key sent before makes no second
// line. Subject (at most 200 characters per language, one language at
// least) is the letter's line, Body (at most 2000) its detail. Link is a
// page of the app ("/item/3", never another site); Poster a TMDB https
// image, any other is dropped.
type NotifyRequest struct {
	Topic    string  `json:"topic"`
	UserIDs  []int64 `json:"userIds,omitempty"`
	Audience string  `json:"audience,omitempty"`
	Key      string  `json:"key"`
	Subject  Text    `json:"subject"`
	Body     *Text   `json:"body,omitempty"`
	Link     string  `json:"link,omitempty"`
	Poster   string  `json:"poster,omitempty"`
}

// PendingQuery is the library.pending hook's body: Scope "mine" asks for
// the titles the profile (X-Antresol-User) asked for, "all" for the
// household's.
type PendingQuery struct {
	Scope string `json:"scope"`
}

// PendingAnswer is the library.pending hook's answer.
type PendingAnswer struct {
	Cards []PendingCard `json:"cards"`
}

// PendingCard is a title on its way into the library, nested as the hook
// sends it ({card, pending}); the core flattens it into its own card of
// GET /api/library/pending.
type PendingCard struct {
	Card    PendingCardInfo `json:"card"`
	Pending PendingState    `json:"pending"`
}

// PendingCardInfo is the card of a PendingCard. ID is the title's library
// item when the library has it already (a new season), else 0. Kind is a
// library kind (movie, series, album, book, audiobook, comic). Poster is
// an image name POST /images gave (any other value is dropped). TmdbKind
// ("movie" | "series") and TmdbID open a card that has no item.
type PendingCardInfo struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Year     int    `json:"year"`
	Poster   string `json:"poster"`
	TmdbKind string `json:"tmdbKind"`
	TmdbID   int64  `json:"tmdbId"`
}

// PendingState is the progress of a PendingCard. Key is the addon's own
// (the core prefixes it "<addonId>:"). Status is active, waiting (for disk
// space), paused, organizing or error (shown to admins only); a card with
// another status is dropped. Done and Size are bytes. Hint is a line under
// the progress, already in the profile's language. Href is the app page
// the card opens ("/…", never another site); without it the card does not
// open.
type PendingState struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Done   int64  `json:"done"`
	Size   int64  `json:"size"`
	Hint   string `json:"hint,omitempty"`
	Href   string `json:"href,omitempty"`
}

// ContinueAnswer is the home.continue hook's answer (its body is {}: the
// profile is the X-Antresol-User header's).
type ContinueAnswer struct {
	Cards []ContinueCard `json:"cards"`
}

// ContinueCard is a title the profile was watching through the addon, for
// «Продолжить» on Home. Key is the addon's own (the core namespaces it
// "addon:<id>:<key>"); a card without one is dropped. Kind is "movie" or
// "series" (another kind is dropped) and TMDBID its TMDB id. Rating is the
// title's age rating ("" = unknown): the core uses its own item's when the
// library has the title (Kind and TMDBID), else this one, and drops a card
// above the profile's limit, or an unrated one for a limited profile.
// Poster is an image name POST /images gave (any other value becomes "").
// Progress is 0..1, ResumeSec where to resume; Season and Episode the
// episode of a series. Href is the app page the card opens ("/…", never
// another site; another value is dropped). UpdatedAt is when the profile
// last watched it: the core merges the cards with its own by it.
type ContinueCard struct {
	Key       string    `json:"key"`
	Kind      string    `json:"kind"`
	TMDBID    int64     `json:"tmdbId"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle"`
	Year      int       `json:"year"`
	Poster    string    `json:"poster"`
	Rating    string    `json:"rating"`
	Progress  float64   `json:"progress"`
	ResumeSec float64   `json:"resumeSec"`
	Season    int       `json:"season"`
	Episode   int       `json:"episode"`
	Href      string    `json:"href"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ChannelReady answers channels.ready (its body is {}): whether the addon
// can find and play titles outside the library for «ТВ» channels now.
type ChannelReady struct {
	Ready bool `json:"ready"`
}

// ChannelRules are the rules of a channel whose «titles outside the
// library» are on, as the core sends them in channels.candidates: titles
// of any of Genres (display names, either language; none: any), made
// from YearFrom to YearTo (0: open), in any of Countries (ISO 3166-1),
// Languages (ISO 639-1, the original language) and Keywords (TMDB's,
// English, lower case), within MaxRating (the channel's age limit, ""
// none).
type ChannelRules struct {
	Genres    []string `json:"genres"`
	YearFrom  int      `json:"yearFrom"`
	YearTo    int      `json:"yearTo"`
	Countries []string `json:"countries"`
	Languages []string `json:"languages"`
	Keywords  []string `json:"keywords"`
	MaxRating string   `json:"maxRating"`
}

// ChannelCandidates is the body of channels.candidates: up to Limit
// titles for Rules that the library does not have.
type ChannelCandidates struct {
	Rules ChannelRules `json:"rules"`
	Limit int          `json:"limit"`
}

// ChannelCandidatesAnswer answers channels.candidates.
type ChannelCandidatesAnswer struct {
	Titles []ChannelTitle `json:"titles"`
}

// ChannelTitle is a title from outside the library a channel may play.
// Key is the addon's own stable identity of it (the core namespaces it
// "addon:<id>:<key>" and sends it back bare in channels.resolve). Kind is
// "movie" (the default) or "episode"; TMDBID the movie's, or the series'
// for an episode (Season, Episode). Overview, Rating (the age rating, ""
// unknown) and Genres (display names) are in the server's metadata
// language; Poster and Backdrop TMDB image paths ("/abc.jpg"). RuntimeMs
// is how long it lasts in the schedule (a title without one is dropped).
// The core rates it with its own item's rating when the library has it.
type ChannelTitle struct {
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	TMDBID    int64    `json:"tmdbId"`
	Title     string   `json:"title"`
	Year      int      `json:"year"`
	Overview  string   `json:"overview"`
	Rating    string   `json:"rating"`
	Genres    []string `json:"genres"`
	Poster    string   `json:"poster"`
	Backdrop  string   `json:"backdrop"`
	RuntimeMs int64    `json:"runtimeMs"`
	Season    int      `json:"season"`
	Episode   int      `json:"episode"`
}

// ChannelResolve is the body of channels.resolve: a stream of Title for
// channel ChannelID, shortly before it airs.
type ChannelResolve struct {
	ChannelID int64        `json:"channelId"`
	Title     ChannelTitle `json:"title"`
}

// ChannelStream answers channels.resolve: Path is a route on the addon's
// socket (an HLS playlist, whose relative URIs stay under its folder, or
// a file) the core reads through its loopback relay, sending Headers with
// every request to the addon.
type ChannelStream struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ChannelRelease is the body of channels.release: the stream the relay
// read as Token (the X-Antresol-Relay header of its requests) and opened
// for Path is over, or was never played.
type ChannelRelease struct {
	Token string `json:"token"`
	Path  string `json:"path"`
}

// DiskUsage answers the disk.usage hook: the addon's segments of the disk
// meter.
type DiskUsage struct {
	Segments []DiskSegment `json:"segments"`
}

// DiskSegment is one segment of the disk meter. With Dir (absolute, below
// Info.MediaRoot or one of Info.FileRoots) the core walks the folder and
// counts what the library does not already hold; without it, Bytes is
// taken as is.
type DiskSegment struct {
	ID    string `json:"id"`
	Label Text   `json:"label"`
	Dir   string `json:"dir,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// RequestsOpenAnswer answers the requests.open hook: whether the addon
// takes kids' requests on this server («Попросить» is offered while any
// running addon says Open). The requests.* hooks come without a profile
// (no X-Antresol-User): they answer for the household.
type RequestsOpenAnswer struct {
	Open bool `json:"open"`
}

// RequestTitleQuery is the requests.title hook's body: the catalog id of
// an album, book, audiobook or comic a kid asks for.
type RequestTitleQuery struct {
	Kind  string `json:"kind"`
	ExtID string `json:"extId"`
}

// RequestTitleAnswer names a requested title (200; 204 or 404: the
// addon's catalog does not know it). Poster is kept only when it is a
// catalog's cover; Explicit refuses the request.
type RequestTitleAnswer struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"` // the artist or the author
	Year     int    `json:"year"`
	Poster   string `json:"poster"`
	Explicit bool   `json:"explicit"`
}

// RequestSearchQuery is the requests.search hook's body: a kid's search
// of a kind ("music", "book", "audiobook" or "comic").
type RequestSearchQuery struct {
	Kind string `json:"kind"`
	Q    string `json:"q"`
}

// RequestSearchAnswer answers the requests.search hook.
type RequestSearchAnswer struct {
	Candidates []RequestCandidate `json:"candidates"`
}

// RequestCandidate is a title a kid may ask for: a TMDB movie or series
// (TMDBID), or an album, book, audiobook or comic of a catalog (ExtID).
// Explicit: the catalog marks it adult content (kept from kids).
type RequestCandidate struct {
	Kind     string   `json:"kind"`
	TMDBID   int      `json:"tmdbId"`
	ExtID    string   `json:"extId"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Year     int      `json:"year"`
	Poster   string   `json:"poster"`
	Explicit bool     `json:"explicit"`
	Aliases  []string `json:"aliases"`
}

// ApproveRequest approves the kids' requests of what admin AdminID
// started getting: by the title's key (TMDBID for movies and series,
// ExtID for the others), or for an album or book by Artist (the author)
// and Title alone.
type ApproveRequest struct {
	AdminID int64  `json:"adminId"`
	Kind    string `json:"kind"`
	TMDBID  int    `json:"tmdbId"`
	ExtID   string `json:"extId"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
}

// TrackingTitle is a tracked series or movie (permission tracking). Kind
// is "series" or "movie"; Status is airing, ended, upcoming or unknown;
// Cert is the TMDB age certification in its Russian form ("" when not
// known). Poster is a TMDB image path.
type TrackingTitle struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId"`
	IMDbID string `json:"imdbId"`
	Name   string `json:"name"`
	Poster string `json:"poster"`
	Status string `json:"status"`
	Cert   string `json:"cert"`
}

// TrackingEpisode is one entry of a tracked title's episode list. AirDate
// is YYYY-MM-DD or "".
type TrackingEpisode struct {
	TitleID int64  `json:"titleId"`
	Season  int    `json:"season"`
	Number  int    `json:"number"`
	Name    string `json:"name"`
	AirDate string `json:"airDate"`
}

// TrackingSource is a place where an episode of a tracked title can be
// watched or got. Kind is library, or a scanner kind an addon declares
// (online's is one); Ref is unique per title, kind, season and episode.
type TrackingSource struct {
	TitleID   int64              `json:"titleId"`
	Season    int                `json:"season"`
	Episode   int                `json:"episode"`
	Kind      string             `json:"kind"`
	Ref       string             `json:"ref"`
	Provider  string             `json:"provider"`
	Quality   string             `json:"quality"`
	Labels    []string           `json:"labels"`
	Meta      TrackingSourceMeta `json:"meta"`
	FirstSeen time.Time          `json:"firstSeen"`
	LastSeen  time.Time          `json:"lastSeen"`
}

// TrackingSourceMeta is what a source offers beyond provider and quality.
// ItemID and FileID name the library item and file of a library source;
// SourceID is the id an addon's scanner gave it; Provider the provider
// the addon named; Voice the voice-over or first studio; Lang the audio language
// (ISO 639-1, "" when unknown); Audio Dub, MVO, AVO, Original or "";
// FileIndex the index of the episode's file within an addon's source.
type TrackingSourceMeta struct {
	ItemID    int64    `json:"itemId,omitempty"`
	FileID    int64    `json:"fileId,omitempty"`
	SourceID  string   `json:"sourceId,omitempty"`
	Provider  string   `json:"providerId,omitempty"`
	Voice     string   `json:"voice,omitempty"`
	Lang      string   `json:"lang,omitempty"`
	Audio     string   `json:"audio,omitempty"`
	Studios   []string `json:"studios,omitempty"`
	Subs      []string `json:"subs,omitempty"`
	FileIndex *int     `json:"fileIndex,omitempty"`
}

// ScanRequest is the body of a tracking.scan:<kind> hook: the tracked
// title and its episode list as the server just fetched it.
type ScanRequest struct {
	Title    TrackingTitle     `json:"title"`
	Episodes []TrackingEpisode `json:"episodes"`
}

// ScanAnswer answers a tracking.scan:<kind> hook. Sources replace the
// title's sources of that kind (an empty list removes them); each one's
// Kind is set to the hook's, and TitleID, FirstSeen and LastSeen are the
// server's. Unchanged: the addon did not look this time, the stored rows
// stay as they are. A 204 or 404 answer is the same as Unchanged.
type ScanAnswer struct {
	Sources   []TrackingSource `json:"sources,omitempty"`
	Unchanged bool             `json:"unchanged,omitempty"`
}

// TitlesAnswer answers the tracking.titles hook: the titles the addon
// wants tracked beside the profiles' marks.
type TitlesAnswer struct {
	Titles []ExtraTitle `json:"titles"`
}

// ExtraTitle is a title the addon wants tracked: Kind "series" or
// "movie", its TMDB id, and the Name and Poster (a TMDB image path) the
// server keeps when it makes the title.
type ExtraTitle struct {
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId"`
	Name   string `json:"name"`
	Poster string `json:"poster"`
}

// WatchedRequest is the body of the tracking.watched hook: which episodes
// of a title profile UserID has watched.
type WatchedRequest struct {
	UserID int64  `json:"userId"`
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId"`
}

// WatchedAnswer answers the tracking.watched hook.
type WatchedAnswer struct {
	Episodes []WatchedEpisode `json:"episodes"`
}

// WatchedEpisode is the profile's state of one episode (a movie is
// season 0, number 0): watched, or stopped at PositionSec.
type WatchedEpisode struct {
	Season      int  `json:"season"`
	Number      int  `json:"number"`
	Watched     bool `json:"watched"`
	PositionSec int  `json:"positionSec"`
}

// InterestRequest is the body of the notify.interest hook: a title that
// got new episodes.
type InterestRequest struct {
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId"`
}

// InterestAnswer answers the notify.interest hook: the profiles to tell
// besides those who marked the title.
type InterestAnswer struct {
	UserIDs []int64 `json:"userIds"`
}

// OrigLangRequest is the body of the voiceover.origLang hook: a library
// file whose original language the server asks for.
type OrigLangRequest struct {
	FileID int64 `json:"fileId"`
}

// OrigLangAnswer answers the voiceover.origLang hook: Lang is ISO 639-1,
// "" when unknown.
type OrigLangAnswer struct {
	Lang string `json:"lang"`
}

// TrailerFetch is the body of the trailers.fetch hook: a trailer the
// server listed, by its TMDB key, and the page to fetch it from (URL).
// MaxHeight, when positive, is the tallest picture wanted; 0: the addon
// chooses.
type TrailerFetch struct {
	Key       string `json:"key"`
	URL       string `json:"url"`
	MaxHeight int    `json:"maxHeight,omitempty"`
}

// TrailerFile answers trailers.fetch with 200: the trailer is ready at
// Path on the addon's socket (under /media/, Range served), Size bytes,
// DurationMs long (0: unknown), of ContentType "video/mp4".
type TrailerFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	DurationMs  int64  `json:"durationMs"`
	ContentType string `json:"contentType"`
}

// TrailerPending answers trailers.fetch with 202: still fetching; the
// server asks again after RetryAfterMs.
type TrailerPending struct {
	RetryAfterMs int64 `json:"retryAfterMs"`
}

// The states of a voice-over track's fit (FitState.State).
const (
	// FitPending: queued, being fitted (or fitted again: the track or its
	// video changed, or a failed fit is retried).
	FitPending = "pending"
	// FitOK: fitted; OffsetMs and Tempo say how the players play it.
	FitOK = "ok"
	// FitMisfit: the track is of another cut: never played.
	FitMisfit = "misfit"
	// FitUnmeasured: its sync could not be checked (the video has no track
	// in the title's original language, the fit failed for good).
	FitUnmeasured = "unmeasured"
)

// FitState is GET /voiceover/fit: the fit of a voice-over track placed
// beside a library file. For FitOK, OffsetMs is the shift the players
// apply (positive: the track plays later; a track whose sound starts 1.5 s
// late fits at offsetMs -1500) and Tempo an atempo factor (1: none); both
// 0 otherwise.
type FitState struct {
	State    string  `json:"state"`
	OffsetMs float64 `json:"offsetMs"`
	Tempo    float64 `json:"tempo"`
}

// ImportJob is a library import job of a profile (permission imports):
// it reads a music service's library (status reading), shows it
// (preview), matches its songs (matching), fetches the albums the
// library lacks (downloading) and ends done, failed or cancelled.
type ImportJob struct {
	ID         int64        `json:"id"`
	UserID     int64        `json:"userId"`
	Service    string       `json:"service"`
	Account    string       `json:"account"`
	Status     string       `json:"status"`
	Favorites  bool         `json:"favorites"`
	Error      string       `json:"error"`
	Report     ImportReport `json:"report"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	StartedAt  *time.Time   `json:"startedAt,omitempty"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
}

// ImportReport is what an import job could not do.
type ImportReport struct {
	SavedAlbumsMissing int `json:"savedAlbumsMissing"`
	Unresolved         int `json:"unresolved"`
}

// ImportGroup is one album of an import job to fetch. State is waiting,
// searching, waiting_space, waiting_download, downloading, done, have,
// not_found, no_format or failed. Key names the download it waits for
// or downloads; SkipKey one given up on (its own search leaves it out);
// Dead and Slow the downloads given up for making no headway or for being
// too slow (oldest first). WaitDone and WaitAt are the bytes of the
// download it waits for and when they last grew (a downloading album's:
// when they last grew); Clock its own download's running time.
type ImportGroup struct {
	ID           int64          `json:"id"`
	JobID        int64          `json:"jobId"`
	ReleaseGroup string         `json:"releaseGroup"`
	Artist       string         `json:"artist"`
	Album        string         `json:"album"`
	Year         int            `json:"year"`
	Aliases      []string       `json:"aliases"`
	AlbumItemID  int64          `json:"albumItemId"`
	State        string         `json:"state"`
	Key          string         `json:"key"`
	Bytes        int64          `json:"bytes"`
	Attempts     int            `json:"attempts"`
	Error        string         `json:"error"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	Songs        int            `json:"songs"`
	WaitingFor   string         `json:"waitingFor"`
	SkipKey      string         `json:"skipKey"`
	AnyFormat    bool           `json:"anyFormat"`
	Dead         []string       `json:"dead"`
	Slow         []string       `json:"slow"`
	WaitDone     int64          `json:"waitDone"`
	WaitAt       *time.Time     `json:"waitAt,omitempty"`
	Clock        ImportRunClock `json:"clock"`
}

// ImportRunClock is a downloading album's own download's clock: how long
// it has run and how long since its bytes last grew (whole seconds), its
// bytes when the clock started (-1: not yet) and then.
type ImportRunClock struct {
	RunSec   int64 `json:"runSec"`
	QuietSec int64 `json:"quietSec"`
	Base     int64 `json:"base"`
	Done     int64 `json:"done"`
}

// ImportTrack is one song of an import job: the service's (Key, Artist,
// Title, …) and its MusicBrainz match (MB…); GroupID is its album (0:
// none), ItemID its library track (0: none).
type ImportTrack struct {
	ID             int64      `json:"id"`
	JobID          int64      `json:"jobId"`
	Key            string     `json:"key"`
	Artist         string     `json:"artist"`
	Title          string     `json:"title"`
	Album          string     `json:"album"`
	AlbumArtist    string     `json:"albumArtist"`
	ISRC           string     `json:"isrc"`
	DurationMs     int64      `json:"durationMs"`
	AddedAt        *time.Time `json:"addedAt,omitempty"`
	Liked          bool       `json:"liked"`
	State          string     `json:"state"`
	Reason         string     `json:"reason"`
	MBRecording    string     `json:"mbRecording"`
	MBReleaseGroup string     `json:"mbReleaseGroup"`
	MBTitle        string     `json:"mbTitle"`
	MBArtist       string     `json:"mbArtist"`
	MBAlbum        string     `json:"mbAlbum"`
	MBYear         int        `json:"mbYear"`
	GroupID        int64      `json:"groupId"`
	ItemID         int64      `json:"itemId"`
}

// ImportSnapshot is what a reading job read from a music service: its
// songs, playlists (Entries are song keys in the service's order;
// Readable false: the service did not give its songs) and saved albums.
type ImportSnapshot struct {
	Service   string               `json:"service"`
	Account   string               `json:"account"`
	Tracks    []ImportSnapTrack    `json:"tracks"`
	Playlists []ImportSnapPlaylist `json:"playlists"`
	Albums    []ImportSnapAlbum    `json:"albums"`
}

// ImportSnapTrack is one song of a snapshot; Key identifies it within the
// snapshot (empty: derived from artist, title and album).
type ImportSnapTrack struct {
	Key         string     `json:"key"`
	Artist      string     `json:"artist"`
	Title       string     `json:"title"`
	Album       string     `json:"album"`
	AlbumArtist string     `json:"albumArtist"`
	ISRC        string     `json:"isrc"`
	DurationMs  int64      `json:"durationMs"`
	AddedAt     *time.Time `json:"addedAt"`
	Liked       bool       `json:"liked"`
}

// ImportSnapPlaylist is a playlist of a snapshot.
type ImportSnapPlaylist struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Readable bool     `json:"readable"`
	Entries  []string `json:"entries"`
}

// ImportSnapAlbum is a saved album of a snapshot.
type ImportSnapAlbum struct {
	Key     string     `json:"key"`
	Artist  string     `json:"artist"`
	Title   string     `json:"title"`
	Year    int        `json:"year"`
	AddedAt *time.Time `json:"addedAt"`
}

// ImportService is a music service whose library an addon reads, as the
// server's «Импорт» page shows its tile: how it connects (redirect, paste,
// device, file or app), whether the owner set it up, the setup steps while
// not, and the files it can also be read from (hook import.services).
type ImportService struct {
	ID         string   `json:"id"`
	Configured bool     `json:"configured"`
	Connect    string   `json:"connect"`
	Setup      []string `json:"setup"`
	Files      []string `json:"files"`
}

// ImportServicesAnswer answers import.services.
type ImportServicesAnswer struct {
	Services []ImportService `json:"services"`
}

// ImportStopRequest is import.stop's body: the job whose read to stop.
type ImportStopRequest struct {
	JobID int64 `json:"jobId"`
}

// ImportResolveRequest is import.resolve's body: video ids an uploaded
// file gives without names.
type ImportResolveRequest struct {
	IDs []string `json:"ids"`
}

// ImportResolveAnswer answers import.resolve: the songs the addon named,
// each with Key = the id asked.
type ImportResolveAnswer struct {
	Tracks []ImportSnapTrack `json:"tracks"`
}

// ImportSearchRequest is import.search's body: the review's search of
// Kind (music) for Q.
type ImportSearchRequest struct {
	Kind string `json:"kind"`
	Q    string `json:"q"`
}

// ImportCandidate is an album the review may pick for a song: ExtID is its
// MusicBrainz release group; InLibrary/LibraryItemID say the library has
// it; MatchedTrack is the song of the query the album holds (a search by
// song title), ByArtist marks the music of the artist the query names.
type ImportCandidate struct {
	TMDBID        int      `json:"tmdbId"`
	ExtID         string   `json:"extId"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle"`
	Year          int      `json:"year"`
	Poster        string   `json:"poster"`
	Overview      string   `json:"overview"`
	InLibrary     bool     `json:"inLibrary"`
	LibraryItemID *int64   `json:"libraryItemId"`
	Aliases       []string `json:"aliases,omitempty"`
	MatchedTrack  string   `json:"matchedTrack,omitempty"`
	ByArtist      string   `json:"byArtist,omitempty"`
}

// ImportSearchAnswer answers import.search.
type ImportSearchAnswer struct {
	Candidates []ImportCandidate `json:"candidates"`
}

// LeftoversRequest is migrate.leftovers' body: Lang is the «Переезд»
// screen's language (ru, en), the one the lines are written in.
type LeftoversRequest struct {
	Lang string `json:"lang"`
}

// LeftoverLine is one thing an old service has that does not move: Source
// is the service's id, Name what it is, Status warning (it stays behind) or
// failed (it could not be checked), Reason the line the screen shows, in
// the request's language (hook migrate.leftovers).
type LeftoverLine struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// LeftoversAnswer answers migrate.leftovers.
type LeftoversAnswer struct {
	Lines []LeftoverLine `json:"lines"`
}
