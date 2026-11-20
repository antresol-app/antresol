package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/iptv"
)

// The viewer's list carries each channel's categories and every category
// once with its count: the AI's topic and the group meaning the same topic
// are one category (named in the request's language), the sources when
// there are two.
func TestListCategories(t *testing.T) {
	e := newTVEnv(t)
	// what the AI helper answered before (iptv Categorize keeps it)
	if _, err := e.db.Exec(`INSERT INTO iptv_channel_ai (name_key, cats_json, lang) VALUES ('новости 1', '["news"]', 'ru')`); err != nil {
		t.Fatal(err)
	}
	e.addSource("П")
	w := e.do("GET", "/api/channels", "", e.admin)
	var l struct {
		Channels   []channelJSON
		Categories []categoryJSON
	}
	json.Unmarshal(w.Body.Bytes(), &l)
	var cats []string
	for _, c := range l.Categories {
		cats = append(cats, fmt.Sprintf("%s=%s/%s/%d", c.ID, c.Name, c.Kind, c.Count))
	}
	if got := strings.Join(cats, ","); got != "ai:news=Новости/ai/2,ai:movies=Кино/ai/1" {
		t.Fatalf("categories %s", got)
	}
	by := map[string][]string{}
	for _, c := range l.Channels {
		by[c.Name] = c.Categories
	}
	if fmt.Sprint(by["Новости 1"]) != "[ai:news]" || fmt.Sprint(by["Кино 1"]) != "[ai:movies]" {
		t.Fatalf("channels %v", by)
	}
	// two sources: a category each
	e.addSource("Второй")
	w = e.do("GET", "/api/channels", "", e.admin)
	if !strings.Contains(w.Body.String(), `"kind":"source"`) || !strings.Contains(w.Body.String(), `"name":"Второй"`) {
		t.Fatalf("sources %s", w.Body)
	}
}

// Categories of one count read as a person reads them: numbers by value
// («Группа 2» before «Группа 10»), case aside; a bigger one still first.
func TestCategoriesNaturalOrder(t *testing.T) {
	a := &channelsAPI{}
	r := httptest.NewRequest("GET", "/api/channels", nil)
	lists := [][]string{{"group:Группа 10"}, {"group:Группа 2"}, {"group:группа 1"}, {"group:Кино"}, {"group:Кино"}, {"group:Группа 1a"}}
	var got []string
	for _, c := range a.categoriesOf(r, lists, nil, nil) {
		got = append(got, c.Name)
	}
	if s := strings.Join(got, ","); s != "Кино,группа 1,Группа 1a,Группа 2,Группа 10" {
		t.Fatalf("order %s", s)
	}
}

// Spellings of one topic meet (the AI's, the groups' in two languages, an
// own generated channel's kind); an adult channel is in its category only,
// last; groups of no topic are cleaned, and only the MaxTags most common
// stay.
func TestCategoriesDedupeCapAdult(t *testing.T) {
	a := &channelsAPI{}
	r := httptest.NewRequest("GET", "/api/channels", nil)
	ext := func(name string, ai []string, groups ...string) []string {
		return extCats(iptv.ExtChannel{Channel: channels.Channel{Name: name}, AI: iptv.AIInfo{Cats: ai}, Groups: groups}, 1)
	}
	lists := [][]string{
		ext("A", []string{"movies"}, "Кино"),
		ext("B", nil, "RU | Фильмы HD"),
		ext("C", nil, "Movies"),
		ownCats(channels.Channel{AutoKey: "genre:комедия"}, "Кино по жанрам"),
		ext("D", []string{"news"}, "Кино"), // a topic of its own: the group's topic is added
		ext("Hot", nil, "XXX"),
		ext("E", []string{"adult"}, "Развлекательные"),
		ext("F", nil, "НОВОСТНЫЕ HD"),
		ext("G", nil, "Премиум"),
		ext("H", nil, "премиум"),
	}
	if got := fmt.Sprint(lists[1], lists[2], lists[3], lists[5], lists[6], lists[8]); got != "[ai:movies] [ai:movies] [own:auto ai:movies] [ai:adult] [ai:adult] [group:Премиум]" {
		t.Fatalf("tags %s", got)
	}
	for i := 0; i < 20; i++ { // twenty more groups of one channel each
		lists = append(lists, ext("x", nil, fmt.Sprintf("Группа %d", i)))
	}
	cats := a.categoriesOf(r, lists, nil, nil)
	var names []string
	tags := 0
	for _, c := range cats {
		names = append(names, fmt.Sprintf("%s/%d", c.ID, c.Count))
		if isTag(c.ID) {
			tags++
		}
	}
	if cats[0].ID != "own:auto" || cats[0].Kind != "own" || cats[0].Name != "Из медиатеки" {
		t.Fatalf("own first: %v", names)
	}
	cats = cats[1:]
	if tags != MaxTags || cats[0].ID != "ai:movies" || cats[0].Count != 5 || cats[1].ID != "ai:news" || cats[1].Count != 2 ||
		cats[2].ID != "group:Премиум" || cats[2].Count != 2 {
		t.Fatalf("categories %v", names)
	}
	if last := cats[len(cats)-1]; last.ID != "ai:adult" || !last.Exclusive || last.Count != 2 {
		t.Fatalf("adult not last: %v", names)
	}
	// a channel loses the tags that did not make it
	if l := lists[len(lists)-1]; len(l) != 0 {
		t.Fatalf("a dropped tag stays on its channel: %v", l)
	}
}

// Adult groups («Взрослые») put a channel in the adult category only, the
// admin's «not adult» on a group wins the AI; a channel's language is a
// category apart from the tags (no cap), shown when there are two or more;
// the library's generated channels are «Из медиатеки» (no tag of their
// group for a franchise).
func TestCategoriesAdultGroupsLangsOwn(t *testing.T) {
	a := &channelsAPI{}
	r := httptest.NewRequest("GET", "/api/channels", nil)
	ext := func(x iptv.ExtChannel) []string { return extCats(x, 1) }
	lists := [][]string{
		ext(iptv.ExtChannel{Channel: channels.Channel{Name: "Ночной"}, Groups: []string{"Взрослые"}, Lang: "ru"}),
		ext(iptv.ExtChannel{Channel: channels.Channel{Name: "Премиум"}, Adult: true, Groups: []string{"Кино"}}),
		ext(iptv.ExtChannel{Channel: channels.Channel{Name: "Вечер"}, NotAdult: true, AI: iptv.AIInfo{Cats: []string{"adult"}}, Groups: []string{"Взрослые"}, Lang: "ru"}),
		ext(iptv.ExtChannel{Channel: channels.Channel{Name: "Adult Swim"}, Groups: []string{"Мультфильмы"}, Lang: "en"}),
		ext(iptv.ExtChannel{Channel: channels.Channel{Name: "#dabeiTV HD DE"}, Groups: []string{"Кино"}, Lang: "de"}),
		ownCats(channels.Channel{AutoKey: "franchise:bond", Category: "Франшизы"}, "Франшизы"),
		ownCats(channels.Channel{Name: "Своё", Category: "Кино"}, "Кино"),
	}
	if got := fmt.Sprint(lists[0], lists[1], lists[5], lists[6]); got != "[ai:adult] [ai:adult] [own:auto] [ai:movies]" {
		t.Fatalf("adult/own %s", got)
	}
	if fmt.Sprint(lists[2]) == "[ai:adult]" || fmt.Sprint(lists[3]) == "[ai:adult]" {
		t.Fatalf("not adult: %v %v", lists[2], lists[3])
	}
	for i := 0; i < 20; i++ { // the tags fill their cap
		lists = append(lists, ext(iptv.ExtChannel{Channel: channels.Channel{Name: "x"}, Groups: []string{fmt.Sprintf("Группа %d", i)}, Lang: "de"}))
	}
	cats := a.categoriesOf(r, lists, nil, nil)
	byID := map[string]categoryJSON{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	if c := byID["lang:de"]; c.Kind != "lang" || c.Name != "Немецкие (DE)" || c.Count != 21 {
		t.Fatalf("lang:de %+v", c)
	}
	if c := byID["lang:en"]; c.Name != "Английские (EN)" {
		t.Fatalf("lang:en %+v", c)
	}
	if c := byID["own:auto"]; c.Kind != "own" || c.Count != 1 {
		t.Fatalf("own %+v", c)
	}
	if last := cats[len(cats)-1]; last.ID != "ai:adult" || last.Count != 2 {
		t.Fatalf("adult last: %+v", last)
	}
	if !slices.Contains(lists[len(lists)-1], "lang:de") {
		t.Fatalf("a language dropped by the cap: %v", lists[len(lists)-1])
	}
	// one language only: no language category
	one := [][]string{ext(iptv.ExtChannel{Channel: channels.Channel{Name: "A"}, Lang: "ru"}), ext(iptv.ExtChannel{Channel: channels.Channel{Name: "B"}, Lang: "ru"})}
	if cats := a.categoriesOf(r, one, nil, nil); len(cats) != 0 || len(one[0]) != 0 {
		t.Fatalf("lone language %+v %v", cats, one)
	}
}
