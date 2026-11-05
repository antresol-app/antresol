package voclean

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

// fitTrack puts a voice-over file beside a video (a stage-B download)
// with its fit (vofit's row).
func (fx *wfix) fitTrack(name, tag, state string, offsetMs, tempo float64) string {
	fx.t.Helper()
	var video string
	var size, mtime int64
	if err := fx.d.QueryRow(`SELECT path, size, mtime FROM files WHERE id=?`, fx.files[name]).Scan(&video, &size, &mtime); err != nil {
		fx.t.Fatal(err)
	}
	p := strings.TrimSuffix(video, ".mkv") + tag
	if err := os.WriteFile(p, []byte("mka"), 0o644); err != nil {
		fx.t.Fatal(err)
	}
	st, _ := os.Stat(p)
	fx.exec(`INSERT INTO external_audio_fit(path, file_id, state, offset_ms, tempo, track_size, track_mtime, file_size, file_mtime) VALUES(?,?,?,?,?,?,?,?,?)`,
		p, fx.files[name], state, offsetMs, tempo, st.Size(), st.ModTime().UnixNano(), size, mtime)
	return p
}

// A voice-over downloaded beside the video and fitted (offset 200 ms) is
// cleaned like the file's own: measured against the file's English track
// with the fit's offset applied to its input, its cleaned track listed.
// One of another cut, or slowed from a PAL master, is never cleaned.
func TestWorkerCleansExternalVoiceOver(t *testing.T) {
	fx := newWFix(t)
	m := fx.item("movie", 0, "M", 1, 0, 0)
	fx.file("m", m, probeOf("eng::2")) // stereo: an English 5.1 beside it would be taken as the original
	lf := fx.fitTrack("m", ".ru.LostFilm.mka", "ok", 200, 1)
	fx.fitTrack("m", ".ru.Other.mka", "misfit", 0, 1)
	fx.fitTrack("m", ".ru.PAL.mka", "ok", 30, 0.959)
	// an English track named after a studio is no voice-over (nor an original)
	fx.fitTrack("m", ".en.LostFilm.mka", "ok", 0, 1)
	var ext []ExternalTrack
	measure := fx.w.Eligible
	fx.w.Eligible = func(ctx context.Context, in Input, pairs []Pair) ([]Measure, error) {
		ext = in.External
		for _, p := range pairs {
			if p.VoInput != 1 || p.Vo.Index != ExternalIndex(lf) || p.Vo.Channels != 6 || p.Orig.Index != 1 {
				t.Errorf("pair %+v", p)
			}
		}
		return measure(ctx, in, pairs)
	}
	fx.w.ProbeTrack = func(_ context.Context, p string) (media.AudioStream, error) {
		return media.AudioStream{Codec: "eac3", Channels: 6, SampleRate: 48000}, nil
	}
	fx.pass()
	idx := ExternalIndex(lf)
	if got := fx.takeCalls(); !reflect.DeepEqual(got, []string{fmt.Sprintf("measure m/%d", idx), fmt.Sprintf("clean m/%d", idx)}) {
		t.Fatalf("calls %v", got)
	}
	if len(ext) != 1 || ext[0].Path != lf || ext[0].OffsetMs != 200 {
		t.Fatalf("external inputs %+v", ext)
	}
	tracks, err := Tracks(context.Background(), fx.d, fx.files["m"])
	if err != nil || len(tracks) != 1 || tracks[0].VoPath != lf || tracks[0].Studio != "LostFilm" || tracks[0].Lang != "ru" {
		t.Fatalf("%+v %v", tracks, err)
	}
	if !strings.HasSuffix(tracks[0].Path, fmt.Sprintf("%d-e%d.mka", fx.files["m"], -idx)) {
		t.Fatalf("path %s", tracks[0].Path)
	}
	// kept by the next pass (not a stray file), nothing measured again
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("second pass %v", c)
	}
	if _, err := os.Stat(tracks[0].Path); err != nil {
		t.Fatal(err)
	}
}
