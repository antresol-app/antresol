package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// Chapter is one chapter embedded in an audio file (M4B/M4A chapter list,
// MP3 CHAP frames): positions in milliseconds from the start of the file.
type Chapter struct {
	Title   string
	StartMs int64
	EndMs   *int64 // nil: unknown (to the end of the file)
}

type ffprobeChapters struct {
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

// Chapters lists the chapters of a local audio file with
// `ffprobe -show_chapters` (file protocol only, see InputArgs). A file
// without chapters gives none and no error.
func Chapters(ctx context.Context, ffprobe, path string) ([]Chapter, error) {
	opts, operand, err := InputArgs(path)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-v", "error"}, opts...)
	args = append(args, "-print_format", "json", "-show_chapters", operand)
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseChapters(out)
}

func parseChapters(js []byte) ([]Chapter, error) {
	var o ffprobeChapters
	if err := json.Unmarshal(js, &o); err != nil {
		return nil, fmt.Errorf("ffprobe chapters: %w", err)
	}
	out := make([]Chapter, 0, len(o.Chapters))
	for _, c := range o.Chapters {
		start, ok := secondsMs(c.StartTime)
		if !ok {
			continue
		}
		ch := Chapter{StartMs: start}
		for k, v := range c.Tags {
			if strings.EqualFold(k, "title") {
				ch.Title = strings.TrimSpace(v)
			}
		}
		if end, ok := secondsMs(c.EndTime); ok && end > start {
			ch.EndMs = &end
		}
		out = append(out, ch)
	}
	return out, nil
}

// secondsMs parses ffprobe's "12.345000" seconds as milliseconds.
func secondsMs(s string) (int64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int64(math.Round(f * 1000)), true
}
