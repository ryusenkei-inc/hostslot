//go:build unix

package platform

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBackgroundJobsRunNiced(t *testing.T) {
	p := testPlatform(t)
	cmd := exec.Command(self(t))
	cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=sleep")
	proc, err := p.Tracker.Start(cmd, StartOptions{Background: true, Cleanup: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.KillTree(); _ = proc.Wait() }()
	// The raw value differs by OS (Linux reports 20 - nice); niced only has
	// to differ from this test's own priority.
	child, err := unix.Getpriority(unix.PRIO_PROCESS, proc.Pid())
	if err != nil {
		t.Fatal(err)
	}
	self, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	if child == self {
		t.Fatalf("background job runs at this process's priority (%d)", self)
	}
}
