package jellyfin

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestItemsPaging(t *testing.T) {
	e := newLibEnv(t)
	for i := 0; i < 22; i++ { // 3 movies already: 25
		e.movie(fmt.Sprintf("Фильм %02d", i), 2000+i, "12+", 0, fmt.Sprintf("m%02d.mkv", i), "")
	}
	for _, q := range []string{"startIndex=20&limit=10", "StartIndex=20&Limit=10"} {
		p := e.items("", "/Items?includeItemTypes=Movie&recursive=true&sortBy=SortName&"+q)
		if len(p.Items) != 5 || p.TotalRecordCount != 25 || p.StartIndex != 20 {
			t.Fatalf("%s: %d items, total %d, start %d", q, len(p.Items), p.TotalRecordCount, p.StartIndex)
		}
	}
	if p := e.items("", "/Items?includeItemTypes=Movie&recursive=true&startIndex=40&limit=10"); len(p.Items) != 0 || p.TotalRecordCount != 25 {
		t.Fatalf("past the end: %+v", p)
	}
}

func TestQueryKeysCaseInsensitive(t *testing.T) {
	e := newLibEnv(t)
	a := e.items("", "/Items?IncludeItemTypes=Movie&Recursive=true")
	b := e.items("", "/items?includeItemTypes=Movie&recursive=true")
	if !reflect.DeepEqual(names(a.Items), names(b.Items)) || a.TotalRecordCount != 3 {
		t.Fatalf("%v vs %v", names(a.Items), names(b.Items))
	}
	// Infuse: legacy path, Emby header
	w := e.do("GET", "/Users/"+UserID(e.anya.ID)+"/Items?IncludeItemTypes=Movie&Recursive=true", infuseHdr(e.anyaTok), nil)
	var c itemsPage
	decode(t, w, &c)
	if w.Code != 200 || !reflect.DeepEqual(names(c.Items), names(a.Items)) {
		t.Fatalf("legacy: %d %v", w.Code, names(c.Items))
	}
}

func TestItemsSortDateCreatedDesc(t *testing.T) {
	e := newLibEnv(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.addedAt(e.cartoon, base)
	e.addedAt(e.interstellar, base.Add(time.Hour))
	e.addedAt(e.deadpool, base.Add(2*time.Hour))
	p := e.items("", "/Items?includeItemTypes=Movie&recursive=true&sortBy=DateCreated,SortName&sortOrder=Descending")
	if got := names(p.Items); !reflect.DeepEqual(got, []string{"Дэдпул", "Интерстеллар", "Мультик"}) {
		t.Fatalf("%v", got)
	}
	p = e.items("", "/Items?includeItemTypes=Movie&recursive=true&sortBy=ProductionYear&sortOrder=Ascending")
	if got := names(p.Items); !reflect.DeepEqual(got, []string{"Мультик", "Интерстеллар", "Дэдпул"}) {
		t.Fatalf("year: %v", got)
	}
}

func TestItemsIncludeTypesRecursive(t *testing.T) {
	e := newLibEnv(t)
	p := e.items("", "/Items?includeItemTypes=Movie,Series&Recursive=true")
	types := map[string]int{}
	for _, it := range p.Items {
		types[it.Type]++
	}
	if types["Movie"] != 3 || types["Series"] != 1 || len(types) != 2 || p.TotalRecordCount != 4 {
		t.Fatalf("%v total %d", types, p.TotalRecordCount)
	}
	p = e.items("", "/Items?includeItemTypes=Movie,Series&excludeItemTypes=Movie&recursive=true")
	if len(p.Items) != 1 || p.Items[0].Type != "Series" {
		t.Fatalf("exclude: %v", names(p.Items))
	}
	p = e.items("", "/Items?includeItemTypes=Episode&recursive=true")
	if len(p.Items) != 4 || p.Items[0].Type != "Episode" {
		t.Fatalf("episodes: %v", names(p.Items))
	}
}

func TestItemsParentView(t *testing.T) {
	e := newLibEnv(t)
	p := e.items("", "/Items?parentId="+ViewID("movies")+"&sortBy=SortName")
	for _, it := range p.Items {
		if it.Type != "Movie" {
			t.Fatalf("%s in movies", it.Type)
		}
	}
	if p.TotalRecordCount != 3 {
		t.Fatalf("movies: %v", names(p.Items))
	}
	p = e.items("", "/Items?parentId="+ViewID("tvshows"))
	if len(p.Items) != 1 || p.Items[0].Id != ItemID(e.series) {
		t.Fatalf("tvshows: %v", names(p.Items))
	}
	p = e.items("", "/Items?parentId="+ItemID(e.series))
	if len(p.Items) != 2 || p.Items[0].Type != "Season" {
		t.Fatalf("series: %v", names(p.Items))
	}
	p = e.items("", "/Items?parentId="+ItemID(e.season1))
	if len(p.Items) != 3 || p.Items[2].Id != ItemID(e.eps[2]) {
		t.Fatalf("season: %v", names(p.Items))
	}
}

func TestItemByViewID(t *testing.T) {
	e := newLibEnv(t)
	for _, path := range []string{"/Items/" + ViewID("tvshows"), "/Users/" + UserID(e.anya.ID) + "/Items/" + ViewID("tvshows")} {
		w := e.get(e.anyaTok, path)
		var it BaseItemDto
		decode(t, w, &it)
		if w.Code != 200 || it.Type != "CollectionFolder" || it.Name != "Сериалы" || it.CollectionType != "tvshows" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestItemsIDs(t *testing.T) {
	e := newLibEnv(t)
	p := e.items("", "/Items?ids="+ItemID(e.cartoon)+","+ItemID(e.interstellar))
	if len(p.Items) != 2 || p.Items[0].Id != ItemID(e.cartoon) || p.Items[1].Id != ItemID(e.interstellar) {
		t.Fatalf("%v", names(p.Items))
	}
	// dashed ids too
	d := ItemID(e.deadpool)
	d = d[:8] + "-" + d[8:12] + "-" + d[12:16] + "-" + d[16:20] + "-" + d[20:]
	if p := e.items("", "/Items?Ids="+d); len(p.Items) != 1 || p.Items[0].Id != ItemID(e.deadpool) {
		t.Fatalf("dashed: %v", names(p.Items))
	}
}

func TestItemsFilters(t *testing.T) {
	e := newLibEnv(t)
	uid := e.anya.ID
	at := time.Now().Add(-time.Hour)
	e.prog.SaveAt(ctx(), uid, e.interstellar, 4_000_000, 10_140_000, at) // resumable
	e.prog.SaveAt(ctx(), uid, e.deadpool, 6_000_000, 6_000_000, at)      // watched
	e.prog.SaveAt(ctx(), uid, e.cartoon, 0, 5_000_000, at)               // opened, not started
	e.prog.AddToWatchlist(ctx(), uid, e.cartoon)

	q := "/Items?includeItemTypes=Movie,Series&recursive=true&sortBy=SortName&filters="
	resumable := e.items("", q+"IsResumable")
	// the Resume shelf rule
	shelf := e.items("", "/UserItems/Resume?userId="+UserID(uid))
	if got := names(resumable.Items); !reflect.DeepEqual(got, []string{"Интерстеллар"}) || !reflect.DeepEqual(got, names(shelf.Items)) {
		t.Fatalf("IsResumable %v, Resume %v", got, names(shelf.Items))
	}
	if got := names(e.items("", q+"IsPlayed").Items); !reflect.DeepEqual(got, []string{"Дэдпул"}) {
		t.Fatalf("IsPlayed %v", got)
	}
	if got := names(e.items("", q+"IsUnplayed").Items); len(got) != 3 {
		t.Fatalf("IsUnplayed %v", got)
	}
	if got := names(e.items("", q+"IsFavorite").Items); !reflect.DeepEqual(got, []string{"Мультик"}) {
		t.Fatalf("IsFavorite %v", got)
	}
	if p := e.items("", q+"IsFolder"); len(p.Items) != 1 || p.Items[0].Type != "Series" || p.TotalRecordCount != 1 {
		t.Fatalf("IsFolder %v", names(p.Items))
	}
	if p := e.items("", q+"IsNotFolder"); len(p.Items) != 3 || p.Items[0].Type != "Movie" {
		t.Fatalf("IsNotFolder %v", names(p.Items))
	}
	if got := names(e.items("", "/Items?includeItemTypes=Movie&recursive=true&sortBy=DatePlayed&sortOrder=Descending&filters=IsPlayed,IsFavorite").Items); len(got) != 0 {
		t.Fatalf("IsPlayed+IsFavorite %v", got)
	}
}

func TestItemsKidHidden(t *testing.T) {
	e := newLibEnv(t)
	// Миша (6+) has the 18+ and 12+ films in progress too: still hidden
	for _, id := range []int64{e.deadpool, e.interstellar, e.cartoon} {
		e.prog.SaveAt(ctx(), e.misha.ID, id, 1_000_000, 5_000_000, time.Now().Add(-time.Hour))
	}
	e.prog.SaveAt(ctx(), e.misha.ID, e.eps[0], 600_000, 600_000, time.Now().Add(-time.Minute))
	hidden := ItemID(e.deadpool)

	p := e.items(e.mishaTok, "/Items?includeItemTypes=Movie,Series&recursive=true")
	if has(p.Items, hidden) || has(p.Items, ItemID(e.interstellar)) || p.TotalRecordCount != 2 || len(p.Items) != 2 {
		t.Fatalf("kid list %v total %d", names(p.Items), p.TotalRecordCount)
	}
	if p := e.items(e.mishaTok, "/Items?ids="+hidden+","+ItemID(e.cartoon)); len(p.Items) != 1 || p.TotalRecordCount != 1 {
		t.Fatalf("kid ids %v", names(p.Items))
	}
	if p := e.items(e.mishaTok, "/Items?parentId="+ViewID("movies")); p.TotalRecordCount != 1 {
		t.Fatalf("kid view %v", names(p.Items))
	}
	for _, path := range []string{"/Items/" + hidden, "/Users/" + UserID(e.misha.ID) + "/Items/" + hidden,
		"/Items/" + hidden + "/Images/Primary", "/Items/" + ItemID(e.interstellar) + "/Images/Primary",
		"/Items/" + ItemID(e.interstellar) + "/Images/Primary?tag=" + imageTag(e.posterRef)} {
		if w := e.get(e.mishaTok, path); w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if p := e.items(e.mishaTok, "/UserItems/Resume"); len(p.Items) != 1 || p.Items[0].Id != ItemID(e.cartoon) {
		t.Fatalf("kid resume %v", names(p.Items))
	}
	if p := e.items(e.mishaTok, "/Shows/NextUp"); len(p.Items) != 1 || p.Items[0].Id != ItemID(e.eps[1]) {
		t.Fatalf("kid nextup %v", names(p.Items))
	}
	var latest []BaseItemDto
	decode(t, e.get(e.mishaTok, "/Items/Latest?parentId="+ViewID("movies")), &latest)
	if has(latest, hidden) || len(latest) != 1 {
		t.Fatalf("kid latest %v", names(latest))
	}
	var hints searchHintsResult
	decode(t, e.get(e.mishaTok, "/Search/Hints?searchTerm="+url.QueryEscape("Дэдпул")), &hints)
	if len(hints.SearchHints) != 0 || hints.TotalRecordCount != 0 {
		t.Fatalf("kid hints %+v", hints)
	}
	if p := e.items(e.mishaTok, "/Items?searchTerm="+url.QueryEscape("Интерстеллар")+"&recursive=true"); len(p.Items) != 0 {
		t.Fatalf("kid search %v", names(p.Items))
	}
	// the adult sees them
	if w := e.get(e.anyaTok, "/Items/"+hidden); w.Code != 200 {
		t.Fatalf("adult: %d", w.Code)
	}
}

var jfDate = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

func TestItemDates(t *testing.T) {
	e := newLibEnv(t)
	e.prog.SaveAt(ctx(), e.anya.ID, e.interstellar, 4_000_000, 10_140_000, time.Now().Add(-time.Hour))
	w := e.get(e.anyaTok, "/Items/"+ItemID(e.interstellar))
	var it BaseItemDto
	decode(t, w, &it)
	if !jfDate.MatchString(it.DateCreated) || !jfDate.MatchString(it.PremiereDate) || it.UserData == nil || !jfDate.MatchString(it.UserData.LastPlayedDate) {
		t.Fatalf("dates %q %q %+v", it.DateCreated, it.PremiereDate, it.UserData)
	}
	if !strings.HasPrefix(it.PremiereDate, "2014-") || it.ProductionYear != 2014 || it.ServerId != e.s.sid() {
		t.Fatalf("year %+v", it)
	}
}

func TestItemFullHasMediaSources(t *testing.T) {
	e := newLibEnv(t)
	w := e.get(e.anyaTok, "/Items/"+ItemID(e.interstellar)+"?userId="+UserID(e.anya.ID))
	var it BaseItemDto
	decode(t, w, &it)
	if w.Code != 200 || len(it.MediaSources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ms := it.MediaSources[0]
	if ms.Path != "Interstellar.2014.mkv" || ms.Container != "mkv" || ms.Id == "" || ms.ETag == "" || ms.Protocol != "File" ||
		ms.Size != 4_000_000_000 || ms.RunTimeTicks != 10_140_000*10_000 || !ms.SupportsDirectPlay || !ms.SupportsTranscoding {
		t.Fatalf("source %+v", ms)
	}
	if it.ProviderIds["Tmdb"] != "157336" || it.Path != "Interstellar.2014.mkv" || it.UserData == nil || it.RunTimeTicks != 10_140_000*10_000 {
		t.Fatalf("item %+v", it)
	}
	var audio, pgs, srt *MediaStream
	for i, s := range ms.MediaStreams {
		switch {
		case s.Type == "Audio" && s.Index == 1:
			audio = &ms.MediaStreams[i]
		case s.Type == "Subtitle" && s.Index == 4:
			pgs = &ms.MediaStreams[i]
		case s.Type == "Subtitle" && s.Index == 3:
			srt = &ms.MediaStreams[i]
		}
	}
	if audio == nil || audio.Language != "rus" || audio.Channels != 6 || !audio.IsDefault {
		t.Fatalf("audio %+v", audio)
	}
	if pgs == nil || pgs.IsTextSubtitleStream || srt == nil || !srt.IsTextSubtitleStream || pgs.Language != "eng" {
		t.Fatalf("subs %+v %+v", pgs, srt)
	}
	for _, s := range ms.MediaStreams {
		if s.Type == "Audio" && s.Index == 2 && s.Language != "eng" {
			t.Fatalf("639-1 → 639-2: %q", s.Language)
		}
	}
	if len(it.MediaStreams) != len(ms.MediaStreams) {
		t.Fatalf("item MediaStreams %d", len(it.MediaStreams))
	}
	// lists: media sources only when asked
	p := e.items("", "/Items?ids="+ItemID(e.interstellar))
	if len(p.Items[0].MediaSources) != 0 {
		t.Fatalf("list without fields has sources")
	}
	p = e.items("", "/Items?ids="+ItemID(e.interstellar)+"&fields=MediaSources,MediaStreams,Genres")
	if len(p.Items[0].MediaSources) != 1 || len(p.Items[0].Genres) != 2 {
		t.Fatalf("fields: %+v", p.Items[0])
	}
}

func TestItemsFiltersEndpointAndGenres(t *testing.T) {
	e := newLibEnv(t)
	w := e.get(e.anyaTok, "/Items/Filters?parentId="+ViewID("movies"))
	var f struct {
		Genres, Tags, OfficialRatings []string
		Years                         []int
	}
	decode(t, w, &f)
	if w.Code != 200 || len(f.Genres) != 2 || f.Tags == nil || len(f.Years) != 3 {
		t.Fatalf("%s", w.Body)
	}
	var g struct {
		Items []struct{ Name, Id, Type string }
	}
	decode(t, e.get(e.anyaTok, "/Genres?parentId="+ViewID("movies")), &g)
	if len(g.Items) != 2 || g.Items[0].Type != "Genre" || g.Items[0].Id == "" {
		t.Fatalf("genres %+v", g)
	}
	decode(t, e.get(e.mishaTok, "/Genres"), &g)
	if len(g.Items) != 0 {
		t.Fatalf("kid sees genres of hidden films: %+v", g)
	}
}

func TestOptionalListsEmpty(t *testing.T) {
	e := newLibEnv(t)
	id := ItemID(e.interstellar)
	for path, want := range map[string]string{
		"/Items/" + id + "/Similar":         `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`,
		"/Items/" + id + "/Intros":          `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`,
		"/MediaSegments/" + id:              `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`,
		"/Persons?searchTerm=x":             `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`,
		"/Studios":                          `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`,
		"/Items/" + id + "/SpecialFeatures": `[]`,
		"/Items/" + id + "/LocalTrailers":   `[]`,
	} {
		if w := e.get(e.anyaTok, path); w.Code != 200 || w.Body.String() != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	// an unknown view: empty, not a crash
	unknown := guid(kindView, 7)
	if p := e.items("", "/Items?parentId="+unknown); len(p.Items) != 0 {
		t.Fatalf("unknown view %v", names(p.Items))
	}
	if w := e.get(e.anyaTok, "/Items/Latest?parentId="+unknown); w.Code != 200 || w.Body.String() != "[]" {
		t.Fatalf("unknown view latest %d %s", w.Code, w.Body)
	}
	if p := e.items("", "/Items?parentId="+unknown+"&includeItemTypes=Movie"); len(p.Items) != 0 {
		t.Fatalf("unknown view+types %v", names(p.Items))
	}
	if w := e.get("", "/Items"); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestResumeShelf(t *testing.T) {
	e := newLibEnv(t)
	uid := e.anya.ID
	pos := int64(4_056_000) // 40 %
	e.prog.SaveAt(ctx(), uid, e.interstellar, pos, 10_140_000, time.Now().Add(-time.Hour))
	e.prog.SaveAt(ctx(), uid, e.deadpool, 6_000_000, 6_000_000, time.Now().Add(-time.Minute))
	for _, path := range []string{"/UserItems/Resume?userId=" + UserID(uid) + "&mediaTypes=Video&limit=20",
		"/Users/" + UserID(uid) + "/Items/Resume?Limit=20"} {
		p := e.items("", path)
		if len(p.Items) != 1 || p.Items[0].Id != ItemID(e.interstellar) || p.TotalRecordCount != 1 {
			t.Fatalf("%s: %v", path, names(p.Items))
		}
		ud := p.Items[0].UserData
		if ud == nil || ud.PlaybackPositionTicks != pos*10_000 || ud.Played || ud.PlayedPercentage == nil || int(*ud.PlayedPercentage) != 40 {
			t.Fatalf("userdata %+v", ud)
		}
	}
	if p := e.items("", "/UserItems/Resume?includeItemTypes=Episode"); len(p.Items) != 0 {
		t.Fatalf("episodes only: %v", names(p.Items))
	}
}

func TestLatestBareArray(t *testing.T) {
	e := newLibEnv(t)
	for _, path := range []string{"/Items/Latest?parentId=" + ViewID("tvshows") + "&groupItems=true&limit=16",
		"/Users/" + UserID(e.anya.ID) + "/Items/Latest?ParentId=" + ViewID("tvshows")} {
		w := e.get(e.anyaTok, path)
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "[") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		var out []BaseItemDto
		decode(t, w, &out)
		if len(out) != 1 || out[0].Type != "Series" {
			t.Fatalf("tvshows latest %v", names(out))
		}
	}
	var out []BaseItemDto
	decode(t, e.get(e.anyaTok, "/Items/Latest?parentId="+ViewID("movies")+"&limit=2"), &out)
	if len(out) != 2 || out[0].Type != "Movie" {
		t.Fatalf("movies latest %v", names(out))
	}
	decode(t, e.get(e.anyaTok, "/Items/Latest"), &out)
	if len(out) != 4 {
		t.Fatalf("all latest %v", names(out))
	}
}

func TestUnplayedItemCount(t *testing.T) {
	e := newLibEnv(t)
	e.prog.MarkWatched(ctx(), e.anya.ID, e.eps[0], true)
	w := e.get(e.anyaTok, "/Items/"+ItemID(e.season1))
	var season BaseItemDto
	decode(t, w, &season)
	if season.UserData == nil || season.UserData.UnplayedItemCount == nil || *season.UserData.UnplayedItemCount != 2 || season.UserData.Played {
		t.Fatalf("season %+v", season.UserData)
	}
	var series BaseItemDto
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.series)), &series)
	if series.UserData == nil || series.UserData.UnplayedItemCount == nil || *series.UserData.UnplayedItemCount != 3 {
		t.Fatalf("series %+v", series.UserData)
	}
	if series.ChildCount == nil || *series.ChildCount != 2 || series.RecursiveItemCount == nil || *series.RecursiveItemCount != 4 {
		t.Fatalf("counts %+v", series)
	}
	for _, ep := range e.eps[1:] {
		e.prog.MarkWatched(ctx(), e.anya.ID, ep, true)
	}
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.series)), &series)
	if *series.UserData.UnplayedItemCount != 0 || !series.UserData.Played {
		t.Fatalf("all watched %+v", series.UserData)
	}
	if p := e.items("", "/Items?includeItemTypes=Series&recursive=true&filters=IsPlayed"); len(p.Items) != 1 {
		t.Fatalf("series IsPlayed %v", names(p.Items))
	}
}
