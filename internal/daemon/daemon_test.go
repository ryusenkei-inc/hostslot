package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ryusenkei-inc/loadslot/internal/gate"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/store"
)

type fakeMetrics struct{ mem platform.Memory }

func (f fakeMetrics) Memory() (platform.Memory, error) { return f.mem, nil }
func (f fakeMetrics) CPUs() int                        { return 8 }

func startServer(t *testing.T, slots int) (*platform.Platform, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	p := platform.Native(platform.Paths{Data: dir, Runtime: dir})
	st := store.Open(filepath.Join(dir, "samples.json"))
	srv := &Server{
		Metrics:    fakeMetrics{platform.Memory{TotalMB: 16384, FreeMB: 12000}},
		Alive:      func(int) bool { return true },
		Store:      st,
		LoadConfig: func() (gate.Config, error) { return gate.Config{Slots: slots, CostMB: 1000}.Normalize(), nil },
		Interval:   20 * time.Millisecond,
	}
	l, err := p.IPC.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { l.Close() })
	return p, st
}

func acquireAsync(t *testing.T, p *platform.Platform, req gate.Request) (<-chan *Lease, func()) {
	t.Helper()
	c, err := p.IPC.Dial(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan *Lease, 1)
	go func() {
		l, err := Acquire(c, req, time.Time{}, nil)
		if err == nil {
			ch <- l
		}
	}()
	return ch, func() { c.Close() }
}

func waitLease(t *testing.T, ch <-chan *Lease) *Lease {
	t.Helper()
	select {
	case l := <-ch:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("not admitted")
		return nil
	}
}

func expectBlocked(t *testing.T, ch <-chan *Lease) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("admitted while the slot was taken")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestReleaseAdmitsNextAndRecordsSample(t *testing.T) {
	p, st := startServer(t, 1)
	req := gate.Request{Command: "build", Dir: "/r", Weight: 1, PID: 1}
	first, _ := acquireAsync(t, p, req)
	l1 := waitLease(t, first)
	second, _ := acquireAsync(t, p, req)
	expectBlocked(t, second)

	status, err := QueryStatus(p)
	if err != nil || len(status.Snapshot.Running) != 1 || len(status.Snapshot.Waiting) != 1 {
		t.Fatalf("status %+v err %v", status, err)
	}

	l1.Release(&store.Sample{PeakMB: 512})
	l2 := waitLease(t, second)
	l2.Release(nil)
	deadline := time.Now().Add(2 * time.Second)
	for len(st.Peaks(req.Key())) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if peaks := st.Peaks(req.Key()); len(peaks) != 1 || peaks[0] != 512 {
		t.Fatalf("peaks %v", peaks)
	}
}

func TestCrashedClientReturnsSlot(t *testing.T) {
	p, _ := startServer(t, 1)
	req := gate.Request{Command: "test", Weight: 1}
	first, crash := acquireAsync(t, p, req)
	waitLease(t, first)
	second, _ := acquireAsync(t, p, req)
	expectBlocked(t, second)
	crash()
	waitLease(t, second).Release(nil)
}

func TestWaitingClientThatLeavesIsDropped(t *testing.T) {
	p, _ := startServer(t, 1)
	first, _ := acquireAsync(t, p, gate.Request{Command: "a", Weight: 1})
	l1 := waitLease(t, first)
	_, leave := acquireAsync(t, p, gate.Request{Command: "b", Weight: 1})
	time.Sleep(100 * time.Millisecond)
	leave()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s, err := QueryStatus(p)
		if err == nil && len(s.Snapshot.Waiting) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting job not dropped: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
	l1.Release(nil)
}

func TestAcquireGivesUpAtTheDeadlineAndLeavesTheQueue(t *testing.T) {
	p, _ := startServer(t, 1)
	req := gate.Request{Command: "build", Dir: "/r", Weight: 1, PID: 1}
	first, _ := acquireAsync(t, p, req)
	l1 := waitLease(t, first)

	c, err := p.IPC.Dial(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(c, req, time.Now().Add(300*time.Millisecond), nil); err != ErrBusy {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	c.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := QueryStatus(p)
		if err == nil && len(s.Snapshot.Waiting) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job that gave up is still queued")
		}
		time.Sleep(20 * time.Millisecond)
	}
	l1.Release(nil)
}

func TestUsageReportsReachTheScheduler(t *testing.T) {
	p, _ := startServer(t, 1)
	ch, _ := acquireAsync(t, p, gate.Request{Command: "build", Dir: "/r", Weight: 1, PID: 1})
	l := waitLease(t, ch)
	l.Report(700)
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := QueryStatus(p)
		if err == nil && len(s.Snapshot.Running) == 1 && s.Snapshot.Running[0].Reported && s.Snapshot.Running[0].CurrentMB == 700 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage not recorded: %+v %v", s, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	l.Release(nil)
}
