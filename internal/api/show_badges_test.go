package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

func epFile(t *testing.T, ep int64, h int, hdr bool, tag string, audio, subs []string) library.File {
	t.Helper()
	p := media.ProbeResult{Video: &media.VideoStream{Height: h, HDR: hdr, CodecTag: tag}}
	for _, l := range audio {
		p.Audio = append(p.Audio, media.AudioStream{Lang: l})
	}
	for _, l := range subs {
		p.Subs = append(p.Subs, media.SubStream{Lang: l})
	}
	f := fileWithProbe(t, "/tv/e.mkv", 1, p)
	f.ItemID = ep
	return f
}

// A show's badges are what a strict majority of its probed episodes carry.
func TestShowFacts(t *testing.T) {
	files := []library.File{
		epFile(t, 1, 2160, true, "", []string{"rus", "eng"}, []string{"eng"}),
		epFile(t, 2, 2160, true, "", []string{"rus", "eng"}, []string{"eng", "rus"}),
		epFile(t, 3, 1080, false, "", []string{"eng", "ukr"}, nil),
		// a second file of episode 3 does not count twice
		epFile(t, 3, 2160, true, "", []string{"rus", "eng", "ukr"}, []string{"rus"}),
		{ItemID: 4}, // not probed yet: not counted
	}
	v, ok := showFacts(files)
	if !ok {
		t.Fatal("no facts")
	}
	// 4K on 2 of 3 episodes; HDR 2/3; RU audio 2/3, EN 3/3, UK 1/3 (stray);
	// EN subtitles 2/3, RU 1/3 (stray)
	if got, want := v.badges(i18n.RU, nil), []string{"4K", "HDR", "Звук RU EN", "Субтитры EN"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	// «Мои языки» filter like a movie's
	if got, want := v.badges(i18n.RU, []string{"ru"}), []string{"4K", "HDR", "Звук RU"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mine: got %v want %v", got, want)
	}

	// half 4K, half 1080p: the tier a majority reaches is 1080p; Dolby
	// Vision on one of two is not a majority, but HDR-or-DV on both is HDR
	v, _ = showFacts([]library.File{
		epFile(t, 1, 2160, false, "dvh1", []string{"rus"}, nil),
		epFile(t, 2, 1080, true, "", []string{"eng"}, nil),
	})
	if got, want := v.badges(i18n.RU, nil), []string{"1080p", "HDR"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mixed: got %v want %v", got, want)
	}

	if _, ok := showFacts([]library.File{{ItemID: 1}}); ok {
		t.Error("nothing probed must give no facts")
	}
}

// The series card and title page (and a season's) carry the aggregated
// badges; a new probe refreshes the cached ones.
func TestSeriesBadgesAPI(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	var series, season int64
	var files []library.File
	for i := 1; i <= 3; i++ {
		p := filepath.Join(e.media, "s01e0"+itoa(int64(i))+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Сёгун", Season: 1, Episode: i, File: library.FileInput{Path: p, Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		fs, _ := e.repo.FilesOf(ctx, id)
		files = append(files, fs[0])
		ep, _ := e.repo.Item(ctx, id)
		s, _ := e.repo.Item(ctx, ep.ParentID)
		series, season = s.ParentID, s.ID
	}
	setProbe := func(f library.File, audio, subs []string) {
		pf := epFile(t, f.ItemID, 1080, false, "", audio, subs)
		if err := e.repo.SetProbe(ctx, f.ID, pf.ProbeJSON, 1000); err != nil {
			t.Fatal(err)
		}
	}
	setProbe(files[0], []string{"rus", "eng"}, []string{"eng"})
	setProbe(files[1], []string{"rus", "eng"}, []string{"eng", "rus"})

	badgesOf := func(path string) []string {
		w := e.do("GET", path, "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		var d ItemDetail
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d.Badges
	}
	// the third episode is not probed yet: RU subtitles on 1 of the 2
	// probed episodes are not a majority
	want := []string{"1080p", "Звук RU EN", "Субтитры EN"}
	if got := badgesOf("/api/items/" + itoa(series)); !reflect.DeepEqual(got, want) {
		t.Fatalf("series: got %v want %v", got, want)
	}
	if got := badgesOf("/api/items/" + itoa(season)); !reflect.DeepEqual(got, want) {
		t.Fatalf("season: got %v want %v", got, want)
	}
	w := e.do("GET", "/api/items?kind=series", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"badges":["1080p","Звук RU EN","Субтитры EN"]`) {
		t.Fatalf("grid: %d %s", w.Code, w.Body)
	}

	// the third episode is probed with RU subtitles: now 2 of 3
	setProbe(files[2], []string{"rus"}, []string{"rus"})
	want = []string{"1080p", "Звук RU EN", "Субтитры EN RU"}
	if got := badgesOf("/api/items/" + itoa(series)); !reflect.DeepEqual(got, want) {
		t.Fatalf("after probe: got %v want %v", got, want)
	}
}
