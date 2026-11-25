package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestIn(t *testing.T) {
	for n, want := range map[int]string{-1: "()", 0: "()", 1: "(?)", 2: "(?,?)", 5: "(?,?,?,?,?)"} {
		if got := In(n); got != want {
			t.Errorf("In(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestArgsAndInList(t *testing.T) {
	if got := Args([]int64{3, 1}); len(got) != 2 || got[0] != int64(3) || got[1] != int64(1) {
		t.Fatalf("Args = %v", got)
	}
	if got := Args[string](nil); got == nil || len(got) != 0 {
		t.Fatalf("Args(nil) = %#v, want an empty list", got)
	}
	in, args := InList([]string{"a", "b", "c"})
	if in != "(?,?,?)" || len(args) != 3 || args[2] != "c" {
		t.Fatalf("InList = %q %v", in, args)
	}
	if in, args := InList[int64](nil); in != "()" || len(args) != 0 {
		t.Fatalf("InList(nil) = %q %v", in, args)
	}
}

// An empty list is valid SQL and matches nothing (no guard, no panic), and a
// full one matches what it names.
func TestInInSQLite(t *testing.T) {
	ctx := context.Background()
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, k := range []string{"a", "b", "c"} {
		if err := PutSetting(ctx, d, k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	count := func(keys []string) int {
		in, args := InList(keys)
		var n int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key IN `+in, args...).Scan(&n); err != nil {
			t.Fatalf("%v: %v", keys, err)
		}
		return n
	}
	if n := count(nil); n != 0 {
		t.Fatalf("empty list matched %d rows", n)
	}
	if n := count([]string{"a", "c", "zz"}); n != 2 {
		t.Fatalf("matched %d rows, want 2", n)
	}
	// the other side of the coin the In doc warns about: NOT IN () is true of
	// every row
	notIn := func(keys []string) int {
		in, args := InList(keys)
		var n int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key NOT IN `+in, args...).Scan(&n); err != nil {
			t.Fatalf("%v: %v", keys, err)
		}
		return n
	}
	if all, none := notIn(nil), notIn([]string{"a", "b", "c"}); all != 3 || none != 0 {
		t.Fatalf("NOT IN () matched %d rows (want all 3), NOT IN of every key %d (want 0)", all, none)
	}
}
