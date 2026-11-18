package channels

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/segments"
)

func TestKeptPieces(t *testing.T) {
	const dur = 22 * 60_000 // a 22-minute episode
	sec := func(s float64) int64 { return int64(s * 1000) }
	cases := []struct {
		name string
		segs []segments.Play
		want []span
	}{
		{"no segments: whole", nil, nil},
		{"intro mid-way: cold open + rest to the credits",
			[]segments.Play{{Kind: "intro", StartSec: 60, EndSec: 90}, {Kind: "credits", StartSec: 1290, EndSec: 1320}},
			[]span{{0, sec(60)}, {sec(90), sec(1290)}}},
		{"recap then intro are one cut",
			[]segments.Play{{Kind: "recap", StartSec: 0, EndSec: 40}, {Kind: "intro", StartSec: 41, EndSec: 71}},
			[]span{{sec(71), dur}}},
		{"overlapping recap and intro",
			[]segments.Play{{Kind: "recap", StartSec: 5, EndSec: 50}, {Kind: "intro", StartSec: 45, EndSec: 75}},
			[]span{{0, sec(5)}, {sec(75), dur}}},
		{"recap and intro apart: two cuts",
			[]segments.Play{{Kind: "recap", StartSec: 0, EndSec: 30}, {Kind: "intro", StartSec: 120, EndSec: 150}},
			[]span{{sec(30), sec(120)}, {sec(150), dur}}},
		{"a scene after the credits keeps them",
			[]segments.Play{{Kind: "intro", StartSec: 60, EndSec: 90}, {Kind: "credits", StartSec: 1200, EndSec: 1260}},
			[]span{{0, sec(60)}, {sec(90), dur}}},
		{"credits only",
			[]segments.Play{{Kind: "credits", StartSec: 1290, EndSec: 1318}},
			[]span{{0, sec(1290)}}},
		{"a sliver of a cold open is dropped",
			[]segments.Play{{Kind: "intro", StartSec: 1, EndSec: 31}},
			[]span{{sec(31), dur}}},
		{"cutting most of the episode is not trusted",
			[]segments.Play{{Kind: "intro", StartSec: 0, EndSec: 400}, {Kind: "credits", StartSec: 600, EndSec: 1320}},
			nil},
	}
	for _, c := range cases {
		got := keptPieces(c.segs, dur)
		if len(got) != len(c.want) {
			t.Fatalf("%s: %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// segFixture: a series of 2 single-file 22-minute episodes; the first has
// a recap, an intro and credits, the second nothing known yet.
func segFixture(t *testing.T) (*fixture, int64, []int64, []int64) {
	f := newFixture(t)
	s, eps := f.series("Sitcom", 1, 2, 22, itemOpt{})
	var files []int64
	for _, e := range eps {
		var id int64
		if err := f.db.QueryRow(`SELECT id FROM files WHERE item_id=?`, e).Scan(&id); err != nil {
			t.Fatal(err)
		}
		files = append(files, id)
	}
	for _, g := range []segments.Segment{{Kind: "recap", StartMs: 0, EndMs: 40_000}, {Kind: "intro", StartMs: 40_500, EndMs: 70_000},
		{Kind: "credits", StartMs: 1_290_000, EndMs: 1_318_000}} {
		f.exec(`INSERT INTO file_segments(file_id, kind, source, start_ms, end_ms) VALUES(?,?,?,?,?)`, files[0], g.Kind, "chapters", g.StartMs, g.EndMs)
	}
	return f, s, eps, files
}

func TestBuildSkipsEpisodeSegments(t *testing.T) {
	f, s, eps, files := segFixture(t)
	ctx := context.Background()
	c := f.create(Channel{Name: "Sitcom", Rules: Rules{Titles: []int64{s}, Order: OrderOrdered, SkipSegments: true}})
	b := f.builder()
	b.Horizon = 40 * time.Minute
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	// episode 1: [70 s, 1290 s) of its file; episode 2 whole (segments not known yet)
	if len(es) != 2 {
		t.Fatalf("entries %+v", es)
	}
	if e := es[0]; e.ItemID != eps[0] || e.FileID != files[0] || e.OffsetMs != 70_000 || e.End.Sub(e.Start) != 1220*time.Second || !e.Start.Equal(f.now) {
		t.Fatalf("episode 1 %+v", e)
	}
	if e := es[1]; e.ItemID != eps[1] || e.OffsetMs != 0 || e.End.Sub(e.Start) != 22*time.Minute || !e.Start.Equal(es[0].End) {
		t.Fatalf("episode 2 %+v", e)
	}
	// the guide: one programme each, the cut episode's real length
	ps := 0
	for i := 0; i < len(es); i++ {
		for i+1 < len(es) && SameProgramme(es[i], es[i], es[i+1]) {
			i++
		}
		ps++
	}
	if ps != 2 {
		t.Fatalf("programmes %d", ps)
	}
}

func TestBuildSkipOffOrMovieUntouched(t *testing.T) {
	f, s, _, _ := segFixture(t)
	ctx := context.Background()
	m := f.movie("Film", 100, itemOpt{})
	var mf int64
	_ = f.db.QueryRow(`SELECT id FROM files WHERE item_id=?`, m).Scan(&mf)
	f.exec(`INSERT INTO file_segments(file_id, kind, source, start_ms, end_ms) VALUES(?,?,?,?,?)`, mf, "credits", "chapters", 5_700_000, 6_000_000)
	b := f.builder()
	b.Horizon = 40 * time.Minute
	off := f.create(Channel{Name: "Off", Rules: Rules{Titles: []int64{s}, Order: OrderOrdered}})
	movie := f.create(Channel{Name: "Film", Rules: Rules{Titles: []int64{m}, Order: OrderOrdered, SkipSegments: true}})
	for _, c := range []Channel{off, movie} {
		if err := b.Build(ctx, c); err != nil {
			t.Fatal(err)
		}
		for _, e := range f.entries(c.ID) {
			if e.OffsetMs != 0 {
				t.Fatalf("%s: cut %+v", c.Name, e)
			}
		}
	}
	if e := f.entries(movie.ID)[0]; e.End.Sub(e.Start) != 100*time.Minute {
		t.Fatalf("movie %+v", e)
	}
	if e := f.entries(off.ID)[0]; e.End.Sub(e.Start) != 22*time.Minute {
		t.Fatalf("rule off %+v", e)
	}
}

// An episode with a cold open plays as two pieces of its file: the
// schedule maps wall time to (piece, offset in the file), the guide shows
// one programme, a repeat of the episode is a new one, an edit keeps the
// piece after the intro of the episode on air, and the encoder seeks there.
func TestCutProgrammePlayoutOffset(t *testing.T) {
	f, s, eps, files := segFixture(t)
	ctx := context.Background()
	f.exec(`DELETE FROM file_segments WHERE file_id=? AND kind='recap'`, files[0])
	f.exec(`UPDATE file_segments SET start_ms=60000, end_ms=90000 WHERE file_id=? AND kind='intro'`, files[0])
	c := f.create(Channel{Name: "Sitcom", Rules: Rules{Titles: []int64{s}, Order: OrderOrdered, SkipSegments: true}})
	b := f.builder()
	b.Horizon = 40 * time.Minute
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	if len(es) != 3 || es[0].OffsetMs != 0 || es[0].End.Sub(es[0].Start) != time.Minute || es[1].OffsetMs != 90_000 || es[1].ItemID != eps[0] {
		t.Fatalf("pieces %+v", es)
	}
	if !SameProgramme(es[0], es[0], es[1]) || SameProgramme(es[0], es[1], es[2]) {
		t.Fatal("pieces of one programme")
	}
	// a repeat of the same episode right after is a new programme
	rep := es[0]
	rep.Start, rep.End = es[1].End, es[1].End.Add(time.Minute)
	if SameProgramme(es[0], es[1], rep) {
		t.Fatal("a repeat merged")
	}
	// 2 minutes in: the second piece, 60 s into it, 150 s into the file
	at := f.now.Add(2 * time.Minute)
	e, err := f.st.At(ctx, c.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if e.Seq != es[1].Seq {
		t.Fatalf("at 2 min: %+v", e)
	}
	if got := (Program{Entry: e}).fileSec(at); got != 150 {
		t.Fatalf("file position %v", got)
	}
	// an edit during the cold open keeps the rest of the episode
	f.now = f.now.Add(30 * time.Second)
	if err := f.st.CutAfterCurrent(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if es := f.entries(c.ID); len(es) != 2 || es[1].OffsetMs != 90_000 {
		t.Fatalf("after edit %+v", es)
	}

	// the encoder: ffmpeg seeks to the piece's place in the file
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	ff := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\necho \"$@\" >> "+argsFile+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := Program{Entry: Entry{ItemID: 1, FileID: 1, OffsetMs: 90_000, Start: now.Add(-30 * time.Second), End: now.Add(20 * time.Minute)},
		Path: "/m/ep.mkv", Probe: media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 160, Height: 120}}}
	live := &Live{FFmpeg: ff, HW: "none", Dir: filepath.Join(dir, "live"), Source: oneProg{p}, ReadyWait: 2 * time.Second,
		Width: 320, Height: 180, ReadySegments: 1}
	defer live.Close()
	_, _ = live.Playlist(ctx, 1, "index.m3u8")
	deadline := time.Now().Add(5 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(argsFile)
		if got = string(b); got != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	first, _, _ := strings.Cut(got, "\n")
	i := strings.Index(first, "-ss ")
	if i < 0 {
		t.Fatalf("no seek: %q", first)
	}
	var ss float64
	if _, err := fmt.Sscan(first[i+4:], &ss); err != nil || ss < 119 || ss > 122 {
		t.Fatalf("seek %v (%v): %q", ss, err, first)
	}
}

// oneProg is a Source of one programme.
type oneProg struct{ p Program }

func (s oneProg) Program(_ context.Context, ch int64, t time.Time) (Program, error) {
	if !t.Before(s.p.End) {
		return Program{}, ErrNothing
	}
	p := s.p
	p.Channel = ch
	return p, nil
}
