package daemon

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
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

// dropMetrics reports plenty of free memory until low is set.
type dropMetrics struct{ low atomic.Bool }

func (d *dropMetrics) Memory() (platform.Memory, error) {
	if d.low.Load() {
		return platform.Memory{TotalMB: 16384, FreeMB: 300}, nil
	}
	return platform.Memory{TotalMB: 16384, FreeMB: 12000}, nil
}
func (d *dropMetrics) CPUs() int { return 8 }

func TestNearlyOutOfMemoryStopsTheLargestBackgroundJob(t *testing.T) {
	dir := t.TempDir()
	p := platform.Native(platform.Paths{Data: dir, Runtime: dir})
	m := &dropMetrics{}
	srv := &Server{
		Metrics: m,
		Alive:   func(int) bool { return true },
		Store:   store.Open(filepath.Join(dir, "samples.json")),
		LoadConfig: func() (gate.Config, error) {
			return gate.Config{Slots: 2, CostMB: 1000, ReserveMB: 1000}.Normalize(), nil
		},
		Interval: 20 * time.Millisecond,
	}
	l, err := p.IPC.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { l.Close() })

	bg := gate.Request{Command: "build", Dir: "/r", Weight: 1, Priority: gate.Background, PID: 1}
	ch1, _ := acquireAsync(t, p, bg)
	small := waitLease(t, ch1)
	ch2, _ := acquireAsync(t, p, bg)
	big := waitLease(t, ch2)
	small.Report(200)
	big.Report(800)
	time.Sleep(100 * time.Millisecond)

	m.low.Store(true)
	select {
	case <-big.Stopped():
	case <-time.After(5 * time.Second):
		t.Fatal("the largest background job was not stopped")
	}
	select {
	case <-small.Stopped():
		t.Fatal("a second job was stopped within the cooldown")
	case <-time.After(300 * time.Millisecond):
	}
	big.Release(nil)
	small.Release(nil)
}

func TestNearlyOutOfMemoryLeavesSmallJobsAlone(t *testing.T) {
	dir := t.TempDir()
	p := platform.Native(platform.Paths{Data: dir, Runtime: dir})
	m := &dropMetrics{}
	srv := &Server{
		Metrics: m,
		Alive:   func(int) bool { return true },
		Store:   store.Open(filepath.Join(dir, "samples.json")),
		LoadConfig: func() (gate.Config, error) {
			return gate.Config{Slots: 2, CostMB: 100, ReserveMB: 1000}.Normalize(), nil
		},
		Interval: 20 * time.Millisecond,
	}
	l, err := p.IPC.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { l.Close() })

	ch, _ := acquireAsync(t, p, gate.Request{Command: "lint", Dir: "/r", Weight: 1, Priority: gate.Background, PID: 1})
	small := waitLease(t, ch)
	small.Report(6)
	time.Sleep(100 * time.Millisecond)

	m.low.Store(true)
	select {
	case <-small.Stopped():
		t.Fatal("a 6 MB job was stopped; it would free nothing")
	case <-time.After(500 * time.Millisecond):
	}
	small.Release(nil)
}

func TestStatusListsRecentJobsWithTheirOutcome(t *testing.T) {
	p, st := startServer(t, 1)
	req := gate.Request{Command: "build", Dir: "/r", Weight: 1, PID: 1}
	ch, _ := acquireAsync(t, p, req)
	l := waitLease(t, ch)

	// A second job gives up waiting.
	c, err := p.IPC.Dial(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(c, gate.Request{Command: "test", Dir: "/r", Weight: 1, PID: 2}, time.Now().Add(200*time.Millisecond), nil); err != ErrBusy {
		t.Fatalf("got %v", err)
	}
	c.Close()
	// The daemon notices the closed connection on its own schedule; wait for
	// that record so the two land in a known order.
	for deadline := time.Now().Add(5 * time.Second); ; {
		if s, err := QueryStatus(p); err == nil && len(s.Recent) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job that gave up was not recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
	l.Release(&store.Sample{PeakMB: 300, DurationMS: 1500, Outcome: "timeout"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := QueryStatus(p)
		if err == nil && len(s.Recent) == 2 {
			if s.Recent[0].Outcome != "timeout" || s.Recent[0].PeakMB != 300 || s.Recent[1].Outcome != "gave up" {
				t.Fatalf("recent: %+v", s.Recent)
			}
			if s.ReserveMB == 0 {
				t.Fatalf("reserve missing: %+v", s)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recent not listed: %+v %v", s, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if peaks := st.Peaks(req.Key()); len(peaks) != 0 {
		t.Fatalf("a timed-out run was learned: %v", peaks)
	}
}
