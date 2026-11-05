package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// TestHomeShelvesLanguage: /api/home titles its shelves in the request
// language; no Accept-Language keeps Russian.
func TestHomeShelvesLanguage(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.movie("Брат", "16+", "0123456789abcdef")
	if err := e.prog.Save(context.Background(), 1, id, 1000, 4000); err != nil {
		t.Fatal(err)
	}
	titles := func(hdr ...string) map[string]string {
		w := e.do("GET", "/api/home", "", e.admin, hdr...)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		out := map[string]string{}
		for _, s := range decodeJSON[shelvesOut](t, w.Body.Bytes()).Shelves {
			out[s.ID] = s.Title
		}
		return out
	}
	ru := map[string]string{"continue": "Продолжить", "recently-played": "Слушали недавно", "reading": "Читаю сейчас",
		"recent-movies": "Недавно добавленные фильмы", "recent-series": "Новые серии", "watchlist": "Хочу посмотреть"}
	en := map[string]string{"continue": "Continue Watching", "recently-played": "Recently Played", "reading": "Currently Reading",
		"recent-movies": "Recently Added Movies", "recent-series": "New Episodes", "watchlist": "Want to Watch"}
	if got := titles(); !reflect.DeepEqual(got, ru) {
		t.Errorf("no header: %v", got)
	}
	if got := titles("Accept-Language", "ru-RU,ru;q=0.9"); !reflect.DeepEqual(got, ru) {
		t.Errorf("ru: %v", got)
	}
	if got := titles("Accept-Language", "en"); !reflect.DeepEqual(got, en) {
		t.Errorf("en: %v", got)
	}
}

func TestBooksHomeEnglish(t *testing.T) {
	e := newLibEnv(t)
	e.audiobook("Война и мир", 1)
	e.comic("Берсерк", "", 2)
	w := e.do("GET", "/api/books/home", "", e.admin, "Accept-Language", "en")
	out := decodeJSON[shelvesOut](t, w.Body.Bytes())
	titles := map[string]string{}
	for _, s := range out.Shelves {
		titles[s.ID] = s.Title
		if s.ID == "comics" && (len(s.Cards) != 1 || s.Cards[0].Subtitle != "2 volumes") {
			t.Errorf("comic card %+v", s.Cards)
		}
	}
	if titles["audiobooks"] != "Audiobooks" || titles["comics"] != "Manga & Comics" {
		t.Errorf("%v", titles)
	}
}

// TestErrorBodyLanguage: a JSON error reads in the request language.
func TestErrorBodyLanguage(t *testing.T) {
	e := newLibEnv(t)
	w := e.do("GET", "/api/books?kind=movie", "", e.admin, "Accept-Language", "en")
	if w.Code != 400 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := decodeJSON[map[string]string](t, w.Body.Bytes())["error"]; got != "Unknown section" {
		t.Errorf("en: %q", got)
	}
	w = e.do("GET", "/api/books?kind=movie", "", e.admin)
	if got := decodeJSON[map[string]string](t, w.Body.Bytes())["error"]; got != "Неизвестный раздел" {
		t.Errorf("ru: %q", got)
	}
}

// TestBookChaptersLanguage: generic chapter titles («Глава 1») follow the
// request language.
func TestBookChaptersLanguage(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.audiobook("Анна Каренина", 2)
	chapters := func(hdr ...string) []string {
		w := e.do("GET", "/api/books/"+itoa(id), "", e.admin, hdr...)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var out []string
		for _, c := range decodeJSON[bookDetailOut](t, w.Body.Bytes()).Chapters {
			out = append(out, c.Title)
		}
		return out
	}
	if got := chapters("Accept-Language", "en-US,en;q=0.9"); !reflect.DeepEqual(got, []string{"Chapter 1", "Chapter 2"}) {
		t.Errorf("en: %v", got)
	}
	if got := chapters(); !reflect.DeepEqual(got, []string{"Глава 1", "Глава 2"}) {
		t.Errorf("ru: %v", got)
	}
}

// TestCardTextEnglish: card subtitles and badges the API builds.
func TestCardTextEnglish(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), i18n.EN)
	if got := episodeLabel(en, 2, 5); got != "Season 2, Episode 5" {
		t.Errorf("%q", got)
	}
	if got := episodeLabel(en, 0, 1); got != "Special 1" {
		t.Errorf("%q", got)
	}
	if got := episodeLabel(context.Background(), 2, 5); got != "Сезон 2, серия 5" {
		t.Errorf("%q", got)
	}
	if got := sizeBadge(i18n.EN, 3<<29); got != "1.5 GB" {
		t.Errorf("%q", got)
	}
	if got := sizeBadge(i18n.EN, 842<<20); got != "842 MB" {
		t.Errorf("%q", got)
	}
	p := media.ProbeResult{Video: &media.VideoStream{Height: 1080}, Audio: []media.AudioStream{{Lang: "rus"}, {Lang: "eng"}},
		Subs: []media.SubStream{{Lang: "eng"}}}
	if got, want := videoBadges(i18n.EN, []library.File{fileWithProbe(t, "/m/f.mkv", 1, p)}, nil), []string{"1080p", "Audio RU EN", "Subtitles EN"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
	for probe, want := range map[string]string{
		`{"Audio":[{"Codec":"flac","SampleRate":88200,"BitDepth":24}]}`:   "Lossless · FLAC 24-bit / 88.2 kHz",
		`{"Audio":[{"Codec":"mp3","SampleRate":44100,"BitRate":320000}]}`: "MP3 320 kbps",
	} {
		codec := "flac"
		if want == "MP3 320 kbps" {
			codec = "mp3"
		}
		if got := qualityLabel(i18n.EN, probe, codec, codec == "flac"); got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

// TestEventLocalized: display text of an event payload follows the
// subscriber's language; unknown text and numbers stay as they are.
func TestEventLocalized(t *testing.T) {
	in := []byte(`{"id":12345678901234567,"lastError":"Не найдено","title":"Брат","list":[{"status":"Файл не найден"}]}`)
	if got := string(localizeEvent(i18n.RU, in)); got != string(in) {
		t.Errorf("ru: %s", got)
	}
	want := `{"id":12345678901234567,"lastError":"Not found","list":[{"status":"File not found"}],"title":"Брат"}`
	if got := string(localizeEvent(i18n.EN, in)); got != want {
		t.Errorf("en: %s", got)
	}
	if got := string(localizeEvent(i18n.EN, []byte(`{"a":1}`))); got != `{"a":1}` {
		t.Errorf("plain: %s", got)
	}
}

// TestScanLabelsLocalized: titles the scanner stored in Russian read in
// the request language; real titles stay as they are.
func TestScanLabelsLocalized(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), i18n.EN)
	for in, want := range map[string]string{"Сезон 2": "Season 2", "Спецвыпуски": "Specials", "Серия 3": "Episode 3",
		"Трек 4": "Track 4", "Том 1 · Глава 5": "Volume 1 · Chapter 5", "Без альбома": "No Album",
		"Разные исполнители": "Various Artists", "Не найдено": "Не найдено", "Брат": "Брат"} {
		if got := scanLabel(en, in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
		if got := scanLabel(context.Background(), in); got != in {
			t.Errorf("ru %q: %q", in, got)
		}
	}
	r := httptest.NewRequest("GET", "/", nil).WithContext(en)
	if got := localizedCopy(r, media.CopyStatus{Error: "Не удалось подготовить копию"}).Error; got == "Не удалось подготовить копию" {
		t.Errorf("copy error %q", got)
	}
}

// TestPlaybackCapsLanguage: the decision's reasons are asked for in the
// request language.
func TestPlaybackCapsLanguage(t *testing.T) {
	h := &lib{Deps: Deps{ClientIsRemote: func(*http.Request) bool { return false }}}
	r := httptest.NewRequest("GET", "/", nil)
	if c, _ := h.playbackCaps(r.WithContext(i18n.WithLanguage(r.Context(), i18n.EN)), media.ClientCaps{}, qualityAuto); c.Lang != i18n.EN {
		t.Errorf("lang %q", c.Lang)
	}
}
