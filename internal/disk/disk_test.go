package disk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/events"
)

func TestLevelOf(t *testing.T) {
	for _, c := range []struct {
		used, total int64
		want        string
	}{{84, 100, LevelOK}, {85, 100, LevelWarn}, {94, 100, LevelWarn}, {95, 100, LevelFull}, {96, 100, LevelFull}, {10, 0, LevelOK}} {
		if got := LevelOf(c.used, c.total); got != c.want {
			t.Errorf("%d/%d: %s, want %s", c.used, c.total, got, c.want)
		}
	}
}

func atPercent(percents ...int64) func(string) (int64, int64, error) {
	i := 0
	return func(string) (int64, int64, error) {
		p := percents[min(i, len(percents)-1)]
		i++
		return 1000, 1000 - p*10, nil
	}
}

func TestStatReservedCountsAsUsed(t *testing.T) {
	m := &Monitor{Root: "/media", Statfs: atPercent(90)}
	u, err := m.Usage()
	if err != nil || u.Used != 900 || u.Total != 1000 || u.Level != LevelWarn {
		t.Fatalf("usage %+v %v", u, err)
	}
	m.Statfs = func(string) (int64, int64, error) { return 0, 0, nil }
	if _, err := m.Usage(); err == nil {
		t.Fatal("a disk of unknown size is an error")
	}
	m.Statfs = func(string) (int64, int64, error) { return 0, 0, errors.New("boom") }
	if _, err := m.Usage(); err == nil {
		t.Fatal("a statfs error is an error")
	}
	if used, total, err := Stat(t.TempDir()); err != nil || total <= 0 || used < 0 || used > total {
		t.Fatalf("Stat %d/%d %v", used, total, err)
	}
}

func TestMonitorPublishesOnChange(t *testing.T) {
	bus := &events.Bus{}
	ch, cancel := bus.Subscribe()
	defer cancel()
	m := &Monitor{Root: "/media", Bus: bus, Statfs: atPercent(80, 86, 87, 96, 50)}
	for i := 0; i < 5; i++ {
		if _, err := m.Check(); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for len(ch) > 0 {
		e := <-ch
		if e.Type != events.DiskWarning {
			t.Fatalf("event %q", e.Type)
		}
		got = append(got, e.Data.(Usage).Level)
	}
	if len(got) != 2 || got[0] != LevelWarn || got[1] != LevelFull {
		t.Fatalf("events %v, want [warn full]", got)
	}
}

func TestMonitorOffline(t *testing.T) {
	bus := &events.Bus{}
	ch, cancel := bus.Subscribe()
	defer cancel()
	const why = "Медиадиск недоступен: нет папки /x"
	m := &Monitor{Root: "/media", Bus: bus, Offline: func() string { return why },
		Statfs: func(string) (int64, int64, error) { t.Fatal("statfs while offline"); return 0, 0, nil }}
	u, err := m.Check()
	if err != nil || u != (Usage{Level: LevelOK, Offline: why}) {
		t.Fatalf("usage %+v %v", u, err)
	}
	if len(ch) != 0 {
		t.Fatal("an offline disk published an event")
	}
}

func TestMonitorRunStopsWithContext(t *testing.T) {
	m := &Monitor{Root: "/media", Statfs: atPercent(10)}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, time.Millisecond); close(done) }()
	time.Sleep(5 * time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
