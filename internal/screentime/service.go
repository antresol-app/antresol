package screentime

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
)

// Signal spans: how far a signal reaches ahead.
const (
	// ReportSpan: a progress report without an earlier one to time it by
	// (the web and the app report every 10 s).
	ReportSpan = 15 * time.Second
	// ReadSpan: a media read without a known length (an HLS segment, a
	// stretch of a direct video stream).
	ReadSpan = 15 * time.Second
	// maxReportSpan bounds a span timed by the reports' own interval.
	maxReportSpan = 60 * time.Second
	// reportSlack is added to the reports' interval.
	reportSlack = 5 * time.Second
	// readsQuiet: media reads of a stream that reported this recently are
	// not counted (a player buffering ahead while paused).
	readsQuiet = 60 * time.Second
	// mediaFresh: a progress report counts only while its title's media
	// was opened or read this recently on the server (a live playback
	// session); a report without one is an offline copy's queue replayed,
	// or a client reporting what it never fetched.
	mediaFresh = 10 * time.Minute
	// maxTrail bounds what a stream that went quiet counts after its last
	// signal: what was delivered ahead may never have been played.
	maxTrail = 5 * time.Minute
)

// Service counts and enforces kid profiles' screen time. A nil *Service
// allows everything and counts nothing; profiles that are not kids are
// never limited.
type Service struct {
	DB *sql.DB
	// Now is the clock (nil: time.Now); Loc the day's time zone (nil:
	// time.Local, which TZ sets).
	Now func() time.Time
	Loc *time.Location

	mu     sync.Mutex
	open   map[streamKey]*stream
	starts map[int64]Limits // the profiles' limits, for the day start of signals
}

type streamKey struct {
	uid int64
	cat Category
	key string
}

type stream struct {
	item int64
	day  string    // the screen day of the last signal
	last time.Time // the last counted signal
	// ahead: how much playback the last signal stands for, not counted yet
	// (a report's span, or media delivered and not yet played)
	ahead      time.Duration
	lastReport time.Time
	media      time.Time // the last media read or open
}

// New is a Service over the database.
func New(db *sql.DB) *Service { return &Service{DB: db} }

func (s *Service) now() time.Time {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	loc := s.Loc
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc)
}

// Day is the date of t (YYYY-MM-DD).
func Day(t time.Time) string { return t.Format(time.DateOnly) }

// MusicStream is the stream key of a profile's music queue: one track
// after another, so what was delivered of the next one plays after the
// rest of this one.
const MusicStream = "music"

// Signal is evidence of playback (Service.Signal).
type Signal struct {
	Category Category
	// Key names the stream: one per title ("item:<id>"), or the profile's
	// music queue ("music").
	Key    string
	ItemID int64
	// Report: a player's progress report; else a read of the media.
	Report bool
	// Media: how much playback a read delivered (bytes over the bitrate);
	// it adds to what the stream has ahead. 0: a read of unknown length
	// (ReadSpan).
	Media time.Duration
}

type commit struct {
	uid  int64
	day  string
	cat  Category
	item int64
	sec  float64
}

// limitsOf are the profile's limits, remembered (SetLimits forgets them).
func (s *Service) limitsOf(ctx context.Context, uid int64) Limits {
	s.mu.Lock()
	l, ok := s.starts[uid]
	s.mu.Unlock()
	if ok {
		return l
	}
	l, err := s.Limits(ctx, uid)
	if err != nil {
		log.Printf("screentime: limits of %d: %v", uid, err)
		return Limits{}
	}
	s.mu.Lock()
	if s.starts == nil {
		s.starts = map[int64]Limits{}
	}
	s.starts[uid] = l
	s.mu.Unlock()
	return l
}

// Opened marks a title's media as opened for playback (POST /api/play,
// Jellyfin PlaybackInfo): its player's reports count from now on.
func (s *Service) Opened(ctx context.Context, u auth.User, c Category, key string, item int64) {
	if s == nil || !u.IsKid || c == "" {
		return
	}
	day := s.limitsOf(ctx, u.ID).DayOf(s.now())
	now := s.now()
	k := streamKey{u.ID, c, key}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		s.open = map[streamKey]*stream{}
	}
	st := s.open[k]
	if st == nil {
		st = &stream{item: item, day: day, last: now}
		s.open[k] = st
	}
	st.media = now
}

// Signal counts the playback of a kid profile (anything else: nothing).
func (s *Service) Signal(ctx context.Context, u auth.User, sig Signal) {
	if s == nil || !u.IsKid || sig.Category == "" {
		return
	}
	day := s.limitsOf(ctx, u.ID).DayOf(s.now())
	now := s.now()
	k := streamKey{u.ID, sig.Category, sig.Key}
	var out []commit
	s.mu.Lock()
	if s.open == nil {
		s.open = map[streamKey]*stream{}
	}
	out = s.sweepLocked(now, out)
	st := s.open[k]
	switch {
	case sig.Report && (st == nil || now.Sub(st.media) > mediaFresh):
		// no live session on the server: an offline queue, not playback
		s.mu.Unlock()
		s.write(ctx, out)
		return
	case !sig.Report && st != nil && !st.lastReport.IsZero() && now.Sub(st.lastReport) < readsQuiet:
		// a reporting player: its reads run ahead of the picture
		st.media = now
		s.mu.Unlock()
		s.write(ctx, out)
		return
	}
	elapsed := time.Duration(0)
	if st != nil {
		elapsed = max(now.Sub(st.last), 0)
		if d := min(elapsed, st.ahead); d > 0 {
			out = append(out, commit{u.ID, st.day, sig.Category, st.item, d.Seconds()})
		}
	} else {
		st = &stream{}
		s.open[k] = st
	}
	switch {
	case sig.Media > 0:
		st.ahead = max(st.ahead-elapsed, 0) + sig.Media
	case sig.Report:
		span := ReportSpan
		if !st.lastReport.IsZero() {
			if iv := now.Sub(st.lastReport); iv > 0 && iv <= maxReportSpan {
				span = min(iv+reportSlack, maxReportSpan)
			}
		}
		st.ahead = span
		st.lastReport = now
	default:
		st.ahead = ReadSpan
	}
	if !sig.Report {
		st.media = now
	}
	st.item, st.day, st.last = sig.ItemID, day, now
	s.mu.Unlock()
	s.write(ctx, out)
}

// pending is what a stream counts now that is not committed: the time
// since its last signal, within what it had ahead and maxTrail.
func (st *stream) pending(now time.Time) time.Duration {
	return max(min(now.Sub(st.last), st.ahead, maxTrail), 0)
}

// sweepLocked commits the streams whose last signal ran out (what they
// had ahead, within maxTrail) and forgets those idle past mediaFresh.
func (s *Service) sweepLocked(now time.Time, out []commit) []commit {
	for k, st := range s.open {
		if st.ahead > 0 && now.Sub(st.last) >= min(st.ahead, maxTrail) {
			out = append(out, commit{k.uid, st.day, k.cat, st.item, min(st.ahead, maxTrail).Seconds()})
			st.ahead = 0
		}
		if st.ahead == 0 && now.Sub(st.last) > mediaFresh && now.Sub(st.media) > mediaFresh {
			delete(s.open, k)
		}
	}
	return out
}

// openSecondsLocked is the uncommitted time of the profile's open streams
// on the screen day, per category and per item.
func (s *Service) openSecondsLocked(uid int64, day string, now time.Time) (video, audio float64, items map[TitleUsage]float64) {
	items = map[TitleUsage]float64{}
	for k, st := range s.open {
		if k.uid != uid || st.day != day {
			continue
		}
		d := st.pending(now).Seconds()
		if d <= 0 {
			continue
		}
		items[TitleUsage{ItemID: st.item, Category: k.cat}] += d
		if k.cat == Audio {
			audio += d
		} else {
			video += d
		}
	}
	return video, audio, items
}

func (s *Service) write(ctx context.Context, cs []commit) {
	for _, c := range cs {
		_, err := s.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO screen_time_usage(user_id, day, category, item_id, seconds)
			VALUES(?,?,?,?,?) ON CONFLICT(user_id, day, category, item_id) DO UPDATE SET seconds = seconds + excluded.seconds`,
			c.uid, c.day, string(c.cat), c.item, c.sec)
		if err != nil {
			log.Printf("screentime: usage of %d: %v", c.uid, err)
		}
	}
}

// --- limits and bonus ---

// Limits are the profile's rules (zero Limits: none).
func (s *Service) Limits(ctx context.Context, uid int64) (Limits, error) {
	var l Limits
	var vm, am, wf, wt, ef, et sql.NullInt64
	var start int
	err := s.DB.QueryRowContext(ctx, `SELECT video_min, audio_min, weekday_from, weekday_to, weekend_from, weekend_to, day_start
		FROM screen_time_limits WHERE user_id=?`, uid).Scan(&vm, &am, &wf, &wt, &ef, &et, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	l.VideoMin, l.AudioMin = intp(vm), intp(am)
	if wf.Valid && wt.Valid {
		l.Weekday = &Window{int(wf.Int64), int(wt.Int64)}
	}
	if ef.Valid && et.Valid {
		l.Weekend = &Window{int(ef.Int64), int(et.Int64)}
	}
	l.DayStart = Clock(start)
	return l, nil
}

func intp(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func windowCols(w *Window) (any, any) {
	if w == nil {
		return nil, nil
	}
	return w.From, w.To
}

// SetLimits saves the profile's rules (ErrInvalid: out of range).
func (s *Service) SetLimits(ctx context.Context, uid int64, l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	wf, wt := windowCols(l.Weekday)
	ef, et := windowCols(l.Weekend)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO screen_time_limits(user_id, video_min, audio_min, weekday_from, weekday_to, weekend_from, weekend_to, day_start, updated_at)
		VALUES(?,?,?,?,?,?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		ON CONFLICT(user_id) DO UPDATE SET video_min=excluded.video_min, audio_min=excluded.audio_min,
		  weekday_from=excluded.weekday_from, weekday_to=excluded.weekday_to,
		  weekend_from=excluded.weekend_from, weekend_to=excluded.weekend_to, day_start=excluded.day_start, updated_at=excluded.updated_at`,
		uid, nullInt(l.VideoMin), nullInt(l.AudioMin), wf, wt, ef, et, l.start())
	s.mu.Lock()
	delete(s.starts, uid)
	s.mu.Unlock()
	return err
}

// AddBonus gives the profile minutes more on today's screen day (the
// day's bonus stays within MaxMinutes; ErrInvalid: minutes out of
// 1…MaxMinutes).
func (s *Service) AddBonus(ctx context.Context, uid int64, minutes int) error {
	if minutes <= 0 || minutes > MaxMinutes {
		return ErrInvalid
	}
	l, err := s.Limits(ctx, uid)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO screen_time_bonus(user_id, day, minutes) VALUES(?,?,?)
		ON CONFLICT(user_id, day) DO UPDATE SET minutes = MIN(minutes + excluded.minutes, ?)`,
		uid, l.DayOf(s.now()), minutes, MaxMinutes)
	return err
}

func (s *Service) bonus(ctx context.Context, uid int64, day string) (int, error) {
	var m int
	err := s.DB.QueryRowContext(ctx, `SELECT minutes FROM screen_time_bonus WHERE user_id=? AND day=?`, uid, day).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return m, err
}

// used is the profile's seconds of the screen day: committed (per item)
// and open, per category.
func (s *Service) used(ctx context.Context, uid int64, day string, now time.Time) (video, audio float64, items map[TitleUsage]float64, err error) {
	// commit what ran out first: its seconds are then in the rows below
	s.mu.Lock()
	var out []commit
	if s.open != nil {
		out = s.sweepLocked(now, out)
	}
	s.mu.Unlock()
	s.write(ctx, out)
	rows, err := s.DB.QueryContext(ctx, `SELECT category, item_id, seconds FROM screen_time_usage WHERE user_id=? AND day=?`, uid, day)
	if err != nil {
		return 0, 0, nil, err
	}
	defer rows.Close()
	s.mu.Lock()
	video, audio, items = s.openSecondsLocked(uid, day, now)
	s.mu.Unlock()
	for rows.Next() {
		var c string
		var item int64
		var sec float64
		if err := rows.Scan(&c, &item, &sec); err != nil {
			return 0, 0, nil, err
		}
		items[TitleUsage{ItemID: item, Category: Category(c)}] += sec
		if Category(c) == Audio {
			audio += sec
		} else {
			video += sec
		}
	}
	return video, audio, items, rows.Err()
}

// --- status ---

// CategoryStatus is how a category stands today.
type CategoryStatus struct {
	// LimitMin is the day's limit without the bonus (nil: no limit).
	LimitMin *int  `json:"limitMin"`
	BonusMin int   `json:"bonusMin"`
	UsedSec  int64 `json:"usedSec"`
	// RemainingSec is how long playback may go on now: until the limit or
	// the end of the allowed hours, whichever is first (nil: nothing ends it).
	RemainingSec *int64 `json:"remainingSec"`
	Allowed      bool   `json:"allowed"`
	// Reason: "limit" (the day's time is used up) or "hours" (outside the
	// allowed hours) when not allowed.
	Reason string `json:"reason,omitempty"`
	// Message is what the kid reads (the request's language).
	Message string `json:"message,omitempty"`
}

// Status is a profile's screen time today.
type Status struct {
	// Limited: some rule applies to the profile.
	Limited bool `json:"limited"`
	// Day is the screen day (it starts at the limits' day start).
	Day string `json:"day"`
	// Hours is the screen day's allowed window (nil: any time).
	Hours *Window        `json:"hours"`
	Video CategoryStatus `json:"video"`
	// Audio is nil when music and audiobooks are not limited.
	Audio *CategoryStatus `json:"audio"`
	// VideoLimited / AudioLimited: some rule limits the category (then
	// nothing of it downloads or goes to the device: Restricted).
	VideoLimited bool `json:"videoLimited"`
	AudioLimited bool `json:"audioLimited"`

	items map[TitleUsage]float64
}

// Status is the profile's screen time now (a profile that is not a kid,
// or a nil Service: not limited).
func (s *Service) Status(ctx context.Context, u auth.User) (Status, error) {
	if s == nil || !u.IsKid {
		return Status{Video: CategoryStatus{Allowed: true}}, nil
	}
	now := s.now()
	l, err := s.Limits(ctx, u.ID)
	if err != nil {
		return Status{}, err
	}
	at := l.at(now)
	day := Day(at.day)
	b, err := s.bonus(ctx, u.ID, day)
	if err != nil {
		return Status{}, err
	}
	v, a, items, err := s.used(ctx, u.ID, day, now)
	if err != nil {
		return Status{}, err
	}
	st := Status{Limited: l.Any(), Day: day, Hours: l.windowOf(at.day), items: items,
		VideoLimited: l.applies(Video), AudioLimited: l.applies(Audio)}
	st.Video = l.categoryStatus(ctx, Video, at, v, b)
	if l.applies(Audio) {
		as := l.categoryStatus(ctx, Audio, at, a, b)
		st.Audio = &as
	}
	return st, nil
}

func (l Limits) categoryStatus(ctx context.Context, c Category, at screenTime, used float64, bonus int) CategoryStatus {
	cs := CategoryStatus{LimitMin: l.minutes(c), UsedSec: int64(used), Allowed: true}
	if !l.applies(c) {
		return cs
	}
	cs.BonusMin = bonus
	var remaining *time.Duration
	if lim := l.minutes(c); lim != nil {
		r := time.Duration(*lim+bonus)*time.Minute - time.Duration(used*float64(time.Second))
		remaining = &r
	}
	if l.windowOf(at.day) != nil {
		open, left := l.open(at)
		if !open {
			since, until := l.closed(at)
			cs.Allowed, cs.Reason = false, "hours"
			if c == Audio {
				cs.Message = i18n.T(ctx, "Сейчас нельзя слушать: с %s до %s", since, until)
			} else {
				cs.Message = i18n.T(ctx, "Сейчас нельзя смотреть: с %s до %s", since, until)
			}
			zero := int64(0)
			cs.RemainingSec = &zero
			return cs
		}
		if remaining == nil || left < *remaining {
			remaining = &left
		}
	}
	if remaining != nil {
		sec := max(int64(remaining.Seconds()), 0)
		cs.RemainingSec = &sec
		if sec <= 0 {
			cs.Allowed, cs.Reason = false, "limit"
			cs.Message = i18n.T(ctx, "Время на сегодня закончилось. Завтра можно снова!")
		}
	}
	return cs
}

// Refusal is why playback is not allowed now.
type Refusal struct {
	Reason  string // "limit" | "hours" | "download"
	Message string // in the request's language
}

func (r *Refusal) Error() string { return r.Message }

// Allow is nil when the profile may play the category now, else why not.
// A failure to read the rules allows (and is logged): a broken database
// must not lock a kid out of a film mid-way silently either way, and the
// failure is the admin's to see.
func (s *Service) Allow(ctx context.Context, u auth.User, c Category) *Refusal {
	if s == nil || !u.IsKid || c == "" {
		return nil
	}
	st, err := s.Status(ctx, u)
	if err != nil {
		log.Printf("screentime: status of %d: %v", u.ID, err)
		return nil
	}
	cs := st.Video
	if c == Audio {
		if st.Audio == nil {
			return nil
		}
		cs = *st.Audio
	}
	if cs.Allowed {
		return nil
	}
	return &Refusal{Reason: cs.Reason, Message: cs.Message}
}

// Restricted: some rule of the profile limits the category (a kid's; a
// failure to read the rules: not restricted, logged).
func (s *Service) Restricted(ctx context.Context, u auth.User, c Category) bool {
	if s == nil || !u.IsKid {
		return false
	}
	l, err := s.Limits(ctx, u.ID)
	if err != nil {
		log.Printf("screentime: limits of %d: %v", u.ID, err)
		return false
	}
	if c == "" {
		return l.Any()
	}
	return l.applies(c)
}

// --- report (the admin's view) ---

// TitleUsage is the seconds of one library item today.
type TitleUsage struct {
	ItemID   int64    `json:"itemId"`
	Category Category `json:"category"`
	Seconds  int64    `json:"seconds"`
}

// DayUsage is the seconds of one day.
type DayUsage struct {
	Day      string `json:"day"`
	VideoSec int64  `json:"videoSec"`
	AudioSec int64  `json:"audioSec"`
}

// Report is the admin's view of a kid profile.
type Report struct {
	Limits Limits       `json:"limits"`
	Status Status       `json:"status"`
	Titles []TitleUsage `json:"titles"` // today (playing ones too), most first
	Days   []DayUsage   `json:"days"`   // the last 7 screen days, today last
}

// ReportDays is how many days Report covers.
const ReportDays = 7

// SortTitles orders titles most time first (then by item).
func SortTitles(ts []TitleUsage) {
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].Seconds != ts[j].Seconds {
			return ts[i].Seconds > ts[j].Seconds
		}
		return ts[i].ItemID < ts[j].ItemID
	})
}

// Report is the profile's limits, today and the last ReportDays days.
func (s *Service) Report(ctx context.Context, u auth.User) (Report, error) {
	var rep Report
	l, err := s.Limits(ctx, u.ID)
	if err != nil {
		return rep, err
	}
	if l.DayStart == "" {
		l.DayStart = Clock(DefaultDayStart)
	}
	st, err := s.Status(ctx, u)
	if err != nil {
		return rep, err
	}
	rep.Limits, rep.Status = l, st
	today := l.at(s.now()).day
	first := Day(today.AddDate(0, 0, -(ReportDays - 1)))
	rows, err := s.DB.QueryContext(ctx, `SELECT day, category, SUM(seconds) FROM screen_time_usage
		WHERE user_id=? AND day>=? AND day<? GROUP BY day, category`, u.ID, first, Day(today))
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	days := map[string]*DayUsage{}
	for rows.Next() {
		var day, cat string
		var sec float64
		if err := rows.Scan(&day, &cat, &sec); err != nil {
			return rep, err
		}
		d := days[day]
		if d == nil {
			d = &DayUsage{Day: day}
			days[day] = d
		}
		if Category(cat) == Audio {
			d.AudioSec += int64(sec)
		} else {
			d.VideoSec += int64(sec)
		}
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}
	for i := ReportDays - 1; i > 0; i-- {
		day := Day(today.AddDate(0, 0, -i))
		if d := days[day]; d != nil {
			rep.Days = append(rep.Days, *d)
		} else {
			rep.Days = append(rep.Days, DayUsage{Day: day})
		}
	}
	// today as the status counts it (playing streams included)
	todayUsage := DayUsage{Day: st.Day}
	rep.Titles = []TitleUsage{}
	for t, sec := range st.items {
		if t.Category == Audio {
			todayUsage.AudioSec += int64(sec)
		} else {
			todayUsage.VideoSec += int64(sec)
		}
		if t.Seconds = int64(sec); t.Seconds > 0 {
			rep.Titles = append(rep.Titles, t)
		}
	}
	rep.Days = append(rep.Days, todayUsage)
	SortTitles(rep.Titles)
	return rep, nil
}
