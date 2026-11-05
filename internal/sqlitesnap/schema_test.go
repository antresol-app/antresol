package sqlitesnap

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemaHelpers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.db")
	d, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE "DownloadClients" ("Id" INTEGER PRIMARY KEY, "Name" TEXT, "Enable" INTEGER)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for table, want := range map[string]bool{"DownloadClients": true, "downloadclients": true, "Tags": false} {
		if got, err := HasTable(ctx, d, table); err != nil || got != want {
			t.Errorf("HasTable(%s) = %v, %v", table, got, err)
		}
	}
	cols, err := Columns(ctx, d, "DownloadClients")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"id": true, "name": true, "enable": true}; !reflect.DeepEqual(cols, want) {
		t.Fatalf("Columns = %v", cols)
	}
	if cols, err := Columns(ctx, d, "Nope"); err != nil || len(cols) != 0 {
		t.Fatalf("Columns(missing) = %v, %v", cols, err)
	}
}
