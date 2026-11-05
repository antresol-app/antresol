package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/lang"
	"github.com/antresol-app/antresol/internal/releaseprefs"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/go-chi/chi/v5"
)

// appSettingsKey stores AppSettings in the settings table (the full
// build's release preferences live under indexer's own key).
const appSettingsKey = "app_settings"

// AppSettings are the server settings outside release preferences.
type AppSettings struct {
	// appSettingsFull: the sources build's own settings (settings_full.go);
	// none in the core build.
	appSettingsFull
	// RemoteQuality is «Качество вне дома»: the cap of «Авто» playback
	// outside the home network (original | 1080 | 720 | 480).
	RemoteQuality string `json:"remoteQuality"`
	// RemoteMusic is «Музыка вне дома»: lossless (the original) | high
	// (AAC 256 кбит/с) | low (AAC 128 кбит/с).
	RemoteMusic string `json:"remoteMusic"`
	// AutoSubtitles: look for missing subtitles on OpenSubtitles (on by
	// default); SubtitleLangs are the languages wanted ("ru", "en").
	AutoSubtitles bool     `json:"autoSubtitles"`
	SubtitleLangs []string `json:"subtitleLangs"`
	// Languages are «Языки»: the viewer's languages, best first (lang
	// codes). Every other language choice offers only these.
	Languages []string `json:"languages"`
	// MetadataLanguage is «Язык метаданных»: the language of the library's
	// stored metadata (titles, overviews, genres from TMDB), applied to
	// future scans and enrichment: ru (default) | en.
	MetadataLanguage string `json:"metadataLanguage"`
	// Mail is «Отправка на Kindle» (kindle_handlers.go).
	Mail MailSettings `json:"mail"`
	// Trickplay is «Превью при перемотке»: the server makes the scrub
	// previews of the library's videos in the background (on by default;
	// off stops making them, those made still show).
	Trickplay bool `json:"trickplay"`
	// TrickplayBudgetGB is «Место для превью»: the most the previews take
	// (the least recently viewed go first); 10 GB by default.
	TrickplayBudgetGB int `json:"trickplayBudgetGB"`
}

// defaultTrickplayBudgetGB: «Место для превью» when unset.
const defaultTrickplayBudgetGB = 10

func validTrickplayBudget(gb int) bool { return gb >= 1 && gb <= 1000 }

// TrickplayBudgetBytes is «Место для превью» in bytes (10 GB when unset).
func (s AppSettings) TrickplayBudgetBytes() int64 {
	gb := s.TrickplayBudgetGB
	if !validTrickplayBudget(gb) {
		gb = defaultTrickplayBudgetGB
	}
	return int64(gb) << 30
}

// validMetadataLanguage: ru | en ("" in a request keeps the stored one).
func validMetadataLanguage(l string) bool { return l == i18n.RU || l == i18n.EN }

// MetadataLanguageOrDefault is MetadataLanguage, ru when unset or unknown.
func (s AppSettings) MetadataLanguageOrDefault() string {
	if validMetadataLanguage(s.MetadataLanguage) {
		return s.MetadataLanguage
	}
	return i18n.RU
}

// defaultLanguages: Russian, then English.
var defaultLanguages = []string{"ru", "en"}

func validSubtitleLangs(l []string) bool { return lang.Valid(l, 1) }

// LanguagesOrDefault is Languages, Russian and English when unset.
func (s AppSettings) LanguagesOrDefault() []string {
	if lang.Valid(s.Languages, 1) {
		return s.Languages
	}
	return append([]string(nil), defaultLanguages...)
}

// withSubtitleDefaults: defaults that say nothing about subtitles (no
// languages) mean automatic subtitles on, in Russian and English.
func (s AppSettings) withSubtitleDefaults() AppSettings {
	if s.SubtitleLangs == nil {
		s.AutoSubtitles = true
		s.SubtitleLangs = append([]string(nil), subtitles.DefaultLangs...)
	}
	return s
}

// SubtitleSettings is what subtitles.Auto needs: the languages and whether
// it is on (the defaults when nothing valid is stored).
func (s AppSettings) SubtitleSettings() (langs []string, enabled bool) {
	if !validSubtitleLangs(s.SubtitleLangs) {
		return append([]string(nil), subtitles.DefaultLangs...), s.AutoSubtitles
	}
	return s.SubtitleLangs, s.AutoSubtitles
}

// remoteMusicKbps maps «Музыка вне дома» to a bitrate cap (0 = original).
var remoteMusicKbps = map[string]int{"lossless": 0, "high": 256, "low": 128}

const defaultRemoteMusic = "high"

func validRemoteMusic(q string) bool { _, ok := remoteMusicKbps[q]; return ok }

// RemoteMusicOrDefault is RemoteMusic, "high" when unset or unknown.
func (s AppSettings) RemoteMusicOrDefault() string {
	if validRemoteMusic(s.RemoteMusic) {
		return s.RemoteMusic
	}
	return defaultRemoteMusic
}

// RemoteMusicKbps is the music bitrate cap outside the home network
// (0 = the original).
func (s AppSettings) RemoteMusicKbps() int { return remoteMusicKbps[s.RemoteMusicOrDefault()] }

// RemoteQualityOrDefault is RemoteQuality, 1080p when unset or unknown.
func (s AppSettings) RemoteQualityOrDefault() string {
	if validRemoteQuality(s.RemoteQuality) {
		return s.RemoteQuality
	}
	return defaultRemoteQuality
}

// LoadAppSettings reads the stored settings over defaults (main applies them
// at startup).
func LoadAppSettings(ctx context.Context, db *sql.DB, defaults AppSettings) (AppSettings, error) {
	defaults = defaults.withSubtitleDefaults()
	defaults.appSettingsFull = defaults.withDefaults()
	defaults.Trickplay = true // stored settings without the field: on
	defaults.TrickplayBudgetGB = defaultTrickplayBudgetGB
	s := defaults
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, appSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return defaults, fmt.Errorf("settings %s: %w", appSettingsKey, err)
	}
	if !validSubtitleLangs(s.SubtitleLangs) {
		s.SubtitleLangs = defaults.SubtitleLangs
	}
	return s, nil
}

// saveAppSettings writes s over the stored row. app_settings is one JSON
// object shared by every build that opens the database (core, the full
// build, later packs), each defining part of it: the keys this build's
// AppSettings does not define are kept as stored, so saving on core does
// not wipe the sources build's settings. Keys this build does define are
// written as s has them, so an omitempty one left out is cleared.
func saveAppSettings(ctx context.Context, db *sql.DB, s AppSettings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil) // immediate: the read and the write are one step
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, appSettingsKey).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw, err = mergeForeignKeys(stored, raw, appSettingsKeys); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, appSettingsKey, string(raw)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	afterSaveAppSettings(db)
	return nil
}

// appSettingsKeys are the top-level keys of app_settings this build owns.
var appSettingsKeys = ownedKeys(reflect.TypeOf(AppSettings{}))

// mergeForeignKeys returns raw (a JSON object) plus the keys of stored
// that are not in owned. A stored row that is not a JSON object is
// replaced (no build could read it).
func mergeForeignKeys(stored, raw []byte, owned map[string]bool) ([]byte, error) {
	var old map[string]json.RawMessage
	if len(stored) == 0 || json.Unmarshal(stored, &old) != nil {
		return raw, nil
	}
	var cur map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cur); err != nil {
		return nil, err
	}
	n := len(cur)
	for k, v := range old {
		if _, set := cur[k]; !owned[k] && !set {
			cur[k] = v
		}
	}
	if len(cur) == n {
		return raw, nil
	}
	return json.Marshal(cur)
}

// ownedKeys are the JSON names of struct type t's fields as
// encoding/json writes them (embedded structs' fields promoted,
// omitempty ones included, "-" and unexported fields left out).
func ownedKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			for k := range ownedKeys(f.Type) {
				keys[k] = true
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys[name] = true
	}
	return keys
}

// settingsView is GET/PUT /api/settings: one flat object, one field per
// row of the «Настройки» screen.
type settingsView struct {
	// settingsViewFull: the sources build's rows (release preferences).
	settingsViewFull
	AppSettings
	// Studios: «Любимые озвучки» in order, best first (release
	// preferences, both editions); a PUT without them keeps the stored ones.
	Studios []string `json:"studios"`
	// SubtitlesAvailable: an OpenSubtitles key is set (response only).
	SubtitlesAvailable bool `json:"subtitlesAvailable"`
	// MailPasswordSet: a mail password is stored (response only; the password
	// itself is never sent back).
	MailPasswordSet bool `json:"mailPasswordSet"`
}

type settingsAPI struct {
	db       *sql.DB
	defaults AppSettings
	apply    func(AppSettings)
	// subtitles: an OpenSubtitles client is configured
	subtitles bool
	// prefs: the release preferences (the studios here)
	prefs releaseprefs.Store
	// settingsFull: where the sources build keeps release preferences
	settingsFull
}

func newSettingsAPI(deps Deps) *settingsAPI {
	return &settingsAPI{db: deps.DB, defaults: deps.SettingsDefaults, apply: deps.ApplySettings,
		subtitles: deps.Subtitles != nil, prefs: releaseprefs.Store{DB: deps.DB}, settingsFull: newSettingsFull(deps)}
}

func mountSettings(r chi.Router, s *settingsAPI) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/settings", s.get)
		r.Put("/api/settings", s.put)
	})
}

func (s *settingsAPI) load(ctx context.Context) (settingsView, error) {
	app, err := LoadAppSettings(ctx, s.db, s.defaults)
	if err != nil {
		return settingsView{}, err
	}
	app.RemoteQuality = app.RemoteQualityOrDefault()
	app.RemoteMusic = app.RemoteMusicOrDefault()
	app.MetadataLanguage = app.MetadataLanguageOrDefault()
	v := settingsView{AppSettings: app, SubtitlesAvailable: s.subtitles}
	v.Languages = app.LanguagesOrDefault()
	v.MailPasswordSet = app.Mail.Password != ""
	v.Mail = app.Mail.normalized()
	v.Mail.Password = ""
	if v.Studios, err = s.prefs.Studios(ctx); err != nil {
		return settingsView{}, err
	}
	if v.Studios == nil {
		v.Studios = []string{}
	}
	if err := s.loadFull(ctx, &v); err != nil {
		return settingsView{}, err
	}
	return v, nil
}

func (s *settingsAPI) get(w http.ResponseWriter, r *http.Request) {
	v, err := s.load(r.Context())
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// validate returns a Russian message for an invalid value.
func (v settingsView) validate() string {
	if msg := v.validateFull(); msg != "" {
		return msg
	}
	if _, err := releaseprefs.CleanStudios(v.Studios); err != nil {
		return err.Error()
	}
	switch {
	case v.RemoteQuality != "" && !validRemoteQuality(v.RemoteQuality):
		return "Выберите качество вне дома: оригинал, 1080p, 720p или 480p"
	case v.RemoteMusic != "" && !validRemoteMusic(v.RemoteMusic):
		return "Неизвестное качество музыки"
	case !validSubtitleLangs(v.SubtitleLangs):
		return "Неизвестный язык субтитров"
	case !lang.Valid(v.Languages, 1):
		return "Выберите хотя бы один язык"
	case v.MetadataLanguage != "" && !validMetadataLanguage(v.MetadataLanguage):
		return "Выберите язык метаданных: русский или английский"
	case v.TrickplayBudgetGB != 0 && !validTrickplayBudget(v.TrickplayBudgetGB):
		return "Место для превью — от 1 до 1000 ГБ"
	}
	if msg := v.Mail.validate(); msg != "" {
		return msg
	}
	return ""
}

// fitLanguages makes every language choice a subset of Languages in its
// order: subtitles keep at least the first language (the sources build's
// choices: fitLanguagesFull).
func (v *settingsView) fitLanguages() {
	v.SubtitleLangs = lang.Ordered(v.SubtitleLangs, v.Languages)
	if len(v.SubtitleLangs) == 0 {
		v.SubtitleLangs = []string{v.Languages[0]}
	}
	v.fitLanguagesFull()
}

func (s *settingsAPI) put(w http.ResponseWriter, r *http.Request) {
	// A client that does not know the subtitle or online fields keeps the
	// stored ones.
	stored, err := LoadAppSettings(r.Context(), s.db, s.defaults)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	cur, err := s.load(r.Context())
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	// the subtitle, online and language fields a client leaves out keep
	// the stored values
	var v settingsView
	v.AppSettings = stored
	v.Languages = stored.LanguagesOrDefault()
	v.Studios = cur.Studios
	v.keepFull(cur)
	if !decode(w, r, &v) {
		return
	}
	v.completeFull(cur)
	// an empty password keeps the stored one (answers never carry it); no
	// login means no account, and no password left behind
	if v.Mail.Password == "" {
		v.Mail.Password = stored.Mail.Password
	}
	v.Mail = v.Mail.normalized()
	if v.Mail.Username == "" {
		v.Mail.Password = ""
	}
	if msg := v.validate(); msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	v.fitLanguages()
	ctx := r.Context()
	// app_settings first: the release prefs are validated above, so their
	// write failing is unlikely, and a stored value is never lost.
	if err := saveAppSettings(ctx, s.db, v.AppSettings); err != nil {
		internalErr(w, r, "settings save", err)
		return
	}
	if err := s.prefs.SetStudios(ctx, v.Studios); err != nil {
		internalErr(w, r, "settings studios", err)
		return
	}
	if !s.saveFull(w, r, v) {
		return
	}
	if s.apply != nil {
		s.apply(v.AppSettings)
	}
	out, err := s.load(ctx)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
