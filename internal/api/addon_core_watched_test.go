package api

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/progress"
)

// GET /watched: of the items and series asked about, the ones the profile
// watched (a series: every episode); another profile's marks never show.
func TestAddonCoreWatched(t *testing.T) {
	c := newCoreLib(t)
	ctx := context.Background()
	var admin, masha int64
	c.db.QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&admin)
	c.db.QueryRow(`SELECT id FROM users WHERE name='Маша'`).Scan(&masha)
	prog := progress.New(c.db)
	rows, err := c.db.Query(`SELECT e.id FROM items e JOIN items se ON se.id=e.parent_id WHERE se.parent_id=?`, c.show)
	if err != nil {
		t.Fatal(err)
	}
	var eps []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		eps = append(eps, id)
	}
	rows.Close()
	if len(eps) != 2 {
		t.Fatalf("episodes %v", eps)
	}
	for _, id := range append([]int64{c.movie}, eps...) {
		if err := prog.MarkWatched(ctx, masha, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := prog.MarkWatched(ctx, admin, c.book, true); err != nil {
		t.Fatal(err)
	}
	q := func(user int64, items, series []int64) string {
		s := func(ids []int64) string {
			var p []string
			for _, id := range ids {
				p = append(p, strconv.FormatInt(id, 10))
			}
			return strings.Join(p, ",")
		}
		return "/watched?userId=" + strconv.FormatInt(user, 10) + "&items=" + s(items) + "&series=" + s(series)
	}
	var out struct {
		Watched map[string]bool `json:"watched"`
	}
	c.get("tok-lib", q(masha, []int64{c.movie, c.book, c.album}, []int64{c.show}), 200, &out)
	if len(out.Watched) != 2 || !out.Watched[strconv.FormatInt(c.movie, 10)] || !out.Watched[strconv.FormatInt(c.show, 10)] {
		t.Fatalf("masha %v", out.Watched)
	}
	out.Watched = nil
	c.get("tok-lib", q(admin, []int64{c.movie, c.book}, []int64{c.show}), 200, &out)
	if len(out.Watched) != 1 || !out.Watched[strconv.FormatInt(c.book, 10)] {
		t.Fatalf("admin %v", out.Watched)
	}
	if body := c.get("tok-lib", q(masha, nil, nil), 200, nil); strings.TrimSpace(body) != `{"watched":{}}` {
		t.Fatalf("nothing asked: %s", body)
	}
	many := make([]int64, 501)
	for i := range many {
		many[i] = int64(i + 1)
	}
	c.get("tok-lib", q(masha, many, nil), 400, nil)
	c.get("tok-lib", q(masha, nil, many), 400, nil)
	c.get("tok-lib", q(masha, many[:500], many[:500]), 200, nil)
	c.get("tok-lib", "/watched?items=1", 400, nil)
	c.get("tok-lib", q(999999, []int64{c.movie}, nil), 400, nil)
	c.get("tok-lib", "/watched?userId="+strconv.FormatInt(masha, 10)+"&items=x", 400, nil)
	c.get("tok-ppl", q(masha, []int64{c.movie}, nil), 403, nil)
}

// GET /users: each profile's «Языки»: its own audio languages, else the
// household's.
func TestAddonCoreUsers_Languages(t *testing.T) {
	c := newCoreLib(t)
	var masha, vasya int64
	c.db.QueryRow(`SELECT id FROM users WHERE name='Маша'`).Scan(&masha)
	c.db.QueryRow(`SELECT id FROM users WHERE name='Вася'`).Scan(&vasya)
	if _, err := c.db.Exec(`INSERT INTO playback_prefs(user_id, audio_langs) VALUES(?, 'en, ru')`, masha); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO playback_prefs(user_id, audio_langs) VALUES(?, '')`, vasya); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO settings(key, value) VALUES(?, '{"languages":["ru","uk"]}')`, appSettingsKey); err != nil {
		t.Fatal(err)
	}
	langs := func() map[string]string {
		t.Helper()
		var users []struct {
			Name      string    `json:"name"`
			Languages *[]string `json:"languages"`
		}
		c.get("tok-ppl", "/users", 200, &users)
		m := map[string]string{}
		for _, u := range users {
			if u.Languages == nil {
				t.Fatalf("%s: no languages", u.Name)
			}
			m[u.Name] = strings.Join(*u.Languages, ",")
		}
		return m
	}
	m := langs()
	if m["Маша"] != "en,ru" || m["Вася"] != "ru,uk" || len(m) != 3 {
		t.Fatalf("languages %v", m)
	}
	for n, l := range m {
		if n != "Маша" && l != "ru,uk" {
			t.Fatalf("%s: %q, want the household's", n, l)
		}
	}
	// an empty household list is the default one (LanguagesOrDefault):
	// [] never reaches a profile while the server has a database
	if _, err := c.db.Exec(`UPDATE settings SET value='{"languages":[]}' WHERE key=?`, appSettingsKey); err != nil {
		t.Fatal(err)
	}
	if m := langs(); m["Вася"] != "ru,en" || m["Маша"] != "en,ru" {
		t.Fatalf("default household %v", m)
	}
}
