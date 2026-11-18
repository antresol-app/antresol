package api

import (
	"encoding/json"
	"fmt"
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
