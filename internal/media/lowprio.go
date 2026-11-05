package media

import (
	"os/exec"
	"sync"
)

// lowPrioTools are the wrappers found once: nice (CPU, lowest priority) and
// ionice (the idle I/O class, Linux). Both exec the program they run, so
// the process keeps its pid and RunYielding's SIGSTOP still reaches it.
var lowPrioTools struct {
	once         sync.Once
	nice, ionice string
}

// LowPriority makes cmd (not started) run at the lowest CPU priority and,
// where ionice exists, in the idle I/O class: a background reader of the
// media (scrub previews) then never slows playback or the web UI. Without
// the tools cmd is left as it is.
func LowPriority(cmd *exec.Cmd) {
	lowPrioTools.once.Do(func() {
		lowPrioTools.nice, _ = exec.LookPath("nice")
		lowPrioTools.ionice, _ = exec.LookPath("ionice")
	})
	lowPriorityWith(cmd, lowPrioTools.nice, lowPrioTools.ionice)
}

func lowPriorityWith(cmd *exec.Cmd, nice, ionice string) {
	if cmd.Process != nil || len(cmd.Args) == 0 {
		return
	}
	prog := cmd.Path
	args := append([]string{prog}, cmd.Args[1:]...)
	if nice != "" {
		args = append([]string{nice, "-n", "19"}, args...)
		prog = nice
	}
	if ionice != "" {
		args = append([]string{ionice, "-c", "3"}, args...)
		prog = ionice
	}
	cmd.Path, cmd.Args = prog, args
}
