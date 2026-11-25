package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
)

// serviceGoldens records (-update) or checks the golden answers of the
// addon-core routes whose services TestAddonCoreGoldens' library env does
// not carry (file holds, inputs, notifications, requests, images), each
// over the env of its own handler tests. It returns the files' names.
func serviceGoldens(t *testing.T) []string {
	t.Helper()
	var names []string
	const v1 = "/api/addon-core/v1"
	rec := func(name, method, path string, w *httptest.ResponseRecorder, root string, scrub ...goldenScrub) {
		t.Helper()
		checkAddonGolden(t, name, method, path, v1+path, root, w, false, scrub...)
		names = append(names, name)
	}
	call := func(e *addonEnv, tok, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var b string
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			b = string(raw)
		}
		return e.do(method, v1+path, b, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	}

	// file holds: one hold on a pack below the addon's downloads root
	{
		c := newCoreHolds(t)
		base := filepath.Dir(c.dl)
		pack := filepath.Join(c.dl, "Pack")
		rec("holds-put", "PUT", "/files/holds",
			call(c.addonEnv, "tok-hd", "PUT", "/files/holds", map[string]any{"holds": []addonkit.Hold{c.hold("k-0001", pack, filepath.Join(pack, "a.mkv"))}}), base)
		rec("holds", "GET", "/files/holds", call(c.addonEnv, "tok-hd", "GET", "/files/holds", nil), base)
	}

	// an input registered for an adult, then dropped; its id is random
	{
		c := newCorePlay(t)
		input := goldenScrub{regexp.MustCompile(`"id":"[0-9a-f]{32}"`), `"id":"{input}"`}
		w := call(c.addonEnv, "tok-pl", "POST", "/play/inputs", addonkit.InputRequest{UserID: c.adultID, Path: "/media/1", Name: "Matrix/Matrix.mkv", Size: 1 << 20, ItemID: c.movie})
		var got addonkit.InputCreated
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.ID == "" {
			t.Fatalf("play input: %d %s", w.Code, w.Body)
		}
		rec("play-input", "POST", "/play/inputs", w, "", input)
		d := call(c.addonEnv, "tok-pl", "DELETE", "/play/inputs/"+got.ID, nil)
		checkAddonGolden(t, "play-input-drop", "DELETE", "/play/inputs/"+got.ID, v1+"/play/inputs/{input}", "", d, false)
		names = append(names, "play-input-drop")
	}

	// a notification to the admins' topic
	{
		c := newCoreNotify(t)
		c.prefs("Папа", "a@example.com", "downloads")
		rec("notify", "POST", "/notify", call(c.addonEnv, "tok-dl", "POST", "/notify",
			addonkit.NotifyRequest{Topic: "downloads", Key: "k-0001", Subject: addonkit.Text{RU: "Готово", EN: "Done"}, Link: "/item/3"}), "")
	}

	// a kid's album request, approved by what the admin started
	{
		e := newReqEnv(t, fakeRequests{}, nil)
		e.addon("dl", "Каталог", []string{"requests"})
		if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Группа крови","subtitle":"Кино"}`, e.kid("")); w.Code != 200 {
			t.Fatalf("ask: %d %s", w.Code, w.Body)
		}
		w := e.do("POST", v1+"/requests/approve", `{"adminId":1,"kind":"music","title":"Группа крови","artist":"Кино"}`, nil,
			"Authorization", "Bearer tok-dl", "Content-Type", "application/json")
		rec("requests-approve", "POST", "/requests/approve", w, "")
	}

	// an image fetched over https into the shared cache; its name hashes
	// the (random) server URL
	{
		c := newCoreWrite(t)
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("\x89PNG fake"))
		}))
		defer srv.Close()
		c.img.HTTP = srv.Client()
		u, _ := url.JoinPath(srv.URL, "p.png")
		rec("image", "POST", "/images", call(c.addonEnv, "tok-img", "POST", "/images", map[string]string{"url": u}), "",
			goldenScrub{regexp.MustCompile(`"name":"[^"]+"`), `"name":"{image}"`})
	}
	return names
}
