package channels

import (
	"context"
	"errors"
	"testing"
)

// an external row (internal/iptv) as the iptv package writes it
func (f *fixture) external(number int, name, key string) int64 {
	return f.exec(`INSERT INTO channels (number, name, category, rules_json, auto_key, enabled, kind) VALUES (?,?,?,'{}',?,1,'external')`,
		number, name, "Провайдер", key)
}

func TestLiveSkipsExternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f.movie("M", 90, itemOpt{year: 2000})
	}
	id := f.external(100, "Внешний", "ext:внешний")
	svc := New(f.db, nil)
	svc.Store.Now = f.st.Now
	if _, err := svc.Program(ctx, id, f.now); !errors.Is(err, ErrNothing) {
		t.Fatalf("program of an external row: %v", err)
	}
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if es := f.entries(id); len(es) != 0 {
		t.Fatalf("external row scheduled: %v", es)
	}
	c, err := svc.Store.Get(ctx, id)
	if err != nil || c.Kind != KindExternal {
		t.Fatalf("kind %+v %v", c, err)
	}
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	if es := f.entries(id); len(es) != 0 {
		t.Fatalf("external row built: %v", es)
	}
	// the own list never carries it: external rows are assembled per set (internal/iptv)
	if VisibleTo(c, Viewer{ID: 1, Admin: true}) {
		t.Fatal("external row visible as an own channel")
	}
}

func TestGeneratorLeavesExternalRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		f.movie("C", 90, itemOpt{year: 1991 + i, genres: []string{"комедия"}})
	}
	ext := f.external(100, "Внешний", "ext:внешний")
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := f.st.Get(ctx, ext)
	if err != nil || !c.Enabled || c.Number != 100 || c.Name != "Внешний" || c.Edited {
		t.Fatalf("external row touched: %+v %v", c, err)
	}
	all, _ := f.st.All(ctx)
	for _, x := range all {
		if x.ID != ext && x.Number >= 100 {
			t.Fatalf("generated channel numbered past the external one: %+v", x)
		}
	}
}
