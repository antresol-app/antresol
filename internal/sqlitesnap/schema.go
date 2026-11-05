package sqlitesnap

import (
	"context"
	"database/sql"
	"strings"
)

// HasTable reports whether the database has the table (name in any case):
// importers read other services' schemas, which differ between versions.
func HasTable(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=? COLLATE NOCASE`, table).Scan(&n)
	return n > 0, err
}

// Columns returns the table's column names, lower-cased (empty for a
// missing table).
func Columns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = true
	}
	return out, rows.Err()
}
