package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/db"
)

func TestApplyFolderSettings(t *testing.T) {
	for _, k := range []string{"VAULT_MOVIES_DIR", "VAULT_SERIES_DIR", "VAULT_EBOOKS_DIR", "VAULT_AUDIOBOOKS_DIR", "VAULT_COMICS_DIR", "VAULT_IMPORT_PATHS"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", "/media")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	// nothing picked: the environment decides
	if got := applyFolderSettings(ctx, cfg, d); got.Folders() != cfg.Folders() {
		t.Fatalf("%+v", got.Folders())
	}
	if _, err := d.Exec(`INSERT INTO settings (key, value) VALUES ('folders', ?)`,
		`{"movies":"Фильмы","series":"tv","music":"music","ebooks":"Книги/эл","audiobooks":"Книги/аудио","comics":"Книги/манга"}`); err != nil {
		t.Fatal(err)
	}
	got := applyFolderSettings(ctx, cfg, d)
	if got.Abs(got.MoviesDir) != "/media/Фильмы" || got.Abs(got.SeriesDir) != "/media/tv" || got.Abs(got.AudiobooksDir) != "/media/Книги/аудио" ||
		got.Abs(got.BooksDir) != "/media/Книги" {
		t.Fatalf("%+v", got.Folders())
	}
	// unreadable settings: the environment's folders, the server still starts
	if _, err := d.Exec(`UPDATE settings SET value='not json' WHERE key='folders'`); err != nil {
		t.Fatal(err)
	}
	if got := applyFolderSettings(ctx, cfg, d); got.Folders() != cfg.Folders() {
		t.Fatalf("%+v", got.Folders())
	}
}

func TestRestarterStopsOnceAndRemembers(t *testing.T) {
	stops := 0
	r := &restarter{stop: func() { stops++ }}
	if r.requested() {
		t.Fatal("requested before asked")
	}
	r.request()
	r.request()
	if stops != 1 || !r.requested() {
		t.Fatalf("stops %d requested %v", stops, r.requested())
	}
}

func TestRestarterLeavesAServerThatIsAlreadyStopping(t *testing.T) {
	// a signal stopped the server: a restart asked in that window (a save in
	// flight, the delayed timer) must not turn the stop into an exec
	ctx, cancel := context.WithCancel(context.Background())
	stops := 0
	r := &restarter{stop: func() { stops++ }, ctx: ctx}
	cancel()
	r.request()
	if stops != 0 || r.requested() {
		t.Fatalf("stops %d requested %v", stops, r.requested())
	}
}

func TestRestarterWatchdogStartsAgainWhenTheShutdownHangs(t *testing.T) {
	forced := make(chan struct{}, 1)
	exited := make(chan int, 1)
	r := &restarter{stop: func() {}, grace: 20 * time.Millisecond,
		force: func() error { forced <- struct{}{}; return nil },
		exit:  func(code int) { exited <- code }}
	r.request()
	select {
	case <-forced:
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog never started the server again")
	}
	// the exec returned (here: always): the process ends, and its supervisor starts it
	select {
	case code := <-exited:
		if code == 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the process did not end after a failed exec")
	}
}

func TestRestarterWatchdogReportsAFailedExec(t *testing.T) {
	var forced atomic.Int32
	exited := make(chan int, 1)
	r := &restarter{stop: func() {}, grace: 10 * time.Millisecond,
		force: func() error { forced.Add(1); return errors.New("no such file") },
		exit:  func(code int) { exited <- code }}
	r.request()
	r.request() // twice asked, once watched
	select {
	case code := <-exited:
		if code != 1 || forced.Load() != 1 {
			t.Fatalf("code %d forced %d", code, forced.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no exit")
	}
}

func TestCanRestart(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "vault")
	if err := os.WriteFile(run, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(p string) func() (string, error) { return func() (string, error) { return p, nil } }
	if !canRestart(at(run)) {
		t.Fatal("a runnable binary cannot restart")
	}
	for name, exe := range map[string]func() (string, error){
		"not executable": at(plain),
		"gone":           at(filepath.Join(dir, "gone")),
		"a folder":       at(dir),
		"unknown":        func() (string, error) { return "", errors.New("no path") },
	} {
		if canRestart(exe) {
			t.Errorf("%s: offered a restart", name)
		}
	}
}

func TestExecSelfRunsTheSameBinaryWithTheSameArguments(t *testing.T) {
	var path string
	var argv, env []string
	err := execSelf(func() (string, error) { return "/usr/local/bin/vault", nil },
		func(p string, a, e []string) error { path, argv, env = p, a, e; return nil })
	if err != nil || path != "/usr/local/bin/vault" || len(argv) != len(os.Args) || len(env) == 0 {
		t.Fatalf("%v %q %v", err, path, argv)
	}
	boom := errors.New("no such file")
	if err := execSelf(func() (string, error) { return "", boom }, nil); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	if err := execSelf(func() (string, error) { return "/x", nil },
		func(string, []string, []string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}
