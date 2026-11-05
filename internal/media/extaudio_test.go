package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The external file comes from outside: file protocol and the audio
// demuxers only (the names are ffmpeg's: ffmpeg -demuxers).
func TestExternalAudioInputWhitelist(t *testing.T) {
	want := "-protocol_whitelist file -format_whitelist matroska,webm,ac3,eac3,dts,aac,flac,mp3,wav,ogg,mov,mp4,m4a,truehd,mlp -i file:/m/a.mka"
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioInput: "/m/a.mka"}
	if j := strings.Join(BuildArgs("none", "/m/v.mkv", d, 0, "/w"), " "); !strings.Contains(j, want) {
		t.Fatal(j)
	}
	pl := DeviceCopyPlan{VideoCopy: true, VideoCodec: "h264", AudioCopy: true, AudioInput: "/m/a.mka"}
	if j := strings.Join(DeviceCopyArgs("none", "/m/v.mkv", pl, "/d/x.part.mp4"), " "); !strings.Contains(j, want) {
		t.Fatal(j)
	}
}

// ProbeAudioFile reads an audio file, never another format under its name.
func TestProbeAudioFileWhitelist(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	ac3, fake := filepath.Join(dir, "a.ac3"), filepath.Join(dir, "b.ac3")
	for _, c := range [][]string{{"-f", "lavfi", "-i", "sine=duration=2", "-c:a", "ac3", ac3},
		{"-f", "lavfi", "-i", "sine=duration=2", "-c:a", "mp2", "-f", "mpegts", fake}} {
		if out, err := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, c...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	if p, err := ProbeAudioFile(context.Background(), probe, ac3); err != nil || len(p.Audio) != 1 || p.Audio[0].Codec != "ac3" {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := ProbeAudioFile(context.Background(), probe, fake); err == nil {
		t.Fatal("an MPEG-TS read as audio")
	}
}

func TestDeviceCopyExternalAudio(t *testing.T) {
	p := ProbeResult{Container: "matroska", Video: &VideoStream{Codec: "h264", Height: 1080, BitDepth: 8},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Default: true}}, Subs: []SubStream{{Index: 2, Codec: "subrip", Lang: "eng"}}}
	ext := []ExternalSub{{Path: "/m/a.ru.srt", Lang: "ru", Codec: "subrip"}}
	pl := PlanDeviceCopyExternal(p, ext, 0, 0, AudioStream{Index: 3, Codec: "dts", Channels: 6, Lang: "ru", Title: "LostFilm"}, "/m/Sound/a.dts")
	if pl.AudioIndex != 0 || pl.AudioCopy || pl.AudioInput != "/m/Sound/a.dts" || pl.AudioLang != "ru" || pl.AudioTitle != "LostFilm" {
		t.Fatalf("%+v", pl)
	}
	j := strings.Join(DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	for _, want := range []string{
		"-i file:/m/a.mkv -protocol_whitelist file -format_whitelist " + AudioFormats + " -i file:/m/Sound/a.dts -protocol_whitelist file -f srt -i file:/m/a.ru.srt",
		"-map 0:v:0 -map 1:a:0 -map 0:2 -map 2:s:0", "-c:a aac", "-metadata:s:a:0 language=rus", "-metadata:s:a:0 title=LostFilm",
		"-metadata:s:s:1 language=rus",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
	// AC-3 plays in AVPlayer as it is
	pl = PlanDeviceCopyExternal(p, nil, 0, 0, AudioStream{Codec: "ac3", Channels: 6}, "/m/a.ac3")
	if !pl.AudioCopy {
		t.Fatalf("%+v", pl)
	}
}

// Without an external track the copy's command is what it always was.
func TestDeviceCopyArgsNoExternalAudioUnchanged(t *testing.T) {
	pl := DeviceCopyPlan{VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true,
		ExternalSubs: []ExternalSub{{Path: "/m/a.ru.srt", Lang: "ru", Codec: "subrip"}}}
	j := strings.Join(DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	if !strings.Contains(j, "-map 0:v:0 -map 0:1 -map 1:s:0") || strings.Contains(j, "metadata:s:a") {
		t.Fatal(j)
	}
}

// Review focus 5: another external track, or none, is another copy.
func TestDeviceCopyKeyExternalAudio(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.ac3"), filepath.Join(dir, "b.ac3")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k := DeviceCopyKey{FileID: 7, Size: 100, ModTime: mod, AudioIndex: -1, ExtSubsHash: ExternalSubsHash(nil)}
	plain := k.ID()
	// the id of a copy without an external track is the one it always had
	want := fmt.Sprintf("v1|%d|%d|%d|%d|%d|%d|%s", k.FileID, k.Size, k.ModTime.UnixNano(), k.MaxHeight, k.MaxBitrate, k.AudioIndex, k.ExtSubsHash)
	sum := sha256.Sum256([]byte(want))
	if plain != hex.EncodeToString(sum[:])[:16] {
		t.Fatal("the plain key changed")
	}
	k.AudioIndex, k.ExtAudioHash = 2000, ExternalAudioHash(a)
	ka := k.ID()
	k.ExtAudioHash = ExternalAudioHash(b)
	kb := k.ID()
	if ka == plain || kb == plain || ka == kb {
		t.Fatal(plain, ka, kb)
	}
	if ExternalAudioHash(a) != ExternalAudioHash(a) || ExternalAudioHash("") != "" {
		t.Fatal("hash")
	}
}

// Review focus 1: a resume at 40 min seeks the external audio to the same
// place as the video (input -ss on both inputs).
func TestBuildArgsExternalAudioSeeksBothInputs(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 0,
		AudioInput: "/m/RUS Sound/Show.S01E02.ac3", AudioInputCodec: "ac3"}
	a := BuildArgs("none", "/m/Show.S01E02.mkv", d, 2400, "/w/s1")
	var inputs, seeks []int
	for i, x := range a {
		switch x {
		case "-i":
			inputs = append(inputs, i)
		case "-ss":
			seeks = append(seeks, i)
		}
	}
	if len(inputs) != 2 || len(seeks) != 2 || !(seeks[0] < inputs[0] && inputs[0] < seeks[1] && seeks[1] < inputs[1]) {
		t.Fatalf("inputs %v seeks %v: %v", inputs, seeks, a)
	}
	if a[seeks[0]+1] != "2400" || a[seeks[1]+1] != "2400" {
		t.Fatalf("seek values: %v", a)
	}
	if a[inputs[1]+1] != "file:/m/RUS Sound/Show.S01E02.ac3" || a[inputs[1]-4] != "-protocol_whitelist" || a[inputs[1]-3] != "file" ||
		a[inputs[1]-2] != "-format_whitelist" {
		t.Fatalf("second input: %v", a)
	}
	j := strings.Join(a, " ")
	if !strings.Contains(j, "-map 1:a:0") || strings.Contains(j, "-map 0:0") || !strings.Contains(j, "-map 0:v:0?") || !strings.Contains(j, "-c:a copy") {
		t.Fatalf("maps: %s", j)
	}
	// a copied track keeps what its demuxer read before the seek point (a
	// Matroska cluster, seconds): dropped, or the muxer would move the
	// stream's start there (checked with ffmpeg 9.0)
	if !strings.Contains(j, `-c:a copy -bsf:a noise=drop=lt(pts\,0)`) {
		t.Fatalf("no drop before the start: %s", j)
	}
	// from the start, or re-encoded (the decoder trims it): nothing to drop
	if j := strings.Join(BuildArgs("none", "/m/Show.S01E02.mkv", d, 0, "/w/s1"), " "); strings.Contains(j, "-bsf:a") {
		t.Fatalf("%s", j)
	}
	d.AudioCopy = false
	if j := strings.Join(BuildArgs("none", "/m/Show.S01E02.mkv", d, 2400, "/w/s1"), " "); strings.Contains(j, "-bsf:a") {
		t.Fatalf("%s", j)
	}
}

func TestBuildArgsExternalAudioFromStart(t *testing.T) {
	d := Decision{Mode: "transcode", VideoCodec: "hevc", AudioIndex: 0, AudioInput: "/m/a.mka", AudioInputCodec: "dts"}
	a := BuildArgs("none", "/m/v.mkv", d, 0, "/w/s1")
	if slices.Contains(a, "-ss") {
		t.Fatalf("seek at 0: %v", a)
	}
	j := strings.Join(a, " ")
	if !strings.Contains(j, "-i file:/m/v.mkv -protocol_whitelist file -format_whitelist "+AudioFormats+" -i file:/m/a.mka -map 0:v:0? -map 1:a:0") ||
		!strings.Contains(j, "-c:a aac") {
		t.Fatalf("%s", j)
	}
}

// Without an external track the command is what it always was.
func TestBuildArgsNoExternalAudioUnchanged(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 1}
	j := strings.Join(BuildArgs("none", "/m/in.mkv", d, 30, "/w/s1"), " ")
	if strings.Count(j, "-i ") != 1 || strings.Count(j, "-ss ") != 1 || !strings.Contains(j, "-map 0:1") {
		t.Fatalf("%s", j)
	}
}

// Review focus 4: an external track never plays direct.
func TestDecideExternalForcesHLS(t *testing.T) {
	p := ProbeResult{Container: "mp4", Video: &VideoStream{Codec: "h264", Width: 1920, Height: 1080},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}}}
	if d := Decide(p, safariCaps, 1); d.Mode != "direct" {
		t.Fatalf("fixture: %+v", d)
	}
	ext := AudioStream{Index: 7, Codec: "ac3", Channels: 6, Lang: "ru"}
	d := DecideExternal(p, safariCaps, ext, "/m/Film.LostFilm.ac3", 0)
	if d.Mode != "remux" || !d.VideoCopy || !d.AudioCopy || d.AudioIndex != 0 || d.AudioInput != "/m/Film.LostFilm.ac3" ||
		d.AudioInputCodec != "ac3" || d.AudioCodec != "ac3" || d.FMP4 {
		t.Fatalf("%+v", d)
	}
	// a codec the client lacks: re-encoded, like an embedded track
	d = DecideExternal(p, ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, Containers: []string{"mp4"}},
		AudioStream{Codec: "dts", Channels: 6}, "/m/a.dts", 0)
	if d.Mode != "remux" || d.AudioCopy || d.AudioInput != "/m/a.dts" {
		t.Fatalf("%+v", d)
	}
	// burnt-in subtitles: the video is transcoded
	d = DecideExternal(p, safariCaps, ext, "/m/a.ac3", 5)
	if d.Mode != "transcode" || d.BurnSub != 5 || d.AudioInput != "/m/a.ac3" {
		t.Fatalf("%+v", d)
	}
	// a file without its own audio
	p.Audio = nil
	if d := DecideExternal(p, safariCaps, ext, "/m/a.ac3", 0); d.Mode != "remux" || d.AudioInput == "" {
		t.Fatalf("%+v", d)
	}
}

// An external track off the video's timeline (a cleaned voice-over made
// when the file began elsewhere) is shifted by its offset with -itsoffset
// on its own input: in the HLS session (before its seek) and in the
// device copy. 0: no option.
func TestExternalAudioInputOffset(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioInput: "/m/a.mka", AudioInputOffsetMs: 21}
	j := strings.Join(BuildArgs("none", "/m/v.mkv", d, 30, "/w"), " ")
	if !strings.Contains(j, "-i file:/m/v.mkv -itsoffset 0.021 -ss 30 -protocol_whitelist file") {
		t.Fatal(j)
	}
	d.AudioInputOffsetMs = -1500
	if j := strings.Join(BuildArgs("none", "/m/v.mkv", d, 0, "/w"), " "); !strings.Contains(j, "-i file:/m/v.mkv -itsoffset -1.5 -protocol_whitelist file") {
		t.Fatal(j)
	}
	d.AudioInputOffsetMs = 0
	if j := strings.Join(BuildArgs("none", "/m/v.mkv", d, 30, "/w"), " "); strings.Contains(j, "itsoffset") {
		t.Fatal(j)
	}
	pl := DeviceCopyPlan{VideoCopy: true, VideoCodec: "h264", AudioCopy: true, AudioInput: "/m/a.mka", AudioInputOffsetMs: 21}
	if j := strings.Join(DeviceCopyArgs("none", "/m/v.mkv", pl, "/d/x.part.mp4"), " "); !strings.Contains(j, "-i file:/m/v.mkv -itsoffset 0.021 -protocol_whitelist file") {
		t.Fatal(j)
	}
	pl.AudioInputOffsetMs = 0
	if j := strings.Join(DeviceCopyArgs("none", "/m/v.mkv", pl, "/d/x.part.mp4"), " "); strings.Contains(j, "itsoffset") {
		t.Fatal(j)
	}
}

// DecideExternalAudio is DecideExternal of an ExternalAudio, its offset kept.
func TestDecideExternalAudioOffset(t *testing.T) {
	p := ProbeResult{Container: "mp4", Video: &VideoStream{Codec: "h264", Width: 1920, Height: 1080},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}}}
	e := ExternalAudio{Path: "/d/audio/1-2.mka", Kind: "cleaned", CleanedOf: 2, OffsetMs: 21, Stream: AudioStream{Codec: "eac3", Channels: 6}}
	d := DecideExternalAudio(p, safariCaps, e, 0)
	if d.Mode != "remux" || d.AudioInput != e.Path || d.AudioInputOffsetMs != 21 || d.AudioInputCodec != "eac3" {
		t.Fatalf("%+v", d)
	}
	e.Stream.Title = "LostFilm"
	pl := PlanDeviceCopyExternalAudio(p, nil, 0, 0, e, "ru")
	if pl.AudioInput != e.Path || pl.AudioInputOffsetMs != 21 || pl.AudioTitle != "LostFilm (очищено)" {
		t.Fatalf("%+v", pl)
	}
	// a title for players without badges; the others as they are
	if e.Title("ru") != "LostFilm (очищено)" || e.Title("en") != "LostFilm (cleaned)" ||
		(ExternalAudio{Stream: AudioStream{Title: "LostFilm"}}).Title("en") != "LostFilm" {
		t.Fatal(e.Title("ru"), e.Title("en"))
	}
	if e.Stream.Title = ""; e.Title("ru") != "Очищено" || e.Title("en") != "Cleaned" {
		t.Fatal(e.Title("ru"))
	}
}

// The probe keeps where the file's timeline begins (format start_time):
// what ffmpeg subtracts from every stream of the file it reads.
func TestParseProbeStartTime(t *testing.T) {
	js := `{"format":{"format_name":"matroska,webm","duration":"10.0","start_time":"1.521000"},"streams":[]}`
	r, err := parseProbe([]byte(js), "/m/a.mkv")
	if err != nil || r.StartMs == nil || *r.StartMs != 1521 {
		t.Fatalf("%v %+v", err, r)
	}
	if r, _ := parseProbe([]byte(`{"format":{"format_name":"matroska,webm","duration":"10.0"},"streams":[]}`), "/m/a.mkv"); r.StartMs != nil {
		t.Fatalf("unknown start: %v", *r.StartMs)
	}
}

// A fitted voice-over of a PAL master (vofit: tempo ≠ 1) plays slowed by
// atempo, so its audio is always re-encoded; its own input is seeked to
// the session's start times the tempo (the track runs faster on its own
// time) and shifted by its offset. Device copies the same, from the start.
func TestExternalAudioTempo(t *testing.T) {
	p := ProbeResult{Container: "mp4", Video: &VideoStream{Codec: "h264", Width: 1920, Height: 1080},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}}}
	e := ExternalAudio{Path: "/m/v.ru.LostFilm.ac3", OffsetMs: 1200, Tempo: 0.959, Stream: AudioStream{Codec: "aac", Channels: 2}}
	d := DecideExternalAudio(p, safariCaps, e, 0)
	if d.AudioCopy || d.AudioInputTempo != 0.959 || d.AudioInputOffsetMs != 1200 {
		t.Fatalf("%+v", d)
	}
	j := strings.Join(BuildArgs("none", "/m/v.mp4", d, 100, "/w"), " ")
	if !strings.Contains(j, "-itsoffset 1.2 -ss 95.9 -protocol_whitelist file") || !strings.Contains(j, "-af atempo=0.959 -c:a aac") {
		t.Fatal(j)
	}
	if j := strings.Join(BuildArgs("none", "/m/v.mp4", d, 0, "/w"), " "); strings.Contains(j, "-ss") || !strings.Contains(j, "-af atempo=0.959") {
		t.Fatal(j)
	}
	// no tempo: no filter, copied as before
	e.Tempo = 1
	if d := DecideExternalAudio(p, safariCaps, e, 0); !d.AudioCopy || strings.Contains(strings.Join(BuildArgs("none", "/m/v.mp4", d, 0, "/w"), " "), "atempo") {
		t.Fatalf("%+v", d)
	}
	e.Tempo = 0.959
	pl := PlanDeviceCopyExternalAudio(p, nil, 0, 0, e, "ru")
	if pl.AudioCopy || pl.AudioInputTempo != 0.959 {
		t.Fatalf("%+v", pl)
	}
	if j := strings.Join(DeviceCopyArgs("none", "/m/v.mp4", pl, "/d/x.part.mp4"), " "); !strings.Contains(j, "-af atempo=0.959 -c:a aac") ||
		!strings.Contains(j, "-itsoffset 1.2 -protocol_whitelist file") {
		t.Fatal(j)
	}
}
