package platform

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestMain doubles as the child process for tracker tests, so they need no
// external programs.
func TestMain(m *testing.M) {
	switch os.Getenv("LOADSLOT_TEST_HELPER") {
	case "alloc":
		buf := make([]byte, 96*mib)
		for i := range buf {
			if i%4096 == 0 {
				buf[i] = 1
			}
		}
		time.Sleep(800 * time.Millisecond)
		os.Exit(3 + int(buf[0]) - 1)
	case "tree":
		exe, _ := os.Executable()
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=alloc")
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func self(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func testPlatform(t *testing.T) *Platform {
	t.Helper()
	dir := t.TempDir()
	return Native(Paths{Data: dir, Runtime: dir})
}

func TestMetrics(t *testing.T) {
	p := testPlatform(t)
	m, err := p.Metrics.Memory()
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalMB < 256 || m.FreeMB == 0 || m.FreeMB > m.TotalMB {
		t.Fatalf("implausible memory reading %+v", m)
	}
	if p.Metrics.CPUs() < 1 {
		t.Fatal("no CPUs")
	}
	t.Logf("memory %+v cpus %d", m, p.Metrics.CPUs())
}

func TestTrackerTreePeakAndExitCode(t *testing.T) {
	p := testPlatform(t)
	if !p.Tracker.Capabilities().TreeMemory {
		t.Skip("tree memory not supported")
	}
	cmd := exec.Command(self(t))
	cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=tree")
	proc, err := p.Tracker.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	err = proc.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("exit: %v", err)
	}
	if peak := proc.PeakMB(); peak < 80 {
		t.Fatalf("peak %d MB, want >= 80 MB from the grandchild", peak)
	}
}

func TestTrackerCurrentMemory(t *testing.T) {
	p := testPlatform(t)
	if !p.Tracker.Capabilities().TreeMemory {
		t.Skip("tree memory not supported")
	}
	cmd := exec.Command(self(t))
	cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=tree")
	proc, err := p.Tracker.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(waited)
	}()
	var seen uint64
	for running := true; running; {
		seen = max(seen, proc.CurrentMB())
		select {
		case <-waited:
			running = false
		case <-time.After(50 * time.Millisecond):
		}
	}
	if seen < 80 {
		t.Fatalf("current memory peaked at %d MB while running, want >= 80 MB", seen)
	}
	if now := proc.CurrentMB(); now != 0 {
		t.Fatalf("current memory %d MB after exit, want 0", now)
	}
}

func TestTrackerKillTree(t *testing.T) {
	p := testPlatform(t)
	cmd := exec.Command(self(t))
	cmd.Env = append(os.Environ(), "LOADSLOT_TEST_HELPER=sleep")
	proc, err := p.Tracker.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	if err := proc.KillTree(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("process not killed")
	}
	if p.Alive(proc.Pid()) {
		t.Fatal("process still alive")
	}
}

func TestAlive(t *testing.T) {
	p := testPlatform(t)
	if !p.Alive(os.Getpid()) {
		t.Fatal("self not alive")
	}
}

func TestLockExcludes(t *testing.T) {
	p := testPlatform(t)
	unlock, ok, err := p.Locks.TryLock("slot-0")
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if _, ok, err := p.Locks.TryLock("slot-0"); err != nil || ok {
		t.Fatalf("second lock acquired: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := p.Locks.TryLock("slot-1"); !ok {
		t.Fatal("independent lock not acquired")
	}
	unlock()
	unlock2, ok, err := p.Locks.TryLock("slot-0")
	if err != nil || !ok {
		t.Fatalf("relock: ok=%v err=%v", ok, err)
	}
	unlock2()
}

func TestIPCRoundTrip(t *testing.T) {
	p := testPlatform(t)
	l, err := p.IPC.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		c.Write([]byte("echo " + line))
	}()
	c, err := p.IPC.Dial(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("hi\n"))
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || got != "echo hi\n" {
		t.Fatalf("got %q err %v", got, err)
	}
}
