package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedDisk makes u report a disk of 1 MB with used bytes in use.
func fixedDisk(u *Usage, used int64) {
	u.statfs = func(string) (int64, int64, int64, error) { return 1 << 20, used, 1<<20 - used, nil }
}

func extraOf(segs ...ExtraSegment) func(context.Context) []ExtraSegment {
	return func(context.Context) []ExtraSegment { return segs }
}

func categoriesOf(t *testing.T, u *Usage) map[string]Category {
	t.Helper()
	r, err := u.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Category{}
	for _, c := range r.Categories {
		if _, dup := got[c.ID]; dup {
			t.Fatalf("category %s twice: %+v", c.ID, r.Categories)
		}
		got[c.ID] = c
	}
	return got
}

var dlLabel = map[string]string{"ru": "Загрузки", "en": "Downloads"}

// A file of the library hardlinked into an addon's folder counts once, in
// the library: the addon's segment holds only its own data, and «other»
// does not list the folder either.
func TestUsage_ExtraDirCountedOnceWithHardlink(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/x.mkv"), 4096)
	if err := os.Link(filepath.Join(mr, "media/movies/x.mkv"), filepath.Join(mr, "downloads/x.mkv")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mr, "downloads/Own/own.bin"), 100)
	u := &Usage{Sandbox: s, Extra: extraOf(ExtraSegment{ID: "addon:dl:data", Label: dlLabel, Dir: filepath.Join(mr, "downloads"), Bytes: 999})}
	got := categoriesOf(t, u)
	if got["movies"].Bytes != 4096 || got["addon:dl:data"].Bytes != 100 {
		t.Fatalf("categories %+v", got)
	}
	for _, id := range []string{"downloads", "stream-cache"} { // the addons' now
		if _, ok := got[id]; ok {
			t.Errorf("%s is a core category", id)
		}
	}
	b := otherDone(t, u)
	for _, e := range b.Entries {
		if e.Name == "downloads" {
			t.Fatalf("the addon's folder in «other»: %s", entriesOf(b.Entries))
		}
	}
}

// A segment without a folder is taken as it says; «other» shrinks by it
// and never goes below zero.
func TestUsage_ExtraBytes(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/x.mkv"), 4096)
	u := &Usage{Sandbox: s, Extra: extraOf(ExtraSegment{ID: "addon:dl:cache", Label: dlLabel, Bytes: 500})}
	fixedDisk(u, 10000)
	got := categoriesOf(t, u)
	if got["addon:dl:cache"].Bytes != 500 || got["other"].Bytes != 10000-4096-500 {
		t.Fatalf("categories %+v", got)
	}
	u = &Usage{Sandbox: s, Extra: extraOf(
		ExtraSegment{ID: "addon:dl:cache", Label: dlLabel, Bytes: 1 << 40},
		ExtraSegment{ID: "addon:dl:neg", Label: dlLabel, Bytes: -5},
	)}
	fixedDisk(u, 10000)
	got = categoriesOf(t, u)
	if got["other"].Bytes != 0 || got["addon:dl:neg"].Bytes != 0 {
		t.Fatalf("clamped %+v", got)
	}
}

// The segment's name comes in the request's language; one the addon left
// out falls back to the other language.
func TestUsage_ExtraLabelByLanguage(t *testing.T) {
	s, _ := testSandbox(t)
	u := &Usage{Sandbox: s, Extra: extraOf(
		ExtraSegment{ID: "addon:dl:a", Label: dlLabel, Bytes: 1},
		ExtraSegment{ID: "addon:dl:b", Label: map[string]string{"ru": "Кеш"}, Bytes: 1},
	)}
	r, err := u.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	labels := func(r UsageReport) map[string]string {
		m := map[string]string{}
		for _, c := range r.Categories {
			m[c.ID] = c.Label
		}
		return m
	}
	en, ru := labels(r.Localized("en")), labels(r.Localized("ru"))
	if en["addon:dl:a"] != "Downloads" || en["addon:dl:b"] != "Кеш" || ru["addon:dl:a"] != "Загрузки" || en["movies"] != "" {
		t.Fatalf("en %v ru %v", en, ru)
	}
	if l := labels(r); l["addon:dl:a"] != "" { // the cached report itself stays unlabelled
		t.Fatalf("report %v", l)
	}
}

// A segment's folder must be absolute, clean and below the media root or a
// file-browser root, its symlinks resolved, and not inside a library
// category or another segment's folder; else the segment is left out.
func TestUsage_ExtraBadDirDropped(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "downloads/ok/a.bin"), 10)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "o.bin"), 20)
	if err := os.Symlink(outside, filepath.Join(mr, "downloads/link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mr, "media/movies/sub/m.mkv"), 30)
	bad := map[string]string{
		"rel":      "rel/dir",
		"unclean":  mr + "/downloads/../downloads/ok",
		"root":     "/",
		"outside":  outside,
		"link":     filepath.Join(mr, "downloads/link"),
		"missing":  filepath.Join(mr, "downloads/missing"),
		"library":  filepath.Join(mr, "media/movies/sub"),
		"nested":   filepath.Join(mr, "downloads/ok/inner"),
		"trailing": filepath.Join(mr, "downloads/ok") + "/",
	}
	segs := []ExtraSegment{{ID: "addon:dl:ok", Label: dlLabel, Dir: filepath.Join(mr, "downloads/ok")}}
	for id, dir := range bad {
		segs = append(segs, ExtraSegment{ID: "addon:dl:" + id, Label: dlLabel, Dir: dir, Bytes: 7})
	}
	segs = append(segs, ExtraSegment{ID: "addon:dl:ok", Label: dlLabel, Bytes: 5})                                       // a repeated id
	segs = append(segs, ExtraSegment{ID: "addon:dl:media", Label: dlLabel, Dir: mr})                                     // around the library
	segs = append(segs, ExtraSegment{ID: "addon:dl:atroot", Label: dlLabel, Dir: filepath.Join(mr, "tmp/vault-stream")}) // at a file-browser root
	u := &Usage{Sandbox: s, Extra: extraOf(segs...)}
	got := categoriesOf(t, u)
	if _, ok := got["addon:dl:media"]; ok {
		t.Errorf("the media root kept: %+v", got)
	}
	if _, ok := got["addon:dl:atroot"]; !ok {
		t.Errorf("a file-browser root itself dropped: %+v", got)
	}
	if got["addon:dl:ok"].Bytes != 10 {
		t.Fatalf("ok %+v", got)
	}
	for id := range bad {
		if c, ok := got["addon:dl:"+id]; ok {
			t.Errorf("%s kept: %+v", id, c)
		}
	}
}

// An addon segment with a folder breaks down like a library category; one
// without has no entries; an addon id the report does not have is unknown.
func TestBreakdown_AddonSegment(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "downloads/Big/a.bin"), 300)
	write(t, filepath.Join(mr, "downloads/small.bin"), 100)
	u := &Usage{Sandbox: s, Extra: extraOf(
		ExtraSegment{ID: "addon:dl:data", Label: dlLabel, Dir: filepath.Join(mr, "downloads")},
		ExtraSegment{ID: "addon:dl:cache", Label: dlLabel, Bytes: 50},
	)}
	ctx := context.Background()
	b, err := u.Breakdown(ctx, "addon:dl:data")
	if err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(b.Entries); b.Bytes != 400 || b.Pending || got != "Big=300,true;small.bin=100,false;" {
		t.Fatalf("data %+v %s", b, got)
	}
	b, err = u.Breakdown(ctx, "addon:dl:cache")
	if err != nil || b.Bytes != 50 || b.Entries == nil || len(b.Entries) != 0 {
		t.Fatalf("cache %+v %v", b, err)
	}
	for _, id := range []string{"addon:dl:nope", "addon:other:data", "addon:"} {
		if _, err := u.Breakdown(ctx, id); !errors.Is(err, ErrUnknownRoot) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

// Addons that do not answer in time leave the library categories alone,
// and the report does not wait past the deadline.
func TestUsage_ExtraHookDown(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/x.mkv"), 4096)
	u := &Usage{Sandbox: s, extraTimeout: 50 * time.Millisecond, Extra: func(ctx context.Context) []ExtraSegment {
		<-ctx.Done()
		return nil
	}}
	start := time.Now()
	got := categoriesOf(t, u)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %v", time.Since(start))
	}
	if got["movies"].Bytes != 4096 || got["series"].ID == "" || got["other"].ID == "" {
		t.Fatalf("categories %+v", got)
	}
	for id := range got {
		if strings.HasPrefix(id, "addon:") {
			t.Fatalf("categories %+v", got)
		}
	}
}

// A segment whose dir is itself a symlink (to a folder inside the roots) is
// walked through it: its bytes are the segment's, and «other» does not
// list the folder it points to.
func TestUsage_ExtraSymlinkedDir(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "data/real/Big/a.bin"), 300)
	if err := os.Symlink(filepath.Join(mr, "data/real"), filepath.Join(mr, "downloads/link")); err != nil {
		t.Fatal(err)
	}
	u := &Usage{Sandbox: s, Extra: extraOf(ExtraSegment{ID: "addon:dl:data", Label: dlLabel, Dir: filepath.Join(mr, "downloads/link")})}
	if got := categoriesOf(t, u); got["addon:dl:data"].Bytes != 300 {
		t.Fatalf("categories %+v", got)
	}
	b, err := u.Breakdown(context.Background(), "addon:dl:data")
	if err != nil || entriesOf(b.Entries) != "Big=300,true;" {
		t.Fatalf("breakdown %+v %v", b, err)
	}
	for _, e := range otherDone(t, u).Entries {
		if strings.HasPrefix(e.Name, "data") {
			t.Fatalf("the segment's data in «other»: %+v", e)
		}
	}
}
