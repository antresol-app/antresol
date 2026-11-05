package jellyfin

import (
	"net/url"
	"testing"
)

func TestSearchHints(t *testing.T) {
	e := newLibEnv(t)
	w := e.get(e.anyaTok, "/Search/Hints?searchTerm="+url.QueryEscape("интер")+"&limit=10&userId="+UserID(e.anya.ID))
	var out searchHintsResult
	decode(t, w, &out)
	if w.Code != 200 || len(out.SearchHints) != 1 || out.TotalRecordCount != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	h := out.SearchHints[0]
	if h.ItemId != ItemID(e.interstellar) || h.Id != h.ItemId || h.Name != "Интерстеллар" || h.Type != "Movie" ||
		h.ProductionYear != 2014 || h.PrimaryImageTag != imageTag(e.posterRef) || h.MediaType != "Video" || h.RunTimeTicks != 10_140_000*10_000 {
		t.Fatalf("hint %+v", h)
	}
	decode(t, e.get(e.anyaTok, "/Search/Hints?searchTerm="+url.QueryEscape("маша")), &out)
	if len(out.SearchHints) != 1 || out.SearchHints[0].Type != "Series" {
		t.Fatalf("series %+v", out)
	}
	if w := e.get(e.anyaTok, "/Search/Hints"); w.Code != 200 || w.Body.String() != `{"SearchHints":[],"TotalRecordCount":0}` {
		t.Fatalf("empty %s", w.Body)
	}
	// Swiftfin: /Items?searchTerm, one call per type
	p := e.items("", "/Items?searchTerm="+url.QueryEscape("Интерстеллар")+"&includeItemTypes=Movie&isRecursive=true&limit=20")
	if len(p.Items) != 1 || p.Items[0].Id != ItemID(e.interstellar) {
		t.Fatalf("items search %v", names(p.Items))
	}
	if p := e.items("", "/Items?searchTerm="+url.QueryEscape("Интерстеллар")+"&includeItemTypes=Series&isRecursive=true"); len(p.Items) != 0 {
		t.Fatalf("series search %v", names(p.Items))
	}
}
