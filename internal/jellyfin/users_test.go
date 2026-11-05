package jellyfin

import (
	"context"
	"strings"
	"testing"
)

func TestUsersPublicEmptyRemote(t *testing.T) {
	e := newEnv(t)
	if w := e.remote("GET", "/Users/Public", nil, nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("remote: %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/Users/Public", nil, nil)
	var list []struct {
		Name, Id, ServerId                 string
		HasPassword, HasConfiguredPassword bool
	}
	decode(t, w, &list)
	if len(list) != 3 {
		t.Fatalf("%s", w.Body)
	}
	for i, u := range []string{"Аня", "Миша", "Гость"} {
		if list[i].Name != u || !hex32.MatchString(list[i].Id) || list[i].ServerId == "" || !list[i].HasPassword || !list[i].HasConfiguredPassword {
			t.Fatalf("%d: %+v", i, list[i])
		}
	}
	if list[1].Id != UserID(e.misha.ID) {
		t.Fatalf("ids: %+v", list)
	}
}

func TestLogoutRevokes(t *testing.T) {
	e := newEnv(t)
	tok := e.login("Аня", e.anyaApp)
	if w := e.do("GET", "/Users/Me", swiftfinHdr(tok), nil); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if w := e.do("POST", "/Sessions/Logout", swiftfinHdr(tok), nil); w.Code != 204 {
		t.Fatalf("logout %d", w.Code)
	}
	if w := e.do("GET", "/Users/Me", swiftfinHdr(tok), nil); w.Code != 401 || w.Body.Len() != 0 {
		t.Fatalf("after logout %d", w.Code)
	}
	// a new app password ends the tokens too
	tok = e.login("Аня", e.anyaApp)
	if _, err := e.users.SetAppPassword(context.Background(), e.anya.ID); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/Users/Me", map[string]string{"X-Emby-Token": tok}, nil); w.Code != 401 {
		t.Fatalf("after a new app password %d", w.Code)
	}
}

func TestOtherUserIDForbidden(t *testing.T) {
	e := newEnv(t)
	tok := e.login("Аня", e.anyaApp)
	h := swiftfinHdr(tok)
	other := UserID(e.misha.ID)
	for _, p := range []string{"/Users/" + other + "/Views", "/Users/" + other, "/UserViews?userId=" + other, "/UserViews?UserId=nonsense",
		"/DisplayPreferences/usersettings?userId=" + other} {
		if w := e.do("GET", p, h, nil); w.Code != 403 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	own := UserID(e.anya.ID)
	dashed := strings.ToUpper(own[:8] + "-" + own[8:12] + "-" + own[12:16] + "-" + own[16:20] + "-" + own[20:])
	for _, p := range []string{"/Users/" + own + "/Views", "/Users/" + dashed, "/UserViews?userId=" + own, "/UserViews"} {
		if w := e.do("GET", p, h, nil); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestUnauthorizedEmptyBody(t *testing.T) {
	e := newEnv(t)
	for _, h := range []map[string]string{nil, {"X-Emby-Token": "0123456789abcdef0123456789abcdef"}, swiftfinHdr("bogus")} { // gitleaks:allow
		if w := e.do("GET", "/Users/Me", h, nil); w.Code != 401 || w.Body.Len() != 0 {
			t.Fatalf("%v: %d %q", h, w.Code, w.Body)
		}
	}
}

func TestUnknownRouteLogged(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		if w := e.do("GET", "/Items/x/Unknown?ApiKey=secret-token", nil, nil); w.Code != 404 || w.Body.Len() != 0 {
			t.Fatalf("%d %q", w.Code, w.Body)
		}
	}
	logs := e.logs.String()
	if n := strings.Count(logs, "jellyfin: unknown route"); n != 1 {
		t.Fatalf("%d lines: %s", n, logs)
	}
	if !strings.Contains(logs, "/items/x/unknown") || !strings.Contains(logs, "method=GET") || strings.Contains(logs, "secret-token") {
		t.Fatalf("%s", logs)
	}
}

func TestCaseInsensitiveRoutes(t *testing.T) {
	e := newEnv(t)
	tok := e.login("Аня", e.anyaApp)
	for _, p := range []string{"/users/me", "/USERS/ME", "/Users/Me"} {
		if w := e.do("GET", p, swiftfinHdr(tok), nil); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestSessionsAndDisplayPreferences(t *testing.T) {
	e := newEnv(t)
	tok := e.login("Аня", e.anyaApp)
	h := swiftfinHdr(tok)
	var sessions []struct{ DeviceId, UserId, Client, Id string }
	decode(t, e.do("GET", "/Sessions?deviceId=3F2B-iphone", h, nil), &sessions)
	if len(sessions) != 1 || sessions[0].DeviceId != "3F2B-iphone" || sessions[0].UserId != UserID(e.anya.ID) ||
		sessions[0].Id == "" || strings.Contains(sessions[0].Id, tok) {
		t.Fatalf("%+v", sessions)
	}
	if w := e.do("GET", "/Sessions?deviceId=other", h, nil); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("other device: %s", w.Body)
	}
	w := e.do("GET", "/DisplayPreferences/usersettings?client=emby", h, nil)
	if w.Code != 200 || w.Body.String() != `{"Id":"usersettings","CustomPrefs":{},"SortBy":"SortName","SortOrder":"Ascending"}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/DisplayPreferences/usersettings", "/Sessions/Capabilities", "/Sessions/Capabilities/Full"} {
		if w := e.do("POST", p, h, `{}`); w.Code != 204 {
			t.Fatalf("POST %s: %d", p, w.Code)
		}
	}
}
