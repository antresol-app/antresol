package channels

import (
	"context"
	"errors"
	"testing"
)

func numbers(t *testing.T, f *fixture) map[string]int {
	t.Helper()
	all, err := f.st.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, c := range all {
		out[c.Name] = c.Number
	}
	return out
}

func TestRenumberOneList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mk := func(name string, enabled bool) Channel {
		t.Helper()
		c, err := f.st.Create(ctx, Channel{Name: name, Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a := mk("A", true)
	b := mk("B", true)
	mk("Скрытый", false)
	c := mk("C", true)
	// an external row numbered the old way (from 100) and a hidden one
	f.exec(`INSERT INTO channels (number, name, category, rules_json, auto_key, enabled, kind) VALUES (100,'Внешний','','{}','ext:1',1,'external')`)
	f.exec(`INSERT INTO channels (number, name, category, rules_json, auto_key, enabled, kind) VALUES (101,'Внешний скрытый','','{}','ext:2',0,'external')`)
	if err := f.st.Renumber(ctx); err != nil {
		t.Fatal(err)
	}
	// own then external, 1…N over the seen ones; the hidden after N
	want := map[string]int{"A": 1, "B": 2, "C": 3, "Внешний": 4, "Скрытый": 5, "Внешний скрытый": 6}
	if got := numbers(t, f); len(got) != len(want) || got["A"] != 1 || got["B"] != 2 || got["C"] != 3 || got["Внешний"] != 4 || got["Скрытый"] != 5 || got["Внешний скрытый"] != 6 {
		t.Fatalf("numbers %v, want %v", got, want)
	}
	// hiding one closes the gap
	b.Enabled = false
	if _, err := f.st.Update(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got := numbers(t, f); got["A"] != 1 || got["C"] != 2 || got["Внешний"] != 3 || got["B"] <= 3 {
		t.Fatalf("after hiding B %v", got)
	}
	// the admin's number: kept, the automatic channel there steps aside
	c, _ = f.st.Get(ctx, c.ID)
	c.Number = 1
	c, err := f.st.Update(ctx, c)
	if err != nil || c.Number != 1 || !c.NumberManual {
		t.Fatalf("manual %+v %v", c, err)
	}
	if got := numbers(t, f); got["C"] != 1 || got["A"] != 2 || got["Внешний"] != 3 {
		t.Fatalf("after C → 1 %v", got)
	}
	// kept through later renumbering, even when it hides
	c.Enabled = false
	if c, err = f.st.Update(ctx, c); err != nil || c.Number != 1 {
		t.Fatalf("hidden manual %+v %v", c, err)
	}
	if got := numbers(t, f); got["C"] != 1 || got["A"] != 2 || got["Внешний"] != 3 {
		t.Fatalf("hidden manual keeps its number %v", got)
	}
	// a number another admin's number holds is refused
	a, _ = f.st.Get(ctx, a.ID)
	a.Number = 1
	if _, err := f.st.Update(ctx, a); !errors.Is(err, ErrNumberTaken) {
		t.Fatalf("taken: %v", err)
	}
}

// The numbering rule on its own, no database: the admin's numbers stay,
// the seen channels are 1…N (own, personal, external by their entries'
// order), the hidden ones follow.
func TestAssignNumbers(t *testing.T) {
	rows := []*numRow{
		{id: 1, enabled: true, place: 30},
		{id: 2, enabled: true, place: 10},
		{id: 3, enabled: true, personal: true, place: 5},
		{id: 4, enabled: true, kind: KindExternal, hasEntry: true, src: 1, srcID: 1, pos: 2, place: 4},
		{id: 5, enabled: true, kind: KindExternal, hasEntry: true, src: 1, srcID: 1, pos: 1, place: 40},
		{id: 6, enabled: false, place: 1},
		{id: 7, enabled: true, manual: 2, place: 50},
		{id: 8, enabled: true, manual: 2, place: 6}, // asks for a number a lower id holds
	}
	assignNumbers(rows)
	got := map[int64]int64{}
	for _, r := range rows {
		got[r.id] = r.final
	}
	// 7 holds 2; then 2 (place 10), 1 (30), 8 (place 6 as automatic: before 2 and 1)
	want := map[int64]int64{8: 1, 7: 2, 2: 3, 1: 4, 3: 5, 5: 6, 4: 7, 6: 8}
	for id, n := range want {
		if got[id] != n {
			t.Fatalf("numbers %v, want %v", got, want)
		}
	}
	for _, r := range rows {
		if r.finalManual != (r.id == 7) {
			t.Fatalf("channel %d manual: %v", r.id, r.finalManual)
		}
	}
}
