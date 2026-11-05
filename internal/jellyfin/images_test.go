package jellyfin

import (
	"image"
	"testing"
)

func TestImagesPrimaryAndTag(t *testing.T) {
	e := newLibEnv(t)
	var it BaseItemDto
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.interstellar)), &it)
	tag := it.ImageTags["Primary"]
	if tag == "" || tag != imageTag(e.posterRef) || len(tag) != 16 {
		t.Fatalf("tag %v", it.ImageTags)
	}
	path := "/Items/" + ItemID(e.interstellar) + "/Images/Primary?tag=" + tag
	w := e.get(e.anyaTok, path)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Cache-Control") != "public, max-age=31536000" {
		t.Fatalf("GET %d %v", w.Code, w.Header())
	}
	full := w.Body.Len()
	h := e.do("HEAD", path, swiftfinHdr(e.anyaTok), nil)
	if h.Code != 200 || h.Body.Len() != 0 {
		t.Fatalf("HEAD %d %d", h.Code, h.Body.Len())
	}
	// resized: maxWidth (Swiftfin), fillHeight (Infuse)
	w = e.get(e.anyaTok, path+"&maxWidth=200&quality=90")
	c, _, err := image.DecodeConfig(w.Body)
	if w.Code != 200 || err != nil || c.Width != 200 || c.Height != 300 {
		t.Fatalf("maxWidth %d %v %+v", w.Code, err, c)
	}
	w = e.get(e.anyaTok, "/items/"+ItemID(e.interstellar)+"/images/primary/0?fillHeight=150&fillWidth=100")
	c, _, err = image.DecodeConfig(w.Body)
	if w.Code != 200 || err != nil || c.Height != 150 {
		t.Fatalf("fill %d %v %+v (orig %d)", w.Code, err, c, full)
	}
	for _, p := range []string{
		"/Items/" + ItemID(e.interstellar) + "/Images/Backdrop", // no backdrop
		"/Items/" + ItemID(e.interstellar) + "/Images/Banner",
		"/Items/" + ItemID(e.eps[0]) + "/Images/Primary", // no still
		"/Items/" + ViewID("movies") + "/Images/Primary",
		"/Items/" + UserID(1) + "/Images/Primary",
		"/Users/" + UserID(e.anya.ID) + "/Images/Primary",
		"/UserImage?userId=" + UserID(e.anya.ID),
	} {
		if w := e.get(e.anyaTok, p); w.Code != 404 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/Items/" + ItemID(e.series) + "/Images/Backdrop/0", "/Items/" + ItemID(e.series) + "/Images/Thumb"} {
		if w := e.get(e.anyaTok, p); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

// Swiftfin loads images without any token (client.url(with:) adds no
// ApiKey, the image session no headers): the tag the item carried is the
// proof that the client saw the item.
func TestImagesWithoutToken(t *testing.T) {
	e := newLibEnv(t)
	base := "/Items/" + ItemID(e.interstellar) + "/Images/Primary"
	if w := e.do("GET", base+"?tag="+imageTag(e.posterRef)+"&maxWidth=300", nil, nil); w.Code != 200 {
		t.Fatalf("with tag: %d", w.Code)
	}
	for _, q := range []string{"", "?tag=0000000000000000", "?Tag=" + imageTag(e.seriesPoster)} {
		if w := e.do("GET", base+q, nil, nil); w.Code != 404 {
			t.Fatalf("%q: %d", q, w.Code)
		}
	}
	// a token that is not valid: 401, as everywhere
	if w := e.get("deadbeef", base+"?tag="+imageTag(e.posterRef)); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
}
