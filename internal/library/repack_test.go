package library

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTS writes a short MPEG-TS film (MPEG-2 video and MP2 audio: both
// in every ffmpeg build). Skips the test when ffmpeg is missing.
func writeTS(t *testing.T, path string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3",
		"-c:v", "mpeg2video", "-c:a", "mp2", "-metadata:s:a:0", "language=rus",
		"-f", "mpegts", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
}

func repackFixture(t *testing.T) (*Scanner, *Repacker, string) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	s, mv, _ := newScanner(t)
	src := filepath.Join(mv, "Madagascar (2005)", "Madagascar.2005.BD-Remux.ts")
	writeTS(t, src)
	if _, err := s.ScanVideo(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := &Repacker{DB: s.Repo.db, Scanner: s, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Keep: 7 * 24 * time.Hour}
	return s, r, src
}

func liveFilePaths(t *testing.T, d *sql.DB) map[int64]string {
	t.Helper()
	rows, err := d.Query(`SELECT id, path FROM files WHERE missing_since IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			t.Fatal(err)
		}
		m[id] = p
	}
	return m
}

// A .ts film becomes .mkv in place: the same file row (progress, subtitles
// and the item stay), the original kept hidden beside it, and a later scan
// sees one film with one file.
func TestRepackTSToMatroska(t *testing.T) {
	s, r, src := repackFixture(t)
	ctx := context.Background()
	before := liveFilePaths(t, s.Repo.db)
	if len(before) != 1 {
		t.Fatalf("files %v", before)
	}
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	mkv := strings.TrimSuffix(src, ".ts") + ".mkv"
	if _, err := os.Stat(mkv); err != nil {
		t.Fatalf("no mkv: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("the .ts is still visible: %v", err)
	}
	orig := filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+repackOriginalSuffix)
	if _, err := os.Stat(orig); err != nil {
		t.Fatalf("original not kept: %v", err)
	}
	after := liveFilePaths(t, s.Repo.db)
	for id := range before {
		if after[id] != mkv {
			t.Fatalf("file %d: %q, want %q", id, after[id], mkv)
		}
	}
	var probe sql.NullString
	if err := s.Repo.db.QueryRow(`SELECT probe_json FROM files WHERE path=?`, mkv).Scan(&probe); err != nil || probe.Valid {
		t.Fatalf("probe kept: %v %v", probe, err)
	}
	// the hidden original and the temp file are not films
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if got := liveFilePaths(t, s.Repo.db); len(got) != 1 || got[firstKey(before)] != mkv {
		t.Fatalf("after a scan: %v", got)
	}
	// streams kept: video, audio with its language
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type:stream_tags=language", "-of", "csv=p=0", mkv).Output()
	if err != nil || !strings.Contains(string(out), "video") || !strings.Contains(string(out), "audio,rus") {
		t.Fatalf("streams %q %v", out, err)
	}
}

func firstKey(m map[int64]string) int64 {
	for k := range m {
		return k
	}
	return 0
}

// The kept original goes once Keep has passed.
func TestRepackPurgesOriginals(t *testing.T) {
	_, r, src := repackFixture(t)
	ctx := context.Background()
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	orig := filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+repackOriginalSuffix)
	r.Now = func() time.Time { return time.Now().Add(6 * 24 * time.Hour) }
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orig); err != nil {
		t.Fatalf("removed before Keep: %v", err)
	}
	r.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orig); !os.IsNotExist(err) {
		t.Fatalf("original still there: %v", err)
	}
}

// A file ffmpeg cannot read stays as it is, leaves no temp file, and is
// not tried again until it changes.
func TestRepackFailureLeavesTheFile(t *testing.T) {
	s, r, src := repackFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(src, []byte("not a transport stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := 0
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if filepath.Base(name) == "ffmpeg" {
			runs++
		}
		return exec.CommandContext(ctx, name, args...)
	}
	for range 2 {
		if err := r.Pass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Fatalf("ffmpeg ran %d times", runs)
	}
	if b, err := os.ReadFile(src); err != nil || string(b) != "not a transport stream" {
		t.Fatalf("source changed: %q %v", b, err)
	}
	es, _ := os.ReadDir(filepath.Dir(src))
	if len(es) != 1 {
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		t.Fatalf("left over: %v", names)
	}
	if got := liveFilePaths(t, s.Repo.db); got[firstKey(got)] != src {
		t.Fatalf("db %v", got)
	}
}

// A file another tool keeps open (shared) and a hard-linked one (another app holds the
// same data) stay .ts.
func TestRepackSkipsTorrentAndLinkedFiles(t *testing.T) {
	s, r, src := repackFixture(t)
	ctx := context.Background()
	if _, err := s.Repo.db.Exec(`UPDATE files SET torrent_infohash='abc'`); err != nil {
		t.Fatal(err)
	}
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("torrent file repacked: %v", err)
	}
	if _, err := s.Repo.db.Exec(`UPDATE files SET torrent_infohash=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(src, filepath.Join(t.TempDir(), "link.ts")); err != nil {
		t.Skip("no hard links here")
	}
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("linked file repacked: %v", err)
	}
}
