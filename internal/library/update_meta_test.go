package library

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestUpdateMetadata(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/x.epub", Size: 1, Mtime: 1}}, Title: "X",
		Authors: []string{"A"}, Description: "own", Cover: "file:/b/x.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	err = r.UpdateMetadata(ctx, id, func(cur Item, m map[string]any) Metadata {
		if cur.Poster != "file:/b/x.jpg" || m["authors"] == nil {
			t.Errorf("cur %+v %v", cur, m)
		}
		m["ol_at"] = "now"
		return Metadata{Year: 1999}
	})
	if err != nil {
		t.Fatal(err)
	}
	it, _ := r.Item(ctx, id)
	if it.Year != 1999 || it.Overview != "own" || it.Poster != "file:/b/x.jpg" ||
		it.MetaJSON != `{"authors":["A"],"ol_at":"now","scan_cover":"file:/b/x.jpg"}` {
		t.Fatalf("%+v", it)
	}

	// concurrent updates of different keys never lose one another
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := r.UpdateMetadata(ctx, id, func(_ Item, m map[string]any) Metadata {
				m[string(rune('a'+i))] = i
				return Metadata{}
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	it, _ = r.Item(ctx, id)
	m, err := parseTestMeta(it.MetaJSON)
	if err != nil || len(m) != 3+20 {
		t.Fatalf("lost updates: %v %v", m, err)
	}

	if err := r.SetMetadata(ctx, id, Metadata{MetaJSON: "{broken"}); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := r.UpdateMetadata(ctx, id, func(Item, map[string]any) Metadata { called = true; return Metadata{} }); err == nil || called {
		t.Fatal("corrupt meta_json must be an error, apply not called")
	}
	if it, _ := r.Item(ctx, id); it.MetaJSON != "{broken" {
		t.Fatal("corrupt meta_json overwritten")
	}
	if err := r.UpdateMetadata(ctx, 9999, func(Item, map[string]any) Metadata { return Metadata{} }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func parseTestMeta(s string) (map[string]any, error) {
	m := map[string]any{}
	return m, json.Unmarshal([]byte(s), &m)
}
