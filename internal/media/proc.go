package media

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// PauseProcess and ResumeProcess stop and continue a process (SIGSTOP and
// SIGCONT; unsupported where there are no signals).
func PauseProcess(p *os.Process) error  { return pauseProcess(p) }
func ResumeProcess(p *os.Process) error { return resumeProcess(p) }

// RunYielding runs cmd, holding it stopped (SIGSTOP) while busy says
// playback needs the media disk: on a spinning disk a full-file read next to
// a seek made the first segment take 2-9 s instead of 0.1 s. Background
// readers of the media (the subtitle prefetch, the skip-segment analysis)
// run through it.
func RunYielding(cmd *exec.Cmd, busy func() bool, every time.Duration) error {
	return RunYieldingLimit(cmd, busy, every, 0)
}

// RunYieldingLimit is RunYielding that kills cmd once it has RUN (not been
// held stopped) longer than limit (0: no limit): a long playback that
// keeps it stopped does not count.
func RunYieldingLimit(cmd *exec.Cmd, busy func() bool, every, limit time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(every)
	defer tick.Stop()
	paused, killed := false, false
	var ran time.Duration
	last := time.Now()
	for {
		select {
		case err := <-done:
			if killed {
				return fmt.Errorf("ran past its time limit (%v): %w", limit, err)
			}
			return err
		case <-tick.C:
			now := time.Now()
			if !paused {
				ran += now.Sub(last)
			}
			last = now
			if limit > 0 && ran > limit && !killed {
				killed = true
				ResumeProcess(cmd.Process) // a stopped process does not die of SIGKILL on every platform at once
				cmd.Process.Kill()
				continue
			}
			if b := busy(); b != paused {
				if b {
					PauseProcess(cmd.Process)
				} else {
					ResumeProcess(cmd.Process)
				}
				paused = b
			}
		}
	}
}
