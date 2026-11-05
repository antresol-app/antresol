package api

import (
	"log"
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/media"
)

// Quality caps of the player's menu and of «Качество вне дома».
const (
	qualityAuto     = "auto"
	qualityOriginal = "original"
	// defaultRemoteQuality is «Качество вне дома» until the admin changes it.
	defaultRemoteQuality = "1080"
)

type qualityCap struct {
	height  int
	bitrate int64
}

var qualityCaps = map[string]qualityCap{
	"1080": {1080, 8_000_000},
	"720":  {720, 4_000_000},
	"480":  {480, 2_000_000},
}

func validRemoteQuality(q string) bool {
	_, ok := qualityCaps[q]
	return ok || q == qualityOriginal
}

// qualityInfo tells the player what its quality menu means right now.
type qualityInfo struct {
	Selected string `json:"selected"` // what the player asked: auto | original | 1080 | 720 | 480
	Applied  string `json:"applied"`  // the cap in effect: original | 1080 | 720 | 480
	Remote   bool   `json:"remote"`   // the viewer is outside the home network
}

// playbackCaps applies the quality choice to the browser's caps. «Авто» at
// home plays the original; «Авто» from outside is capped by «Качество вне
// дома» (1080p 8 Мбит/с by default); an explicit choice applies anywhere.
func (h *lib) playbackCaps(r *http.Request, c media.ClientCaps, quality string) (media.ClientCaps, qualityInfo) {
	var remote bool
	if h.ClientIsRemote != nil {
		remote = h.ClientIsRemote(r)
	} else {
		remote = !h.Network.IsTrusted(r) // the same home rule as passwordless sign-in
	}
	q := qualityInfo{Selected: quality, Remote: remote}
	c.Lang = i18n.FromContext(r.Context()) // the language of Decision.Reason
	if _, ok := qualityCaps[quality]; !ok && quality != qualityOriginal {
		q.Selected = qualityAuto
	}
	q.Applied = q.Selected
	if q.Selected == qualityAuto {
		q.Applied = qualityOriginal
		if q.Remote {
			q.Applied = h.remoteQuality(r)
		}
	}
	if cp, ok := qualityCaps[q.Applied]; ok {
		if c.MaxHeight <= 0 || c.MaxHeight > cp.height {
			c.MaxHeight = cp.height
		}
		if c.MaxBitrate <= 0 || c.MaxBitrate > cp.bitrate {
			c.MaxBitrate = cp.bitrate
		}
	}
	return c, q
}

// remoteQuality is the saved «Качество вне дома».
func (h *lib) remoteQuality(r *http.Request) string {
	if h.DB == nil {
		return defaultRemoteQuality
	}
	s, err := LoadAppSettings(r.Context(), h.DB, h.SettingsDefaults)
	if err != nil {
		log.Printf("api: settings for remote quality: %v", err)
		return defaultRemoteQuality
	}
	return s.RemoteQualityOrDefault()
}
