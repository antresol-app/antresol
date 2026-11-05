// Package trickplay makes the scrub previews of the library's videos: a
// small frame every IntervalSec, tiled into JPEG sheets of Columns×Rows,
// with an index the players read (GET /api/files/{id}/trickplay). They are
// made in the background, one file at a time, at the lowest priority and
// only while nothing plays (Worker), and kept under the data dir by the
// file's path, size and mtime (a replaced file gets new ones).
package trickplay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// Version names the stored previews: bump it when the frames, the
	// sheets or the index change (the old ones are pruned and redone).
	Version = 2
	// IntervalSec: one frame every this many seconds of the file.
	IntervalSec = 10
	// TileWidth is a frame's width; its height follows the picture
	// (240×136 at 16:9, about 5 KB a frame: a 2 h film is ~3.5 MB).
	TileWidth = 240
	// Columns × Rows frames make one sheet.
	Columns, Rows = 10, 10
	// MinDuration: shorter files get none (a scrub over a clip needs no preview).
	MinDuration = 5 * time.Minute
	jpegQuality = 60
)

// Index is what a player needs to pick a frame: frame i shows the time
// [i·interval, (i+1)·interval) and sits in sheet i/(columns·rows), at
// column i%columns and row (i/columns)%rows of that sheet. Every sheet is
// columns·width × rows·height pixels (the last one padded with black).
type Index struct {
	Version int `json:"version"`
	// Key changes whenever the previews are made again (a replaced file):
	// the sheet URLs carry it (?v=) so a cached sheet is never stale.
	Key      string `json:"key"`
	Interval int    `json:"interval"` // seconds
	Width    int    `json:"width"`    // one frame, pixels
	Height   int    `json:"height"`
	Columns  int    `json:"columns"`
	Rows     int    `json:"rows"`
	Count    int    `json:"count"`  // frames
	Sheets   int    `json:"sheets"` // 0.jpg … (sheets-1).jpg
}

// Key names a file's previews: its path, size and mtime and Version.
func Key(path string, size, mtime int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|v%d", path, size, mtime, Version)))
	return hex.EncodeToString(h[:16])
}

// Store reads the previews kept under Dir: Dir/<key>/index.json and
// Dir/<key>/<n>.jpg.
type Store struct{ Dir string }

func (s Store) dir(key string) string { return filepath.Join(s.Dir, key) }

// Index is the stored index of a file's previews (false: none yet).
func (s Store) Index(path string, size, mtime int64) (Index, bool) {
	if s.Dir == "" {
		return Index{}, false
	}
	b, err := os.ReadFile(filepath.Join(s.dir(Key(path, size, mtime)), "index.json"))
	if err != nil {
		return Index{}, false
	}
	var ix Index
	if json.Unmarshal(b, &ix) != nil || ix.Version != Version || ix.Sheets <= 0 {
		return Index{}, false
	}
	return ix, true
}

// Sheet is the path of sheet n of a file's previews (false: no such sheet).
func (s Store) Sheet(path string, size, mtime int64, n int) (string, bool) {
	ix, ok := s.Index(path, size, mtime)
	if !ok || n < 0 || n >= ix.Sheets {
		return "", false
	}
	p := filepath.Join(s.dir(ix.Key), strconv.Itoa(n)+".jpg")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// TileHeight is the height of a TileWidth frame of a w×h picture (even,
// 68…240 px; 136 when the size is unknown).
func TileHeight(w, h int) int {
	if w <= 0 || h <= 0 {
		return DefaultTileHeight
	}
	th := (TileWidth*h/w + 1) &^ 1
	return min(max(th, 68), 240)
}

// DefaultTileHeight: a 16:9 frame.
const DefaultTileHeight = 136
