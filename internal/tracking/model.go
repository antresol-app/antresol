// Package tracking keeps the household's tracked titles: marks per profile,
// episode lists and where each episode can be got.
package tracking

import "time"

// Title is one tracked series or movie.
type Title struct {
	ID          int64
	Kind        string // series | movie
	TMDBID      int64
	ShikimoriID *int64
	MyShowsID   *int64
	AniDBID     *int64
	IMDbID      string
	Name        string
	Poster      string
	Status      string // airing | ended | upcoming | unknown
	Cert        string // TMDB age certification (RU form), "" = unknown
	NextCheck   time.Time
	Refreshed   time.Time
}

// MarkState is a profile's list state of a title.
type MarkState string

const (
	Planned   MarkState = "planned"
	Watching  MarkState = "watching"
	Completed MarkState = "completed"
	OnHold    MarkState = "on_hold"
	Dropped   MarkState = "dropped"
)

// Mark is one profile's mark on a title.
type Mark struct {
	UserID, TitleID int64
	State           MarkState
	Score           *int
	UpdatedAt       time.Time
	Origin          string // antresol | shikimori | myshows
}

// Episode is one entry of a title's episode list.
type Episode struct {
	TitleID       int64
	Season        int
	Number        int
	Name, AirDate string // AirDate: YYYY-MM-DD or ""
}

// Source is a place where an episode can be watched or fetched.
type Source struct {
	TitleID             int64
	Season, Episode     int
	Kind                string // library | pinned | torrent | online
	Ref, Provider       string
	Quality             string
	Labels              []string
	Meta                SourceMeta
	FirstSeen, LastSeen time.Time
}

// SourceMeta is what a source offers beyond provider and quality.
type SourceMeta struct {
	ItemID    int64    `json:"itemId,omitempty"`     // library: the episode (or movie) item to play
	FileID    int64    `json:"fileId,omitempty"`     // library: the file
	SourceID  string   `json:"sourceId,omitempty"`   // torrent/pinned: indexer source id
	Provider  string   `json:"providerId,omitempty"` // online: balancer id (online_progress.source)
	Voice     string   `json:"voice,omitempty"`      // online: voice name; torrent/pinned: first studio or ""
	Lang      string   `json:"lang,omitempty"`       // audio language: ISO 639-1 ("ru","en","ja"), "" = unknown
	Audio     string   `json:"audio,omitempty"`      // Dub|MVO|AVO|Original|""
	Studios   []string `json:"studios,omitempty"`
	Subs      []string `json:"subs,omitempty"`
	FileIndex *int     `json:"fileIndex,omitempty"` // pinned: engine file index
}

// Choice is a profile's preferred source of a title; every field is optional.
type Choice struct {
	Kind     string `json:"kind,omitempty"`
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
	Lang     string `json:"lang,omitempty"`
	Quality  string `json:"quality,omitempty"`
}
