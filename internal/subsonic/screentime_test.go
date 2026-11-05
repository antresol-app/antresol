package subsonic

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/screentime"
)

// A kid's music on Subsonic: free until the audio limit is set; then
// stream and download answer error 50 with the kid's message. A stream's
// start counts the track.
func TestScreenTimeSubsonic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	st := &screentime.Service{DB: e.db, Now: func() time.Time { return now }, Loc: time.UTC}
	e.srv.ScreenTime = st
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Вася", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, tracks := e.album("Кино", "Звезда", 1988, "Рок", tTrack{1, "Звезда"}, tTrack{2, "Война"})
	// 120 s each at 1000 B/s: what is sent is what plays
	for _, id := range tracks {
		tr, _ := e.repo.TrackByID(ctx, id)
		f, _ := e.repo.FileByID(ctx, tr.FileID)
		if err := os.WriteFile(f.Path, make([]byte, 120_000), 0o644); err != nil {
			t.Fatal(err)
		}
		e.db.Exec(`UPDATE files SET size=120000 WHERE id=?`, tr.FileID)
	}
	sum := md5.Sum([]byte(app + "abc123"))
	call := func(method string, id int64) map[string]any {
		p := url.Values{"u": {kid.Name}, "t": {hex.EncodeToString(sum[:])}, "s": {"abc123"}, "c": {"test"}, "v": {"1.16.1"},
			"f": {"json"}, "id": {itoa(id)}, "format": {"raw"}}
		w := e.req(method, "", p)
		if w.Header().Get("Content-Type") == "application/json" || w.Header().Get("Content-Type") == "application/json; charset=utf-8" {
			return e.decode(w)
		}
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body)
		}
		return nil
	}
	if r := call("stream", tracks[0]); r != nil {
		t.Fatalf("free: %v", r)
	}
	now = now.Add(30 * time.Second) // skipped to the next track after 30 s
	if r := call("stream", tracks[1]); r != nil {
		t.Fatalf("free: %v", r)
	}
	if err := st.SetLimits(ctx, kid.ID, screentime.Limits{AudioMin: new(int)}); err != nil {
		t.Fatal(err)
	}
	// the first track up to the skip (30 s), the second just started
	s, _ := st.Status(ctx, kid)
	if s.Audio == nil || s.Audio.UsedSec != 30 {
		t.Fatalf("%+v", s.Audio)
	}
	if code, msg := errCode(call("stream", tracks[0])); code != codeForbidden || msg != "Время на сегодня закончилось. Завтра можно снова!" {
		t.Fatalf("stream: %d %q", code, msg)
	}
	// no files to keep (a client's cache) while music is limited
	if code, msg := errCode(call("download", tracks[0])); code != codeForbidden || msg != "Скачивание выключено родителем" {
		t.Fatalf("download: %d %q", code, msg)
	}
	// the admin listens on
	p := e.creds()
	p.Set("id", itoa(tracks[0]))
	p.Set("format", "raw")
	if w := e.req("stream", "", p); w.Code != 200 || w.Header().Get("Content-Type") == "application/json" {
		t.Fatalf("admin %d", w.Code)
	}
}
