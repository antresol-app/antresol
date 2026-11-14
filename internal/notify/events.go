package notify

import (
	"context"
	"encoding/json"
	"strings"
)

// Event is one thing to tell a profile.
type Event struct {
	UserID int64
	Topic  string
	// Key names the thing ("ep:123"): an event with a key the profile
	// already has (waiting or sent) is not queued again.
	Key string
	// ItemID is the library item the letter links to (0: none);
	// GroupID the series several episodes are listed under (0: none).
	ItemID, GroupID int64
	Data            Data
}

// Data is what the letter says about an event.
type Data struct {
	// Title: the series of an episode, else the title itself.
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	Kind  string `json:"kind,omitempty"` // library kind of ItemID (the link)
	// Path: the page the letter links to when there is no library item
	// (a server path, "/..."; "" none).
	Path    string `json:"path,omitempty"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
	// EpisodeTitle: what an episode without numbers is listed by.
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	// Poster: a remote https TMDB image ("" none).
	Poster string `json:"poster,omitempty"`
	// Status: done | failed (downloads), approved | in_library (requests).
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"` // why a download failed
}

// tmdbImageHost: the only pictures a letter shows.
const tmdbImageHost = "https://image.tmdb.org/"

// RemotePoster is p when it is a TMDB image over https, else "".
func RemotePoster(p string) string {
	if strings.HasPrefix(p, tmdbImageHost) && !strings.ContainsAny(p, "\"'<> \r\n") {
		return p
	}
	return ""
}

// posterOf is the remote poster kept in an item's meta_json (poster_url).
func posterOf(metaJSON string) string {
	var m struct {
		PosterURL string `json:"poster_url"`
	}
	_ = json.Unmarshal([]byte(metaJSON), &m)
	return RemotePoster(m.PosterURL)
}

// Enqueue keeps e for the profile's next digest when the profile has an
// address and e's topic on; an event already known by its key is skipped.
// It reports whether e was queued.
func (s *Service) Enqueue(ctx context.Context, e Event) (bool, error) {
	if s == nil {
		return false, nil
	}
	p, err := s.Prefs(ctx, e.UserID)
	if err != nil {
		return false, err
	}
	if p.Email == "" || !p.On(e.Topic) {
		return false, nil
	}
	return s.insert(ctx, e)
}

func (s *Service) insert(ctx context.Context, e Event) (bool, error) {
	e.Data.Poster = RemotePoster(e.Data.Poster)
	b, err := json.Marshal(e.Data)
	if err != nil {
		return false, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO notify_events(user_id, topic, key, item_id, group_id, data_json, created_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id, key) DO NOTHING`,
		e.UserID, e.Topic, e.Key, e.ItemID, e.GroupID, string(b), s.now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// EnqueueAdmins queues e for every admin profile that has its topic on.
func (s *Service) EnqueueAdmins(ctx context.Context, e Event) error {
	if s == nil {
		return nil
	}
	want, err := s.wanting(ctx, e.Topic)
	if err != nil || len(want) == 0 {
		return err
	}
	for uid := range want {
		u, err := s.Users.Get(ctx, uid)
		if err != nil || !u.IsAdmin {
			continue
		}
		e.UserID = uid
		if _, err := s.insert(ctx, e); err != nil {
			return err
		}
	}
	return nil
}
