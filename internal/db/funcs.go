package db

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"fmt"

	"modernc.org/sqlite"
)

// SQL functions the migrations use.
func init() {
	// vault_sha256_hex(text): lower-case hex sha256 of its UTF-8 bytes
	// (migration 0020 hashes session tokens with it).
	sqlite.MustRegisterDeterministicScalarFunction("vault_sha256_hex", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		var b []byte
		switch v := args[0].(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		default:
			return nil, fmt.Errorf("vault_sha256_hex: want text, got %T", args[0])
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:]), nil
	})
}
