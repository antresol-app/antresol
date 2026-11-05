//go:build !sources

package api

import (
	"context"
	"database/sql"
	"net/http"
)

// The core build's settings are the library's own: playback outside
// the home network, subtitles, languages and metadata.
type appSettingsFull struct{}

func (s appSettingsFull) withDefaults() appSettingsFull { return s }

func afterSaveAppSettings(*sql.DB) {}

type settingsViewFull struct{}

type settingsFull struct{}

func newSettingsFull(Deps) settingsFull { return settingsFull{} }

func (s *settingsAPI) loadFull(context.Context, *settingsView) error { return nil }
func (v settingsView) validateFull() string                          { return "" }
func (v *settingsView) fitLanguagesFull()                            {}
func (v *settingsView) keepFull(settingsView)                        {}
func (v *settingsView) completeFull(settingsView)                    {}
func (s *settingsAPI) saveFull(http.ResponseWriter, *http.Request, settingsView) bool {
	return true
}
