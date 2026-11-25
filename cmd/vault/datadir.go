package main

import (
	"log"
	"os"
	"path/filepath"
)

// secureDataDir keeps the data folder to the server's own user: 0700 on the
// folder and 0600 on the database and its -wal and -shm files, where
// sessions, tokens and mail credentials live. SQLite gives a new -wal or
// -shm the database file's mode, so they stay closed. Best effort: a
// folder that is not ours, or a share that has no modes, only logs.
func secureDataDir(dir, dbName string) {
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o700}, {filepath.Join(dir, dbName), 0o600}, {filepath.Join(dir, dbName+"-wal"), 0o600}, {filepath.Join(dir, dbName+"-shm"), 0o600}} {
		if err := os.Chmod(p.path, p.mode); err != nil && !os.IsNotExist(err) {
			log.Printf("data: chmod %o %s: %v", p.mode, filepath.Base(p.path), err)
		}
	}
}
