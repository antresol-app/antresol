package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

// Repacker turns MPEG-TS films and episodes (.ts, .m2ts, .mts: Blu-ray
// remuxes) into Matroska without re-encoding. Every seek in a transport
// stream is a bisection over the whole file: on the server's disk ~10 s,
// and a far seek in the player needs two (the keyframe, then ffmpeg's
// start), while Matroska's cues answer at once and give the player a
// complete playlist.
//
// The file row keeps its id (progress, subtitles and the item stay) and
// points at the .mkv; the original stays beside it as a hidden
// ".<name>.vault-original" (scans skip dot files) for Keep, then goes.
// A file another tool shares, or one hard-linked from another app, is left
// as it is; a file ffmpeg cannot repack is not tried again until it
// changes.
type Repacker struct {
	DB *sql.DB
	// Scanner (optional): the swap waits for a running scan.
	Scanner         *Scanner
	FFmpeg, FFprobe string
	Keep            time.Duration
	Log             *slog.Logger
	Now             func() time.Time
	// Command runs ffmpeg and ffprobe (tests count the runs).
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd

	once sync.Once
	kick chan struct{}
}

const (
	repackOriginalSuffix = ".vault-original"
	repackTempSuffix     = ".vault-repack.part"
	// settings keys: kept originals (path → unix time), failed sources (path → size:mtime)
	repackOriginalsKey = "repack_originals"
	repackFailedKey    = "repack_failed"
)

var repackExt = map[string]bool{".ts": true, ".m2ts": true, ".mts": true}

func (r *Repacker) init() {
	r.once.Do(func() {
		r.kick = make(chan struct{}, 1)
		if r.Log == nil {
			r.Log = slog.Default()
		}
		if r.Now == nil {
			r.Now = time.Now
		}
		if r.Command == nil {
			r.Command = exec.CommandContext
		}
	})
}

// Kick asks Run for a pass (after a scan); never blocks.
func (r *Repacker) Kick() {
	r.init()
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run makes a pass on every Kick until ctx ends.
func (r *Repacker) Run(ctx context.Context) {
	r.init()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
			if err := r.Pass(ctx); err != nil && ctx.Err() == nil {
				r.Log.Warn("library: repack", "err", err)
			}
		}
	}
}

// Pass repacks every candidate, one at a time, then removes the originals
// kept longer than Keep.
func (r *Repacker) Pass(ctx context.Context) error {
	r.init()
	rows, err := r.DB.QueryContext(ctx, `SELECT f.id, f.path FROM files f JOIN items i ON i.id = f.item_id
  WHERE i.kind IN ('movie', 'episode') AND f.missing_since IS NULL AND f.torrent_infohash IS NULL`)
	if err != nil {
		return err
	}
	type cand struct {
		id   int64
		path string
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.path); err != nil {
			rows.Close()
			return err
		}
		if repackExt[strings.ToLower(filepath.Ext(c.path))] {
			cs = append(cs, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range cs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.repackOne(ctx, c.id, c.path); err != nil {
			r.Log.Warn("library: repack", "file", c.path, "err", err)
		}
	}
	return r.purge(ctx)
}

func (r *Repacker) repackOne(ctx context.Context, id int64, src string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return nil // gone: the scan marks it missing
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) > 1 {
		return nil // another app's hard link: a copy would double the space
	}
	failed, err := r.loadMap(ctx, repackFailedKey)
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	if failed[src] == stamp {
		return nil
	}
	dir, base := filepath.Dir(src), filepath.Base(src)
	out := strings.TrimSuffix(src, filepath.Ext(src)) + ".mkv"
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(out))
	}
	tmp := filepath.Join(dir, "."+filepath.Base(out)+repackTempSuffix)
	defer os.Remove(tmp) // a failed run leaves nothing; a done one renamed it
	start := time.Now()
	r.Log.Info("library: repack to Matroska", "file", src)
	cmd := r.Command(ctx, r.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", src, "-map", "0:v?", "-map", "0:a?", "-map", "0:s?", "-c", "copy", "-f", "matroska", tmp)
	// a film read and written whole: at the lowest CPU and I/O priority,
	// like the other background readers of the media disk
	media.LowPriority(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return r.fail(ctx, src, stamp, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(b))))
	}
	if err := r.same(ctx, src, tmp); err != nil {
		return r.fail(ctx, src, stamp, err)
	}
	orig := filepath.Join(dir, "."+base+repackOriginalSuffix)
	info, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if err := r.swap(ctx, id, src, tmp, out, orig, info); err != nil {
		return err
	}
	r.Log.Info("library: repacked", "file", out, "took", time.Since(start).Round(time.Second))
	kept, err := r.loadMap(ctx, repackOriginalsKey)
	if err != nil {
		return err
	}
	kept[orig] = strconv.FormatInt(r.Now().Unix(), 10)
	return r.saveMap(ctx, repackOriginalsKey, kept)
}

// swap puts the .mkv in the .ts's place: renames on disk and the file row
// together, under the scan lock, so no scan sees one without the other.
func (r *Repacker) swap(ctx context.Context, id int64, src, tmp, out, orig string, info os.FileInfo) error {
	if r.Scanner != nil {
		r.Scanner.mu.Lock()
		defer r.Scanner.mu.Unlock()
	}
	if err := os.Rename(src, orig); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return errors.Join(err, os.Rename(orig, src))
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE files SET path=?, size=?, mtime=?, probe_json=NULL, duration_ms=NULL WHERE id=? AND path=?`,
		out, info.Size(), info.ModTime().Unix(), id, src)
	if err != nil {
		return errors.Join(err, os.Rename(out, tmp), os.Rename(orig, src))
	}
	return nil
}

// same checks the copy: as long as the source (within 2 s or 0.5 %) and
// with as many video, audio and subtitle streams.
func (r *Repacker) same(ctx context.Context, src, dst string) error {
	a, err := r.probe(ctx, src)
	if err != nil {
		return err
	}
	b, err := r.probe(ctx, dst)
	if err != nil {
		return err
	}
	if math.Abs(a.dur-b.dur) > max(2, a.dur*0.005) {
		return fmt.Errorf("duration %.1f s, source %.1f s", b.dur, a.dur)
	}
	for _, k := range []string{"video", "audio", "subtitle"} {
		if a.streams[k] != b.streams[k] {
			return fmt.Errorf("%s streams %d, source %d", k, b.streams[k], a.streams[k])
		}
	}
	return nil
}

type repackProbe struct {
	dur     float64
	streams map[string]int
}

func (r *Repacker) probe(ctx context.Context, path string) (repackProbe, error) {
	out, err := r.Command(ctx, r.FFprobe, "-v", "error", "-show_entries", "format=duration:stream=codec_type",
		"-of", "json", path).Output()
	if err != nil {
		return repackProbe{}, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), err)
	}
	var v struct {
		Format  struct{ Duration string } `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return repackProbe{}, err
	}
	p := repackProbe{streams: map[string]int{}}
	p.dur, _ = strconv.ParseFloat(v.Format.Duration, 64)
	for _, s := range v.Streams {
		p.streams[s.CodecType]++
	}
	return p, nil
}

func (r *Repacker) fail(ctx context.Context, src, stamp string, cause error) error {
	failed, err := r.loadMap(ctx, repackFailedKey)
	if err != nil {
		return errors.Join(cause, err)
	}
	failed[src] = stamp
	return errors.Join(cause, r.saveMap(ctx, repackFailedKey, failed))
}

// purge removes the originals kept longer than Keep.
func (r *Repacker) purge(ctx context.Context) error {
	kept, err := r.loadMap(ctx, repackOriginalsKey)
	if err != nil || len(kept) == 0 {
		return err
	}
	changed := false
	for p, at := range kept {
		sec, _ := strconv.ParseInt(at, 10, 64)
		if r.Now().Sub(time.Unix(sec, 0)) < r.Keep {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			r.Log.Warn("library: repack: remove original", "file", p, "err", err)
			continue
		}
		delete(kept, p)
		changed = true
	}
	if !changed {
		return nil
	}
	return r.saveMap(ctx, repackOriginalsKey, kept)
}

func (r *Repacker) loadMap(ctx context.Context, key string) (map[string]string, error) {
	m := map[string]string{}
	var raw string
	err := r.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("settings %s: %w", key, err)
	}
	return m, nil
}

func (r *Repacker) saveMap(ctx context.Context, key string, m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(b))
	return err
}
