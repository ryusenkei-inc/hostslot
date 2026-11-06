package platform

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBackgroundJobsRunBelowNormalPriority(t *testing.T) {
	p := testPlatform(t)
	cmd := exec.Command(self(t))
	cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=sleep")
	proc, err := p.Tracker.Start(cmd, StartOptions{Background: true, Cleanup: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.KillTree(); _ = proc.Wait() }()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(proc.Pid()))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	class, err := windows.GetPriorityClass(h)
	if err != nil || class != windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Fatalf("priority class %#x err %v, want below normal", class, err)
	}
}
