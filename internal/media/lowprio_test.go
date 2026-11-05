package media

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestLowPriorityWith(t *testing.T) {
	cmd := exec.Command("/bin/ffmpeg", "-i", "x")
	lowPriorityWith(cmd, "/usr/bin/nice", "/usr/bin/ionice")
	if cmd.Path != "/usr/bin/ionice" {
		t.Fatalf("path %q", cmd.Path)
	}
	want := []string{"/usr/bin/ionice", "-c", "3", "/usr/bin/nice", "-n", "19", "/bin/ffmpeg", "-i", "x"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args %q", cmd.Args)
	}

	cmd = exec.Command("/bin/ffmpeg", "-i", "x")
	lowPriorityWith(cmd, "/usr/bin/nice", "")
	if want := []string{"/usr/bin/nice", "-n", "19", "/bin/ffmpeg", "-i", "x"}; cmd.Path != "/usr/bin/nice" || !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("nice only: %q %q", cmd.Path, cmd.Args)
	}

	cmd = exec.Command("/bin/ffmpeg", "-i", "x")
	lowPriorityWith(cmd, "", "")
	if cmd.Path != "/bin/ffmpeg" || len(cmd.Args) != 3 {
		t.Fatalf("no tools: %q %q", cmd.Path, cmd.Args)
	}
}

// The wrapped command still runs (the real nice, where there is one).
func TestLowPriorityRuns(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("no echo")
	}
	cmd := exec.Command("echo", "hi")
	LowPriority(cmd)
	out, err := cmd.Output()
	if err != nil || string(out) != "hi\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}
