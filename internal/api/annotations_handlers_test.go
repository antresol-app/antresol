package api

import (
	"encoding/json"
	"strings"
	"testing"
)

type annotationOut struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Locator   string  `json:"locator"`
	Fraction  float64 `json:"fraction"`
	Label     string  `json:"label"`
	Excerpt   string  `json:"excerpt"`
	Note      string  `json:"note"`
	Color     string  `json:"color"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Rev       int64   `json:"rev"`
	Deleted   bool    `json:"deleted"`
}

func (e *libEnv) item(kind, title string) int64 {
	e.t.Helper()
	res, err := e.db.Exec(`INSERT INTO items(kind,title) VALUES(?,?)`, kind, title)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func decodeAnnotation(t *testing.T, body []byte) annotationOut {
	t.Helper()
	var a annotationOut
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return a
}

func TestAnnotationsAPI(t *testing.T) {
	e := newLibEnv(t)
	book := e.item("book", "Мастер и Маргарита")
	base := "/api/annotations/" + itoa(book)

	// create with the client's id; the device's time (a year ahead) is a hint, the server's is stored
	body := `{"id":"h-1","kind":"highlight","locator":"epubcfi(/6/4!/4/2,/1:0,/1:5)","fraction":0.3,"label":"Глава 1","excerpt":"- Никогда не разговаривайте","color":"green","updatedAt":"2027-10-25T19:30:00.123Z"}`
	w := e.do("PUT", base+"/h-1", body, e.admin)
	if w.Code != 200 {
		t.Fatalf("put %d %s", w.Code, w.Body)
	}
	h1 := decodeAnnotation(t, w.Body.Bytes())
	if h1.ID != "h-1" || h1.Color != "green" || h1.Rev != 1 || strings.HasPrefix(h1.UpdatedAt, "2027") || h1.CreatedAt != h1.UpdatedAt {
		t.Fatalf("put %+v", h1)
	}
	// an offline queue sends it again: one row, unchanged
	if again := decodeAnnotation(t, e.do("PUT", base+"/h-1", body, e.admin).Body.Bytes()); again.Rev != 1 {
		t.Fatalf("resent %+v", again)
	}
	// POST without an id: the server makes one
	w = e.do("POST", base, `{"kind":"bookmark","locator":"epubcfi(/6/8!/4/2/1:0)","fraction":0.6,"note":"тут"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("post %d %s", w.Code, w.Body)
	}
	bm := decodeAnnotation(t, w.Body.Bytes())
	if bm.ID == "" || bm.Kind != "bookmark" || bm.Note != "тут" || bm.Color != "" {
		t.Fatalf("post %+v", bm)
	}
	// the server says what is wrong, in the request's language
	w = e.do("PUT", base+"/x", `{"kind":"highlight","locator":"epubcfi(/6/4)","color":"purple"}`, e.admin, "Accept-Language", "en")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Invalid highlight color") {
		t.Fatalf("bad color %d %s", w.Code, w.Body)
	}
	// a note on the highlight, made on its revision
	w = e.do("PUT", base+"/h-1", `{"kind":"note","locator":"epubcfi(/6/4!/4/2,/1:0,/1:5)","fraction":0.3,"excerpt":"- Никогда не разговаривайте","note":"1. с неизвестными\n> цитата","color":"green","rev":1}`, e.admin)
	if n := decodeAnnotation(t, w.Body.Bytes()); n.Kind != "note" || n.Rev <= bm.Rev {
		t.Fatalf("note %d %s", w.Code, w.Body)
	}
	// line starts that would open a list or a quote stay text
	if w := e.do("GET", base+"/export.md", "", e.admin); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "# Мастер и Маргарита") ||
		!strings.Contains(w.Body.String(), "> \\- Никогда не разговаривайте") ||
		!strings.Contains(w.Body.String(), "1\\. с неизвестными  \n\\> цитата") ||
		!strings.Contains(w.Body.String(), "тут") ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export %d %v %s", w.Code, w.Header(), w.Body)
	}
	// a delete wins; an edit made on the revision before it does not bring it back
	if w := e.do("DELETE", base+"/h-1", "", e.admin); w.Code != 200 || !decodeAnnotation(t, w.Body.Bytes()).Deleted {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", base+"/h-1", body, e.admin); w.Code != 200 || !decodeAnnotation(t, w.Body.Bytes()).Deleted {
		t.Fatalf("resurrected %d %s", w.Code, w.Body)
	}
	w = e.do("GET", base, "", e.admin)
	var list struct {
		Annotations []annotationOut `json:"annotations"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Annotations) != 2 || list.Annotations[0].ID != "h-1" || !list.Annotations[0].Deleted || list.Annotations[1].ID != bm.ID {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", base+"/export.md", "", e.admin); strings.Contains(w.Body.String(), "Никогда") {
		t.Fatalf("export shows a deleted highlight: %s", w.Body)
	}

	// each profile has its own
	kid := e.kid("")
	if w := e.do("GET", base, "", kid); w.Code != 200 || strings.Contains(w.Body.String(), "h-1") {
		t.Fatalf("kid list %d %s", w.Code, w.Body)
	}

	// an audiobook keeps bookmarks at ms:N; a highlight there makes no sense
	ab := e.item("audiobook", "Аудио")
	if w := e.do("PUT", "/api/annotations/"+itoa(ab)+"/a1", `{"kind":"bookmark","locator":"ms:61000","note":"важно"}`, e.admin); w.Code != 200 {
		t.Fatalf("audiobook bookmark %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/annotations/"+itoa(ab)+"/a2", `{"kind":"highlight","locator":"ms:61000"}`, e.admin); w.Code != 400 {
		t.Fatalf("audiobook highlight %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/annotations/"+itoa(ab)+"/a3", `{"kind":"bookmark","locator":"page:2"}`, e.admin); w.Code != 400 {
		t.Fatalf("audiobook page locator %d %s", w.Code, w.Body)
	}
	movie, _ := e.movie("Брат", "16+", "x")
	if w := e.do("PUT", "/api/annotations/"+itoa(movie)+"/m1", `{"kind":"bookmark","locator":"ms:1"}`, e.admin); w.Code != 400 {
		t.Fatalf("movie %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", base, "", nil); w.Code != 401 {
		t.Fatalf("anon %d", w.Code)
	}
	if w := e.do("GET", "/api/annotations/999999", "", e.admin); w.Code != 404 {
		t.Fatalf("unknown %d", w.Code)
	}
}
