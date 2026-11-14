package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChannelTagsPreviewAndSuggest(t *testing.T) {
	e, _ := newChannelsEnv(t)
	su, _ := e.movie("Солярис", "12+", "a")
	fr, _ := e.movie("Амели", "16+", "b")
	e.movie("Без данных", "", "c")
	e.withDuration()
	set := func(id int64, meta string) {
		if _, err := e.db.Exec(`UPDATE items SET meta_json=?, year=? WHERE id=?`, meta, 1972, id); err != nil {
			t.Fatal(err)
		}
	}
	set(su, `{"genres":["Фантастика"],"countries":["SU"],"orig_lang":"ru","keywords":["space"]}`)
	set(fr, `{"genres":["Комедия"],"countries":["FR"],"orig_lang":"fr","keywords":["paris"]}`)

	w := e.do("GET", "/api/channels/options", "", e.admin)
	var o struct {
		Countries []tagCount `json:"countries"`
		Languages []tagCount `json:"languages"`
		YearMin   int        `json:"yearMin"`
	}
	json.Unmarshal(w.Body.Bytes(), &o)
	if w.Code != 200 || len(o.Countries) != 2 || len(o.Languages) != 2 || o.YearMin != 1972 {
		t.Fatalf("options %d %s", w.Code, w.Body)
	}

	w = e.do("POST", "/api/channels/preview", `{"rules":{"countries":["su"]}}`, e.admin)
	var p struct {
		Movies, Series int
		Items          []previewTitle
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.Movies != 1 || len(p.Items) != 1 || p.Items[0].Title != "Солярис" || !strings.HasPrefix(p.Items[0].Poster, "/api/images/") {
		t.Fatalf("preview %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/channels/preview", `{"rules":{}}`, e.admin)
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.Movies != 3 {
		t.Fatalf("preview all %s", w.Body)
	}

	w = e.do("POST", "/api/channels/suggest", `{"text":"создай канал который будет показывать советские фильмы"}`, e.admin)
	var s struct {
		Name  string
		Via   string
		Rules struct{ Countries, Kinds []string }
	}
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s.Name != "Советские фильмы" || s.Via != "words" || len(s.Rules.Countries) != 1 || s.Rules.Countries[0] != "SU" {
		t.Fatalf("suggest %d %s", w.Code, w.Body)
	}
	// a country the library does not have is left out (no empty channel)
	w = e.do("POST", "/api/channels/suggest", `{"text":"японские фильмы"}`, e.admin)
	if strings.Contains(w.Body.String(), `"JP"`) {
		t.Fatalf("suggest unknown %s", w.Body)
	}
	if w := e.do("POST", "/api/channels/suggest", `{"text":"  "}`, e.admin); w.Code != 400 {
		t.Fatalf("empty %d", w.Code)
	}
	// saved with the new tags
	if w := e.do("POST", "/api/channels", `{"name":"СССР","rules":{"countries":["su"],"keywords":["Space"]}}`, e.admin); w.Code != 201 || !strings.Contains(w.Body.String(), `"countries":["SU"]`) || !strings.Contains(w.Body.String(), `"keywords":["space"]`) {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
}
