package jellyfin

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
)

// playOf is the request's play session (PlaySessionId in the query, or
// in the path of a segment) of item {id}; ok false: the 404 is written.
func (s *Server) playOf(w http.ResponseWriter, r *http.Request, psid string) (play, bool) {
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	if s.Player == nil || !ok || k != kindItem {
		w.WriteHeader(http.StatusNotFound)
		return play{}, false
	}
	p, ok := s.plays.get(psid, userOf(r).ID)
	if !ok || p.itemID != id {
		w.WriteHeader(http.StatusNotFound)
		return play{}, false
	}
	// the profile may have lost the right to see it since (a rating change)
	if _, ok, err := s.conv(r.Context(), userOf(r), nil).visibleItem(id); err != nil || !ok {
		w.WriteHeader(http.StatusNotFound)
		return play{}, false
	}
	return p, true
}

// startHLS starts (again: restart, the session idled out) the HLS session
// of a play session; one start at a time per play session, so two
// requests of a player never start two sessions (other plays start
// meanwhile).
func (s *Server) startHLS(r *http.Request, psid string, p play, restart bool) (string, error) {
	p.start.Lock()
	defer p.start.Unlock()
	if cur, ok := s.plays.get(psid, p.uid); ok && cur.hls != "" && (!restart || cur.hls != p.hls) {
		return cur.hls, nil // started meanwhile
	}
	replace := s.plays.lastHLS(p.uid, p.device, p.itemID)
	sid, err := s.Player.StartHLS(r, p.file, p.d, p.duration, replace)
	if err != nil {
		return "", err
	}
	s.plays.setHLS(psid, sid)
	return sid, nil
}

// hlsUnavailable answers a refused start: limits and a full disk are 503
// with Retry-After (the player tries again), anything else 500.
func (s *Server) hlsUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, media.ErrUserLimit) || errors.Is(err, media.ErrBusy) || errors.Is(err, media.ErrNoSpace) {
		s.log().Info("jellyfin: hls refused", "err", err)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	s.internal(w, r, "hls start", err)
}

// master is the HLS master playlist of a play session: one variant (the
// session's media playlist) and the text subtitles as WebVTT renditions.
// The session starts here.
func (s *Server) master(w http.ResponseWriter, r *http.Request) {
	psid := qv(r, "PlaySessionId")
	p, ok := s.playOf(w, r, psid)
	if !ok {
		return
	}
	if ref := s.ScreenTime.Allow(r.Context(), userOf(r), screentime.Video); ref != nil {
		s.log().Info("jellyfin: hls refused by screen time", "profile", userOf(r).ID, "reason", ref.Reason)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if p.hls == "" {
		if _, err := s.startHLS(r, psid, p, false); err != nil {
			s.hlsUnavailable(w, r, err)
			return
		}
	}
	// the URIs carry the play session, never the access token (requirePlay)
	q := url.Values{"PlaySessionId": {psid}}.Encode()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	msID := FileID(p.file.ID)
	for i, st := range p.subs {
		name := st.DisplayTitle
		if name == "" {
			name = strconv.Itoa(st.Index)
		}
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=%s,DEFAULT=NO,AUTOSELECT=%s,FORCED=%s",
			quoted(fmt.Sprintf("%s (%d)", name, i+1)), yesNo(!st.IsForced), yesNo(st.IsForced))
		if st.Language != "" {
			fmt.Fprintf(&b, ",LANGUAGE=%s", quoted(st.Language))
		}
		fmt.Fprintf(&b, ",URI=\"%s/subtitles/%d/subtitles.m3u8?%s\"\n", msID, st.Index, q)
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d", bandwidth(p))
	if w, h := resolution(p); w > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", w, h)
	}
	if len(p.subs) > 0 {
		b.WriteString(",SUBTITLES=\"subs\"")
	}
	fmt.Fprintf(&b, "\nmain.m3u8?%s\n", q)
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(b.String()))
}

func yesNo(v bool) string {
	if v {
		return "YES"
	}
	return "NO"
}

// quoted is an HLS quoted-string (no double quote or line break inside).
func quoted(v string) string {
	return `"` + strings.NewReplacer(`"`, "'", "\n", " ", "\r", " ").Replace(v) + `"`
}

// bandwidth is the variant's peak bit rate: the cap of a re-encode, else
// the source's, else 20 Mbit/s.
func bandwidth(p play) int64 {
	if !p.d.VideoCopy && p.d.MaxBitrate > 0 {
		return p.d.MaxBitrate
	}
	if p.duration > 0 && p.file.Size > 0 {
		return p.file.Size * 8 * 1000 / p.duration
	}
	return 20_000_000
}

// resolution is the variant's picture size (0: unknown).
func resolution(p play) (int, int) {
	v := p.probeVideo()
	if v == nil || v.Width <= 0 || v.Height <= 0 {
		return 0, 0
	}
	h := v.Height
	if p.d.TargetHeight > 0 && p.d.TargetHeight < h {
		h = p.d.TargetHeight
	}
	w := int(math.Round(float64(v.Width)*float64(h)/float64(v.Height)/2)) * 2
	return w, h
}

// probeVideo is the file's cached video stream (nil: none or not probed).
func (p play) probeVideo() *media.VideoStream {
	pr, _ := cachedProbe(p.file)
	return pr.Video
}

// mainPlaylist is the session's media playlist: the complete VOD playlist
// when the session has one, every URI pointing at hls1/{PlaySessionId}/
// (the play session in the path is the segments' credential: AVPlayer and
// mpv fetch them without headers). A session that idled out (a long
// pause) is started again.
func (s *Server) mainPlaylist(w http.ResponseWriter, r *http.Request) {
	psid := qv(r, "PlaySessionId")
	p, ok := s.playOf(w, r, psid)
	if !ok {
		return
	}
	uri := func(u string) string { return "hls1/" + psid + "/" + u }
	s.serveHLS(w, r, psid, p, "index.m3u8", uri)
}

// hlsSegment serves a segment (or init.mp4) of a play session's HLS session.
func (s *Server) hlsSegment(w http.ResponseWriter, r *http.Request) {
	psid := chi.URLParam(r, "play")
	p, ok := s.playOf(w, r, psid)
	if !ok {
		return
	}
	defer s.mediaRead()()
	s.serveHLS(w, r, psid, p, chi.URLParam(r, "name"), nil)
}

// serveHLS serves name of the play's HLS session, starting the session
// first when it has none or it is gone.
func (s *Server) serveHLS(w http.ResponseWriter, r *http.Request, psid string, p play, name string, uri func(string) string) {
	sid := p.hls
	for try := 0; try < 2; try++ {
		if sid == "" || try > 0 {
			var err error
			if sid, err = s.startHLS(r, psid, p, try > 0); err != nil {
				s.hlsUnavailable(w, r, err)
				return
			}
			p.hls = sid
		}
		if err := s.Player.ServeHLS(w, r, sid, name, uri); !errors.Is(err, media.ErrNoSession) {
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// stopEncoding ends the HLS session of a play session
// (DELETE /Videos/ActiveEncodings?playSessionId=).
func (s *Server) stopEncoding(w http.ResponseWriter, r *http.Request) {
	s.stopPlay(r, qv(r, "playSessionId"))
	w.WriteHeader(http.StatusNoContent)
}

// stopPlay ends a play session's HLS session (the play session stays: a
// player may resume it; its next request starts a new HLS session).
func (s *Server) stopPlay(r *http.Request, psid string) {
	if psid == "" || s.Player == nil {
		return
	}
	if p, ok := s.plays.get(psid, userOf(r).ID); ok && p.hls != "" {
		s.Player.StopHLS(r, p.hls)
		s.plays.setHLS(psid, "")
	}
}

// touchPlay is the heartbeat of a play session's HLS session.
func (s *Server) touchPlay(r *http.Request, psid string) {
	if psid == "" || s.Player == nil {
		return
	}
	if p, ok := s.plays.get(psid, userOf(r).ID); ok && p.hls != "" {
		s.Player.TouchHLS(r, p.hls)
	}
}

// defaultTimelineOffset is a transcode's TS offset (ffmpeg's mux delay),
// used when the session is unknown.
const defaultTimelineOffset = 1.4

// vttTimestampMap ties WebVTT cue time 0 to the HLS segments' clock in a
// subtitle rendition (RFC 8216 §3.5): the play session's timeline offset
// (Player.TimelineOffset) on the 90 kHz MPEG-TS clock.
func (s *Server) vttTimestampMap(r *http.Request) string {
	off := defaultTimelineOffset
	if p, ok := s.plays.get(qv(r, "PlaySessionId"), userOf(r).ID); ok && p.hls != "" {
		if v, ok := s.Player.TimelineOffset(p.hls); ok {
			off = v
		}
	}
	return fmt.Sprintf("X-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:00:00:00.000", int64(math.Round(off*90000)))
}

// subtitle serves a subtitle stream: Stream.vtt / Stream.srt (the web's
// WebVTT, SRT converted from it), or subtitles.m3u8, the one-segment
// playlist of an HLS rendition.
func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	_, f, ok := s.videoItem(w, r, chi.URLParam(r, "id"), chi.URLParam(r, "ms"))
	if !ok {
		return
	}
	if k, fid, ok := ParseID(chi.URLParam(r, "ms")); !ok || k != kindFile || fid != f.ID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// authorised by a play session: only that play's file
	if psid := scopedPlay(r); psid != "" {
		if p, ok := s.plays.get(psid, userOf(r).ID); !ok || p.file.ID != f.ID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	name := chi.URLParam(r, "file")
	if name == "subtitles.m3u8" {
		dur := float64(f.DurationMs) / 1000
		if pr, ok := cachedProbe(f); ok && pr.DurationMs > 0 {
			dur = float64(pr.DurationMs) / 1000
		}
		if dur <= 0 {
			dur = 1
		}
		seg := "0/stream.vtt?" + url.Values{"hls": {"1"}, "PlaySessionId": {qv(r, "PlaySessionId")}}.Encode()
		body := fmt.Sprintf("#EXTM3U\n#EXT-X-TARGETDURATION:%d\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:%.3f,\n%s\n#EXT-X-ENDLIST\n",
			int(math.Ceil(dur)), dur, seg)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte(body))
		return
	}
	format, ok := strings.CutPrefix(name, "stream.")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	format = strings.ToLower(format)
	inHLS := qBool(r, "hls", false)
	if format == "vtt" || format == "webvtt" {
		if !inHLS {
			s.Player.ServeSubtitle(w, r, f, idx)
			return
		}
	} else if format != "srt" && format != "subrip" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// a converted answer: the WebVTT first
	rec := httptest.NewRecorder()
	s.Player.ServeSubtitle(rec, r, f, idx)
	res := rec.Result()
	body := rec.Body.Bytes()
	for k, v := range res.Header {
		if k != "Content-Length" && k != "Content-Type" {
			w.Header()[k] = v
		}
	}
	if res.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		w.Write(body)
		return
	}
	if inHLS {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Write(withTimestampMap(body, s.vttTimestampMap(r)))
		return
	}
	w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
	w.Write(vttToSRT(body))
}

// withTimestampMap puts the X-TIMESTAMP-MAP header after the WEBVTT line.
func withTimestampMap(vtt []byte, header string) []byte {
	head, rest, _ := bytes.Cut(vtt, []byte("\n"))
	if bytes.Contains(vtt, []byte("X-TIMESTAMP-MAP")) {
		return vtt
	}
	out := append([]byte{}, bytes.TrimRight(head, "\r")...)
	out = append(out, '\n')
	out = append(out, header...)
	out = append(out, '\n')
	return append(out, rest...)
}

// vttToSRT converts ffmpeg's WebVTT to SubRip: numbered cues, a comma
// before the milliseconds, no header, no cue settings, tags kept.
func vttToSRT(vtt []byte) []byte {
	text := strings.ReplaceAll(string(vtt), "\r\n", "\n")
	var b strings.Builder
	n := 0
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.Trim(block, "\n")
		lines := strings.Split(block, "\n")
		at := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				at = i
				break
			}
		}
		if at < 0 {
			continue // the header, NOTE and STYLE blocks
		}
		from, to, _ := strings.Cut(lines[at], "-->")
		to = strings.TrimSpace(to)
		if i := strings.IndexByte(to, ' '); i > 0 {
			to = to[:i] // cue settings
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n", n, srtTime(strings.TrimSpace(from)), srtTime(to))
		for _, l := range lines[at+1:] {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// srtTime is a WebVTT time ([hh:]mm:ss.ttt) as SubRip's hh:mm:ss,ttt.
func srtTime(t string) string {
	if strings.Count(t, ":") == 1 {
		t = "00:" + t
	}
	return strings.Replace(t, ".", ",", 1)
}
