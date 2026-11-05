package progress

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const hl = "epubcfi(/6/4!/4/2,/1:0,/1:5)"

func TestAnnotationsServerOrder(t *testing.T) {
	e := newEnv(t)
	u, other, b := e.user("a"), e.user("b"), e.movie("Книга")
	put := func(user int64, a Annotation) Annotation {
		t.Helper()
		got, err := e.s.PutAnnotation(ctx, user, b, a)
		e.must(err)
		return got
	}
	// the device's clock is a day ahead: the server's time is stored, and the next revision
	ahead := e.clock.Add(24 * time.Hour)
	got := put(u, Annotation{ID: "h1", Kind: KindHighlight, Locator: hl, Fraction: 0.2, Excerpt: "Слово", Color: "green", UpdatedAt: ahead})
	if got.Rev != 1 || got.UpdatedAt.After(e.clock) || !got.CreatedAt.Equal(got.UpdatedAt) || got.Deleted {
		t.Fatalf("created %+v", got)
	}
	// an edit made on the latest revision applies, whatever its device time
	behind := e.clock.Add(-48 * time.Hour)
	got = put(u, Annotation{ID: "h1", Rev: 1, Kind: KindNote, Locator: hl, Note: "мысль", Color: "blue", UpdatedAt: behind})
	if got.Rev != 2 || got.Kind != KindNote || got.Note != "мысль" || got.Color != "blue" {
		t.Fatalf("edit on the latest revision %+v", got)
	}
	created := got.CreatedAt
	// an edit made on an older revision (an offline device) over a newer edit: by its device time, a hint
	if got = put(u, Annotation{ID: "h1", Rev: 1, Kind: KindNote, Locator: hl, Note: "старое", Color: "blue", UpdatedAt: behind.Add(-time.Hour)}); got.Note != "мысль" || got.Rev != 2 {
		t.Fatalf("stale edit applied %+v", got)
	}
	// a delete from a device whose clock is far behind still wins: it is the latest write
	del := put(u, Annotation{ID: "h1", Rev: 1, Kind: KindNote, Locator: hl, Deleted: true, UpdatedAt: behind.Add(-30 * 24 * time.Hour)})
	if !del.Deleted || del.Note != "" || del.Excerpt != "" || del.Rev != 3 || !del.CreatedAt.Equal(created) {
		t.Fatalf("tombstone %+v", del)
	}
	// an edit made before the delete (on its revision or older), even one with a clock ahead, does not bring it back
	for _, base := range []int64{2, 0} {
		if got = put(u, Annotation{ID: "h1", Rev: base, Kind: KindNote, Locator: hl, Note: "ожило", Color: "blue", UpdatedAt: ahead}); !got.Deleted || got.Rev != 3 {
			t.Fatalf("resurrected from rev %d: %+v", base, got)
		}
	}
	// DeleteAnnotation: a tombstone, also of what was never there (made and deleted offline)
	if d, err := e.s.DeleteAnnotation(ctx, u, b, "never"); err != nil || !d.Deleted || d.Rev != 4 {
		t.Fatalf("delete unknown %+v %v", d, err)
	}
	if late := put(u, Annotation{ID: "never", Kind: KindBookmark, Locator: "page:2"}); !late.Deleted {
		t.Fatalf("a late create landed over its delete %+v", late)
	}
	// another profile sees none of them
	if list, _ := e.s.Annotations(ctx, other, b); len(list) != 0 {
		t.Fatalf("other profile %+v", list)
	}
	list, err := e.s.Annotations(ctx, u, b)
	e.must(err)
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
}

func TestAnnotationValidation(t *testing.T) {
	e := newEnv(t)
	u, b := e.user("a"), e.movie("Книга")
	long := strings.Repeat("я", 1200)
	got, err := e.s.PutAnnotation(ctx, u, b, Annotation{ID: "x", Kind: KindHighlight, Locator: hl, Excerpt: long})
	e.must(err)
	if n := len([]rune(got.Excerpt)); n != MaxExcerpt || got.Color != "yellow" {
		t.Fatalf("excerpt %d runes, color %q", n, got.Color)
	}
	bad := []Annotation{
		{ID: "", Kind: KindBookmark, Locator: "page:1"},
		{ID: "a b", Kind: KindBookmark, Locator: "page:1"},
		{ID: "k", Kind: "comment", Locator: "page:1"},
		{ID: "k", Kind: KindBookmark, Locator: ""},
		{ID: "k", Kind: KindHighlight, Locator: "epubcfi(/6/4)", Color: "purple"},
		{ID: "k", Kind: KindNote, Locator: "epubcfi(/6/4)", Note: strings.Repeat("a", MaxNote+1)},
	}
	for _, a := range bad {
		if _, err := e.s.PutAnnotation(ctx, u, b, a); !errors.Is(err, ErrBadAnnotation) {
			t.Fatalf("%+v: %v", a, err)
		}
	}
}

func TestAnnotationsCapPerItem(t *testing.T) {
	e := newEnv(t)
	u, b := e.user("a"), e.movie("Книга")
	old := maxPerItem
	maxPerItem = 3
	t.Cleanup(func() { maxPerItem = old })
	for _, id := range []string{"a", "b", "c"} {
		_, err := e.s.PutAnnotation(ctx, u, b, Annotation{ID: id, Kind: KindBookmark, Locator: "page:1"})
		e.must(err)
	}
	if _, err := e.s.PutAnnotation(ctx, u, b, Annotation{ID: "d", Kind: KindBookmark, Locator: "page:1"}); !errors.Is(err, ErrBadAnnotation) {
		t.Fatalf("over the cap: %v", err)
	}
	// an edit of one already there, and a delete, still go
	if _, err := e.s.PutAnnotation(ctx, u, b, Annotation{ID: "a", Rev: 1, Kind: KindBookmark, Locator: "page:2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DeleteAnnotation(ctx, u, b, "b"); err != nil {
		t.Fatal(err)
	}
	// a deleted one leaves room
	if _, err := e.s.PutAnnotation(ctx, u, b, Annotation{ID: "d", Kind: KindBookmark, Locator: "page:1"}); err != nil {
		t.Fatal(err)
	}
}
