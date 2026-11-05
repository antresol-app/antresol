package subsonic

import (
	"encoding/xml"
	"testing"
)

// TestSongReplayGain: OpenSubsonic replayGain on songs with a known gain
// (JSON and XML), none on a song without.
func TestSongReplayGain(t *testing.T) {
	e := newEnv(t)
	album, tr := e.album("Кино", "Группа крови", 1988, "Рок", tTrack{1, "Группа крови"}, tTrack{2, "Закрой за мной дверь"})
	if _, err := e.db.Exec(`UPDATE tracks SET rg_track_gain=-6.5, rg_track_peak=0.9, loud_album_gain=-7, loud_album_peak=0.95 WHERE item_id=?`, tr[0]); err != nil {
		t.Fatal(err)
	}
	got := e.mustOK(e.call("getAlbum", "id", itoa(album)))
	songs := got["album"].(map[string]any)["song"].([]any)
	rg, ok := songs[0].(map[string]any)["replayGain"].(map[string]any)
	if !ok || rg["trackGain"] != -6.5 || rg["trackPeak"] != 0.9 || rg["albumGain"] != -7.0 || rg["albumPeak"] != 0.95 {
		t.Fatalf("replayGain %v", songs[0])
	}
	if _, has := songs[1].(map[string]any)["replayGain"]; has {
		t.Fatalf("a song without a gain has replayGain: %v", songs[1])
	}
	p := e.creds()
	p.Set("id", itoa(tr[0]))
	w := e.req("getSong", "", p)
	var out struct {
		Song struct {
			ReplayGain struct {
				TrackGain float64 `xml:"trackGain,attr"`
				AlbumPeak float64 `xml:"albumPeak,attr"`
			} `xml:"http://subsonic.org/restapi replayGain"`
		} `xml:"http://subsonic.org/restapi song"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Song.ReplayGain.TrackGain != -6.5 || out.Song.ReplayGain.AlbumPeak != 0.95 {
		t.Fatalf("%v %+v\n%s", err, out, w.Body)
	}
}
