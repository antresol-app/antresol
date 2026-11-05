package screentime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/i18n"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func ip(n int) *int                  { return &n }
func win(from, to string) *Window {
	f, _ := parseClock(from)
	t, _ := parseClock(to)
	return &Window{f, t}
}
func at(s string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", s, time.UTC); return t }
func secs(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// Monday 2026-11-02 is a weekday, Saturday 2026-11-07 a weekend day.
func newSvc(t *testing.T, start string) (*Service, *clock, auth.User) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := auth.NewStore(d)
	u, err := st.CreateUser(context.Background(), auth.NewUser{Name: "Вася", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{at(start)}
	return &Service{DB: d, Now: c.now, Loc: time.UTC}, c, u
}

func TestWindowJSONAndValidate(t *testing.T) {
	var l Limits
	if err := json.Unmarshal([]byte(`{"videoMin":60,"audioMin":null,"weekday":{"from":"08:00","to":"20:30"},"weekend":null,"dayStart":"05:00"}`), &l); err != nil {
		t.Fatal(err)
	}
	if *l.VideoMin != 60 || l.AudioMin != nil || l.Weekday.From != 480 || l.Weekday.To != 1230 || l.Weekend != nil || l.start() != 300 {
		t.Fatalf("%+v", l)
	}
	b, _ := json.Marshal(l)
	if string(b) != `{"videoMin":60,"audioMin":null,"weekday":{"from":"08:00","to":"20:30"},"weekend":null,"dayStart":"05:00"}` {
		t.Fatal(string(b))
	}
	for _, bad := range []Limits{{VideoMin: ip(-1)}, {AudioMin: ip(MaxMinutes + 1)}, {Weekday: win("08:00", "08:00")},
		{Weekday: win("02:00", "06:00")}, // crosses the 04:00 day start
		{DayStart: "25:00"}} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if err := (Limits{Weekday: win("20:00", "02:00")}).Validate(); err != nil {
		t.Fatalf("a window past midnight: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"weekday":{"from":"25:00","to":"20:30"}}`), &l); err == nil {
		t.Fatal("bad clock accepted")
	}
}

func TestNotKidNeverLimited(t *testing.T) {
	s, _, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{VideoMin: ip(0)}); err != nil {
		t.Fatal(err)
	}
	adult := u
	adult.IsKid = false
	if r := s.Allow(ctx, adult, Video); r != nil || s.Restricted(ctx, adult, "") {
		t.Fatal(r)
	}
	if r := s.Allow(ctx, u, Video); r == nil || r.Reason != "limit" || !s.Restricted(ctx, u, Video) || s.Restricted(ctx, u, Audio) {
		t.Fatal(r)
	}
	var nilSvc *Service
	if r := nilSvc.Allow(ctx, u, Video); r != nil || nilSvc.Restricted(ctx, u, "") {
		t.Fatal(r)
	}
	nilSvc.Signal(ctx, u, Signal{Category: Video, Key: "item:1"})
	nilSvc.Opened(ctx, u, Video, "item:1", 1)
}

func opened(s *Service, u auth.User, item int64) Signal {
	key := "item:" + string(rune('0'+item))
	s.Opened(context.Background(), u, Video, key, item)
	return Signal{Category: Video, Key: key, ItemID: item, Report: true}
}

// Reports every 10 s count their wall time; a pause stops the clock after
// one span; reads of a reporting stream do not count.
func TestReportsCountWallTime(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{VideoMin: ip(60)}); err != nil {
		t.Fatal(err)
	}
	sig := opened(s, u, 7)
	for i := 0; i < 31; i++ { // 0…300 s
		s.Signal(ctx, u, sig)
		// the player's buffer reads in between: ignored
		s.Signal(ctx, u, Signal{Category: Video, Key: sig.Key, ItemID: 7})
		c.add(10 * time.Second)
	}
	st, err := s.Status(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if st.Video.UsedSec < 300 || st.Video.UsedSec > 316 {
		t.Fatalf("used %d", st.Video.UsedSec)
	}
	c.add(30 * time.Minute) // a long pause adds the span only
	st, _ = s.Status(ctx, u)
	if st.Video.UsedSec < 300 || st.Video.UsedSec > 316 {
		t.Fatalf("after pause %d", st.Video.UsedSec)
	}
	if !st.Limited || !st.Video.Allowed || secs(st.Video.RemainingSec) != 3600-st.Video.UsedSec || st.Audio != nil {
		t.Fatalf("%+v", st)
	}
}

// Reports without a live session on the server (an offline copy's queue
// replayed, whatever the device's clock says) count nothing.
func TestReportsWithoutSessionIgnored(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		s.Signal(ctx, u, Signal{Category: Video, Key: "item:7", ItemID: 7, Report: true})
		c.add(10 * time.Second)
	}
	// a session long gone: its media went quiet past mediaFresh
	sig := opened(s, u, 8)
	c.add(mediaFresh + time.Minute)
	for i := 0; i < 10; i++ {
		s.Signal(ctx, u, sig)
		c.add(10 * time.Second)
	}
	if st, _ := s.Status(ctx, u); st.Video.UsedSec != 0 {
		t.Fatalf("used %d", st.Video.UsedSec)
	}
}

// A client that never reports is counted by its media reads.
func TestReadsCountWithoutReports(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	for i := 0; i < 13; i++ { // a segment every 5 s for 60 s
		s.Signal(ctx, u, Signal{Category: Video, Key: "item:7", ItemID: 7})
		c.add(5 * time.Second)
	}
	c.add(time.Hour)
	st, _ := s.Status(ctx, u)
	if st.Video.UsedSec < 60 || st.Video.UsedSec > 76 || st.Limited {
		t.Fatalf("%+v", st)
	}
}

// Two titles at once (two devices) count twice; the report lists what is
// playing now too.
func TestConcurrentStreamsEachCount(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	a, b := opened(s, u, 1), opened(s, u, 2)
	for i := 0; i < 7; i++ {
		s.Signal(ctx, u, a)
		s.Signal(ctx, u, b)
		c.add(10 * time.Second)
	}
	rep, err := s.Report(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	// still playing: the open spans are in the titles
	if len(rep.Titles) != 2 || rep.Titles[0].Seconds < 70 || rep.Titles[1].Seconds < 70 {
		t.Fatalf("%+v", rep.Titles)
	}
	if got := rep.Days[len(rep.Days)-1].VideoSec; got < 140 || got > 160 || rep.Days[len(rep.Days)-1].Day != "2026-11-02" {
		t.Fatalf("day %+v", rep.Days)
	}
	if rep.Limits.DayStart != "04:00" || len(rep.Days) != ReportDays {
		t.Fatalf("%+v", rep)
	}
}

func TestLimitBonusAndMessage(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{VideoMin: ip(1)}); err != nil {
		t.Fatal(err)
	}
	sig := opened(s, u, 1)
	for i := 0; i < 7; i++ {
		s.Signal(ctx, u, sig)
		c.add(10 * time.Second)
	}
	r := s.Allow(ctx, u, Video)
	if r == nil || r.Reason != "limit" || r.Message != "Время на сегодня закончилось. Завтра можно снова!" {
		t.Fatalf("%+v", r)
	}
	en := i18n.WithLanguage(ctx, i18n.EN)
	if r := s.Allow(en, u, Video); r == nil || r.Message != "That’s all for today. You can watch again tomorrow!" {
		t.Fatalf("%+v", r)
	}
	if r := s.Allow(ctx, u, Audio); r != nil { // audio is not limited without its own limit
		t.Fatal(r)
	}
	if err := s.AddBonus(ctx, u.ID, 30); err != nil {
		t.Fatal(err)
	}
	if r := s.Allow(ctx, u, Video); r != nil {
		t.Fatal(r)
	}
	st, _ := s.Status(ctx, u)
	if st.Video.BonusMin != 30 || secs(st.Video.RemainingSec) < 29*60 {
		t.Fatalf("%+v", st.Video)
	}
	if err := s.AddBonus(ctx, u.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// past midnight it is still the same screen day; at 04:00 a new one
	c.t = at("2026-11-03 02:00")
	if st, _ := s.Status(ctx, u); st.Day != "2026-11-02" || st.Video.BonusMin != 30 || st.Video.UsedSec < 60 {
		t.Fatalf("02:00 %+v", st)
	}
	c.t = at("2026-11-03 04:00")
	if st, _ := s.Status(ctx, u); st.Day != "2026-11-03" || st.Video.UsedSec != 0 || st.Video.BonusMin != 0 || !st.Video.Allowed {
		t.Fatalf("04:00 %+v", st)
	}
}

func TestAllowedHours(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 21:00") // Monday
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{Weekday: win("08:00", "20:30"), Weekend: win("09:00", "21:00")}); err != nil {
		t.Fatal(err)
	}
	r := s.Allow(ctx, u, Video)
	if r == nil || r.Reason != "hours" || r.Message != "Сейчас нельзя смотреть: с 20:30 до 08:00" {
		t.Fatalf("%+v", r)
	}
	c.t = at("2026-11-03 07:00") // Tuesday morning: since Monday's end
	if r := s.Allow(ctx, u, Video); r == nil || r.Message != "Сейчас нельзя смотреть: с 20:30 до 08:00" {
		t.Fatalf("%+v", r)
	}
	c.t = at("2026-11-06 22:00") // Friday night: until Saturday's start
	if r := s.Allow(ctx, u, Video); r == nil || r.Message != "Сейчас нельзя смотреть: с 20:30 до 09:00" {
		t.Fatalf("%+v", r)
	}
	c.t = at("2026-11-07 20:00") // Saturday: allowed, an hour left
	st, _ := s.Status(ctx, u)
	if !st.Video.Allowed || secs(st.Video.RemainingSec) != 3600 || st.Hours.From != 540 {
		t.Fatalf("%+v", st)
	}
	c.t = at("2026-11-02 23:00") // audio without its limit: never limited
	if r := s.Allow(ctx, u, Audio); r != nil {
		t.Fatal(r)
	}
	if err := s.SetLimits(ctx, u.ID, Limits{AudioMin: ip(120), Weekday: win("08:00", "20:30")}); err != nil {
		t.Fatal(err)
	}
	if r := s.Allow(ctx, u, Audio); r == nil || r.Message != "Сейчас нельзя слушать: с 20:30 до 08:00" {
		t.Fatalf("%+v", r)
	}
}

// Windows past midnight belong to the screen day they start on (it starts
// at 04:00): Friday 16:00–02:00 is a weekday's, Saturday's 01:30 still
// Friday; Sunday night and Monday morning are closed since Sunday's end.
func TestWindowsPastMidnight(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-06 23:30") // Friday
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{VideoMin: ip(600), Weekday: win("16:00", "02:00"), Weekend: win("10:00", "23:00")}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(ctx, u)
	if !st.Video.Allowed || secs(st.Video.RemainingSec) != 150*60 || st.Day != "2026-11-06" {
		t.Fatalf("Fri 23:30 %+v", st)
	}
	c.t = at("2026-11-07 01:30") // Saturday by the calendar, Friday's window
	sig := opened(s, u, 3)
	for i := 0; i < 7; i++ {
		s.Signal(ctx, u, sig)
		c.add(10 * time.Second)
	}
	if err := s.AddBonus(ctx, u.ID, 15); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx, u)
	if !st.Video.Allowed || st.Day != "2026-11-06" || st.Video.BonusMin != 15 || st.Video.UsedSec < 60 ||
		secs(st.Video.RemainingSec) > 29*60 || secs(st.Video.RemainingSec) < 28*60 {
		t.Fatalf("Sat 01:31 %+v", st)
	}
	for when, want := range map[string]string{
		"2026-11-07 03:00": "Сейчас нельзя смотреть: с 02:00 до 10:00", // after Friday's window
		"2026-11-07 09:00": "Сейчас нельзя смотреть: с 02:00 до 10:00", // Saturday before its own
		"2026-11-08 23:30": "Сейчас нельзя смотреть: с 23:00 до 16:00", // Sunday night
		"2026-11-09 02:00": "Сейчас нельзя смотреть: с 23:00 до 16:00", // Monday 02:00: still Sunday
		"2026-11-09 15:00": "Сейчас нельзя смотреть: с 23:00 до 16:00", // Monday before its window
	} {
		c.t = at(when)
		if r := s.Allow(ctx, u, Video); r == nil || r.Reason != "hours" || r.Message != want {
			t.Errorf("%s: %+v", when, r)
		}
	}
	c.t = at("2026-11-07 01:59") // a minute left: the warning's clock is the window's end
	if st, _ := s.Status(ctx, u); secs(st.Video.RemainingSec) != 60 {
		t.Fatalf("Sat 01:59 %+v", st.Video)
	}
	// the Saturday 01:30 playback is Friday's usage
	rep, _ := s.Report(ctx, u)
	if rep.Days[len(rep.Days)-1].Day != "2026-11-06" || rep.Days[len(rep.Days)-1].VideoSec < 60 {
		t.Fatalf("%+v", rep.Days)
	}
}

// Music counts what was delivered as it plays: the next track's reads
// count the first one up to there; a two-hour mix sent ahead and left
// counts at most maxTrail.
func TestAudioMediaDelivered(t *testing.T) {
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{AudioMin: ip(30)}); err != nil {
		t.Fatal(err)
	}
	music := func(item int64, d time.Duration) {
		s.Signal(ctx, u, Signal{Category: Audio, Key: MusicStream, ItemID: item, Media: d})
	}
	music(3, 200*time.Second) // the whole track at once
	c.add(200 * time.Second)
	music(4, 300*time.Second)
	c.add(30 * time.Second) // skipped after 30 s
	music(5, 2*time.Hour)   // a mix sent ahead, then nothing
	st, _ := s.Status(ctx, u)
	if st.Audio == nil || st.Audio.UsedSec != 230 || st.Video.UsedSec != 0 {
		t.Fatalf("%+v %+v", st.Audio, st.Video)
	}
	c.add(3 * time.Hour)
	st, _ = s.Status(ctx, u)
	if st.Audio.UsedSec != 230+int64(maxTrail.Seconds()) {
		t.Fatalf("mix %+v", st.Audio)
	}
}
