// Package screentime is «Экранное время»: what an admin allows a kid
// profile to watch (and, optionally, to listen to) per day, counted from
// real playback on the server and enforced at every playback entry point.
//
// Accounting. Playback sends signals (Service.Signal): a player's progress
// report (the web and app PUT /api/progress every 10 s while playing,
// Jellyfin clients POST their playstate) or a read of the media (an HLS
// segment, a chunk of a direct stream, the start of a music track). Each
// stream (a library title, or the profile's music queue) keeps the time of
// its last signal and how far that signal reaches ahead (its span); the
// next signal of the stream adds the time since the last one, never more
// than the span, so a pause (no signals) stops the clock after one span
// and a burst of reads (a player filling its buffer) adds next to nothing.
// While a player reports, media reads of its stream are not counted (they
// run ahead of the picture by the buffer); clients that do not report are
// counted by their reads. Concurrent streams of different titles each
// count (two devices, two films: twice the time); two devices on the same
// title count once.
//
// The day is the server's (TZ); books and comics are never limited.
package screentime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// Category is what a limit applies to.
type Category string

const (
	Video Category = "video" // movies and episodes
	Audio Category = "audio" // music and audiobooks
)

// CategoryOf is the category of a library item kind (false: never limited).
func CategoryOf(kind string) (Category, bool) {
	switch kind {
	case library.KindMovie, library.KindEpisode:
		return Video, true
	case library.KindTrack, library.KindAudiobook:
		return Audio, true
	}
	return "", false
}

// Window is a time of day playback is allowed: [From, To) in minutes
// since midnight; From > To crosses midnight.
type Window struct {
	From int
	To   int
}

// Contains: the minute of the day m is inside the window.
func (w Window) Contains(m int) bool {
	if w.From < w.To {
		return m >= w.From && m < w.To
	}
	return m >= w.From || m < w.To
}

// Clock formats minutes since midnight as HH:MM.
func Clock(m int) string { return fmt.Sprintf("%02d:%02d", m/60%24, m%60) }

func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || mm < 0 || mm > 59 || hh > 24 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("screentime: bad time %q", s)
	}
	return (hh*60 + mm) % (24 * 60), nil
}

// MarshalJSON: {"from":"08:00","to":"20:30"}.
func (w Window) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"from": Clock(w.From), "to": Clock(w.To)})
}

func (w *Window) UnmarshalJSON(b []byte) error {
	var v struct{ From, To string }
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	f, err := parseClock(v.From)
	if err != nil {
		return err
	}
	t, err := parseClock(v.To)
	if err != nil {
		return err
	}
	*w = Window{f, t}
	return nil
}

// Limits are a kid profile's rules. A nil minute limit is no limit; a nil
// AudioMin leaves music and audiobooks alone entirely (the hours too); a
// nil window is any time of the day.
//
// The screen day starts at DayStart («день начинается в 04:00»), not at
// midnight: usage and the bonus belong to it, and so does a window that
// runs past midnight (Friday 20:00–02:00 is Friday's: a weekday's). A
// window may not cross the day start.
type Limits struct {
	VideoMin *int    `json:"videoMin"`
	AudioMin *int    `json:"audioMin"`
	Weekday  *Window `json:"weekday"` // screen days Monday to Friday
	Weekend  *Window `json:"weekend"` // screen days Saturday and Sunday
	// DayStart is when the screen day starts, "HH:MM" ("": DefaultDayStart).
	DayStart string `json:"dayStart"`
}

// DefaultDayStart: the screen day starts at 04:00.
const DefaultDayStart = 4 * 60

// MaxMinutes bounds a daily limit and a day's bonus.
const MaxMinutes = 24 * 60

const dayMinutes = 24 * 60

// ErrInvalid: limits out of range.
var ErrInvalid = errors.New("screentime: invalid")

// start is the day start in minutes since midnight.
func (l Limits) start() int {
	if l.DayStart == "" {
		return DefaultDayStart
	}
	m, err := parseClock(l.DayStart)
	if err != nil {
		return DefaultDayStart
	}
	return m
}

// screenRange is a window in minutes since the screen day's start
// [from, to): to may be dayMinutes.
func (l Limits) screenRange(w Window) (from, to int) {
	s := l.start()
	from = (w.From - s + dayMinutes) % dayMinutes
	to = (w.To - s + dayMinutes) % dayMinutes
	if to == 0 {
		to = dayMinutes
	}
	return from, to
}

// Validate checks the ranges: minutes 0…1440, a window of two different
// times that does not cross the day start, a valid day start.
func (l Limits) Validate() error {
	for _, m := range []*int{l.VideoMin, l.AudioMin} {
		if m != nil && (*m < 0 || *m > MaxMinutes) {
			return fmt.Errorf("%w: minutes", ErrInvalid)
		}
	}
	if l.DayStart != "" {
		if _, err := parseClock(l.DayStart); err != nil {
			return fmt.Errorf("%w: day start", ErrInvalid)
		}
	}
	for _, w := range []*Window{l.Weekday, l.Weekend} {
		if w == nil {
			continue
		}
		if w.From == w.To || w.From < 0 || w.To < 0 || w.From >= dayMinutes || w.To >= dayMinutes {
			return fmt.Errorf("%w: window", ErrInvalid)
		}
		if from, to := l.screenRange(*w); from >= to {
			return fmt.Errorf("%w: window crosses the day start", ErrInvalid)
		}
	}
	return nil
}

// Any: some rule applies.
func (l Limits) Any() bool {
	return l.VideoMin != nil || l.AudioMin != nil || l.Weekday != nil || l.Weekend != nil
}

func weekend(d time.Weekday) bool { return d == time.Saturday || d == time.Sunday }

// screenTime is a moment on the screen-day clock.
type screenTime struct {
	day time.Time // the screen day's date (midnight of it)
	sec float64   // seconds since the screen day's start
}

// at is t on the screen-day clock.
func (l Limits) at(t time.Time) screenTime {
	shifted := t.Add(-time.Duration(l.start()) * time.Minute)
	day := time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, t.Location())
	sec := float64(shifted.Hour()*3600+shifted.Minute()*60+shifted.Second()) + float64(shifted.Nanosecond())/1e9
	return screenTime{day, sec}
}

// DayOf is the screen day of t (YYYY-MM-DD).
func (l Limits) DayOf(t time.Time) string { return Day(l.at(t).day) }

// windowOf is the allowed window of a screen day (nil: any time).
func (l Limits) windowOf(day time.Time) *Window {
	if weekend(day.Weekday()) {
		return l.Weekend
	}
	return l.Weekday
}

// applies: the category is limited at all (audio only with its own limit).
func (l Limits) applies(c Category) bool {
	if c == Audio {
		return l.AudioMin != nil
	}
	return l.VideoMin != nil || l.Weekday != nil || l.Weekend != nil
}

func (l Limits) minutes(c Category) *int {
	if c == Audio {
		return l.AudioMin
	}
	return l.VideoMin
}

// open: inside the screen day's window (no window: always); how long it
// stays open (meaningless without a window).
func (l Limits) open(st screenTime) (bool, time.Duration) {
	w := l.windowOf(st.day)
	if w == nil {
		return true, 0
	}
	from, to := l.screenRange(*w)
	if st.sec < float64(from*60) || st.sec >= float64(to*60) {
		return false, 0
	}
	return true, time.Duration((float64(to*60) - st.sec) * float64(time.Second))
}

// closed is the closed span around st as clock times: since the last
// window ended (this screen day's, or the day before's) until the next
// one opens (the next day's; without one: the day start).
func (l Limits) closed(st screenTime) (since, until string) {
	w := l.windowOf(st.day)
	if w == nil {
		return "", ""
	}
	from, _ := l.screenRange(*w)
	dayStart := Clock(l.start())
	if st.sec >= float64(from*60) { // after the day's window
		if nw := l.windowOf(st.day.AddDate(0, 0, 1)); nw != nil {
			return Clock(w.To), Clock(nw.From)
		}
		return Clock(w.To), dayStart
	}
	since = dayStart
	if pw := l.windowOf(st.day.AddDate(0, 0, -1)); pw != nil {
		since = Clock(pw.To)
	}
	return since, Clock(w.From)
}
