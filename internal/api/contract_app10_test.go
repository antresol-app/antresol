package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// The App Store app build 1.0 decodes these answers: their shape is frozen
// in contract/app-1.0.json, which the maintainers derive from the app's
// captured fixtures (keys and types, no values) and its models (which keys
// it requires, which numbers it decodes as integers, which values a mode
// may take). A server change that removes a key the app requires, or
// retypes a key it reads, breaks the app people already have, so it fails
// here. A new key is fine.

type contractShape struct {
	Type     any                       `json:"type"` // a type name or a list of them
	Keys     map[string]*contractShape `json:"keys"`
	Required []string                  `json:"required"`
	Items    *contractShape            `json:"items"`
	Enum     []string                  `json:"enum"`
}

type contractEndpoint struct {
	Name   string        `json:"name"`
	Method string        `json:"method"`
	Path   string        `json:"path"`
	Shape  contractShape `json:"shape"`
}

func loadContract(t *testing.T) map[string]contractEndpoint {
	t.Helper()
	b, err := os.ReadFile("contract/app-1.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Endpoints []contractEndpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	m := map[string]contractEndpoint{}
	for _, e := range c.Endpoints {
		m[e.Name] = e
	}
	return m
}

// jsonType names v's JSON type; a whole number is "integer" (the app
// decodes ids and indexes as Int, a fraction there fails to decode).
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x := v.(float64); x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer" // a whole number; "number" takes it too
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func (s *contractShape) types() []string {
	switch t := s.Type.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// checkShape lists every way v breaks s (at path).
func checkShape(path string, s *contractShape, v any) []string {
	ts := s.types()
	if slices.Contains(ts, "any") {
		return nil
	}
	got := jsonType(v)
	if !slices.Contains(ts, got) && !(got == "integer" && slices.Contains(ts, "number")) {
		return []string{fmt.Sprintf("%s: %s, want %s", path, got, strings.Join(ts, "|"))}
	}
	var errs []string
	switch x := v.(type) {
	case string:
		if len(s.Enum) > 0 && !slices.Contains(s.Enum, x) {
			errs = append(errs, fmt.Sprintf("%s: %q is not one of %v", path, x, s.Enum))
		}
	case []any:
		if s.Items != nil {
			for i, e := range x {
				errs = append(errs, checkShape(fmt.Sprintf("%s[%d]", path, i), s.Items, e)...)
			}
		}
	case map[string]any:
		for _, k := range s.Required {
			if _, ok := x[k]; !ok {
				errs = append(errs, fmt.Sprintf("%s.%s: missing", path, k))
			}
		}
		for k, ks := range s.Keys {
			if e, ok := x[k]; ok {
				errs = append(errs, checkShape(path+"."+k, ks, e)...)
			}
		}
	}
	return errs
}

// The app's own capabilities (PlayCaps.apple(av1: false)), as build 1.0 sends them.
const app10Caps = `{"videoCodecs":["h264","hevc"],"audioCodecs":["aac","mp3","ac3","eac3","alac"],"containers":["mp4","mov"],"hevcNeedsHvc1":true}`

func app10Device(userID int64, password string) string {
	b, _ := json.Marshal(map[string]any{"userId": userID, "password": password,
		"device": map[string]string{"id": "contract-device-0001", "name": "Test Mac", "model": "Mac15,3", "version": "1.0.0"}})
	return string(b)
}

// TestApp10Contract starts a seeded core server (an administrator with a
// password, a passwordless kid profile, two films: one the app plays
// direct, one it gets as HLS) and asks it what build 1.0 asks, the way it
// asks: health, setup, profiles, auth/token, me, a card list and play.
func TestApp10Contract(t *testing.T) {
	contract := loadContract(t)
	e := newLibEnv(t) // the administrator «Папа», password "secret"
	kid := e.do("POST", "/api/users", `{"name":"Ребёнок","isKid":true,"maxRating":"12+"}`, e.admin)
	if kid.Code != 201 {
		t.Fatalf("kid %d %s", kid.Code, kid.Body)
	}
	_, direct := e.movie("Fixture Three", "", "0123456789")
	e.probeJSON(direct, `{"Container":"mp4","DurationMs":6000,"Video":{"Codec":"h264","Width":640,"Height":360,"BitDepth":8},
	 "Audio":[{"Index":1,"Codec":"aac","Lang":"und","Channels":1,"Default":true}]}`)
	_, remux := e.movie("Fixture Two", "", "0123456789")
	e.probeJSON(remux, `{"Container":"mkv","DurationMs":6021,"Video":{"Codec":"hevc","Width":640,"Height":360,"BitDepth":10},
	 "Audio":[{"Index":1,"Codec":"eac3","Lang":"rus","Channels":1,"Default":true},{"Index":2,"Codec":"aac","Lang":"eng","Channels":2}],
	 "Subs":[{"Index":3,"Codec":"subrip","Lang":"rus"}]}`)

	srv := httptest.NewServer(e.h) // a peer on loopback: at home, as the app on the sofa
	t.Cleanup(srv.Close)
	call := func(method, path, body, token string) any {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s %s: %v %s", method, path, err, b)
		}
		return v
	}
	check := func(name string, v any) {
		t.Helper()
		ep, ok := contract[name]
		if !ok {
			t.Fatalf("contract has no %q", name)
		}
		for _, msg := range checkShape(name, &ep.Shape, v) {
			t.Error(msg)
		}
	}
	path := func(name string) string { return contract[name].Path }

	check("health", call("GET", path("health"), "", ""))
	check("setup", call("GET", path("setup"), "", ""))
	profiles := call("GET", path("profiles"), "", "")
	check("profiles", profiles)
	var adminID, kidID int64
	for _, p := range profiles.([]any) {
		p := p.(map[string]any)
		if p["hasPassword"] == true {
			adminID = int64(p["id"].(float64))
		} else {
			kidID = int64(p["id"].(float64))
		}
	}
	if adminID == 0 || kidID == 0 {
		t.Fatalf("profiles %v", profiles)
	}
	token := func(v any) string { return v.(map[string]any)["token"].(string) }
	admin := call("POST", path("authToken"), app10Device(adminID, "secret"), "")
	check("authToken", admin)
	kidTok := call("POST", path("authToken"), app10Device(kidID, ""), "")
	check("authToken", kidTok)
	check("me", call("GET", path("me"), "", token(admin)))
	check("me", call("GET", path("me"), "", token(kidTok)))
	items := call("GET", path("items"), "", token(admin))
	check("items", items)
	if n := len(items.(map[string]any)["items"].([]any)); n != 2 {
		t.Fatalf("items: %d cards", n)
	}
	play := func(file int64, want string) {
		t.Helper()
		v := call("POST", strings.Replace(path("play"), "{fileId}", itoa(file), 1), `{"caps":`+app10Caps+`,"startSec":0,"quality":"auto"}`, token(admin))
		check("play", v)
		if m := v.(map[string]any)["mode"]; m != want {
			t.Errorf("play %d: mode %v, want %s", file, m, want)
		}
	}
	play(direct, "direct")
	play(remux, "remux")
}

// The check itself: a new key passes; a removed or retyped key, or a mode
// the app does not know, fails.
func TestApp10ContractCheck(t *testing.T) {
	contract := loadContract(t)
	me := contract["me"].Shape
	ok := func(js string) []string {
		var v any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			t.Fatal(err)
		}
		return checkShape("me", &me, v)
	}
	base := `"id":1,"name":"A","avatar":"grad-1","hasPassword":true,"isAdmin":true,"maxRating":null,"remote":false`
	if errs := ok(`{` + base + `,"isKid":false}`); len(errs) > 0 {
		t.Fatalf("fixture shape: %v", errs)
	}
	if errs := ok(`{` + base + `,"isKid":false,"newField":[1]}`); len(errs) > 0 {
		t.Fatalf("a new field failed: %v", errs)
	}
	if errs := ok(`{` + base + `,"maxRating":"12+","isKid":true}`); len(errs) > 0 {
		t.Fatalf("a kid's maxRating failed: %v", errs)
	}
	if errs := ok(`{` + base + `}`); len(errs) != 1 || !strings.Contains(errs[0], "isKid: missing") {
		t.Fatalf("removed isKid: %v", errs)
	}
	if errs := ok(`{` + base + `,"isKid":"no"}`); len(errs) != 1 {
		t.Fatalf("retyped isKid: %v", errs)
	}
	play := contract["play"].Shape
	var v map[string]any
	json.Unmarshal([]byte(`{"mode":"stream","url":"/x","audio":[],"audioIndex":1,"durationMs":1,"quality":{"selected":"auto","applied":"original","remote":false},"reason":"","startSec":0,"subtitles":[]}`), &v)
	if errs := checkShape("play", &play, v); len(errs) != 1 || !strings.Contains(errs[0], "stream") {
		t.Fatalf("unknown mode: %v", errs)
	}
	if got := play.Keys["mode"].Enum; !slices.Equal(got, []string{"direct", "remux", "transcode"}) {
		t.Fatalf("PlayMode %v", got)
	}
}

// Required and optional follow how build 1.0 decodes (its Swift models), not
// which keys one capture happened to hold: a card without a year (the server
// omits it), a text subtitle's url (omitted for image tracks) and keys the
// app never decodes (onServer) may be missing. The app decodes ids and
// indexes as Int: a fraction there fails, a whole number fits a "number".
func TestApp10ContractOptionality(t *testing.T) {
	contract := loadContract(t)
	check := func(name, js string) []string {
		t.Helper()
		var v any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			t.Fatal(err)
		}
		s := contract[name].Shape
		return checkShape(name, &s, v)
	}
	if errs := check("items", `{"items":[{"id":1,"kind":"movie","title":"A"}],"total":1}`); len(errs) > 0 {
		t.Errorf("a card without year/onServer/images: %v", errs)
	}
	if errs := check("items", `{"items":[{"kind":"movie","title":"A"}],"total":1}`); len(errs) != 1 || !strings.Contains(errs[0], "id: missing") {
		t.Errorf("a card without id: %v", errs)
	}
	if errs := check("items", `{"items":[{"id":1.5,"kind":"movie","title":"A","year":2000}],"total":1}`); len(errs) != 1 || !strings.Contains(errs[0], "integer") {
		t.Errorf("a fractional id: %v", errs)
	}
	if errs := check("items", `{"items":[{"id":1,"kind":"movie","title":"A","year":"2000"}],"total":1}`); len(errs) != 1 {
		t.Errorf("a string year: %v", errs)
	}
	sub := `{"index":3,"codec":"hdmv_pgs_subtitle","lang":"rus","title":"","forced":false,"default":false,"external":false,"burnIn":true}`
	if errs := check("play", `{"mode":"remux","url":"/api/hls/x/index.m3u8","subtitles":[`+sub+`]}`); len(errs) > 0 {
		t.Errorf("an image subtitle without url, play without the optional keys: %v", errs)
	}
	if errs := check("play", `{"mode":"remux","url":"/x","audioIndex":1.5}`); len(errs) != 1 || !strings.Contains(errs[0], "audioIndex") {
		t.Errorf("a fractional audioIndex: %v", errs)
	}
	if errs := check("play", `{"mode":"remux","url":"/x","startSec":12.5,"durationMs":6021}`); len(errs) > 0 {
		t.Errorf("a fractional startSec (Double): %v", errs)
	}
	if errs := check("me", `{"id":1,"name":"A","avatar":"grad-1","hasPassword":true,"isAdmin":true,"isKid":false,"remote":false}`); len(errs) > 0 {
		t.Errorf("me without maxRating (String?): %v", errs)
	}
	if got := contract["items"].Shape.Keys["items"].Items.Keys["id"].types(); !slices.Equal(got, []string{"integer"}) {
		t.Errorf("card id type %v, want integer", got)
	}
}

func TestJSONTypeInteger(t *testing.T) {
	num := &contractShape{Type: "number"}
	integer := &contractShape{Type: "integer"}
	if errs := checkShape("n", num, float64(3)); len(errs) > 0 {
		t.Errorf("3 is a number: %v", errs)
	}
	if errs := checkShape("n", integer, float64(3)); len(errs) > 0 {
		t.Errorf("3 is an integer: %v", errs)
	}
	if errs := checkShape("n", integer, 3.5); len(errs) != 1 {
		t.Errorf("3.5 is not an integer: %v", errs)
	}
	if errs := checkShape("n", integer, "3"); len(errs) != 1 {
		t.Errorf(`"3" is not an integer: %v`, errs)
	}
}
