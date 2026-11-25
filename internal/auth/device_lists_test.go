package auth

import (
	"context"
	"testing"
	"time"
)

// The admin's device lists: the apps once per profile and device, the
// browsers grouped and cut by age, the IPTV-style tokens of one protocol.
func TestDeviceLists(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	if _, err := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "tv-00001", Name: "Apple TV", Model: "AppleTV11,1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateBrowser(ctx, a.ID, "Safari", "macOS"); err != nil {
		t.Fatal(err)
	}
	old, err := ss.CreateBrowser(ctx, a.ID, "Firefox", "Linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE sessions SET last_seen='2020-01-01T00:00:00Z' WHERE token=?`, TokenHash(old)); err != nil {
		t.Fatal(err)
	}
	apps, err := ss.NativeDevices(ctx)
	if err != nil || len(apps) != 1 || apps[0].UserID != a.ID || apps[0].Profile != "Papa" || apps[0].DeviceID != "tv-00001" ||
		apps[0].Name != "Apple TV" || apps[0].Model != "AppleTV11,1" || apps[0].LastSeen.IsZero() {
		t.Fatalf("apps %+v %v", apps, err)
	}
	web, err := ss.RecentBrowsers(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || len(web) != 1 || web[0].Name != "Safari" || web[0].Model != "macOS" || web[0].DeviceID != "" || web[0].LastSeen.IsZero() {
		t.Fatalf("browsers %+v %v", web, err)
	}
	if all, _ := ss.RecentBrowsers(ctx, time.Time{}); len(all) != 2 {
		t.Fatalf("browsers since ever: %+v", all)
	}

	tk := NewAppTokens(d)
	tok, err := tk.Issue(ctx, a.ID, "iptv", Device{ID: "link-1", Name: "Plex"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Issue(ctx, a.ID, "jellyfin", Device{ID: "jf-1", Name: "Infuse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE app_tokens SET set_id=NULL WHERE token=?`, tok); err != nil {
		t.Fatal(err)
	}
	links, err := tk.ByProto(ctx, "iptv")
	if err != nil || len(links) != 1 || links[0].Token != tok || links[0].Profile != "Papa" || links[0].Device != "Plex" || links[0].SetID != 0 || links[0].LastSeen.IsZero() {
		t.Fatalf("tokens %+v %v", links, err)
	}
}
