package jellyfin

import (
	"context"
	"testing"
	"time"
)

func ctx() context.Context { return context.Background() }

func TestEpisodeParentImages(t *testing.T) {
	e := newLibEnv(t)
	w := e.get(e.anyaTok, "/Items/"+ItemID(e.eps[0]))
	var ep BaseItemDto
	decode(t, w, &ep)
	if w.Code != 200 || ep.Type != "Episode" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, ok := ep.ImageTags["Primary"]; ok {
		t.Fatalf("episode without a still has a Primary tag: %v", ep.ImageTags)
	}
	if ep.SeriesPrimaryImageTag != imageTag(e.seriesPoster) || ep.SeriesId != ItemID(e.series) || ep.SeriesName != "Маша и Медведь" {
		t.Fatalf("series fields %+v", ep)
	}
	if ep.ParentBackdropItemId != ItemID(e.series) || len(ep.ParentBackdropImageTags) != 1 || ep.ParentBackdropImageTags[0] != imageTag(e.seriesBackdrop) {
		t.Fatalf("backdrop %+v", ep)
	}
	if ep.SeasonId != ItemID(e.season1) || ep.ParentId != ItemID(e.season1) || *ep.IndexNumber != 1 || *ep.ParentIndexNumber != 1 {
		t.Fatalf("season fields %+v", ep)
	}
	var se BaseItemDto
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.season1)), &se)
	if se.SeriesPrimaryImageTag != imageTag(e.seriesPoster) || se.ParentBackdropItemId != ItemID(e.series) || se.ParentId != ItemID(e.series) || !se.IsFolder {
		t.Fatalf("season %+v", se)
	}
	var sr BaseItemDto
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.series)), &sr)
	if sr.ImageTags["Primary"] != imageTag(e.seriesPoster) || len(sr.BackdropImageTags) != 1 || sr.PrimaryImageAspectRatio < 0.66 || sr.PrimaryImageAspectRatio > 0.67 {
		t.Fatalf("series %+v", sr)
	}
}

func TestSeasonsEpisodes(t *testing.T) {
	e := newLibEnv(t)
	p := e.items("", "/Shows/"+ItemID(e.series)+"/Seasons?userId="+UserID(e.anya.ID)+"&fields=ItemCounts")
	if len(p.Items) != 2 || *p.Items[0].IndexNumber != 1 || *p.Items[1].IndexNumber != 2 || p.Items[0].SeriesId != ItemID(e.series) {
		t.Fatalf("seasons %v", names(p.Items))
	}
	p = e.items("", "/Shows/"+ItemID(e.series)+"/Episodes?seasonId="+ItemID(e.season2))
	if len(p.Items) != 1 || p.Items[0].Id != ItemID(e.eps[3]) || *p.Items[0].ParentIndexNumber != 2 || p.Items[0].SeriesId != ItemID(e.series) {
		t.Fatalf("season 2 %v", names(p.Items))
	}
	p = e.items("", "/Shows/"+ItemID(e.series)+"/Episodes?Season=1")
	if len(p.Items) != 3 {
		t.Fatalf("season=1 %v", names(p.Items))
	}
	p = e.items("", "/Shows/"+ItemID(e.series)+"/Episodes")
	if len(p.Items) != 4 || p.TotalRecordCount != 4 {
		t.Fatalf("all %v", names(p.Items))
	}
	p = e.items("", "/Shows/"+ItemID(e.series)+"/Episodes?startItemId="+ItemID(e.eps[2])+"&limit=10")
	if len(p.Items) != 2 || p.Items[0].Id != ItemID(e.eps[2]) {
		t.Fatalf("startItemId %v", names(p.Items))
	}
	p = e.items("", "/Shows/"+ItemID(e.series)+"/Episodes?startIndex=1&limit=2")
	if len(p.Items) != 2 || p.Items[0].Id != ItemID(e.eps[1]) || p.TotalRecordCount != 4 || p.StartIndex != 1 {
		t.Fatalf("paging %v", names(p.Items))
	}
	// a season of another series: nothing
	if w := e.get(e.anyaTok, "/Shows/"+ItemID(e.interstellar)+"/Seasons"); w.Code != 404 {
		t.Fatalf("movie seasons: %d", w.Code)
	}
}

func TestNextUp(t *testing.T) {
	e := newLibEnv(t)
	e.prog.SaveAt(ctx(), e.anya.ID, e.eps[0], 600_000, 600_000, time.Now().Add(-time.Minute))
	for _, path := range []string{"/Shows/NextUp?userId=" + UserID(e.anya.ID) + "&limit=20&enableResumable=false",
		"/Shows/NextUp?seriesId=" + ItemID(e.series)} {
		p := e.items("", path)
		if len(p.Items) != 1 || p.Items[0].Id != ItemID(e.eps[1]) || p.Items[0].Type != "Episode" {
			t.Fatalf("%s: %v", path, names(p.Items))
		}
	}
	if p := e.items("", "/Shows/NextUp?seriesId="+ItemID(e.interstellar)); len(p.Items) != 0 {
		t.Fatalf("other series %v", names(p.Items))
	}
}
