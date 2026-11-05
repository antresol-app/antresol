package jellyfin

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// mountPlayback registers the playback routes behind requireToken.
func (s *Server) mountPlayback(r chi.Router) {
	r.Get("/playback/bitratetest", s.bitrateTest)
	r.Post("/items/{id}/playbackinfo", s.playbackInfo)
	r.Get("/items/{id}/playbackinfo", s.playbackInfo)
	r.Get("/items/{id}/download", s.download)
	r.Delete("/videos/activeencodings", s.stopEncoding)
	r.Post("/livestreams/close", s.noContent)
}

// mountPlayFiles registers the routes players fetch without headers
// (requirePlay: the access token, or the play session in the URL).
func (s *Server) mountPlayFiles(r chi.Router) {
	r = r.With(s.requirePlay)
	// /videos/{id}/stream[.{container}] (the token only), master.m3u8, main.m3u8
	r.Get("/videos/{id}/{file}", s.videoFile)
	r.Get("/videos/{id}/hls1/{play}/{name}", s.hlsSegment)
	r.Get("/videos/{id}/{ms}/subtitles/{idx}/{file}", s.subtitle)
	r.Get("/videos/{id}/{ms}/subtitles/{idx}/{ticks}/{file}", s.subtitle)
}

// maxBitrateTest bounds /Playback/BitrateTest: Swiftfin's largest test
// (PlaybackBitrateTestSize.largest; its default is 5 MB, so a smaller cap
// would fail «Авто» bitrate outright).
const maxBitrateTest = 10_000_000

// bitrateTestEvery: one full test per access token in this time; a test
// meanwhile gets bitrateTestShort bytes (an estimate on the high side, as
// if the network were fast: still capped by Vault's own quality rules) —
// not an error, which would fail the playback a re-resolve (a track
// change) starts.
const (
	bitrateTestEvery = 10 * time.Second
	bitrateTestShort = 64 << 10
)

// bitrateTest sends size random bytes: Swiftfin times it for «Авто» bitrate.
func (s *Server) bitrateTest(w http.ResponseWriter, r *http.Request) {
	n := qInt(r, "size", 102400)
	if n <= 0 || n > maxBitrateTest {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !s.bitrateAllowed(sessionOf(r).token) {
		n = min(n, bitrateTestShort)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	io.CopyN(w, rand.Reader, int64(n))
}

// bitrateAllowed: the token has not run a full bitrate test in
// bitrateTestEvery (and now has).
func (s *Server) bitrateAllowed(tok string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := time.Now()
	if s.rateAt == nil {
		s.rateAt = map[string]time.Time{}
	}
	for k, t := range s.rateAt {
		if now.Sub(t) >= bitrateTestEvery {
			delete(s.rateAt, k)
		}
	}
	if _, ok := s.rateAt[tok]; ok {
		return false
	}
	s.rateAt[tok] = now
	return true
}

// PlaybackInfoDto is the body of POST /Items/{id}/PlaybackInfo.
type PlaybackInfoDto struct {
	MaxStreamingBitrate  int64          `json:"MaxStreamingBitrate"`
	AudioStreamIndex     *int           `json:"AudioStreamIndex"`
	SubtitleStreamIndex  *int           `json:"SubtitleStreamIndex"`
	MediaSourceId        string         `json:"MediaSourceId"`
	DeviceProfile        *DeviceProfile `json:"DeviceProfile"`
	EnableDirectPlay     *bool          `json:"EnableDirectPlay"`
	EnableDirectStream   *bool          `json:"EnableDirectStream"`
	EnableTranscoding    *bool          `json:"EnableTranscoding"`
	AllowVideoStreamCopy *bool          `json:"AllowVideoStreamCopy"`
	AllowAudioStreamCopy *bool          `json:"AllowAudioStreamCopy"`
}

type PlaybackInfoResponse struct {
	MediaSources  []MediaSourceInfo `json:"MediaSources"`
	PlaySessionId string            `json:"PlaySessionId,omitempty"`
	ErrorCode     string            `json:"ErrorCode,omitempty"`
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// readPlaybackInfo is the request's PlaybackInfoDto: the body (POST),
// with the query as a fallback for what it lacks (GET; Infuse).
func readPlaybackInfo(r *http.Request) (PlaybackInfoDto, error) {
	var in PlaybackInfoDto
	if r.Method == http.MethodPost && r.ContentLength != 0 {
		err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
		if err != nil && !errors.Is(err, io.EOF) {
			return in, err
		}
	}
	intq := func(dst **int, key string) {
		if *dst == nil && qv(r, key) != "" {
			if n, err := strconv.Atoi(qv(r, key)); err == nil {
				*dst = &n
			}
		}
	}
	boolq := func(dst **bool, key string) {
		if *dst == nil && qv(r, key) != "" {
			v := qBool(r, key, true)
			*dst = &v
		}
	}
	intq(&in.AudioStreamIndex, "audioStreamIndex")
	intq(&in.SubtitleStreamIndex, "subtitleStreamIndex")
	boolq(&in.EnableDirectPlay, "enableDirectPlay")
	boolq(&in.EnableDirectStream, "enableDirectStream")
	boolq(&in.EnableTranscoding, "enableTranscoding")
	boolq(&in.AllowVideoStreamCopy, "allowVideoStreamCopy")
	boolq(&in.AllowAudioStreamCopy, "allowAudioStreamCopy")
	if in.MediaSourceId == "" {
		in.MediaSourceId = qv(r, "mediaSourceId")
	}
	if in.MaxStreamingBitrate == 0 {
		in.MaxStreamingBitrate, _ = strconv.ParseInt(qv(r, "maxStreamingBitrate"), 10, 64)
	}
	return in, nil
}

// videoItem is a movie or an episode the profile may see, and its file
// msID (a file id; "" or the item's own id: its first file). ok false: the
// 404 is written.
func (s *Server) videoItem(w http.ResponseWriter, r *http.Request, rawID, msID string) (library.Item, library.File, bool) {
	k, id, ok := ParseID(rawID)
	if !ok || k != kindItem || s.Player == nil {
		w.WriteHeader(http.StatusNotFound)
		return library.Item{}, library.File{}, false
	}
	c := s.conv(r.Context(), userOf(r), nil)
	it, ok, err := c.visibleItem(id)
	if err != nil {
		s.internal(w, r, "item", err)
		return it, library.File{}, false
	}
	if !ok || (it.Kind != library.KindMovie && it.Kind != library.KindEpisode) {
		w.WriteHeader(http.StatusNotFound)
		return it, library.File{}, false
	}
	fs, err := s.Library.FilesOf(r.Context(), it.ID)
	if err != nil {
		s.internal(w, r, "files", err)
		return it, library.File{}, false
	}
	want := int64(0)
	if k, fid, ok := ParseID(msID); ok && k == kindFile {
		want = fid
	}
	for _, f := range fs {
		if want == 0 || f.ID == want {
			return it, f, true
		}
	}
	w.WriteHeader(http.StatusNotFound)
	return it, library.File{}, false
}

// durationMs is the file's duration (the probe's, else the scan's).
func durationMs(f library.File, p media.ProbeResult) int64 {
	if p.DurationMs > 0 {
		return p.DurationMs
	}
	return f.DurationMs
}

// decision is how PlaybackInfo serves a file.
type decision struct {
	direct bool
	d      media.Decision // the HLS session's (not direct)
}

// errNoStream: nothing the client allows can play the file.
var errNoStream = errors.New("jellyfin: no compatible stream")

// decide maps the client's DeviceProfile onto media.Decide: direct play
// when a direct-play profile takes the file (the default audio, and the
// wanted track's codec: the player switches tracks itself), otherwise an
// HLS session with the transcoding profile's codecs (copy when they
// allow, else H.264). Vault's quality caps apply to both (Player.Caps).
//
// An external audio track (audio, index externalAudioBase+i) is never
// direct: the HLS session takes its file as a second input.
func (s *Server) decide(r *http.Request, p media.ProbeResult, in PlaybackInfoDto, audioFiles []media.ExternalAudio, burn int,
	needHLS bool) (decision, error) {
	prof := in.DeviceProfile
	maxBitrate := in.MaxStreamingBitrate
	if maxBitrate <= 0 && prof != nil {
		maxBitrate = prof.MaxStreamingBitrate
	}
	audio := -1
	if in.AudioStreamIndex != nil {
		audio = *in.AudioStreamIndex
	}
	finish := func(c media.ClientCaps) media.ClientCaps {
		c.MaxBitrate = maxBitrate
		prof.limits(&c, p.Video)
		return s.Player.Caps(r, c)
	}
	var external *media.ExternalAudio
	if i := audio - externalAudioBase; i >= 0 && i < len(audioFiles) {
		external = &audioFiles[i]
	}
	if boolOr(in.EnableDirectPlay, true) && burn == 0 && !needHLS && external == nil {
		var tries []media.ClientCaps
		if dps := prof.videoDirect(); len(dps) > 0 {
			for _, dp := range dps {
				tries = append(tries, directCaps(dp, p))
			}
		} else {
			tries = append(tries, anyCaps(p))
		}
		for _, c := range tries {
			c = finish(c)
			if d := media.Decide(p, c, -1); d.Mode == "direct" && audioIn(p, audio, c.AudioCodecs) {
				return decision{direct: true}, nil
			}
		}
	}
	if !boolOr(in.EnableTranscoding, true) && !boolOr(in.EnableDirectStream, true) {
		return decision{}, errNoStream
	}
	t, _ := prof.hlsProfile()
	c := finish(hlsCaps(t))
	if !boolOr(in.AllowVideoStreamCopy, true) {
		c.VideoCodecs = nil
	}
	if !boolOr(in.AllowAudioStreamCopy, true) {
		c.AudioCodecs = nil
	}
	var d media.Decision
	if external != nil {
		d = media.DecideExternalAudio(p, c, *external, burn)
	} else if burn > 0 {
		d = media.DecideBurn(p, c, audio, burn)
	} else {
		d = media.Decide(p, c, audio)
	}
	if d.Mode == "direct" { // HLS has no direct: copy both streams
		d.Mode = "remux"
	}
	return decision{d: d}, nil
}

// audioIn: the wanted audio track (the default when -1 or unknown) has a
// codec in list.
func audioIn(p media.ProbeResult, idx int, list []string) bool {
	for _, a := range p.Audio {
		if a.Index == idx {
			for _, c := range list {
				if media.NormCodec(c) == media.NormCodec(a.Codec) {
					return true
				}
			}
			return false
		}
	}
	return true
}

// playbackInfo answers PlaybackInfo: the file as a MediaSource with how
// to play it (direct: SupportsDirectPlay, no TranscodingUrl; else an HLS
// TranscodingUrl) and a PlaySessionId; ffmpeg starts with master.m3u8.
func (s *Server) playbackInfo(w http.ResponseWriter, r *http.Request) {
	in, err := readPlaybackInfo(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	it, f, ok := s.videoItem(w, r, chi.URLParam(r, "id"), in.MediaSourceId)
	if !ok {
		return
	}
	// Jellyfin's PlaybackErrorCode for a viewer who may not play now
	if ref := s.ScreenTime.Allow(r.Context(), userOf(r), screentime.Video); ref != nil {
		s.log().Info("jellyfin: playback refused by screen time", "profile", userOf(r).ID, "reason", ref.Reason)
		writeJSON(w, PlaybackInfoResponse{MediaSources: []MediaSourceInfo{}, ErrorCode: "NotAllowed"})
		return
	}
	s.ScreenTime.Opened(r.Context(), userOf(r), screentime.Video, "item:"+strconv.FormatInt(it.ID, 10), it.ID)
	p, err := s.Player.Probe(r.Context(), f)
	if err != nil {
		s.internal(w, r, "probe", err)
		return
	}
	ext := subtitles.FindExternal(f.Path, s.DataDir, f.ID)
	// the audio files beside the video play only through the HLS session
	// (its second input): listed in an HLS answer, never in a direct one
	audio := s.Player.ExternalAudio(r.Context(), f, durationMs(f, p), p.StartMs)
	ms := mediaSource(r.Context(), f, p, nil, ext...)
	prof := in.DeviceProfile

	// the subtitle the viewer chose: an image one the client wants burnt
	// in, a text one it takes only inside the HLS manifest
	burn, needHLS := 0, false
	if in.SubtitleStreamIndex != nil && *in.SubtitleStreamIndex >= 0 {
		for _, st := range ms.MediaStreams {
			if st.Type != "Subtitle" || st.Index != *in.SubtitleStreamIndex {
				continue
			}
			m := prof.subMethods(st.Codec)
			switch {
			case !st.IsTextSubtitleStream && !st.IsExternal && m["encode"] && !m["embed"]:
				burn = st.Index
			case st.IsTextSubtitleStream && sidecarFormat(prof) == "" && !(m["embed"] && !st.IsExternal) &&
				prof.subMethods("vtt")["hls"]:
				needHLS = true
			}
		}
	}
	dec, err := s.decide(r, p, in, audio, burn, needHLS)
	if errors.Is(err, errNoStream) {
		writeJSON(w, PlaybackInfoResponse{MediaSources: []MediaSourceInfo{}, ErrorCode: "NoCompatibleStream"})
		return
	}
	if err != nil {
		s.internal(w, r, "decide", err)
		return
	}
	if !dec.direct && len(audio) > 0 {
		ms = mediaSource(r.Context(), f, p, audio, ext...)
	}
	se := sessionOf(r)
	pl := &play{uid: userOf(r).ID, token: se.token, device: se.dev.ID, itemID: it.ID, file: f, d: dec.d, duration: durationMs(f, p)}
	// the play session id is the credential of the URLs below, never the access token
	psid := newPlaySessionID()
	itemID := ItemID(it.ID)
	if in.AudioStreamIndex != nil && !dec.direct {
		ms.DefaultAudioStreamIndex = intp(dec.d.AudioIndex)
		if dec.d.AudioInput != "" {
			ms.DefaultAudioStreamIndex = intp(*in.AudioStreamIndex)
		}
	}
	for i := range ms.MediaStreams {
		st := &ms.MediaStreams[i]
		if st.Type != "Subtitle" {
			continue
		}
		st.DeliveryMethod, st.DeliveryUrl = s.subDelivery(prof, *st, dec.direct, burn, itemID, ms.Id, psid)
		if st.DeliveryMethod == "Hls" && !dec.direct {
			pl.subs = append(pl.subs, *st)
		}
	}
	if in.SubtitleStreamIndex != nil {
		ms.DefaultSubtitleStreamIndex = *in.SubtitleStreamIndex
	}
	s.plays.add(psid, pl)
	if dec.direct {
		ms.SupportsDirectPlay, ms.SupportsDirectStream, ms.SupportsTranscoding = true, true, true
	} else {
		ms.SupportsDirectPlay, ms.SupportsDirectStream, ms.SupportsTranscoding = false, false, true
		q := url.Values{"MediaSourceId": {ms.Id}, "PlaySessionId": {psid}}
		ms.TranscodingUrl = "/videos/" + itemID + "/master.m3u8?" + q.Encode()
		ms.TranscodingSubProtocol, ms.TranscodingContainer = "hls", "ts"
		if dec.d.FMP4 {
			ms.TranscodingContainer = "mp4"
		}
	}
	s.log().Info("jellyfin: playback info", "profile", pl.uid, "item", it.ID, "file", f.ID, "direct", dec.direct,
		"mode", dec.d.Mode, "reason", dec.d.Reason)
	writeJSON(w, PlaybackInfoResponse{MediaSources: []MediaSourceInfo{ms}, PlaySessionId: psid})
}

// sidecarFormat is the format a text subtitle is sent in as a file of its
// own: "vtt" or "srt" by the profile's External methods, "vtt" without
// subtitle profiles; "": the client takes no sidecar.
func sidecarFormat(prof *DeviceProfile) string {
	if !prof.hasSubProfiles() {
		return "vtt"
	}
	if prof.subMethods("vtt")["external"] {
		return "vtt"
	}
	if prof.subMethods("subrip")["external"] || prof.subMethods("srt")["external"] {
		return "srt"
	}
	return ""
}

// subtitleURL is a subtitle file's path. Players fetch it without headers
// (Swiftfin resolves DeliveryUrl with url(path:), which adds no token): the
// play session in the query is its credential (requirePlay).
func subtitleURL(itemID, msID string, idx int, format, psid string) string {
	return "/videos/" + itemID + "/" + msID + "/subtitles/" + strconv.Itoa(idx) + "/0/stream." + format + "?PlaySessionId=" + psid
}

// subDelivery is how a subtitle stream reaches the client: Embed (it is in
// the file the client plays directly and the profile reads it there),
// External (a WebVTT or SRT file), Hls (a rendition of the HLS manifest),
// Encode (burnt into the picture: an image subtitle), or Drop.
func (s *Server) subDelivery(prof *DeviceProfile, st MediaStream, direct bool, burn int, itemID, msID, psid string) (string, string) {
	m := prof.subMethods(st.Codec)
	switch {
	case !prof.hasSubProfiles():
		if st.IsTextSubtitleStream {
			return "External", subtitleURL(itemID, msID, st.Index, "vtt", psid)
		}
		if direct {
			return "Embed", ""
		}
		return "Encode", ""
	case direct && !st.IsExternal && m["embed"]:
		return "Embed", ""
	case st.IsTextSubtitleStream && sidecarFormat(prof) != "":
		return "External", subtitleURL(itemID, msID, st.Index, sidecarFormat(prof), psid)
	case st.IsTextSubtitleStream && prof.subMethods("vtt")["hls"]:
		// direct: the client asks again for this track, and gets HLS
		return "Hls", ""
	case !st.IsTextSubtitleStream && !st.IsExternal && (m["encode"] || st.Index == burn):
		return "Encode", ""
	}
	return "Drop", ""
}

// download serves an item's file (Swiftfin's offline copies).
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	_, f, ok := s.videoItem(w, r, chi.URLParam(r, "id"), qv(r, "mediaSourceId"))
	// a kid whose video is limited keeps no offline copies (Swiftfin's downloads)
	if ok && s.ScreenTime.Restricted(r.Context(), userOf(r), screentime.Video) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if ok {
		defer s.mediaRead()()
		s.Player.ServeFile(w, r, f)
	}
}

// videoFile serves /Videos/{id}/stream[.ext] (the file with Range: direct
// play), master.m3u8 and main.m3u8 (HLS).
func (s *Server) videoFile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	switch {
	case name == "stream" || strings.HasPrefix(name, "stream."):
		// the client builds this URL with its own token (Swiftfin:
		// url(with:queryAPIKey:)); a play session never opens the file
		if scopedPlay(r) != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, f, ok := s.videoItem(w, r, chi.URLParam(r, "id"), qv(r, "mediaSourceId"))
		if ok {
			defer s.mediaRead()()
			s.Player.ServeFile(w, r, f)
		}
	case name == "master.m3u8":
		s.master(w, r)
	case name == "main.m3u8":
		s.mainPlaylist(w, r)
	default:
		s.notFound(w, r)
	}
}
