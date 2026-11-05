package subsonic

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// flacAlbum adds one real FLAC (secs long) as a one-track album.
func (e *env) flacAlbum(secs int) (trackID int64) {
	e.t.Helper()
	dir := filepath.Join(e.media, "Кино", "Группа крови")
	p := filepath.Join(dir, "01.flac")
	sine(e.t, p, secs)
	st, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	ctx := context.Background()
	al, err := e.repo.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{{
		File: library.FileInput{Path: p, Size: st.Size(), Mtime: st.ModTime().Unix()}, AlbumDir: dir,
		Album: "Группа крови", Artist: "Кино", Title: "Группа крови", Track: 1, Codec: "flac", Lossless: true}})
	if err != nil {
		e.t.Fatal(err)
	}
	trs, _ := e.repo.AlbumTracks(ctx, al)
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=? WHERE id=?`, secs*1000, trs[0].FileID); err != nil {
		e.t.Fatal(err)
	}
	return trs[0].ID
}

func TestStreamMaxBitRate128Transcodes(t *testing.T) {
	e := newEnv(t)
	ffmpeg(t)
	id := e.flacAlbum(2)
	p := e.creds()
	p.Set("id", itoa(id))
	p.Set("maxBitRate", "128")
	w := e.req("stream", "", p)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/mpeg" || w.Header().Get("Accept-Ranges") == "bytes" || w.Body.Len() == 0 {
		t.Fatalf("maxBitRate=128: %d %v (%d bytes)", w.Code, w.Header(), w.Body.Len())
	}
	// HEAD of a transcode answers the headers without running ffmpeg (a
	// bogus ffmpeg would fail it)
	good := e.srv.Audio.Transcoder
	e.srv.Audio.Transcoder = &media.AudioTranscoder{FFmpeg: "/nonexistent/ffmpeg", Limit: 4}
	r := httptest.NewRequest("HEAD", "/rest/stream?"+p.Encode(), nil)
	r.RemoteAddr = homeAddr
	hw := httptest.NewRecorder()
	e.srv.ServeHTTP(hw, r)
	e.srv.Audio.Transcoder = good
	if hw.Code != 200 || hw.Header().Get("Content-Type") != "audio/mpeg" || hw.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %v", hw.Code, hw.Header())
	}
	// at home without a limit: the original with Range
	p.Del("maxBitRate")
	w = e.req("stream", "", p)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/flac" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("home original: %d %v", w.Code, w.Header())
	}
	// maxBitRate=0 is "no preference": outside the home network the remote cap applies
	p.Set("maxBitRate", "0")
	w = e.req("stream", remoteAddr, p)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("remote maxBitRate=0: %d %v", w.Code, w.Header())
	}
	// format=raw is the original even outside
	p.Set("format", "raw")
	if w = e.req("stream", remoteAddr, p); w.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("raw: %v", w.Header())
	}
	// download is the original
	p.Del("format")
	if w = e.req("download", remoteAddr, p); w.Code != 200 || w.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("download: %d %v", w.Code, w.Header())
	}
}

func TestStreamCueTrack(t *testing.T) {
	e := newEnv(t)
	ff := ffmpeg(t)
	dir := filepath.Join(e.media, "Кино", "Образ")
	img := filepath.Join(dir, "image.flac")
	sine(t, img, 4)
	st, _ := os.Stat(img)
	ctx := context.Background()
	end := int64(2000)
	fi := library.FileInput{Path: img, Size: st.Size(), Mtime: st.ModTime().Unix()}
	al, err := e.repo.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{
		{File: fi, AlbumDir: dir, Album: "Образ", Artist: "Кино", Title: "Первая", Track: 1, EndMs: &end, Codec: "flac", Lossless: true},
		{File: fi, AlbumDir: dir, Album: "Образ", Artist: "Кино", Title: "Вторая", Track: 2, StartMs: 2000, Codec: "flac", Lossless: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	trs, _ := e.repo.AlbumTracks(ctx, al)
	if len(trs) != 2 {
		t.Fatalf("tracks %v", trs)
	}
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=4000 WHERE id=?`, trs[1].FileID); err != nil {
		t.Fatal(err)
	}
	p := e.creds()
	p.Set("id", itoa(trs[1].ID))
	w := e.req("stream", "", p)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	out := filepath.Join(t.TempDir(), "part")
	if err := os.WriteFile(out, w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := strings.TrimSuffix(ff, "ffmpeg") + "ffprobe"
	// a piped FLAC has no duration in its header: the end of the last packet
	b, err := exec.Command(probe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time",
		"-of", "csv=p=0", out).Output()
	if err != nil {
		t.Skipf("ffprobe: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(string(b)))
	if len(lines) == 0 {
		t.Fatalf("no packets in the response (%s)", w.Header().Get("Content-Type"))
	}
	f := strings.Split(lines[len(lines)-1], ",")
	pts, _ := strconv.ParseFloat(f[0], 64)
	dur := 0.0
	if len(f) > 1 {
		dur, _ = strconv.ParseFloat(f[1], 64)
	}
	if d := pts + dur; d < 1.7 || d > 2.3 {
		t.Fatalf("track 2 of the image lasts %.2f s (want ≈ 2 s), content-type %s", d, w.Header().Get("Content-Type"))
	}

	// F17: download (the client's offline cache) is the track cut from the
	// image, not the whole image, lossless as FLAC, even outside the home
	w = e.req("download", remoteAddr, p)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/flac" || w.Body.Len() == 0 || int64(w.Body.Len()) >= st.Size() {
		t.Fatalf("download of a CUE track: %d %v (%d of %d bytes)", w.Code, w.Header(), w.Body.Len(), st.Size())
	}
	if err := os.WriteFile(out, w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err = exec.Command(probe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time",
		"-of", "csv=p=0", out).Output(); err != nil {
		t.Fatalf("ffprobe of the download: %v", err)
	}
	lines = strings.Fields(strings.TrimSpace(string(b)))
	f = strings.Split(lines[len(lines)-1], ",")
	pts, _ = strconv.ParseFloat(f[0], 64)
	dur = 0
	if len(f) > 1 {
		dur, _ = strconv.ParseFloat(f[1], 64)
	}
	if d := pts + dur; d < 1.7 || d > 2.3 {
		t.Fatalf("downloaded track 2 lasts %.2f s (want ≈ 2 s)", d)
	}
	// getSong describes what download sends: a FLAC about half the image
	song := e.mustOK(e.call("getSong", "id", itoa(trs[1].ID)))["song"].(map[string]any)
	size, _ := song["size"].(float64)
	if song["suffix"] != "flac" || song["contentType"] != "audio/flac" || size <= 0 || int64(size) >= st.Size() {
		t.Fatalf("getSong of a CUE track: %v (image %d bytes)", song, st.Size())
	}
}

func TestScrobbleRecordsPlay(t *testing.T) {
	e := newEnv(t)
	_, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"})
	e.mustOK(e.call("scrobble", "id", itoa(tr[0]), "submission", "false"))
	if n, _ := e.music.PlayCount(context.Background(), tr[0]); n != 0 {
		t.Fatalf("now playing stored a play: %d", n)
	}
	e.mustOK(e.call("scrobble", "id", itoa(tr[0]), "time", "1700000000000"))
	if n, _ := e.music.PlayCount(context.Background(), tr[0]); n != 1 {
		t.Fatalf("plays %d", n)
	}
	if code, _ := errCode(e.call("scrobble", "id", "99999")); code != 70 {
		t.Fatalf("unknown track: %d", code)
	}
}

func TestLyrics(t *testing.T) {
	e := newEnv(t)
	_, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"})
	if _, err := e.db.Exec(`UPDATE tracks SET lyrics=? WHERE item_id=?`, "[ar:Кино]\n[00:01.50]Тёплое место\n[00:12.00][00:30.00]Но улицы ждут", tr[0]); err != nil {
		t.Fatal(err)
	}
	l := e.mustOK(e.call("getLyrics", "artist", "кино", "title", "группа крови"))["lyrics"].(map[string]any)
	if l["value"] != "Тёплое место\nНо улицы ждут\nНо улицы ждут" {
		t.Fatalf("getLyrics %v", l)
	}
	sl := e.mustOK(e.call("getLyricsBySongId", "id", itoa(tr[0])))["lyricsList"].(map[string]any)["structuredLyrics"].([]any)
	if len(sl) != 1 {
		t.Fatalf("%v", sl)
	}
	s := sl[0].(map[string]any)
	lines := s["line"].([]any)
	if s["synced"] != true || len(lines) != 3 || lines[0].(map[string]any)["start"] != 1500.0 || lines[2].(map[string]any)["start"] != 30000.0 {
		t.Fatalf("structured %v", s)
	}
}

func TestCoverArt(t *testing.T) {
	e := newEnv(t)
	al, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"})
	cover := filepath.Join(e.media, "cover.jpg")
	if err := os.WriteFile(cover, []byte("\xff\xd8\xff\xe0 jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET poster=? WHERE id=?`, "file:"+cover, al); err != nil {
		t.Fatal(err)
	}
	if a := e.mustOK(e.call("getAlbum", "id", itoa(al)))["album"].(map[string]any); a["coverArt"] != "al-"+itoa(al) {
		t.Fatalf("album with a cover: %v", a)
	}
	for _, id := range []string{"al-" + itoa(al), itoa(tr[0]), itoa(al)} {
		p := e.creds()
		p.Set("id", id)
		p.Set("size", "300")
		if w := e.req("getCoverArt", "", p); w.Code != 200 || w.Body.String() != "\xff\xd8\xff\xe0 jpeg" {
			t.Fatalf("cover %s: %d %q", id, w.Code, w.Body)
		}
	}
	got := e.mustOK(e.call("getArtists"))
	artistID := got["artists"].(map[string]any)["index"].([]any)[0].(map[string]any)["artist"].([]any)[0].(map[string]any)["id"].(string)
	p := e.creds()
	p.Set("id", "ar-"+artistID)
	if w := e.req("getCoverArt", "", p); w.Code != 200 || w.Body.Len() != len("\xff\xd8\xff\xe0 jpeg") {
		t.Fatalf("artist cover falls back to the album: %d %q", w.Code, w.Body)
	}
	pl := e.mustOK(e.call("createPlaylist", "name", "Дорога", "songId", itoa(tr[0])))["playlist"].(map[string]any)
	if pl["coverArt"] != pl["id"] {
		t.Fatalf("playlist cover %v", pl)
	}
	p.Set("id", pl["id"].(string))
	if w := e.req("getCoverArt", "", p); w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("playlist cover: %d", w.Code)
	}
}

func TestUserTranscodeCap(t *testing.T) {
	e := newEnv(t)
	_, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"})
	e.srv.UserTranscodes = 1
	if !e.srv.acquireStream(e.admin.ID) { // another stream of the same profile is running
		t.Fatal("first slot")
	}
	p := e.creds()
	p.Set("f", "json")
	p.Set("id", itoa(tr[0]))
	p.Set("maxBitRate", "128")
	w := e.req("stream", "", p)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "Слишком много потоков") {
		t.Fatalf("over the cap: %d %v %s", w.Code, w.Header(), w.Body)
	}
	// direct play takes no transcode slot
	p.Del("maxBitRate")
	if w := e.req("stream", "", p); w.Code != 200 {
		t.Fatalf("direct over the cap: %d %s", w.Code, w.Body)
	}
	e.srv.releaseStream(e.admin.ID)
	if e.srv.streams[e.admin.ID] != 0 {
		t.Fatal("slot not released")
	}
}

func TestAlbumWithoutCoverHasNoCoverArt(t *testing.T) {
	e := newEnv(t)
	al, _ := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"})
	a := e.mustOK(e.call("getAlbum", "id", itoa(al)))["album"].(map[string]any)
	if _, ok := a["coverArt"]; ok {
		t.Fatalf("coverArt without a cover: %v", a)
	}
	if _, ok := a["song"].([]any)[0].(map[string]any)["coverArt"]; ok {
		t.Fatalf("song coverArt without a cover")
	}
}

func TestSubsonicCoverArtSize(t *testing.T) {
	e := newEnv(t)
	al, _ := e.album("Кино", "Звезда по имени Солнце", 1989, "", tTrack{1, "Звезда"})
	img := image.NewRGBA(image.Rect(0, 0, 600, 600))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join(e.media, "big.jpg")
	os.WriteFile(cover, buf.Bytes(), 0o644)
	if _, err := e.db.Exec(`UPDATE items SET poster=? WHERE id=?`, "file:"+cover, al); err != nil {
		t.Fatal(err)
	}
	p := e.creds()
	p.Set("id", "al-"+itoa(al))
	p.Set("size", "120")
	w := e.req("getCoverArt", "", p)
	c, _, err := image.DecodeConfig(w.Body)
	if w.Code != 200 || err != nil || c.Width > 120 || c.Width == 0 {
		t.Fatalf("size=120: %d %v %+v", w.Code, err, c)
	}
	p.Del("size")
	if w := e.req("getCoverArt", "", p); w.Code != 200 || w.Body.Len() != buf.Len() {
		t.Fatalf("without size: the original (%d bytes, want %d)", w.Body.Len(), buf.Len())
	}
}
