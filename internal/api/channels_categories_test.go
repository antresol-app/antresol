package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// The viewer's list carries each channel's categories and every category
// once with its count: the playlist's groups, the sources when there are
// two, the AI's (named in the request's language) first.
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
	if got := strings.Join(cats, ","); got != "ai:news=Новости/ai/1,group:Новости=Новости/group/2,group:Кино=Кино/group/1" {
		t.Fatalf("categories %s", got)
	}
	by := map[string][]string{}
	for _, c := range l.Channels {
		by[c.Name] = c.Categories
	}
	if fmt.Sprint(by["Новости 1"]) != "[ai:news group:Новости]" || fmt.Sprint(by["Кино 1"]) != "[group:Кино]" {
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
