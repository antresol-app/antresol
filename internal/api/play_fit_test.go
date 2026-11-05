package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/voclean"
	"github.com/antresol-app/antresol/internal/vofit"
)

// fitTrack stores a fit of x.ext (as vofit's worker does).
func (x *extAudioEnv) fitTrack(state string, offsetMs, tempo float64) {
	x.t.Helper()
	st, err := os.Stat(x.ext)
	if err != nil {
		x.t.Fatal(err)
	}
	var size, mtime int64
	if err := x.db.QueryRow(`SELECT size, mtime FROM files WHERE id=?`, x.file).Scan(&size, &mtime); err != nil {
		x.t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO external_audio_fit(path, file_id, state, offset_ms, tempo, track_size, track_mtime, file_size, file_mtime)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET state=excluded.state, offset_ms=excluded.offset_ms, tempo=excluded.tempo`,
		x.ext, x.file, state, offsetMs, tempo, st.Size(), st.ModTime().UnixNano(), size, mtime); err != nil {
		x.t.Fatal(err)
	}
}

// The cleaned version of a downloaded voice-over (voclean of an external
// track) is listed after it, made from it (cleanedFrom: its index); gone
// with it when the track is not offered.
func TestPlayListsCleanedExternalVoiceOver(t *testing.T) {
	x := newExtAudioEnv(t)
	x.fitTrack(vofit.StateOK, 200, 1)
	cleaned := filepath.Join(t.TempDir(), "7-e5.mka")
	if err := os.WriteFile(cleaned, []byte("mka"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO cleaned_audio(file_id, vo_index, orig_index, studio, lang, title, state, path, file_size, file_mtime, vo_path)
		SELECT id, ?, 1, 'LostFilm', 'ru', 'LostFilm', 'done', ?, size, mtime, ? FROM files WHERE id=?`, voclean.ExternalIndex(x.ext), cleaned, x.ext, x.file); err != nil {
		t.Fatal(err)
	}
	out, code, body := x.play(`{` + extDirectCaps + `}`)
	if code != 200 || len(out.Audio) != 3 {
		t.Fatalf("%d %s", code, body)
	}
	c := out.Audio[2]
	if c.Kind != media.KindCleaned || c.CleanedFrom == nil || *c.CleanedFrom != externalAudioBase || out.Audio[1].Src != extaudio.ID(x.ext) {
		t.Fatalf("%s", body)
	}
	x.fitTrack(vofit.StateMisfit, 0, 1)
	if out, _, body := x.play(`{` + extDirectCaps + `}`); len(out.Audio) != 1 {
		t.Fatalf("misfit: %s", body)
	}
}

// A fit made of another video or track than now (replaced in place, a
// rescan) hides the track until it is fitted again: a misfit never comes
// back, a PAL track never plays without its tempo.
func TestPlayHidesStaleFit(t *testing.T) {
	for _, st := range []string{vofit.StateOK, vofit.StateMisfit} {
		x := newExtAudioEnv(t)
		x.fitTrack(st, 1200, 0.959)
		if _, err := x.db.Exec(`UPDATE files SET size=size+1 WHERE id=?`, x.file); err != nil {
			t.Fatal(err)
		}
		if out, _, body := x.play(`{` + extDirectCaps + `}`); len(out.Audio) != 1 {
			t.Fatalf("%s, video changed: %s", st, body)
		}
		y := newExtAudioEnv(t)
		y.fitTrack(st, 1200, 0.959)
		if err := os.WriteFile(y.ext, []byte("another track"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, _, body := y.play(`{` + extDirectCaps + `}`); len(out.Audio) != 1 {
			t.Fatalf("%s, track changed: %s", st, body)
		}
	}
}

// A fitted voice-over plays shifted by its offset and, from a PAL master,
// slowed by atempo (re-encoded); a misfit (another cut), one still
// waiting for its fit, unmeasured or whose fit failed is not listed.
func TestPlayAppliesFit(t *testing.T) {
	x := newExtAudioEnv(t)
	x.fitTrack(vofit.StateOK, 1200, 0.959)
	pick := `{` + extDirectCaps + `,"audioIndex":2000,"audioSrc":"` + extaudio.ID(x.ext) + `","startSec":100}`
	if _, code, body := x.play(pick); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	d := x.hls.started[len(x.hls.started)-1]
	if d.AudioInput != x.ext || d.AudioInputOffsetMs != 1200 || d.AudioInputTempo != 0.959 || d.AudioCopy {
		t.Fatalf("%+v", d)
	}
	args := strings.Join(media.BuildArgs("none", "/m/v.mp4", d, 100, "/w"), " ")
	if !strings.Contains(args, "-itsoffset 1.2 -ss 95.9") || !strings.Contains(args, "-af atempo=0.959") {
		t.Fatal(args)
	}
	for _, st := range []string{vofit.StateMisfit, vofit.StatePending} {
		x.fitTrack(st, 0, 1)
		out, code, body := x.play(`{` + extDirectCaps + `}`)
		if code != 200 || len(out.Audio) != 1 || out.Audio[0].External {
			t.Fatalf("%s: %d %s", st, code, body)
		}
	}
	// not measured (no original-language track, too short) or the fit
	// failed: never played unverified
	for _, st := range []string{vofit.StateUnmeasured, vofit.StateFailed} {
		x.fitTrack(st, 0, 1)
		if out, _, body := x.play(`{` + extDirectCaps + `}`); len(out.Audio) != 1 {
			t.Fatalf("%s: %s", st, body)
		}
	}
	// the Jellyfin and device-copy side (Player.ExternalAudio) carries the fit too
	x.fitTrack(vofit.StateOK, 1200, 0.959)
	p := &Player{h: &lib{Deps: Deps{DB: x.db, Library: x.repo, Probe: func(context.Context, string) (media.ProbeResult, error) {
		return media.ProbeResult{Container: "matroska", DurationMs: 61_000, Audio: []media.AudioStream{{Codec: "ac3", Channels: 6}}}, nil
	}}}}
	f, err := x.repo.FileByID(context.Background(), x.file)
	if err != nil {
		t.Fatal(err)
	}
	ext := p.ExternalAudio(context.Background(), f, 60_000, nil)
	if len(ext) != 1 || ext[0].OffsetMs != 1200 || ext[0].Tempo != 0.959 {
		t.Fatalf("%+v", ext)
	}
}
