// Package sqlitesnap reads the SQLite databases of other services without
// writing to them: a consistent copy (database plus -wal) is taken into a
// private folder and opened read-only from there.
package sqlitesnap

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Stamp identifies a version of a file (size and mtime); a missing file has
// the zero stamp.
type Stamp struct {
	Size  int64
	Mtime int64
}

// StampOf returns the stamp of p.
func StampOf(p string) Stamp {
	st, err := os.Stat(p)
	if err != nil {
		return Stamp{}
	}
	return Stamp{st.Size(), st.ModTime().UnixNano()}
}

// CopyTo copies a WAL database (src and src-wal) to dst (and dst-wal) while
// its owner may be writing it: the copy counts only when neither file
// changed during it (a checkpoint between the two copies would pair a
// database with a WAL that does not belong to it). Retries a few times.
// src is only ever opened for reading.
func CopyTo(src, dst string) error {
	var last error
	for try := 0; try < 5; try++ {
		before := [2]Stamp{StampOf(src), StampOf(src + "-wal")}
		for _, suffix := range []string{"", "-wal"} {
			os.Remove(dst + suffix)
			if err := copyFile(src+suffix, dst+suffix); err != nil && (suffix == "" || !errors.Is(err, fs.ErrNotExist)) {
				return err
			}
		}
		if after := [2]Stamp{StampOf(src), StampOf(src + "-wal")}; after == before {
			return nil
		}
		last = errors.New("database changed while copying")
		time.Sleep(time.Duration(100*(try+1)) * time.Millisecond)
	}
	return last
}

// Snapshot copies src (and its -wal, when there is one) into dstDir as
// <base(src)>-<8 hex> and returns the path of the copy.
func Snapshot(src, dstDir string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	dst := filepath.Join(dstDir, filepath.Base(src)+"-"+hex.EncodeToString(b[:]))
	if err := CopyTo(src, dst); err != nil {
		os.Remove(dst)
		os.Remove(dst + "-wal")
		return "", err
	}
	return dst, nil
}

// OpenRO opens a database read-only (query_only on every connection of the
// pool). A copy with a -wal but no -shm cannot be opened with mode=ro
// (SQLite would have to create the -shm): then it is opened without mode=ro,
// still query_only — use it only on a private copy from Snapshot.
func OpenRO(p string) (*sql.DB, error) {
	d, err := open(p, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err == nil || !cantOpen(err) {
		return d, err
	}
	return open(p, "_pragma=query_only(1)&_pragma=busy_timeout(5000)")
}

func open(p, query string) (*sql.DB, error) {
	dsn := (&url.URL{Scheme: "file", Path: p, RawQuery: query}).String()
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func cantOpen(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_CANTOPEN
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
