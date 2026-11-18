package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/config"
)

// applyFolderSettings puts the library folders an administrator picked (the
// first-run wizard, «Настройки сервера → Медиатека») over the environment's
// VAULT_*_DIR. It returns cfg as it was when nothing was picked or the saved
// folders cannot be read: a server that cannot read its settings still
// starts on its environment.
func applyFolderSettings(ctx context.Context, cfg config.Config, db *sql.DB) config.Config {
	f, custom, err := api.LoadFolderSettings(ctx, db)
	if err != nil {
		log.Printf("settings: folders: %v (using VAULT_*_DIR)", err)
		return cfg
	}
	if !custom {
		return cfg
	}
	log.Printf("settings: library folders picked in the settings are in use: %+v", f)
	return cfg.WithFolders(f)
}

// errRestart ends run() when the server asked to be started again.
var errRestart = errors.New("restart requested")

// restartGrace is how long a restart may take to shut down before the process
// starts again regardless. Docker's stop gives 30 s and then kills; a
// restart asked from the settings has no such end, so a stuck goroutine
// would leave a server with its listeners closed and nothing to wake it.
const restartGrace = 30 * time.Second

// restarter turns a request to restart into a normal shutdown (stop, the
// signal context's cancel) that run() then reports as errRestart.
type restarter struct {
	stop func()
	// ctx is the signal context: once it is done the server is stopping
	// for good (a signal), and a restart asked meanwhile must not undo that.
	ctx context.Context
	// grace and force: when the shutdown has not ended after grace, force
	// (the same re-exec run()'s end does; nil: restartSelf) takes over, and
	// exit ends the process if even that fails (nil: os.Exit).
	grace time.Duration
	force func() error
	exit  func(code int)

	mu   sync.Mutex
	done bool
}

// request stops the server; asking twice stops it once, and a server that
// is already stopping is left to stop.
func (r *restarter) request() {
	if r.ctx != nil && r.ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.done = true
	r.mu.Unlock()
	log.Printf("restarting to use the new settings")
	r.stop()
	if r.grace > 0 {
		time.AfterFunc(r.grace, r.overdue)
	}
}

// overdue is the watchdog: the shutdown that a restart began is still going.
// Everything the server was waiting for is only waiting, so start again now.
func (r *restarter) overdue() {
	log.Printf("restart: the shutdown has not ended in %s, starting again anyway", r.grace)
	force, exit := r.force, r.exit
	if force == nil {
		force = restartSelf
	}
	if exit == nil {
		exit = os.Exit
	}
	if err := force(); err != nil {
		log.Printf("restart: %v", err)
	}
	exit(1) // exec returned: the container's restart policy starts it
}

func (r *restarter) requested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

// execSelf replaces this process with a new run of the same binary with the
// same arguments and environment (the pid stays, so a container goes on
// running). Only called after run() has shut everything down.
func execSelf(exe func() (string, error), exec func(argv0 string, argv []string, envv []string) error) error {
	path, err := exe()
	if err != nil {
		return err
	}
	return exec(path, os.Args, os.Environ())
}

func restartSelf() error { return execSelf(os.Executable, syscall.Exec) }

// canRestart: the binary can be started again (it is there and runs), so a
// restart asked from the settings is offered at all; otherwise the page says
// to restart the server by hand.
func canRestart(exe func() (string, error)) bool {
	path, err := exe()
	if err != nil {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// restartFunc is what the settings call to restart the server: nil (the
// administrator restarts it by hand) when this binary cannot start again.
func restartFunc(r *restarter) func() {
	if !canRestart(os.Executable) {
		log.Printf("restart: the server's binary cannot be started again from here; folder changes need a manual restart")
		return nil
	}
	return r.request
}
