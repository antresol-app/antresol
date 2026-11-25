package media

import "context"

// ProbeLowPriority is Probe at the lowest CPU and disk priority: for the
// library's background pass over every unprobed file, which must not slow a
// playback or the web UI.
func ProbeLowPriority(ctx context.Context, ffprobe, path string) (ProbeResult, error) {
	return probe(ctx, ffprobe, path, nil, LowPriority)
}
