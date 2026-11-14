package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/media"
)

func TestStatsTranscoding(t *testing.T) {
	if (&stats{}).transcoding() != nil {
		t.Fatal("reported without hardware settings")
	}
	a := &media.Accel{Resolve: func(context.Context, media.HWOptions) media.HWStatus {
		return media.HWStatus{Mode: media.HWVAAPI, Requested: "auto", Device: "/dev/dri/renderD128"}
	}}
	a.Apply(context.Background(), media.AccelConfig{Mode: "auto"}, false)
	b, _ := json.Marshal(statsOverview{Transcoding: (&stats{accel: a}).transcoding()})
	if !strings.Contains(string(b), `"transcoding":{"mode":"vaapi","name":"VA-API","requested":"auto","device":"/dev/dri/renderD128"}`) {
		t.Fatal(string(b))
	}
}

// newTranscodingEnv: a database and an Accel whose test encodes are
// recorded (each answers the mode asked, auto = qsv; vaapi has HEVC).
func newTranscodingEnv(t *testing.T) (*env, *media.Accel, *[]media.HWOptions) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var probes []media.HWOptions
	a := &media.Accel{Resolve: func(_ context.Context, o media.HWOptions) media.HWStatus {
		probes = append(probes, o)
		mode := o.Mode
		if mode == "auto" {
			mode = media.HWQSV
		}
		return media.HWStatus{Mode: mode, Requested: o.Mode, Device: o.Device, HEVC: mode == media.HWVAAPI,
			Probes: []media.HWProbe{{Mode: mode, OK: true}}}
	}}
	defaults := media.AccelConfig{Mode: "auto", Tonemap: true, HEVC: true}
	a.Apply(context.Background(), defaults, false)
	probes = nil
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	e := &env{db: d, t: t, store: st, h: NewRouter(Deps{Users: st, Sessions: ss, DB: d, Accel: a, TranscodingDefaults: defaults})}
	return e, a, &probes
}

func TestTranscodingSettings(t *testing.T) {
	renderNodes = func() []string { return []string{"/dev/dri/renderD128", "/dev/dri/renderD129"} }
	defer func() { renderNodes = media.RenderNodes }()
	e, a, probes := newTranscodingEnv(t)
	admin := e.setup()
	var v transcodingView
	get := func() {
		t.Helper()
		w := e.do("GET", "/api/settings/transcoding", "", admin)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		v = transcodingView{}
		json.Unmarshal(w.Body.Bytes(), &v)
	}
	get()
	if v.Custom || v.Settings.Mode != "auto" || !v.Settings.Tonemap || !v.Settings.HEVC || v.Status.Mode != media.HWQSV ||
		v.Status.Name != "Intel Quick Sync" || len(v.Devices) != 2 || len(v.Status.Probes) != 1 {
		t.Fatalf("%+v", v)
	}
	// saved: in effect at once (new sessions), stored for the next start
	w := e.do("PUT", "/api/settings/transcoding", `{"mode":"vaapi","device":"/dev/dri/renderD129","tonemap":false,"hevc":true}`, admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if !v.Custom || v.Settings.Mode != media.HWVAAPI || v.Settings.Tonemap || v.Status.Mode != media.HWVAAPI || !v.Status.HEVC ||
		a.Mode() != media.HWVAAPI || len(*probes) != 1 || (*probes)[0].Device != "/dev/dri/renderD129" || (*probes)[0].Fresh {
		t.Fatalf("%+v %+v", v, *probes)
	}
	cfg, custom, err := LoadTranscodingSettings(context.Background(), e.db, media.AccelConfig{Mode: "auto", Tonemap: true})
	if err != nil || !custom || cfg != (media.AccelConfig{Mode: media.HWVAAPI, Device: "/dev/dri/renderD129", HEVC: true}) {
		t.Fatalf("%+v %v %v", cfg, custom, err)
	}
	// «Проверить заново»: the same settings, fresh test encodes
	if w := e.do("POST", "/api/settings/transcoding/probe", "", admin); w.Code != 200 || len(*probes) != 2 || !(*probes)[1].Fresh || (*probes)[1].Mode != media.HWVAAPI {
		t.Fatalf("%d %+v", w.Code, *probes)
	}
	// bad values
	for _, body := range []string{`{"mode":"cuda"}`, `{"mode":"vaapi","device":"/dev/sda"}`, `{"mode":"vaapi","device":"/dev/dri/renderD200"}`} {
		if w := e.do("PUT", "/api/settings/transcoding", body, admin); w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	// a field left out keeps its value
	e.do("PUT", "/api/settings/transcoding", `{"tonemap":true}`, admin)
	if cfg, _ := a.State(); cfg.Mode != media.HWVAAPI || !cfg.Tonemap || !cfg.HEVC {
		t.Fatalf("%+v", cfg)
	}
	// back to the server's defaults
	w = e.do("PUT", "/api/settings/transcoding", `{"reset":true}`, admin)
	json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v.Custom || v.Settings.Mode != "auto" || a.Mode() != media.HWQSV {
		t.Fatalf("%d %+v", w.Code, v)
	}
	// admins only
	if w := e.do("GET", "/api/settings/transcoding", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	// the settings screen's summary
	w = e.do("GET", "/api/settings", "", admin)
	if !strings.Contains(w.Body.String(), `"transcodingName":"Intel Quick Sync"`) {
		t.Fatal(w.Body.String())
	}
}
