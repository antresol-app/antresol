package jellyfin

import (
	"net/http/httptest"
	"testing"
)

func parse(hdr map[string]string, target string) ClientInfo {
	if target == "" {
		target = "/Users/Me"
	}
	r := httptest.NewRequest("GET", target, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return ParseAuth(r)
}

func TestParseAuthSwiftfinUnquoted(t *testing.T) {
	c := parse(map[string]string{"Authorization": "MediaBrowser DeviceId=X, Device=iPhone, Client=Swiftfin iOS, Version=1.3, Token=abc"}, "")
	want := ClientInfo{Client: "Swiftfin iOS", Device: "iPhone", DeviceID: "X", Version: "1.3", Token: "abc"}
	if c != want {
		t.Fatalf("%+v", c)
	}
}

func TestParseAuthInfuseQuoted(t *testing.T) {
	c := parse(map[string]string{"X-Emby-Authorization": `MediaBrowser Client="Infuse-Direct", Device="Apple TV", DeviceId="ab", Version="8.5.6", Token="t1"`}, "")
	want := ClientInfo{Client: "Infuse-Direct", Device: "Apple TV", DeviceID: "ab", Version: "8.5.6", Token: "t1"}
	if c != want {
		t.Fatalf("%+v", c)
	}
	c = parse(map[string]string{"X-Emby-Authorization": `Emby UserId="", Client="Infuse", Device="Mac, M1", DeviceId="d"`}, "")
	if c.Client != "Infuse" || c.Device != "Mac, M1" || c.DeviceID != "d" || c.Token != "" {
		t.Fatalf("emby scheme, comma in quotes: %+v", c)
	}
	if c := parse(map[string]string{"Authorization": "Basic YTpi"}, ""); c != (ClientInfo{}) {
		t.Fatalf("other scheme: %+v", c)
	}
}

func TestParseAuthMissingClient(t *testing.T) {
	c := parse(map[string]string{"X-Emby-Token": "t"}, "")
	if c.Token != "t" || c.Client != "" || c.DeviceID != "" {
		t.Fatalf("%+v", c)
	}
	if c := parse(map[string]string{"X-MediaBrowser-Token": "m"}, ""); c.Token != "m" {
		t.Fatalf("%+v", c)
	}
	// a header token without Client
	if c := parse(map[string]string{"Authorization": "MediaBrowser Token=only"}, ""); c.Token != "only" || c.Client != "" {
		t.Fatalf("%+v", c)
	}
}

func TestParseAuthQueryApiKey(t *testing.T) {
	for _, q := range []string{"?ApiKey=t", "?api_key=t", "?APIKEY=t", "?static=true&Api_Key=t"} {
		if c := parse(nil, "/Videos/1/stream"+q); c.Token != "t" {
			t.Fatalf("%s: %+v", q, c)
		}
	}
	// the header wins over the query
	if c := parse(map[string]string{"X-Emby-Token": "h"}, "/x?ApiKey=q"); c.Token != "h" {
		t.Fatalf("%+v", c)
	}
}

func TestParseAuthURLEncoded(t *testing.T) {
	c := parse(map[string]string{"Authorization": "MediaBrowser Client=Swiftfin%20iOS, Device=Apple%20TV, DeviceId=a%2Cb, Version=1.3"}, "")
	if c.Device != "Apple TV" || c.Client != "Swiftfin iOS" || c.DeviceID != "a,b" {
		t.Fatalf("%+v", c)
	}
}
