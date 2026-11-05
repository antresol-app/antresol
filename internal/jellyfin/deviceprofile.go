package jellyfin

import (
	"slices"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/media"
)

// DeviceProfile is what a client can play (PlaybackInfoDto.DeviceProfile).
// Only the parts Vault's decision reads; the rest is ignored.
type DeviceProfile struct {
	MaxStreamingBitrate int64                `json:"MaxStreamingBitrate"`
	MaxStaticBitrate    int64                `json:"MaxStaticBitrate"`
	DirectPlayProfiles  []DirectPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles []TranscodingProfile `json:"TranscodingProfiles"`
	CodecProfiles       []CodecProfile       `json:"CodecProfiles"`
	SubtitleProfiles    []SubtitleProfile    `json:"SubtitleProfiles"`
}

// DirectPlayProfile: comma-separated lists; an empty list is "any".
type DirectPlayProfile struct {
	Container  string `json:"Container"`
	AudioCodec string `json:"AudioCodec"`
	VideoCodec string `json:"VideoCodec"`
	Type       string `json:"Type"`
}

type TranscodingProfile struct {
	Container  string `json:"Container"`
	Type       string `json:"Type"`
	VideoCodec string `json:"VideoCodec"`
	AudioCodec string `json:"AudioCodec"`
	Protocol   string `json:"Protocol"`
}

type CodecProfile struct {
	Type       string             `json:"Type"`
	Codec      string             `json:"Codec"`
	Conditions []ProfileCondition `json:"Conditions"`
}

type ProfileCondition struct {
	Condition  string `json:"Condition"`
	Property   string `json:"Property"`
	Value      string `json:"Value"`
	IsRequired bool   `json:"IsRequired"`
}

// SubtitleProfile: Method is Encode, Embed, External, Hls or Drop.
type SubtitleProfile struct {
	Format string `json:"Format"`
	Method string `json:"Method"`
}

// csv splits a profile list (lower case, no empty entries).
func csv(s string) []string {
	var out []string
	for _, v := range strings.Split(strings.ToLower(s), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// jfContainers maps Jellyfin container names to the probe's (media.containerName).
var jfContainers = map[string]string{"matroska": "mkv", "mpegts": "ts", "m2ts": "ts", "m4v": "mp4", "mov": "mov", "3gp": "mp4", "3g2": "mp4"}

func containers(list []string) []string {
	out := slices.Clone(list)
	for _, c := range list {
		if v := jfContainers[c]; v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// videoDirect: the profile lists video direct-play profiles (none: the
// client plays anything directly, as Infuse, which sends no profile).
func (p *DeviceProfile) videoDirect() []DirectPlayProfile {
	if p == nil {
		return nil
	}
	var out []DirectPlayProfile
	for _, d := range p.DirectPlayProfiles {
		if strings.EqualFold(d.Type, "Video") {
			out = append(out, d)
		}
	}
	return out
}

// hlsProfile is the profile's video HLS transcoding profile (ok false: none).
func (p *DeviceProfile) hlsProfile() (TranscodingProfile, bool) {
	if p == nil {
		return TranscodingProfile{}, false
	}
	for _, t := range p.TranscodingProfiles {
		if strings.EqualFold(t.Type, "Video") && strings.EqualFold(t.Protocol, "hls") {
			return t, true
		}
	}
	return TranscodingProfile{}, false
}

// limits applies the profile's video codec conditions Vault understands:
// a maximum height or width (the client's resolution setting) and HEVC
// only with the hvc1 tag (AVPlayer).
func (p *DeviceProfile) limits(c *media.ClientCaps, src *media.VideoStream) {
	if p == nil {
		return
	}
	for _, cp := range p.CodecProfiles {
		if !strings.EqualFold(cp.Type, "Video") {
			continue
		}
		codecs := csv(cp.Codec)
		for _, cond := range cp.Conditions {
			n, _ := strconv.Atoi(strings.TrimSpace(cond.Value))
			le := strings.EqualFold(cond.Condition, "LessThanEqual")
			switch {
			case strings.EqualFold(cond.Property, "VideoCodecTag") && slices.Contains(codecs, "hevc"):
				tags := strings.ToLower(cond.Value)
				if strings.Contains(tags, "hvc1") && !strings.Contains(tags, "hev1") {
					c.HevcNeedsHvc1 = true
				}
			case len(codecs) > 0 && (src == nil || !slices.Contains(codecs, media.NormCodec(src.Codec))):
				// a condition of another codec
			case le && n > 0 && strings.EqualFold(cond.Property, "Height"):
				if c.MaxHeight == 0 || n < c.MaxHeight {
					c.MaxHeight = n
				}
			case le && n > 0 && strings.EqualFold(cond.Property, "Width") && src != nil && src.Width > n && src.Height > 0:
				if h := n * src.Height / src.Width; c.MaxHeight == 0 || h < c.MaxHeight {
					c.MaxHeight = h
				}
			}
		}
	}
}

// subMethods are the delivery methods the profile allows for a subtitle
// format (Jellyfin names: subrip, ass, vtt, pgssub…).
func (p *DeviceProfile) subMethods(format string) map[string]bool {
	out := map[string]bool{}
	if p == nil {
		return out
	}
	for _, s := range p.SubtitleProfiles {
		if strings.EqualFold(s.Format, format) {
			out[strings.ToLower(s.Method)] = true
		}
	}
	return out
}

func (p *DeviceProfile) hasSubProfiles() bool { return p != nil && len(p.SubtitleProfiles) > 0 }

// jfSubCodecs maps ffmpeg subtitle codecs to Jellyfin's format names.
var jfSubCodecs = map[string]string{"webvtt": "vtt", "hdmv_pgs_subtitle": "pgssub", "dvd_subtitle": "dvdsub",
	"dvb_subtitle": "dvbsub", "srt": "subrip", "text": "subrip"}

func jfSubCodec(c string) string {
	if v := jfSubCodecs[c]; v != "" {
		return v
	}
	return c
}

// directCaps are the caps of one direct-play profile for the source:
// an empty list allows what the source has.
func directCaps(d DirectPlayProfile, p media.ProbeResult) media.ClientCaps {
	var c media.ClientCaps
	c.Containers = containers(csv(d.Container))
	if len(c.Containers) == 0 {
		c.Containers = []string{p.Container}
	}
	c.VideoCodecs = csv(d.VideoCodec)
	if len(c.VideoCodecs) == 0 && p.Video != nil {
		c.VideoCodecs = []string{p.Video.Codec}
	}
	c.AudioCodecs = csv(d.AudioCodec)
	if len(c.AudioCodecs) == 0 {
		for _, a := range p.Audio {
			c.AudioCodecs = append(c.AudioCodecs, a.Codec)
		}
	}
	return c
}

// anyCaps: a client without a video direct-play profile plays the source as it is.
func anyCaps(p media.ProbeResult) media.ClientCaps { return directCaps(DirectPlayProfile{}, p) }

// hlsCaps are the caps of an HLS session: the transcoding profile's codecs
// (none: H.264 and AAC), no container (so never "direct").
func hlsCaps(t TranscodingProfile) media.ClientCaps {
	c := media.ClientCaps{VideoCodecs: csv(t.VideoCodec), AudioCodecs: csv(t.AudioCodec)}
	if len(c.VideoCodecs) == 0 {
		c.VideoCodecs = []string{"h264"}
	}
	if len(c.AudioCodecs) == 0 {
		c.AudioCodecs = []string{"aac"}
	}
	return c
}
