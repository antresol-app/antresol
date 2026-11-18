// Package addons: installable extensions of the server. A manifest
// (antresol-addon.json) describes one addon release.
package addons

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
)

// ContractVersion is the addon contract this server speaks.
const ContractVersion = 1

// ManifestName is the manifest file name inside a release.
const ManifestName = "antresol-addon.json"

// Platform is one downloadable release asset with its checksum.
type Platform struct {
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

// Text is a user-facing string in both interface languages.
type Text = addonkit.Text

// NotifyTopic is a notification topic an addon may publish to.
type NotifyTopic struct {
	Topic    string `json:"topic"`
	Title    Text   `json:"title"`
	Audience string `json:"audience"` // "admins" | "adults"
}

// KnownHooks are the fixed hook names an addon may answer; additionally
// "tracking.scan:<kind>" with kind matching scanKindRe.
var KnownHooks = []string{"files.plan", "files.pause", "files.resume", "files.stop", "files.detach", "files.attach",
	"play.progress", "library.pending", "disk.usage", "requests.open", "requests.title", "requests.search", "tracking.titles", "tracking.watched",
	"notify.interest", "voiceover.origLang", "trailers.fetch", "import.services", "import.stop", "import.resolve", "import.search",
	"migrate.leftovers", "library.afterScan", "home.continue",
	"channels.ready", "channels.candidates", "channels.resolve", "channels.release"}

// ChannelHooks are the hooks of «ТВ» channels' titles outside the
// library: a manifest declares all four or none.
var ChannelHooks = []string{"channels.ready", "channels.candidates", "channels.resolve", "channels.release"}

// Manifest describes an addon release.
type Manifest struct {
	ID          string              `json:"id"`
	Name        Text                `json:"name"`
	Description Text                `json:"description"`
	Version     string              `json:"version"` // semver, no leading v
	Author      string              `json:"author"`
	Homepage    string              `json:"homepage"`
	Contract    int                 `json:"contract"`         // must equal ContractVersion
	Platforms   map[string]Platform `json:"platforms"`        // "linux/amd64" → asset
	Web         *Platform           `json:"web,omitempty"`    // tar.gz of the web bundle; nil: no UI
	Permissions []string            `json:"permissions"`      // see knownPerms
	Access      string              `json:"access,omitempty"` // "" (= "admins") | "admins" | "adults"
	Hooks       []string            `json:"hooks,omitempty"`
	Notify      []NotifyTopic       `json:"notify,omitempty"`
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	semverRe   = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	topicRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)
	scanKindRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	shaRe      = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

	// "run" is the supervisor's socket directory next to the addon dirs.
	reservedIDs = map[string]bool{"web": true, "preview": true, "x": true, "ui": true, "run": true,
		"addon": true, "core": true, "contract": true, "locale": true}
	knownPerms = map[string]bool{"library.read": true, "events": true,
		"library.write": true, "files.holds": true, "metadata": true, "images": true, "play": true, "notify": true,
		"users.read": true, "tracking": true, "imports": true, "requests": true, "voiceover": true,
		"progress": true}
	knownPlatforms = map[string]bool{"linux/amd64": true, "linux/arm64": true, "darwin/arm64": true}
)

// ParseManifest decodes and validates a manifest. Errors are Russian
// user messages (translated through internal/i18n).
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, userError("Неверный манифест расширения: не удалось прочитать JSON")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Manifest{}, userError("Неверный манифест расширения: не удалось прочитать JSON")
	}
	if reservedIDs[m.ID] || strings.HasPrefix(m.ID, "addon-") || strings.HasPrefix(m.ID, "core-") {
		return Manifest{}, userError("Неверный манифест расширения: id зарезервирован")
	}
	if !idRe.MatchString(m.ID) {
		return Manifest{}, userError("Неверный манифест расширения: неверный id")
	}
	if !semverRe.MatchString(m.Version) {
		return Manifest{}, userError("Неверный манифест расширения: неверная версия")
	}
	if strings.TrimSpace(m.Name.RU) == "" && strings.TrimSpace(m.Name.EN) == "" {
		return Manifest{}, userError("Неверный манифест расширения: нет названия")
	}
	if m.Contract != ContractVersion {
		return Manifest{}, userError("Неверный манифест расширения: неподдерживаемая версия контракта")
	}
	if len(m.Platforms) == 0 && m.Web == nil {
		return Manifest{}, userError("Неверный манифест расширения: нет ни платформ, ни веб-части")
	}
	for key, p := range m.Platforms {
		if !knownPlatforms[key] {
			return Manifest{}, userError("Неверный манифест расширения: неизвестная платформа")
		}
		if err := checkAsset(p); err != nil {
			return Manifest{}, err
		}
	}
	if m.Web != nil {
		if err := checkAsset(*m.Web); err != nil {
			return Manifest{}, err
		}
	}
	seen := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		if !knownPerms[p] {
			return Manifest{}, userError("Неверный манифест расширения: неизвестное разрешение")
		}
		if seen[p] {
			return Manifest{}, userError("Неверный манифест расширения: повторяется разрешение")
		}
		seen[p] = true
	}
	if m.Access != "" && m.Access != "admins" && m.Access != "adults" {
		return Manifest{}, userError("Неверный манифест расширения: неверный доступ")
	}
	hooks := make(map[string]bool, len(m.Hooks))
	for _, h := range m.Hooks {
		if !knownHook(h) {
			return Manifest{}, userError("Неверный манифест расширения: неизвестный хук")
		}
		if hooks[h] {
			return Manifest{}, userError("Неверный манифест расширения: повторяется хук")
		}
		hooks[h] = true
	}
	n := 0
	for _, h := range ChannelHooks {
		if hooks[h] {
			n++
		}
	}
	if n != 0 && n != len(ChannelHooks) {
		return Manifest{}, userError("Нужны все хуки channels.*")
	}
	topics := make(map[string]bool, len(m.Notify))
	for _, n := range m.Notify {
		if !topicRe.MatchString(n.Topic) {
			return Manifest{}, userError("Неверный манифест расширения: неверная тема уведомлений")
		}
		if topics[n.Topic] {
			return Manifest{}, userError("Неверный манифест расширения: повторяется тема уведомлений")
		}
		topics[n.Topic] = true
		if n.Audience != "admins" && n.Audience != "adults" {
			return Manifest{}, userError("Неверный манифест расширения: неверная аудитория уведомлений")
		}
	}
	return m, nil
}

func knownHook(h string) bool {
	if kind, ok := strings.CutPrefix(h, "tracking.scan:"); ok {
		return scanKindRe.MatchString(kind)
	}
	for _, k := range KnownHooks {
		if k == h {
			return true
		}
	}
	return false
}

// ForAdults reports whether the addon's UI is open to adult members, not
// only admins.
func (m Manifest) ForAdults() bool { return m.Access == "adults" }

// OpenTo is the one rule of who gets an addon (its bundle, its hooks run
// for a profile): an admin gets every addon, any other profile only one
// for adults. Kids are kept out by the callers before this.
func (m Manifest) OpenTo(admin bool) bool { return admin || m.ForAdults() }

// AnswersChannels: the manifest declares the channels.* hooks (all four,
// as ParseManifest makes sure).
func (m Manifest) AnswersChannels() bool {
	for _, h := range ChannelHooks {
		if !m.Answers(h) {
			return false
		}
	}
	return true
}

// Answers reports whether the manifest declares the hook.
func (m Manifest) Answers(hook string) bool {
	for _, h := range m.Hooks {
		if h == hook {
			return true
		}
	}
	return false
}

func checkAsset(p Platform) error {
	if p.Asset == "" || strings.ContainsAny(p.Asset, `/\`) || p.Asset == "." || p.Asset == ".." {
		return userError("Неверный манифест расширения: неверное имя файла")
	}
	if !shaRe.MatchString(p.SHA256) {
		return userError("Неверный манифест расширения: неверная контрольная сумма")
	}
	return nil
}

// PlatformFor returns the asset for goos/goarch.
func (m Manifest) PlatformFor(goos, goarch string) (Platform, bool) {
	p, ok := m.Platforms[goos+"/"+goarch]
	return p, ok
}
