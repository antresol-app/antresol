package api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
)

// F15: the series page carries its TMDB id so missing seasons can be offered.
func TestSeriesDetailHasTmdbID(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	epID, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Разделение", SeriesTmdbID: 95396, Season: 1, Episode: 1,
		File: library.FileInput{Path: e.media + "/s.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := e.repo.Item(ctx, epID)
	season, _ := e.repo.Item(ctx, ep.ParentID)
	w := e.do("GET", "/api/items/"+itoa(season.ParentID), "", e.admin)
	var d ItemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d.TmdbID != 95396 || d.Kind != library.KindSeries {
		t.Fatalf("tmdbId %d kind %s", d.TmdbID, d.Kind)
	}
}

// F22: shutdown closes the bus, and every open /api/events stream ends.
func TestEventsEndOnBusClose(t *testing.T) {
	bus := &events.Bus{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Events = bus })
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.AddCookie(e.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for bus.Subscribers() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	bus.Close()
	done := make(chan struct{})
	go func() {
		r := bufio.NewReader(resp.Body)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("event stream still open after the bus closed")
	}
}
