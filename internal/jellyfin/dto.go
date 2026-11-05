package jellyfin

import "time"

// Jellyfin response shapes (PascalCase, as jellyfin-sdk-swift decodes
// them). Only the fields Swiftfin and Infuse read (map-clients.md §1).

// QueryResult is the paged list most list endpoints return.
type QueryResult[T any] struct {
	Items            []T `json:"Items"`
	TotalRecordCount int `json:"TotalRecordCount"`
	StartIndex       int `json:"StartIndex"`
}

type UserConfiguration struct {
	AudioLanguagePreference    string   `json:"AudioLanguagePreference"`
	SubtitleLanguagePreference string   `json:"SubtitleLanguagePreference"`
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleMode               string   `json:"SubtitleMode"`
	OrderedViews               []string `json:"OrderedViews"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
}

// UserPolicy: IsAdministrator is false for everyone, Vault admins too
// (otherwise Swiftfin opens its server dashboard, which Vault lacks).
type UserPolicy struct {
	IsAdministrator                bool `json:"IsAdministrator"`
	IsDisabled                     bool `json:"IsDisabled"`
	EnableMediaPlayback            bool `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding bool `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding bool `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing         bool `json:"EnablePlaybackRemuxing"`
	EnableContentDownloading       bool `json:"EnableContentDownloading"`
	EnableAllFolders               bool `json:"EnableAllFolders"`
	EnableRemoteAccess             bool `json:"EnableRemoteAccess"`
	// jellyfin-sdk-swift decodes both with `decode`, not `decodeIfPresent`
	// (Sources/Entities/UserPolicy.swift): without them Swiftfin cannot sign in.
	AuthenticationProviderId string `json:"AuthenticationProviderId"`
	PasswordResetProviderId  string `json:"PasswordResetProviderId"`
}

// The providers a Jellyfin server names in UserPolicy.
const (
	authProvider  = "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider"
	resetProvider = "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider"
)

type UserDto struct {
	Name                  string             `json:"Name"`
	Id                    string             `json:"Id"`
	ServerId              string             `json:"ServerId"`
	HasPassword           bool               `json:"HasPassword"`
	HasConfiguredPassword bool               `json:"HasConfiguredPassword"`
	EnableAutoLogin       bool               `json:"EnableAutoLogin"`
	PrimaryImageTag       string             `json:"PrimaryImageTag,omitempty"`
	Configuration         *UserConfiguration `json:"Configuration,omitempty"`
	Policy                *UserPolicy        `json:"Policy,omitempty"`
}

type SessionInfoDto struct {
	Id                    string   `json:"Id"`
	UserId                string   `json:"UserId"`
	UserName              string   `json:"UserName"`
	Client                string   `json:"Client"`
	DeviceId              string   `json:"DeviceId"`
	DeviceName            string   `json:"DeviceName"`
	ApplicationVersion    string   `json:"ApplicationVersion"`
	ServerId              string   `json:"ServerId"`
	IsActive              bool     `json:"IsActive"`
	SupportsRemoteControl bool     `json:"SupportsRemoteControl"`
	LastActivityDate      string   `json:"LastActivityDate"`
	PlayableMediaTypes    []string `json:"PlayableMediaTypes"`
	SupportedCommands     []string `json:"SupportedCommands"`
}

type AuthenticationResult struct {
	User        UserDto        `json:"User"`
	SessionInfo SessionInfoDto `json:"SessionInfo"`
	AccessToken string         `json:"AccessToken"`
	ServerId    string         `json:"ServerId"`
}

type NameIDPair struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

type BaseItemPerson struct {
	Name            string `json:"Name"`
	Id              string `json:"Id"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
}

type UserItemDataDto struct {
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Played                bool     `json:"Played"`
	PlayedPercentage      *float64 `json:"PlayedPercentage,omitempty"`
	UnplayedItemCount     *int     `json:"UnplayedItemCount,omitempty"`
	LastPlayedDate        string   `json:"LastPlayedDate,omitempty"`
	Key                   string   `json:"Key"`
	ItemId                string   `json:"ItemId"`
}

// BaseItemDto is an item (view, movie, series, season, episode).
// ImageTags is always present ({} without images): clients index it.
type BaseItemDto struct {
	Name                    string            `json:"Name"`
	OriginalTitle           string            `json:"OriginalTitle,omitempty"`
	Id                      string            `json:"Id"`
	ServerId                string            `json:"ServerId"`
	Etag                    string            `json:"Etag,omitempty"`
	Type                    string            `json:"Type"`
	IsFolder                bool              `json:"IsFolder"`
	MediaType               string            `json:"MediaType,omitempty"`
	CollectionType          string            `json:"CollectionType,omitempty"`
	ParentId                string            `json:"ParentId,omitempty"`
	SeriesId                string            `json:"SeriesId,omitempty"`
	SeriesName              string            `json:"SeriesName,omitempty"`
	SeasonId                string            `json:"SeasonId,omitempty"`
	SeasonName              string            `json:"SeasonName,omitempty"`
	IndexNumber             *int              `json:"IndexNumber,omitempty"`
	ParentIndexNumber       *int              `json:"ParentIndexNumber,omitempty"`
	ProductionYear          int               `json:"ProductionYear,omitempty"`
	PremiereDate            string            `json:"PremiereDate,omitempty"`
	DateCreated             string            `json:"DateCreated,omitempty"`
	RunTimeTicks            int64             `json:"RunTimeTicks,omitempty"`
	Overview                string            `json:"Overview,omitempty"`
	Genres                  []string          `json:"Genres,omitempty"`
	GenreItems              []NameIDPair      `json:"GenreItems,omitempty"`
	Studios                 []NameIDPair      `json:"Studios,omitempty"`
	People                  []BaseItemPerson  `json:"People,omitempty"`
	OfficialRating          string            `json:"OfficialRating,omitempty"`
	CommunityRating         float64           `json:"CommunityRating,omitempty"`
	ProviderIds             map[string]string `json:"ProviderIds,omitempty"`
	Container               string            `json:"Container,omitempty"`
	Path                    string            `json:"Path,omitempty"`
	LocationType            string            `json:"LocationType,omitempty"`
	VideoType               string            `json:"VideoType,omitempty"`
	HasSubtitles            bool              `json:"HasSubtitles,omitempty"`
	ChildCount              *int              `json:"ChildCount,omitempty"`
	RecursiveItemCount      *int              `json:"RecursiveItemCount,omitempty"`
	ImageTags               map[string]string `json:"ImageTags"`
	BackdropImageTags       []string          `json:"BackdropImageTags,omitempty"`
	PrimaryImageAspectRatio float64           `json:"PrimaryImageAspectRatio,omitempty"`
	SeriesPrimaryImageTag   string            `json:"SeriesPrimaryImageTag,omitempty"`
	ParentBackdropItemId    string            `json:"ParentBackdropItemId,omitempty"`
	ParentBackdropImageTags []string          `json:"ParentBackdropImageTags,omitempty"`
	ParentThumbItemId       string            `json:"ParentThumbItemId,omitempty"`
	ParentThumbImageTag     string            `json:"ParentThumbImageTag,omitempty"`
	SeriesThumbImageTag     string            `json:"SeriesThumbImageTag,omitempty"`
	UserData                *UserItemDataDto  `json:"UserData,omitempty"`
	MediaSources            []MediaSourceInfo `json:"MediaSources,omitempty"`
	MediaStreams            []MediaStream     `json:"MediaStreams,omitempty"`
}

// jfTime is the date form jellyfin-sdk-swift parses (3-digit fraction,
// Z): one unparsable date fails the whole item there.
func jfTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// ticks converts milliseconds to Jellyfin ticks (100 ns).
func ticks(ms int64) int64 { return ms * 10_000 }
