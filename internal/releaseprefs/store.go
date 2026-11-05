package releaseprefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/release"
)

// SettingsKey is the settings row of the release preferences.
const SettingsKey = "release_prefs"

// Store keeps the release preferences in the settings table.
type Store struct{ DB *sql.DB }

// Load returns the stored release preferences (defaults when unset; fields
// missing in the stored JSON keep their defaults).
func (s Store) Load(ctx context.Context) (Prefs, error) {
	p := Default()
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, SettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Default(), fmt.Errorf("releaseprefs: %s: %w", SettingsKey, err)
	}
	return p, nil
}

// Save validates and stores the preferences.
func (s Store) Save(ctx context.Context, p Prefs) error {
	switch p.MaxHeight {
	case 480, 576, 720, 1080, 2160:
	default:
		return i18n.Errorf("Неизвестное качество: %dp", p.MaxHeight)
	}
	if p.MusicFormat != "" && !ValidMusicFormat(p.MusicFormat) {
		return i18n.Errorf("Неизвестный формат музыки: %s", p.MusicFormat)
	}
	if p.MP3Bitrate != "" && !ValidMP3Bitrate(p.MP3Bitrate) {
		return i18n.Errorf("Неизвестный битрейт: %s", p.MP3Bitrate)
	}
	if p.MinSeeders < 0 || p.MaxSizeGB < 0 {
		return i18n.Errorf("Значения не могут быть отрицательными")
	}
	studios, err := CleanStudios(p.Studios)
	if err != nil {
		return err
	}
	p.Studios = studios
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SettingsKey, string(raw))
	return err
}

// Studios are «Любимые озвучки», best first (nil: none).
func (s Store) Studios(ctx context.Context) ([]string, error) {
	p, err := s.Load(ctx)
	return p.Studios, err
}

// SetStudios stores «Любимые озвучки» and keeps the other preferences.
func (s Store) SetStudios(ctx context.Context, studios []string) error {
	p, err := s.Load(ctx)
	if err != nil {
		return err
	}
	p.Studios = studios
	return s.Save(ctx, p)
}

// Limits of «Любимые озвучки».
const (
	maxStudios      = 30
	maxStudioLength = 40 // characters
)

// CleanStudios trims the favourite studios, drops blank and repeated
// ones (a known studio by any spelling) and spells known studios by
// their canonical names; too many or too long names are refused.
func CleanStudios(in []string) ([]string, error) {
	for _, s := range in {
		if utf8.RuneCountInString(strings.TrimSpace(s)) > maxStudioLength {
			return nil, i18n.Errorf("Слишком длинное название студии (до %d символов)", maxStudioLength)
		}
	}
	out := release.MergeStudios(in)
	if len(out) > maxStudios {
		return nil, i18n.Errorf("Слишком много студий (до %d)", maxStudios)
	}
	return out, nil
}
