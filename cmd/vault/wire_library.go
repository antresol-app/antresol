//go:build !sources

package main

import (
	"context"
	"sync"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/antresol-app/antresol/internal/vofit"
)

// serverName opens the "listening" log line.
const serverName = "antresol"

// extras: the core build serves its library and nothing else; the
// sources build's other features are not compiled in.
type extras struct{}

func newExtras(core) (*extras, error) { return &extras{}, nil }

func (*extras) close()                                 {}
func (*extras) settingsDefaults() api.AppSettings      { return api.AppSettings{} }
func (*extras) applySettings(api.AppSettings)          {}
func (*extras) fileHooks(*files.ServiceConfig)         {}
func (*extras) afterScan(context.Context)              {}
func (*extras) notifyHooks(*notify.Service)            {}
func (*extras) trackingHooks(*tracking.Service)        {}
func (*extras) apiDeps(*api.Deps, *auth.Store) error   { return nil }
func (*extras) start(context.Context, *sync.WaitGroup) {}
func (*extras) wait()                                  {}
func (*extras) voiceOverFit(*vofit.Worker) bool        { return false } // no stage B: nothing is fitted
