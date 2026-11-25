package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func openSettings(t *testing.T) Querier {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSettingGetPutDelete(t *testing.T) {
	ctx := context.Background()
	q := openSettings(t)

	if v, ok, err := GetSetting(ctx, q, "k"); v != "" || ok || err != nil {
		t.Fatalf("no row: %q %v %v", v, ok, err)
	}
	if err := PutSetting(ctx, q, "k", "one"); err != nil {
		t.Fatal(err)
	}
	if err := PutSetting(ctx, q, "k", "two"); err != nil { // replaces
		t.Fatal(err)
	}
	if v, ok, err := GetSetting(ctx, q, "k"); v != "two" || !ok || err != nil {
		t.Fatalf("stored: %q %v %v", v, ok, err)
	}
	if err := PutSetting(ctx, q, "empty", ""); err != nil { // an empty value is a row
		t.Fatal(err)
	}
	if v, ok, err := GetSetting(ctx, q, "empty"); v != "" || !ok || err != nil {
		t.Fatalf("empty row: %q %v %v", v, ok, err)
	}
	if err := DeleteSetting(ctx, q, "k"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSetting(ctx, q, "k"); err != nil { // none: nothing happens
		t.Fatal(err)
	}
	if _, ok, err := GetSetting(ctx, q, "k"); ok || err != nil {
		t.Fatalf("deleted: %v %v", ok, err)
	}
}

func TestSettingJSONKeepsDefaults(t *testing.T) {
	ctx := context.Background()
	q := openSettings(t)
	type cfg struct {
		A int    `json:"a"`
		B string `json:"b"`
	}

	v := cfg{A: 1, B: "b"}
	if found, err := GetJSON(ctx, q, "j", &v); found || err != nil || v != (cfg{A: 1, B: "b"}) {
		t.Fatalf("no row: %v %v %+v", found, err, v)
	}
	if err := PutSetting(ctx, q, "j", `{"a":7}`); err != nil {
		t.Fatal(err)
	}
	if found, err := GetJSON(ctx, q, "j", &v); !found || err != nil || v != (cfg{A: 7, B: "b"}) {
		t.Fatalf("partial row keeps the default: %v %v %+v", found, err, v)
	}
	if err := PutJSON(ctx, q, "j", cfg{A: 2, B: "x"}); err != nil {
		t.Fatal(err)
	}
	var got cfg
	if found, err := GetJSON(ctx, q, "j", &got); !found || err != nil || got != (cfg{A: 2, B: "x"}) {
		t.Fatalf("round trip: %v %v %+v", found, err, got)
	}
	if err := PutSetting(ctx, q, "j", `not json`); err != nil {
		t.Fatal(err)
	}
	found, err := GetJSON(ctx, q, "j", &got)
	if !found || err == nil || !strings.Contains(err.Error(), "settings j") {
		t.Fatalf("a bad row names the key: %v %v", found, err)
	}
}

func TestSettingInTransaction(t *testing.T) {
	ctx := context.Background()
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := PutSetting(ctx, tx, "t", "in tx"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := GetSetting(ctx, tx, "t"); !ok || v != "in tx" {
		t.Fatalf("read in the transaction: %q %v", v, ok)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := GetSetting(ctx, d, "t"); ok {
		t.Fatal("a rolled-back write is not stored")
	}
}
