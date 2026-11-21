package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	probeVideoOnly = `{"format":{"format_name":"matroska,webm","duration":"60"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p"}]}`
	probeVideoAudio = `{"format":{"format_name":"matroska,webm","duration":"60"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p"},
		{"index":1,"codec_type":"audio","codec_name":"ac3","channels":6,"sample_rate":"48000"}]}`
	probeAudioNoChannels = `{"format":{"format_name":"matroska,webm","duration":"60"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p"},
		{"index":1,"codec_type":"audio","codec_name":"dts"}]}`
	probeNoSize = `{"format":{"format_name":"matroska,webm","duration":"60"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264"},
		{"index":1,"codec_type":"audio","codec_name":"ac3","channels":2,"sample_rate":"48000"}]}`
	probeHDRVideo = `{"format":{"format_name":"matroska,webm","duration":"60"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084"},
		{"index":1,"codec_type":"audio","codec_name":"eac3","channels":6,"sample_rate":"48000"}]}`
	probeAudioOnly = `{"format":{"format_name":"flac","duration":"60"},"streams":[
		{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"44100"}]}`
	probeHDRMetaJSON = `{"streams":[{"side_data_list":[]}],"frames":[{"side_data_list":[{"side_data_type":"Mastering display metadata"}]}]}`
)

// fakeProbe is a script ffprobe: it logs each command line and answers
// quick (a run with -probesize), full (a run without) or meta (a run that
// shows frames, the HDR metadata look); fail makes quick runs exit 1, and
// metaQuickFails the quick metadata looks only.
func fakeProbe(t *testing.T, quick, full string, fail bool, metaQuickFails ...bool) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "args")
	for name, body := range map[string]string{"quick.json": quick, "full.json": full, "meta.json": probeHDRMetaJSON} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	failQuick := ""
	if fail {
		failQuick = "echo boom >&2; exit 1"
	} else {
		failQuick = "cat " + filepath.Join(dir, "quick.json")
	}
	metaQuick := ""
	if len(metaQuickFails) > 0 && metaQuickFails[0] {
		metaQuick = "*-probesize*-show_frames*) echo boom >&2; exit 1;;\n"
	}
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"case \"$*\" in\n" + metaQuick +
		"*-show_frames*) cat " + filepath.Join(dir, "meta.json") + ";;\n" +
		"*-probesize*) " + failQuick + ";;\n" +
		"*) cat " + filepath.Join(dir, "full.json") + ";;\n" +
		"esac\n"
	bin = filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func probeRuns(t *testing.T, log string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func limited(run string) bool {
	return strings.Contains(run, "-probesize 2M") && strings.Contains(run, "-analyzeduration 1M")
}

// A started input is probed with the quick limits, once, when the streams
// come back whole; a library file keeps the defaults.
func TestProbeInputQuickLook(t *testing.T) {
	bin, log := fakeProbe(t, probeVideoAudio, probeVideoAudio, false)
	r, err := ProbeInput(context.Background(), bin, "http://127.0.0.1:9/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if runs := probeRuns(t, log); len(runs) != 1 || !limited(runs[0]) {
		t.Fatalf("runs: %q", runs)
	}
	if r.Video == nil || len(r.Audio) != 1 || r.Audio[0].Channels != 6 {
		t.Fatalf("result: %+v", r)
	}
	os.Remove(log)
	if _, err := Probe(context.Background(), bin, "/media/a.mkv"); err != nil {
		t.Fatal(err)
	}
	if runs := probeRuns(t, log); len(runs) != 1 || strings.Contains(runs[0], "-probesize") || strings.Contains(runs[0], "-analyzeduration") {
		t.Fatalf("a library file's probe: %q", runs)
	}
}

// A quick look that finds no audio (it ended before the first audio packet)
// is repeated once with the defaults, and the full answer is the result.
func TestProbeInputRetriesWithoutAudio(t *testing.T) {
	bin, log := fakeProbe(t, probeVideoOnly, probeVideoAudio, false)
	r, err := ProbeInput(context.Background(), bin, "http://127.0.0.1:9/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	runs := probeRuns(t, log)
	if len(runs) != 2 || !limited(runs[0]) || strings.Contains(runs[1], "-probesize") {
		t.Fatalf("runs: %q", runs)
	}
	if len(r.Audio) != 1 {
		t.Fatalf("result has no audio: %+v", r)
	}
}

// An audio stream without its channels, or a video without its size, is
// incomplete too.
func TestProbeInputRetriesIncompleteStreams(t *testing.T) {
	for name, quick := range map[string]string{"channels": probeAudioNoChannels, "size": probeNoSize} {
		t.Run(name, func(t *testing.T) {
			bin, log := fakeProbe(t, quick, probeVideoAudio, false)
			r, err := ProbeInput(context.Background(), bin, "/tmp/x.mkv")
			if err != nil {
				t.Fatal(err)
			}
			if runs := probeRuns(t, log); len(runs) != 2 || !limited(runs[0]) || limited(runs[1]) {
				t.Fatalf("runs: %q", runs)
			}
			if r.Video == nil || r.Video.Width != 1920 || r.Audio[0].Channels != 6 {
				t.Fatalf("result: %+v", r)
			}
		})
	}
}

// A file that has no audio at all is probed twice at most, and answers.
func TestProbeInputNoAudioAnywhere(t *testing.T) {
	bin, log := fakeProbe(t, probeVideoOnly, probeVideoOnly, false)
	r, err := ProbeInput(context.Background(), bin, "/tmp/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if runs := probeRuns(t, log); len(runs) != 2 {
		t.Fatalf("runs: %q", runs)
	}
	if r.Video == nil || len(r.Audio) != 0 {
		t.Fatalf("result: %+v", r)
	}
}

// A failing quick look is an error: not retried (the input is not read twice).
func TestProbeInputErrorNotRetried(t *testing.T) {
	bin, log := fakeProbe(t, "", probeVideoAudio, true)
	if _, err := ProbeInput(context.Background(), bin, "/tmp/x.mkv"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if runs := probeRuns(t, log); len(runs) != 1 {
		t.Fatalf("runs: %q", runs)
	}
	// an unsafe input is refused before any run
	os.Remove(log)
	if _, err := ProbeInput(context.Background(), bin, "http://192.168.1.5/x.mkv"); err != ErrUnsafeInput {
		t.Fatalf("err = %v", err)
	}
	if runs := probeRuns(t, log); len(runs) != 0 {
		t.Fatalf("runs: %q", runs)
	}
}

// The HDR10 metadata look of an input uses the quick limits too.
func TestProbeInputHDRMetaLook(t *testing.T) {
	bin, log := fakeProbe(t, probeHDRVideo, probeHDRVideo, false)
	r, err := ProbeInput(context.Background(), bin, "/tmp/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	runs := probeRuns(t, log)
	if len(runs) != 2 || !limited(runs[0]) || !limited(runs[1]) || !strings.Contains(runs[1], "-show_frames") {
		t.Fatalf("runs: %q", runs)
	}
	if r.Video == nil || r.Video.HDRMastering == nil || !*r.Video.HDRMastering {
		t.Fatalf("hdr meta: %+v", r.Video)
	}
}

// An HDR10 picture whose quick metadata look found no frame (a first frame
// bigger than the limit) is probed again with the defaults.
func TestProbeInputHDRMetaRetried(t *testing.T) {
	bin, log := fakeProbe(t, probeHDRVideo, probeHDRVideo, false, true)
	r, err := ProbeInput(context.Background(), bin, "/tmp/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	runs := probeRuns(t, log)
	if len(runs) != 4 || !limited(runs[0]) || !limited(runs[1]) || limited(runs[2]) || limited(runs[3]) {
		t.Fatalf("runs: %q", runs)
	}
	if r.Video == nil || r.Video.HDRMastering == nil || !*r.Video.HDRMastering {
		t.Fatalf("hdr meta: %+v", r.Video)
	}
}

// An audio-only input is whole with its audio: one quick run.
func TestProbeInputAudioOnly(t *testing.T) {
	bin, log := fakeProbe(t, probeAudioOnly, probeAudioOnly, false)
	r, err := ProbeInput(context.Background(), bin, "/tmp/x.flac")
	if err != nil {
		t.Fatal(err)
	}
	if runs := probeRuns(t, log); len(runs) != 1 || r.Video != nil || len(r.Audio) != 1 {
		t.Fatalf("runs %q result %+v", runs, r)
	}
}
