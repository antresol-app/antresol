package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/go-chi/chi/v5"
)

// transcodingKey stores «Перекодирование» (media.AccelConfig) in the
// settings table; without a row the server's VAULT_HWACCEL* apply.
const transcodingKey = "transcoding"

// LoadTranscodingSettings are the stored transcoding settings, else
// defaults (VAULT_HWACCEL, VAULT_HWACCEL_DEVICE, tone mapping and HEVC
// on); custom: a stored row.
func LoadTranscodingSettings(ctx context.Context, db *sql.DB, defaults media.AccelConfig) (cfg media.AccelConfig, custom bool, err error) {
	var raw string
	err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, transcodingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaults, false, nil
	}
	if err != nil {
		return defaults, false, err
	}
	cfg = defaults
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return defaults, false, fmt.Errorf("settings %s: %w", transcodingKey, err)
	}
	if !media.ValidHW(cfg.Mode) {
		cfg.Mode = defaults.Mode
	}
	return cfg, true, nil
}

// renderNodes lists the render nodes offered for VA-API (tests replace it).
var renderNodes = media.RenderNodes

// transcodingAPI is «Настройки сервера → Перекодирование» (admin).
type transcodingAPI struct {
	db       *sql.DB
	accel    *media.Accel
	defaults media.AccelConfig
	// segs says whether ffmpeg can analyse episodes (nil: no analysis)
	segs Segmenter
}

func mountTranscoding(r chi.Router, deps Deps) {
	if deps.Accel == nil || deps.DB == nil {
		return
	}
	t := &transcodingAPI{db: deps.DB, accel: deps.Accel, defaults: deps.TranscodingDefaults, segs: deps.Segments}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/settings/transcoding", t.get)
		r.Put("/api/settings/transcoding", t.put)
		r.Post("/api/settings/transcoding/probe", t.probe)
	})
}

// transcodingView is GET /api/settings/transcoding: the settings, the
// server's defaults, whether they were changed here, the render nodes and
// what the settings resolved to (with each test encode).
type transcodingView struct {
	Settings media.AccelConfig `json:"settings"`
	Defaults media.AccelConfig `json:"defaults"`
	Custom   bool              `json:"custom"`
	Devices  []string          `json:"devices"`
	Status   transcodingStatus `json:"status"`
	// Fingerprint: ffmpeg has chromaprint, the analysis that finds the
	// intros and credits of episodes (skipping them in the players and on
	// the channels); false: only chapters and the online databases give
	// them. Absent without the segments worker.
	Fingerprint *bool `json:"fingerprint,omitempty"`
}

type transcodingStatus struct {
	media.HWStatus
	Name string `json:"name"`
}

func (t *transcodingAPI) view(ctx context.Context) (transcodingView, error) {
	_, custom, err := LoadTranscodingSettings(ctx, t.db, t.defaults)
	if err != nil {
		return transcodingView{}, err
	}
	cfg, st := t.accel.State()
	devs := renderNodes()
	if devs == nil {
		devs = []string{}
	}
	if st.Probes == nil {
		st.Probes = []media.HWProbe{}
	}
	v := transcodingView{Settings: cfg, Defaults: t.defaults, Custom: custom, Devices: devs,
		Status: transcodingStatus{HWStatus: st, Name: media.HWName(st.Mode)}}
	if t.segs != nil {
		ok := t.segs.CanFingerprint()
		v.Fingerprint = &ok
	}
	return v, nil
}

func (t *transcodingAPI) get(w http.ResponseWriter, r *http.Request) {
	v, err := t.view(r.Context())
	if err != nil {
		internalErr(w, r, "transcoding settings", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// transcodingPut is PUT /api/settings/transcoding: new settings, or
// reset: back to the server's defaults (the stored row is dropped).
type transcodingPut struct {
	media.AccelConfig
	Reset bool `json:"reset"`
}

func (t *transcodingAPI) put(w http.ResponseWriter, r *http.Request) {
	cur, _ := t.accel.State()
	in := transcodingPut{AccelConfig: cur}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	cfg := in.AccelConfig
	if in.Reset {
		if _, err := t.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, transcodingKey); err != nil {
			internalErr(w, r, "transcoding settings", err)
			return
		}
		cfg = t.defaults
	} else {
		if !media.ValidHW(cfg.Mode) {
			writeErr(w, r, http.StatusBadRequest, "Неизвестный режим ускорения")
			return
		}
		// a render node of this machine, or the first one ("")
		if cfg.Device != "" && (!strings.HasPrefix(cfg.Device, "/dev/dri/renderD") || !slices.Contains(renderNodes(), cfg.Device)) {
			writeErr(w, r, http.StatusBadRequest, "Нет такого устройства видеокарты")
			return
		}
		raw, _ := json.Marshal(cfg)
		if _, err := t.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, transcodingKey, string(raw)); err != nil {
			internalErr(w, r, "transcoding settings", err)
			return
		}
	}
	// in effect for new sessions now; running ones keep theirs. The test
	// encodes are not tied to the request: a closed page does not cut them.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	t.accel.Apply(pctx, cfg, false)
	t.get(w, r)
}

// probe is «Проверить заново»: the test encodes again, with the settings
// in effect.
func (t *transcodingAPI) probe(w http.ResponseWriter, r *http.Request) {
	cfg, _ := t.accel.State()
	pctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	t.accel.Apply(pctx, cfg, true)
	t.get(w, r)
}
