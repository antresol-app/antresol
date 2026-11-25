package channels

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

// The lead time and the disk budgets of channels with fetching.

const (
	// estBytesPerSec estimates a download from the programme's runtime
	// (≈ 5 Mbit/s, a 1080p release) until the addon says its size.
	estBytesPerSec = 600_000
	// defaultFetchSpeed is the speed assumed before any download was seen
	// (16 Mbit/s); minFetchSpeed bounds what is learnt from slow sources.
	defaultFetchSpeed = 2_000_000.0
	minFetchSpeed     = 250_000.0
	// fetchSafety stretches the transfer time (sources vary).
	fetchSafety = 1.5
	// fetchSearchTime: finding a release and starting it; fetchOrganizeTime:
	// moving it into the library and probing it.
	fetchSearchTime   = 15 * time.Minute
	fetchOrganizeTime = 15 * time.Minute
	// fetchLateMargin: a programme must be on disk this long before its
	// slot starts, or the slot goes to another programme.
	fetchLateMargin = 5 * time.Minute
	// fetchSpeedWeight: how much one sighting moves the learnt speed.
	fetchSpeedWeight = 0.3
)

// Keys of the settings table.
const (
	settingFetchBudget = "channels_fetch_budget_gb"
	settingFetchSpeed  = "channels_fetch_speed"
	settingFetchDrops  = "channels_fetch_drops"
)

func estimateBytes(runtimeMs int64) int64 { return runtimeMs / 1000 * estBytesPerSec }

// FetchGlobalGB is the disk every channel's fetches together may take.
func (s *Store) FetchGlobalGB(ctx context.Context) float64 {
	if v, ok, _ := db.GetSetting(ctx, s.DB, settingFetchBudget); ok {
		if gb, err := strconv.ParseFloat(v, 64); err == nil && gb > 0 {
			return gb
		}
	}
	return DefaultFetchGlobalGB
}

// SetFetchGlobalGB sets it (0: the default).
func (s *Store) SetFetchGlobalGB(ctx context.Context, gb float64) error {
	if gb < 0 || gb > maxFetchGB || math.IsNaN(gb) {
		return &Invalid{"Недопустимый объём диска для каналов"}
	}
	if gb == 0 {
		return db.DeleteSetting(ctx, s.DB, settingFetchBudget)
	}
	return db.PutSetting(ctx, s.DB, settingFetchBudget, strconv.FormatFloat(gb, 'f', -1, 64))
}

// fetchSpeed is the download speed learnt so far (bytes a second).
func (s *Store) fetchSpeed(ctx context.Context) float64 {
	if v, ok, _ := db.GetSetting(ctx, s.DB, settingFetchSpeed); ok {
		if x, err := strconv.ParseFloat(v, 64); err == nil && x > 0 {
			return max(x, minFetchSpeed)
		}
	}
	return defaultFetchSpeed
}

// sawFetchSpeed moves the learnt speed toward rate (the channels'
// downloads together, bytes a second).
func (s *Store) sawFetchSpeed(ctx context.Context, rate float64) error {
	if rate <= 0 {
		return nil
	}
	old := defaultFetchSpeed
	if v, ok, _ := db.GetSetting(ctx, s.DB, settingFetchSpeed); ok {
		if x, err := strconv.ParseFloat(v, 64); err == nil && x > 0 {
			old = x
		}
	}
	return db.PutSetting(ctx, s.DB, settingFetchSpeed, strconv.FormatFloat(old*(1-fetchSpeedWeight)+rate*fetchSpeedWeight, 'f', 0, 64))
}

// FetchUsage is what fetching takes: bytes on disk or on their way (or
// reserved by a schedule), and the budget.
type FetchUsage struct {
	Used   int64 `json:"used"`
	Budget int64 `json:"budget"`
}

// fetchPlan judges, for one build of channel c, whether a programme not on
// disk can take a slot: it will be there fetchLateMargin before the slot
// (the lead time: its size over the speed, after what is in the pipe
// already) and the channel's and the global budgets have room for it.
type fetchPlan struct {
	now        time.Time
	speed      float64
	chanBudget int64
	allBudget  int64
	chanUsed   int64
	allUsed    int64
	queue      int64           // bytes in the pipe: what is fetched now, then what this build placed
	reserved   map[int64]bool  // rows of c counted in the budgets already
	eta        map[int64]int64 // a requested row → when it is on disk (ms)
}

// fetchCounts: the rows that take budget — fetching or fetched for a
// channel, or not on disk yet but in a schedule ahead (reserved).
const fetchCounts = `(state IN ('requested','ready') OR (state IN ('wanted','removed','failed')
  AND id IN (SELECT fetch_id FROM channel_playout WHERE fetch_id IS NOT NULL AND end_ms > ?)))`

func fetchUsageRows(ctx context.Context, q dbtx, now time.Time) ([]fetchRow, error) {
	return fetchRows(ctx, q, fetchCounts+` ORDER BY requested_ms, id`, ms(now))
}

// sumUsage: by channel, and all of them (a programme two channels fetch
// is counted once).
func sumUsage(rows []fetchRow) (map[int64]int64, int64) {
	by := map[int64]int64{}
	var all int64
	seen := map[string]bool{}
	for _, r := range rows {
		by[r.Channel] += r.bytes()
		if k := r.key(); !seen[k] {
			seen[k] = true
			all += r.bytes()
		}
	}
	return by, all
}

func newFetchPlan(ctx context.Context, st *Store, c Channel) (*fetchPlan, error) {
	now := st.now()
	p := &fetchPlan{now: now, speed: st.fetchSpeed(ctx), chanBudget: c.Rules.Fetch.budget(),
		allBudget: int64(st.FetchGlobalGB(ctx) * (1 << 30)), reserved: map[int64]bool{}, eta: map[int64]int64{}}
	rows, err := fetchUsageRows(ctx, st.DB, now)
	if err != nil {
		return nil, err
	}
	by, all := sumUsage(rows)
	p.chanUsed, p.allUsed = by[c.ID], all
	for _, r := range rows {
		if r.Channel == c.ID {
			p.reserved[r.ID] = true
		}
		if r.State != fetchRequested {
			continue
		}
		left := max(r.bytes()-r.Done, 0)
		p.queue += left
		at := now.Add(fetchOrganizeTime + p.transfer(p.queue))
		if r.Size == 0 {
			at = at.Add(fetchSearchTime)
		}
		p.eta[r.ID] = ms(at)
	}
	return p, nil
}

// transfer is how long bytes take at the learnt speed, with the margin.
func (p *fetchPlan) transfer(bytes int64) time.Duration {
	return time.Duration(float64(bytes) * fetchSafety / p.speed * float64(time.Second))
}

// lead is how long a programme of bytes not asked for yet takes to be on
// disk, after what is in the pipe.
func (p *fetchPlan) lead(bytes int64) time.Duration {
	return fetchSearchTime + fetchOrganizeTime + p.transfer(p.queue+bytes)
}

func (p *fetchPlan) fits(r fetchRow, start int64) bool {
	if r.ID == 0 {
		return false
	}
	deadline := start - fetchLateMargin.Milliseconds()
	switch {
	case r.State == fetchRequested:
		at, ok := p.eta[r.ID]
		return ok && at <= deadline
	case r.State == fetchWanted || r.State == fetchRemoved || (r.State == fetchFailed && r.FailedUntilMs <= ms(p.now)):
	default:
		return false // on disk but not playable yet (being probed), or failed lately
	}
	est := r.bytes()
	if ms(p.now.Add(p.lead(est))) > deadline {
		return false
	}
	if !p.reserved[r.ID] {
		if p.chanUsed+est > p.chanBudget || p.allUsed+est > p.allBudget {
			return false
		}
		p.chanUsed += est
		p.allUsed += est
		p.reserved[r.ID] = true
	}
	p.queue += est
	return true
}

// errNoFetch: nothing fetches programmes on this server now.
var errNoFetch = errors.New("channels: no fetcher")
