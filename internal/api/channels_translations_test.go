package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// guideTranslator is TMDB's text of movie 106646 and series 90790 in
// Russian and English; Peek and Has serve only what Get fetched.
type guideTranslator struct {
	mu    sync.Mutex
	cache map[string]tmdb.Translation
	gets  []string // "kind/id/tag/seasons"
}

func (f *guideTranslator) Language() string { return "ru-RU" }

func guideText(kind string, id int, tag string) (tmdb.Translation, bool) {
	ru := tag == "ru-RU"
	switch {
	case kind == "movie" && id == 106646 && ru:
		return tmdb.Translation{Title: "Волк с Уолл-стрит", Overview: "Брокер"}, true
	case kind == "movie" && id == 106646:
		return tmdb.Translation{Title: "The Wolf of Wall Street", Overview: "A broker"}, true
	case kind == "tv" && id == 90790 && ru:
		return tmdb.Translation{Title: "Семь миров, одна планета", Seasons: map[int][]tmdb.EpisodeText{1: {{Number: 6, Name: "Города", Overview: "О городах"}}}}, true
	case kind == "tv" && id == 90790:
		return tmdb.Translation{Title: "Seven Worlds, One Planet", Seasons: map[int][]tmdb.EpisodeText{1: {{Number: 6, Name: "Cities"}}}}, true
	}
	return tmdb.Translation{}, false
}

func (f *guideTranslator) key(kind string, id int, tag string) string {
	return fmt.Sprintf("%s/%d/%s", kind, id, tag)
}

func (f *guideTranslator) Peek(_ context.Context, kind string, id int, tag string) (tmdb.Translation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.cache[f.key(kind, id, tag)]
	return t, ok
}

func (f *guideTranslator) Has(_ context.Context, kind string, id int, tag string, seasons []int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.cache[f.key(kind, id, tag)]
	for _, s := range seasons {
		ok = ok && slices.Contains(t.Covers, s)
	}
	return ok
}

func (f *guideTranslator) Get(_ context.Context, kind string, id int, tag string, seasons []int) (tmdb.Translation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, fmt.Sprintf("%s/%v", f.key(kind, id, tag), seasons))
	t, ok := guideText(kind, id, tag)
	if !ok {
		return t, false
	}
	t.Covers = seasons
	if f.cache == nil {
		f.cache = map[string]tmdb.Translation{}
	}
	f.cache[f.key(kind, id, tag)] = t
	return t, true
}

func (f *guideTranslator) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.gets)
}

// guideEnv: a channel of an English-stored movie (in a Russian-metadata
// library: its title is the file's) and a series' episode, the guide's
// translator tr (nil: no TMDB key).
func guideEnv(t *testing.T, tr Translator) (e *libEnv, svc *channels.Service, movie int64, chans [2]string) {
	t.Helper()
	e = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = channels.New(db, nil)
		d.Channels, d.DB, d.IPTV, d.Translations = svc, db, true, tr
	})
	m, _ := e.movie("The Wolf of Wall Street", "18+", "w")
	ctx := context.Background()
	ep, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Seven Worlds, One Planet", SeriesTmdbID: 90790, Season: 1, Episode: 6,
		Title: "Cities", Overview: "Cities overview", File: library.FileInput{Path: e.media + "/s.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	epi, _ := e.repo.Item(ctx, ep)
	season, _ := e.repo.Item(ctx, epi.ParentID)
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=106646, overview='A New York stockbroker' WHERE id=?`, m); err != nil {
		t.Fatal(err)
	}
	e.withDuration()
	for i, c := range []string{
		`{"name":"Кино","rules":{"titles":[` + itoa(m) + `]}}`,
		`{"name":"Природа","rules":{"titles":[` + itoa(season.ParentID) + `]}}`,
	} {
		w := e.do("POST", "/api/channels", c, e.admin)
		var j channelJSON
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &j) != nil {
			t.Fatalf("create %d %s", w.Code, w.Body)
		}
		chans[i] = itoa(j.ID)
	}
	return e, svc, m, chans
}

// warmGuide runs the service until the warmer fetched n texts.
func warmGuide(t *testing.T, svc *channels.Service, f *guideTranslator, n int) {
	t.Helper()
	old := warmPause
	warmPause = time.Millisecond
	t.Cleanup(func() { warmPause = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, time.Hour) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); len(f.fetched()) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("warmed %v", f.fetched())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func guideTitles(t *testing.T, e *libEnv, path, lang string) string {
	t.Helper()
	w := e.do("GET", path, "", e.admin, "Accept-Language", lang)
	if w.Code != 200 {
		t.Fatalf("%s %d %s", path, w.Code, w.Body)
	}
	return w.Body.String()
}

// The warmer fetches the text of every movie and series airing, in each
// interface language, with the seasons of the episodes scheduled; then
// the list (now/next), a channel's guide and the grid show it in the
// viewer's language, the Russian one too although Russian is the
// metadata language (the stored title is the file's).
func TestChannelGuideInTheViewersLanguage(t *testing.T) {
	f := &guideTranslator{}
	e, svc, _, ch := guideEnv(t, f)
	if body := guideTitles(t, e, "/api/channels", "ru"); !strings.Contains(body, `"title":"The Wolf of Wall Street"`) {
		t.Fatalf("before warming: the stored text %s", body)
	}
	warmGuide(t, svc, f, 4)
	got := f.fetched()
	slices.Sort(got)
	want := []string{"movie/106646/en-US/[]", "movie/106646/ru-RU/[]", "tv/90790/en-US/[1]", "tv/90790/ru-RU/[1]"}
	if !slices.Equal(got, want) {
		t.Fatalf("warmed %v, want %v", got, want)
	}
	list := guideTitles(t, e, "/api/channels", "ru")
	for _, s := range []string{`"title":"Волк с Уолл-стрит"`, `"overview":"Брокер"`, `"title":"Семь миров, одна планета"`, `"subtitle":"S1 E6 · Города"`, `"overview":"О городах"`} {
		if !strings.Contains(list, s) {
			t.Fatalf("ru list lacks %s: %s", s, list)
		}
	}
	for _, path := range []string{"/api/channels/" + ch[0] + "/guide?hours=24", "/api/channels/epg?hours=24"} {
		if body := guideTitles(t, e, path, "ru"); !strings.Contains(body, `"title":"Волк с Уолл-стрит"`) {
			t.Fatalf("ru %s: %s", path, body)
		}
	}
	if body := guideTitles(t, e, "/api/channels/"+ch[1]+"/guide?hours=24", "ru"); !strings.Contains(body, `"subtitle":"S1 E6 · Города"`) {
		t.Fatalf("ru series guide: %s", body)
	}
	if body := guideTitles(t, e, "/api/channels", "en"); !strings.Contains(body, `"title":"The Wolf of Wall Street"`) || !strings.Contains(body, `"overview":"A broker"`) {
		t.Fatalf("en list: %s", body)
	}
}

// No TMDB key (no translator), or no text for the title: the stored text.
func TestChannelGuideWithoutTranslation(t *testing.T) {
	e, _, _, _ := guideEnv(t, nil)
	if body := guideTitles(t, e, "/api/channels", "ru"); !strings.Contains(body, `"title":"The Wolf of Wall Street"`) ||
		!strings.Contains(body, `"subtitle":"S1 E6 · Cities"`) || !strings.Contains(body, `"overview":"A New York stockbroker"`) {
		t.Fatalf("no translator: %s", body)
	}
	f := &guideTranslator{}
	e, svc, m, _ := guideEnv(t, f)
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=999 WHERE id=?`, m); err != nil { // a title TMDB has no text of
		t.Fatal(err)
	}
	warmGuide(t, svc, f, 4)
	if body := guideTitles(t, e, "/api/channels", "ru"); !strings.Contains(body, `"title":"The Wolf of Wall Street"`) ||
		!strings.Contains(body, `"overview":"A New York stockbroker"`) || !strings.Contains(body, `"subtitle":"S1 E6 · Города"`) {
		t.Fatalf("no text: %s", body)
	}
}

// The IPTV guide is in the link profile's interface language.
func TestIPTVGuideInTheProfilesLanguage(t *testing.T) {
	f := &guideTranslator{}
	e, svc, _, _ := guideEnv(t, f)
	warmGuide(t, svc, f, 4)
	w := e.do("GET", "/api/channels/iptv", "", e.admin)
	var l iptvLinks
	json.Unmarshal(w.Body.Bytes(), &l)
	guide := strings.TrimPrefix(l.Guide, "http://example.com")
	type doc struct {
		Programmes []struct {
			Title struct {
				Lang string `xml:"lang,attr"`
				Text string `xml:",chardata"`
			} `xml:"title"`
			SubTitle string `xml:"sub-title"`
			Desc     string `xml:"desc"`
		} `xml:"programme"`
	}
	read := func(lang string) doc {
		t.Helper()
		if w := e.do("PUT", "/api/me/ui-language", `{"language":"`+lang+`"}`, e.admin); w.Code != 200 {
			t.Fatalf("ui language %d %s", w.Code, w.Body)
		}
		// the player's own Accept-Language does not matter
		w := e.do("GET", guide, "", nil, "Accept-Language", "fr")
		var d doc
		if err := xml.Unmarshal(w.Body.Bytes(), &d); err != nil || len(d.Programmes) == 0 {
			t.Fatalf("epg %v %s", err, w.Body)
		}
		return d
	}
	has := func(d doc, lang, title, sub, desc string) bool {
		for _, p := range d.Programmes {
			if p.Title.Lang == lang && p.Title.Text == title && (sub == "" || p.SubTitle == sub) && (desc == "" || p.Desc == desc) {
				return true
			}
		}
		return false
	}
	if d := read("ru"); !has(d, "ru", "Волк с Уолл-стрит", "", "Брокер") || !has(d, "ru", "Семь миров, одна планета", "S1 E6 · Города", "О городах") {
		t.Fatalf("ru: %+v", d)
	}
	if d := read("en"); !has(d, "en", "The Wolf of Wall Street", "", "A broker") || !has(d, "en", "Seven Worlds, One Planet", "S1 E6 · Cities", "") {
		t.Fatalf("en: %+v", d)
	}
}
