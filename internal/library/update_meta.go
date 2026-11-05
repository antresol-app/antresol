package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// UpdateMetadata reads a visible item, lets apply edit its decoded
// meta_json and choose the fields to fill, and writes both, all in one
// transaction (a concurrent scan cannot slip in between and lose its
// keys). Fields follow SetMetadata: empty values keep the stored ones. A
// corrupt meta_json is an error and is left as it is; ErrNotFound when the
// item is gone or missing.
func (r *Repo) UpdateMetadata(ctx context.Context, id int64, apply func(cur Item, meta map[string]any) Metadata) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id=? AND i.missing_since IS NULL`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		meta := map[string]any{}
		if strings.TrimSpace(cur.MetaJSON) != "" {
			if err := json.Unmarshal([]byte(cur.MetaJSON), &meta); err != nil {
				return fmt.Errorf("library: item %d meta_json: %w", id, err)
			}
		}
		if meta == nil {
			meta = map[string]any{}
		}
		m := apply(cur, meta)
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		m.MetaJSON = string(b)
		return setMetadata(ctx, tx, id, m)
	})
}
