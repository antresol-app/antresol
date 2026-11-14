package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// copyEnv is a libEnv with device copies whose ffmpeg writes 2000 bytes
// (after gate is closed, when set) and records its command lines.
type copyEnv struct {
	*libEnv
	dc   *media.DeviceCopies
	mu   sync.Mutex
	args [][]string
	gate chan struct{}
	free int64
	subs map[string]string // decoded subtitle inputs under Dir/subs, read while ffmpeg runs
}

func newCopyEnv(t *testing.T, opt func(*Deps, *sql.DB)) *copyEnv {
	c := &copyEnv{free: 1 << 50, subs: map[string]string{}}
	c.dc = &media.DeviceCopies{Dir: t.TempDir(), HW: "none",
		FreeBytes: func(string) (int64, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.free, nil },
		Run: func(ctx context.Context, args []string, progress func(int64)) error {
			c.mu.Lock()
			c.args = append(c.args, args)
			for i, a := range args {
				if a == "-i" && strings.HasPrefix(args[i+1], "file:"+filepath.Join(c.dc.Dir, "subs")) {
					b, _ := os.ReadFile(strings.TrimPrefix(args[i+1], "file:"))
					c.subs[strings.TrimPrefix(args[i+1], "file:")] = string(b)
				}
			}
			gate := c.gate
			c.mu.Unlock()
			if gate != nil {
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return os.WriteFile(args[len(args)-1], make([]byte, 2000), 0o644)
		}}
	t.Cleanup(c.dc.Close)
	c.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DeviceCopies = c.dc
		if opt != nil {
			opt(d, db)
		}
	})
	return c
}

func (c *copyEnv) lastArgs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.args) == 0 {
		return nil
	}
	return c.args[len(c.args)-1]
}

func (c *copyEnv) post(fileID int64, quality string, ck *http.Cookie) (int, media.CopyStatus, string) {
	w := c.do("POST", "/api/device-copies", `{"fileId":`+itoa(fileID)+`,"quality":"`+quality+`"}`, ck)
	var st media.CopyStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	return w.Code, st, w.Body.String()
}

func (c *copyEnv) waitReady(id string, ck *http.Cookie) media.CopyStatus {
	c.t.Helper()
	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) {
		w := c.do("GET", "/api/device-copies/"+id, "", ck)
		var st media.CopyStatus
		json.Unmarshal(w.Body.Bytes(), &st)
		if w.Code != 200 {
			c.t.Fatalf("%d %s", w.Code, w.Body)
		}
		if st.State == "ready" {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("copy never ready")
	return media.CopyStatus{}
}

func TestDeviceCopyFlow(t *testing.T) {
	c := newCopyEnv(t, nil)
	_, f := c.movie("Брат", "16+", "0123456789")
	c.probeJSON(f, remuxProbe)
	code, st, body := c.post(f, "original", c.admin)
	if code != 202 || len(st.ID) != 16 || st.FileID != f || st.Quality.Applied != "original" {
		t.Fatalf("%d %s", code, body)
	}
	ready := c.waitReady(st.ID, c.admin)
	if ready.Size != 2000 || ready.Progress != 1 {
		t.Fatalf("%+v", ready)
	}
	if !slices.Contains(c.lastArgs(), "copy") || !strings.HasSuffix(c.lastArgs()[len(c.lastArgs())-1], ".part.mp4") {
		t.Fatalf("%q", c.lastArgs())
	}
	w := c.do("GET", "/api/device-copies/"+st.ID+"/file", "", c.admin, "Range", "bytes=0-99")
	if w.Code != 206 || w.Header().Get("Content-Type") != "video/mp4" || w.Body.Len() != 100 || w.Header().Get("Last-Modified") == "" {
		t.Fatalf("%d %v %d", w.Code, w.Header(), w.Body.Len())
	}
	if code, st2, body := c.post(f, "original", c.admin); code != 200 || st2.State != "ready" || st2.ID != st.ID {
		t.Fatalf("again: %d %s", code, body)
	}
	if w := c.do("GET", "/api/device-copies/0123456789abcdef", "", c.admin); w.Code != 404 || !strings.Contains(w.Body.String(), "Копия не найдена") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := c.do("GET", "/api/device-copies/..%2f..%2fx/file", "", c.admin); w.Code != 404 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestDeviceCopyAutoQuality(t *testing.T) {
	c := newCopyEnv(t, func(d *Deps, _ *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return true }
	})
	_, f := c.movie("Волк", "18+", "x")
	c.probeJSON(f, remuxProbe) // 30 Mbit/s 1080p
	code, st, body := c.post(f, "auto", c.admin)
	if code != 202 || st.Quality.Applied != "1080" || st.Quality.Selected != "auto" || !st.Quality.Remote {
		t.Fatalf("%d %s", code, body)
	}
	c.waitReady(st.ID, c.admin)
	j := strings.Join(c.lastArgs(), " ")
	if !strings.Contains(j, "libx264") || !strings.Contains(j, "-maxrate 8000000") {
		t.Fatalf("not capped: %s", j)
	}
}

func TestDeviceCopyKidHidden(t *testing.T) {
	c := newCopyEnv(t, nil)
	_, f := c.movie("Волк", "18+", "x")
	c.probeJSON(f, remuxProbe)
	kid := c.kid("12")
	if code, _, body := c.post(f, "original", kid); code != 404 {
		t.Fatalf("kid post: %d %s", code, body)
	}
	_, st, _ := c.post(f, "original", c.admin)
	c.waitReady(st.ID, c.admin)
	for _, p := range []string{"/api/device-copies/" + st.ID, "/api/device-copies/" + st.ID + "/file"} {
		w := c.do("GET", p, "", kid)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "Копия не найдена") {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestDeviceCopyFileBeforeReady(t *testing.T) {
	c := newCopyEnv(t, nil)
	c.gate = make(chan struct{})
	defer close(c.gate)
	_, f := c.movie("Брат", "16+", "x")
	c.probeJSON(f, remuxProbe)
	_, st, _ := c.post(f, "original", c.admin)
	w := c.do("GET", "/api/device-copies/"+st.ID+"/file", "", c.admin)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Копия ещё готовится") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestDeviceCopyBadQuality(t *testing.T) {
	c := newCopyEnv(t, nil)
	_, f := c.movie("Брат", "16+", "x")
	c.probeJSON(f, remuxProbe)
	for _, q := range []string{"8k", ""} {
		if code, _, body := c.post(f, q, c.admin); code != 400 || !strings.Contains(body, "Неизвестное качество") {
			t.Fatalf("%q: %d %s", q, code, body)
		}
	}
}

func TestDeviceCopyTooMany(t *testing.T) {
	c := newCopyEnv(t, nil)
	c.gate = make(chan struct{})
	defer close(c.gate)
	for i := 0; i < 4; i++ {
		_, f := c.movie("Фильм "+itoa(int64(i)), "16+", "x")
		c.probeJSON(f, remuxProbe)
		code, _, body := c.post(f, "original", c.admin)
		if i < 3 && code != 202 {
			t.Fatalf("%d: %d %s", i, code, body)
		}
		if i == 3 && (code != 429 || !strings.Contains(body, "Слишком много копий в очереди. Дождитесь готовых.")) {
			t.Fatalf("4th: %d %s", code, body)
		}
	}
}

func TestDeviceCopyNoSpaceAndDisabled(t *testing.T) {
	c := newCopyEnv(t, nil)
	c.free = 1
	_, f := c.movie("Брат", "16+", "x")
	c.probeJSON(f, remuxProbe)
	if code, _, body := c.post(f, "original", c.admin); code != 507 || !strings.Contains(body, "На сервере не хватает места для копии") {
		t.Fatalf("%d %s", code, body)
	}
	e := newLibEnv(t)
	_, f = e.movie("Брат", "16+", "x")
	if w := e.do("POST", "/api/device-copies", `{"fileId":`+itoa(f)+`,"quality":"auto"}`, e.admin); w.Code != 503 ||
		!strings.Contains(w.Body.String(), "Перекодирование недоступно") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestDeviceCopyUTF16SubDecoded(t *testing.T) {
	c := newCopyEnv(t, nil)
	_, f := c.movie("Брат", "16+", "x")
	c.probeJSON(f, remuxProbe)
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "1\n00:00:01,000 --> 00:00:02,000\nПока\n" {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	c.writeNextTo(f, "Брат.ru.srt", u16)
	c.writeNextTo(f, "Брат.uk.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\n\xcf\xf0\xe8\xe2\xe5\xf2\n")) // CP1251
	_, st, body := c.post(f, "original", c.admin)
	if st.ID == "" {
		t.Fatal(body)
	}
	c.waitReady(st.ID, c.admin)
	args := c.lastArgs()
	var inputs []string
	for i, a := range args {
		if a == "-i" {
			inputs = append(inputs, args[i+1])
		}
	}
	if len(inputs) != 3 {
		t.Fatalf("%q", args)
	}
	decoded := strings.TrimPrefix(inputs[1], "file:")
	if filepath.Dir(decoded) != filepath.Join(c.dc.Dir, "subs") || !strings.HasPrefix(filepath.Base(decoded), st.ID+"-") {
		t.Fatalf("decoded at %s", decoded)
	}
	c.mu.Lock()
	got := c.subs[decoded]
	c.mu.Unlock()
	if got != subtitles.Decode(u16) || !strings.Contains(got, "Пока") {
		t.Fatalf("decoded %q from %s; captured %v", got, decoded, c.subs)
	}
	if _, err := os.Stat(decoded); !os.IsNotExist(err) {
		t.Fatal("decoded subtitle kept after the job")
	}
	j := strings.Join(args, " ")
	if !strings.Contains(j, "-sub_charenc CP1251 -protocol_whitelist file -f srt -i file:") || !strings.Contains(j, "language=rus") ||
		!strings.Contains(j, "language=ukr") {
		t.Fatalf("%s", j)
	}
}

// A kid with a video limit makes no device copies and fetches none (a
// copy plays offline, out of the clock's reach); without rules it may.
func TestDeviceCopyScreenTime(t *testing.T) {
	c := newCopyEnv(t, func(d *Deps, db *sql.DB) { d.ScreenTime = screentime.New(db) })
	_, f := c.movie("Смешарики", "0+", "0123456789")
	c.probeJSON(f, remuxProbe)
	kid, kc := c.profile(`{"name":"Вася","isKid":true}`)
	code, st, body := c.post(f, "original", kc)
	if code != 202 {
		t.Fatalf("no rules: %d %s", code, body)
	}
	c.waitReady(st.ID, kc)
	if w := c.do("PUT", "/api/users/"+itoa(kid)+"/screen-time", `{"videoMin":60}`, c.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	code, _, body = c.post(f, "original", kc)
	if code != 403 || !strings.Contains(body, "Скачивание на устройство выключено родителем") || !strings.Contains(body, `"code":"screen_time"`) {
		t.Fatalf("%d %s", code, body)
	}
	if w := c.do("GET", "/api/device-copies/"+st.ID+"/file", "", kc); w.Code != 403 {
		t.Fatalf("fetch %d %s", w.Code, w.Body)
	}
	if w := c.do("GET", "/api/device-copies/"+st.ID+"/file", "", c.admin); w.Code != 200 {
		t.Fatalf("admin %d", w.Code)
	}
}

// An app that declares HEVC gets a re-encoded copy in HEVC (hvc1, its own
// copy id) when the hardware encodes it; without caps it is H.264 as before.
func TestDeviceCopyHEVCForHEVCClient(t *testing.T) {
	c := newCopyEnv(t, func(d *Deps, _ *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return true } // «Авто» outside: 1080, re-encoded
	})
	accel := &media.Accel{Resolve: func(context.Context, media.HWOptions) media.HWStatus {
		return media.HWStatus{Mode: media.HWVAAPI, HEVC: true, TonemapCPU: true}
	}}
	accel.Apply(context.Background(), media.AccelConfig{Mode: media.HWVAAPI, HEVC: true}, false)
	c.dc.Accel = accel
	_, f := c.movie("Волк", "18+", "x")
	c.probeJSON(f, remuxProbe)
	post := func(body string) media.CopyStatus {
		w := c.do("POST", "/api/device-copies", body, c.admin)
		var st media.CopyStatus
		if json.Unmarshal(w.Body.Bytes(), &st); w.Code != 202 && w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		c.waitReady(st.ID, c.admin)
		return st
	}
	h264 := post(`{"fileId":` + itoa(f) + `,"quality":"auto"}`)
	if j := strings.Join(c.lastArgs(), " "); !strings.Contains(j, "-c:v h264_vaapi") || strings.Contains(j, "hvc1") {
		t.Fatalf("no caps: %s", j)
	}
	hevc := post(`{"fileId":` + itoa(f) + `,"quality":"auto","caps":{"videoCodecs":["h264","hevc"],"audioCodecs":["aac"],"containers":["mp4"],"hevcNeedsHvc1":true}}`)
	if j := strings.Join(c.lastArgs(), " "); hevc.ID == h264.ID || !strings.Contains(j, "-c:v hevc_vaapi") ||
		!strings.Contains(j, "-tag:v hvc1") || !strings.Contains(j, "+faststart") {
		t.Fatalf("hevc caps: %s %s %s", hevc.ID, h264.ID, j)
	}
	// caps without hevc: the H.264 copy
	if st := post(`{"fileId":` + itoa(f) + `,"quality":"auto","caps":{"videoCodecs":["h264"]}}`); st.ID != h264.ID {
		t.Fatal(st.ID, h264.ID)
	}
	// «HEVC на выходе» off: the H.264 copy too
	accel.Apply(context.Background(), media.AccelConfig{Mode: media.HWVAAPI}, false)
	w := c.do("POST", "/api/device-copies", `{"fileId":`+itoa(f)+`,"quality":"auto","caps":{"videoCodecs":["hevc"]}}`, c.admin)
	var st media.CopyStatus
	if json.Unmarshal(w.Body.Bytes(), &st); st.ID != h264.ID {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A PQ file probed before HDR10 metadata was recorded is looked at once
// more: without mastering metadata its copy tone-maps on the CPU at once
// (the GPU still encodes), and the cache keeps what was found.
func TestDeviceCopyOldProbeGetsHDRMeta(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ff, err := lookFFmpeg()
	if err != nil {
		t.Skip("no ffmpeg")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("no ffprobe")
	}
	clip := filepath.Join(t.TempDir(), "pq.mkv")
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=0.4",
		"-vf", "format=yuv420p10le", "-c:v", "libx265", "-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc",
		clip).CombinedOutput(); err != nil {
		t.Skipf("no libx265: %v %s", err, out)
	}
	b, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	c := newCopyEnv(t, func(d *Deps, _ *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return true }
	})
	accel := &media.Accel{Resolve: func(context.Context, media.HWOptions) media.HWStatus {
		return media.HWStatus{Mode: media.HWVAAPI, HEVC: true, TonemapGPU: true, TonemapCPU: true}
	}}
	accel.Apply(context.Background(), media.AccelConfig{Mode: media.HWVAAPI, Tonemap: true}, false)
	c.dc.Accel = accel
	_, f := c.movie("HDR", "18+", string(b))
	// an old cache row: 4K HDR10, nothing about its metadata
	c.probeJSON(f, `{"Container":"mkv","DurationMs":400,"Video":{"Codec":"hevc","Width":3840,"Height":2160,"HDR":true,"BitRate":40000000,"PixFmt":"yuv420p10le","BitDepth":10,"ColorTransfer":"smpte2084"}}`)
	code, st, body := c.post(f, "auto", c.admin)
	if code != 202 {
		t.Fatalf("%d %s", code, body)
	}
	c.waitReady(st.ID, c.admin)
	if j := strings.Join(c.lastArgs(), " "); !strings.Contains(j, "zscale") || strings.Contains(j, "tonemap_vaapi") || !strings.Contains(j, "h264_vaapi") {
		t.Fatalf("%s", j)
	}
	var js string
	if err := c.db.QueryRow(`SELECT probe_json FROM files WHERE id=?`, f).Scan(&js); err != nil || !strings.Contains(js, `"HDRMastering":false`) {
		t.Fatalf("%s %v", js, err)
	}
}
