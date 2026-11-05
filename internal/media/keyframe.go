package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// errNoKeyframe: no video keyframe was found before the requested time.
var errNoKeyframe = errors.New("media: no keyframe before the start")

// KeyframeBefore returns the time of the last video keyframe at or before t
// in input (a local file or a loopback URL, see InputArgs). A remux copies
// the video, so it can only start on a keyframe: ffmpeg's -ss lands there
// and the stream's time 0 is that keyframe, not t. The player must use the
// keyframe as its offset, or subtitles and saved progress run ahead of the
// picture by up to a GOP.
func KeyframeBefore(ctx context.Context, ffprobe, input string, t float64) (float64, error) {
	opts, operand, err := InputArgs(input)
	if err != nil {
		return 0, err
	}
	if t <= 0 {
		return 0, nil
	}
	for _, back := range []float64{12, 45} {
		from := max(0, t-back)
		args := append([]string{"-v", "error"}, opts...)
		args = append(args, "-select_streams", "v:0",
			"-read_intervals", fmt.Sprintf("%.3f%%%.3f", from, t+0.001),
			"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", operand)
		out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return 0, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
			}
			return 0, fmt.Errorf("ffprobe: %w", err)
		}
		if k, ok := lastKeyframe(out, t); ok {
			return k, nil
		}
		if from == 0 {
			break
		}
	}
	return 0, errNoKeyframe
}

// lastKeyframe parses ffprobe "pts_time,flags" lines.
func lastKeyframe(out []byte, t float64) (float64, bool) {
	best, found := 0.0, false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(f) < 2 || !strings.Contains(f[1], "K") {
			continue
		}
		pts, err := strconv.ParseFloat(f[0], 64)
		if err != nil || pts > t+0.0005 {
			continue
		}
		if !found || pts > best {
			best, found = pts, true
		}
	}
	return best, found
}
