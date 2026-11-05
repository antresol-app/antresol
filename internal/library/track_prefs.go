package library

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// PlaybackPrefs are a profile's audio and subtitle defaults («Звук и
// субтитры»), used by the players when nothing more specific applies (a
// series' remembered tracks come first).
type PlaybackPrefs struct {
	// AudioLangs: lang codes, best first ("ru", "en").
	AudioLangs []string `json:"audioLangs"`
	// SubMode: off (forced subtitles only) | foreign (when the audio is
	// in none of AudioLangs) | always.
	SubMode string `json:"subMode"`
	// SubLangs: the subtitle languages, best first ([]: AudioLangs).
	SubLangs []string `json:"subLangs"`
	// SubSize: s | m | l | xl; SubBackground: none | translucent | solid
	// (the web player's look; the app follows the system's caption style).
	SubSize       string `json:"subSize"`
	SubBackground string `json:"subBackground"`
	// PreferCleaned: «Выбирать очищенную озвучку» — the cleaned version of
	// a voice-over (the original speech subtracted) plays instead of the
	// studio's track whenever it exists. Kept apart (playback_flags): the
	// API fills it in, SetPlaybackPrefs never writes it.
	PreferCleaned bool `json:"preferCleaned"`
}

// Subtitle modes and looks of PlaybackPrefs.
var (
	SubModes       = []string{"off", "foreign", "always"}
	SubSizes       = []string{"s", "m", "l", "xl"}
	SubBackgrounds = []string{"none", "translucent", "solid"}
)

// TrackChoice is a track a viewer chose, by what survives from one episode
// file to the next: its language tag and its title (a dub's studio).
type TrackChoice struct {
	Lang  string `json:"lang"`
	Title string `json:"title"`
	// Kind: "cleaned" when the voice was its cleaned version (audio only;
	// "" the track itself).
	Kind string `json:"kind,omitempty"`
}

// TrackKinds are the kinds of a remembered audio track.
var TrackKinds = []string{"", "cleaned"}

// SeriesTracks is what a profile chose in a series' episodes.
type SeriesTracks struct {
	Audio    *TrackChoice `json:"audio"`
	Subtitle *TrackChoice `json:"subtitle"`
	// SubtitlesOff: subtitles were turned off (Subtitle is nil then).
	SubtitlesOff bool `json:"subtitlesOff"`
}

func splitLangs(s string) []string {
	out := []string{}
	for _, c := range strings.Split(s, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// PlaybackPrefs is the profile's stored defaults; false when it has none.
func (r *Repo) PlaybackPrefs(ctx context.Context, userID int64) (PlaybackPrefs, bool, error) {
	var p PlaybackPrefs
	var audio, subs string
	err := r.db.QueryRowContext(ctx, `SELECT audio_langs, sub_mode, sub_langs, sub_size, sub_background
		FROM playback_prefs WHERE user_id=?`, userID).Scan(&audio, &p.SubMode, &subs, &p.SubSize, &p.SubBackground)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.AudioLangs, p.SubLangs = splitLangs(audio), splitLangs(subs)
	return p, true, nil
}

// SetPlaybackPrefs stores the profile's defaults (checked by the caller).
func (r *Repo) SetPlaybackPrefs(ctx context.Context, userID int64, p PlaybackPrefs) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO playback_prefs(user_id, audio_langs, sub_mode, sub_langs, sub_size, sub_background)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET audio_langs=excluded.audio_langs, sub_mode=excluded.sub_mode,
		  sub_langs=excluded.sub_langs, sub_size=excluded.sub_size, sub_background=excluded.sub_background,
		  updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		userID, strings.Join(p.AudioLangs, ","), p.SubMode, strings.Join(p.SubLangs, ","), p.SubSize, p.SubBackground)
	return err
}

// PreferCleaned is the profile's «Выбирать очищенную озвучку» (false: none kept).
func (r *Repo) PreferCleaned(ctx context.Context, userID int64) (bool, error) {
	var on bool
	err := r.db.QueryRowContext(ctx, `SELECT prefer_cleaned FROM playback_flags WHERE user_id=?`, userID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetPreferCleaned keeps the profile's «Выбирать очищенную озвучку»; its
// «Звук и субтитры» defaults stay as they are (the household's or its own).
func (r *Repo) SetPreferCleaned(ctx context.Context, userID int64, on bool) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO playback_flags(user_id, prefer_cleaned) VALUES(?,?)
		ON CONFLICT(user_id) DO UPDATE SET prefer_cleaned=excluded.prefer_cleaned`, userID, on)
	return err
}

// ClearPlaybackPrefs drops the profile's defaults (back to the household's).
func (r *Repo) ClearPlaybackPrefs(ctx context.Context, userID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM playback_prefs WHERE user_id=?`, userID)
	return err
}

// SeriesTracks is what the profile chose in the series; false when nothing.
func (r *Repo) SeriesTracks(ctx context.Context, userID, seriesID int64) (SeriesTracks, bool, error) {
	var s SeriesTracks
	var hasAudio bool
	var aLang, aTitle, aKind, state, sLang, sTitle string
	err := r.db.QueryRowContext(ctx, `SELECT has_audio, audio_lang, audio_title, audio_kind, sub_state, sub_lang, sub_title
		FROM series_tracks WHERE user_id=? AND series_id=?`, userID, seriesID).Scan(&hasAudio, &aLang, &aTitle, &aKind, &state, &sLang, &sTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if hasAudio {
		s.Audio = &TrackChoice{Lang: aLang, Title: aTitle, Kind: aKind}
	}
	switch state {
	case "on":
		s.Subtitle = &TrackChoice{Lang: sLang, Title: sTitle}
	case "off":
		s.SubtitlesOff = true
	}
	return s, s.Audio != nil || state != "", nil
}

// RememberSeriesAudio keeps the audio track chosen in an episode of the series.
func (r *Repo) RememberSeriesAudio(ctx context.Context, userID, seriesID int64, c TrackChoice) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO series_tracks(user_id, series_id, has_audio, audio_lang, audio_title, audio_kind)
		VALUES(?,?,1,?,?,?)
		ON CONFLICT(user_id, series_id) DO UPDATE SET has_audio=1, audio_lang=excluded.audio_lang,
		  audio_title=excluded.audio_title, audio_kind=excluded.audio_kind, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		userID, seriesID, c.Lang, c.Title, c.Kind)
	return err
}

// RememberSeriesSubtitle keeps the subtitles chosen in an episode of the
// series; nil: turned off.
func (r *Repo) RememberSeriesSubtitle(ctx context.Context, userID, seriesID int64, c *TrackChoice) error {
	state, lang, title := "off", "", ""
	if c != nil {
		state, lang, title = "on", c.Lang, c.Title
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO series_tracks(user_id, series_id, sub_state, sub_lang, sub_title)
		VALUES(?,?,?,?,?)
		ON CONFLICT(user_id, series_id) DO UPDATE SET sub_state=excluded.sub_state, sub_lang=excluded.sub_lang,
		  sub_title=excluded.sub_title, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		userID, seriesID, state, lang, title)
	return err
}

// SubtitleOffsets are the profile's «Субтитры раньше / позже» of the files
// (only those that have one).
func (r *Repo) SubtitleOffsets(ctx context.Context, userID int64, fileIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(fileIDs) == 0 {
		return out, nil
	}
	args := []any{userID}
	for _, id := range fileIDs {
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT file_id, offset_ms FROM subtitle_offsets
		WHERE user_id=? AND file_id IN (?`+strings.Repeat(",?", len(fileIDs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var ms int
		if err := rows.Scan(&id, &ms); err != nil {
			return nil, err
		}
		out[id] = ms
	}
	return out, rows.Err()
}

// SetSubtitleOffset keeps the profile's subtitle offset of the file (0 drops it).
func (r *Repo) SetSubtitleOffset(ctx context.Context, userID, fileID int64, ms int) error {
	if ms == 0 {
		_, err := r.db.ExecContext(ctx, `DELETE FROM subtitle_offsets WHERE user_id=? AND file_id=?`, userID, fileID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO subtitle_offsets(user_id, file_id, offset_ms) VALUES(?,?,?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET offset_ms=excluded.offset_ms, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		userID, fileID, ms)
	return err
}
