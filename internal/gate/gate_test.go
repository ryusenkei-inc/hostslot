package gate

import (
	"testing"
	"time"
)

type fakeEstimator map[string][]uint64

func (f fakeEstimator) Peaks(key string) []uint64   { return f[key] }
func (f fakeEstimator) Prior(string) (uint64, bool) { return 0, false }

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func cfg() Config {
	return Config{CostMB: 1000, ReserveMB: 1000, MemoryFraction: 0.5, MinSamples: 3, Warmup: 10 * time.Second}
}

func TestSlots(t *testing.T) {
	cases := []struct {
		name  string
		total uint64
		cpus  int
		c     Config
		want  int
	}{
		{"16GB defaults", 16384, 16, Config{}, 4},
		{"capped by cpus", 65536, 2, Config{}, 2},
		{"at least one", 2048, 8, Config{}, 1},
		{"override", 16384, 16, Config{Slots: 7}, 7},
	}
	for _, tc := range cases {
		if got := Slots(tc.total, tc.cpus, tc.c); got != tc.want {
			t.Errorf("%s: Slots = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEstimateUsesWeightUntilEnoughSamples(t *testing.T) {
	c := cfg()
	if est, w := Estimate(nil, 0, 2, 8, c); est != 2000 || w != 2 {
		t.Fatalf("no samples: got %d MB weight %d", est, w)
	}
	// A large early peak raises the estimate above the hint.
	if est, w := Estimate([]uint64{3500}, 0, 1, 8, c); est != 3500 || w != 4 {
		t.Fatalf("one large sample: got %d MB weight %d", est, w)
	}
	// With enough samples, a small learned peak replaces a pessimistic hint.
	if est, w := Estimate([]uint64{300, 400, 350}, 0, 3, 8, c); est != 400 || w != 1 {
		t.Fatalf("learned: got %d MB weight %d", est, w)
	}
	// Effective weight never exceeds the slot count.
	if _, w := Estimate([]uint64{9000, 9000, 9000}, 0, 1, 4, c); w != 4 {
		t.Fatalf("clamp: weight %d", w)
	}
}

func TestAdmitRespectsSlots(t *testing.T) {
	s := New(cfg(), 2, nil)
	for range 3 {
		s.Enqueue(Request{Command: "x", Weight: 1}, t0)
	}
	got := s.Admit(t0, 100000)
	if len(got) != 2 {
		t.Fatalf("admitted %d, want 2", len(got))
	}
	if snap := s.Snapshot(); snap.Used != 2 || len(snap.Waiting) != 1 {
		t.Fatalf("snapshot %+v", snap)
	}
	s.Remove(got[0].ID)
	if got := s.Admit(t0, 100000); len(got) != 1 {
		t.Fatalf("after release admitted %d, want 1", len(got))
	}
}

func TestAdmitChecksFreeMemory(t *testing.T) {
	s := New(cfg(), 4, nil)
	s.Enqueue(Request{Command: "a", Weight: 1}, t0)
	s.Enqueue(Request{Command: "b", Weight: 1}, t0)
	// The first job is admitted even with little free memory: nothing runs.
	if got := s.Admit(t0, 500); len(got) != 1 {
		t.Fatalf("first admit %d, want 1", len(got))
	}
	// The second must wait: 1500 free minus 1000 warming leaves 500 < 1000.
	if got := s.Admit(t0.Add(time.Second), 1500); len(got) != 0 {
		t.Fatalf("admitted during warmup with low memory")
	}
	// After warmup, the reading already includes the first job.
	if got := s.Admit(t0.Add(time.Minute), 1500); len(got) != 1 {
		t.Fatalf("not admitted after warmup")
	}
}

func TestPriorityAndFIFO(t *testing.T) {
	s := New(cfg(), 1, nil)
	b1 := s.Enqueue(Request{Command: "b1", Weight: 1, Priority: Background}, t0)
	b2 := s.Enqueue(Request{Command: "b2", Weight: 1, Priority: Background}, t0)
	i1 := s.Enqueue(Request{Command: "i1", Weight: 1, Priority: Interactive}, t0)
	order := []uint64{i1.ID, b1.ID, b2.ID}
	for _, want := range order {
		got := s.Admit(t0, 100000)
		if len(got) != 1 || got[0].ID != want {
			t.Fatalf("admitted %+v, want job %d", got, want)
		}
		s.Remove(want)
	}
}

func TestHeadOfLineBlocksLighterJobs(t *testing.T) {
	s := New(cfg(), 3, nil)
	s.Enqueue(Request{Command: "small", Weight: 1}, t0)
	s.Admit(t0, 100000)
	s.Enqueue(Request{Command: "big", Weight: 3}, t0)
	s.Enqueue(Request{Command: "small2", Weight: 1}, t0)
	if got := s.Admit(t0, 100000); len(got) != 0 {
		t.Fatalf("lighter job overtook the heavy head: %+v", got)
	}
}

func TestCrashReturnsSlot(t *testing.T) {
	s := New(cfg(), 1, nil)
	j := s.Enqueue(Request{Command: "x", Weight: 1, PID: 42}, t0)
	s.Enqueue(Request{Command: "y", Weight: 1, PID: 43}, t0)
	s.Admit(t0, 100000)
	dead := s.Reap(func(pid int) bool { return pid != 42 })
	if len(dead) != 1 || dead[0].ID != j.ID {
		t.Fatalf("reaped %+v", dead)
	}
	if got := s.Admit(t0, 100000); len(got) != 1 {
		t.Fatalf("slot not returned")
	}
}

func TestAdmitHoldsHeadroomOfGrowingLearnedJobs(t *testing.T) {
	learned := Request{Command: "tsc -b", Dir: "/repo", Weight: 1}
	est := fakeEstimator{learned.Key(): {2000, 2400, 2200}}
	s := New(cfg(), 4, est)
	a := s.Enqueue(learned, t0)
	if got := s.Admit(t0, 100000); len(got) != 1 || got[0] != a {
		t.Fatalf("first admit %v", got)
	}
	s.Enqueue(Request{Command: "b", Weight: 1}, t0)
	// Past the warmup, the learned job reports 400 MB of its 2400 MB peak:
	// 2000 MB may still come, so 2500 free leaves too little for 1000.
	s.Report(a.ID, 400)
	if got := s.Admit(t0.Add(time.Minute), 2500); len(got) != 0 {
		t.Fatalf("admitted next to a job that is still growing")
	}
	// Once it has grown, its memory is in the reading.
	s.Report(a.ID, 2300)
	if got := s.Admit(t0.Add(2*time.Minute), 1200); len(got) != 1 {
		t.Fatalf("not admitted after the job reached its peak")
	}
}

func TestReportIgnoresUnknownJobs(t *testing.T) {
	s := New(cfg(), 1, nil)
	s.Report(42, 100)
	if snap := s.Snapshot(); len(snap.Running) != 0 {
		t.Fatalf("report created a job: %+v", snap)
	}
}

func TestLearningChangesWeight(t *testing.T) {
	r := Request{Command: "pnpm test", Dir: "/repo", Weight: 2}
	est := fakeEstimator{r.Key(): {200, 300, 250}}
	s := New(cfg(), 2, est)
	a := s.Enqueue(r, t0)
	b := s.Enqueue(r, t0)
	if a.Slots != 1 || a.EstimateMB != 300 {
		t.Fatalf("learned job: slots %d est %d", a.Slots, a.EstimateMB)
	}
	// Both fit at once because the learned weight is 1, not the hint of 2.
	if got := s.Admit(t0, 100000); len(got) != 2 {
		t.Fatalf("admitted %d, want 2 (%+v %+v)", len(got), a, b)
	}
}

func TestRemoveWaiting(t *testing.T) {
	s := New(cfg(), 1, nil)
	s.Enqueue(Request{Command: "a", Weight: 1}, t0)
	b := s.Enqueue(Request{Command: "b", Weight: 1}, t0)
	if s.Position(b.ID) != 2 || !s.Remove(b.ID) || s.Position(b.ID) != 0 {
		t.Fatalf("remove waiting failed")
	}
}

func TestWorkers(t *testing.T) {
	if w := Workers(16, 1, 4); w != 4 {
		t.Fatalf("workers %d", w)
	}
	if w := Workers(2, 1, 4); w != 1 {
		t.Fatalf("workers floor %d", w)
	}
}

func TestCeiling(t *testing.T) {
	if got := Ceiling(16000, Config{}); got != 12000 {
		t.Fatalf("default: %d", got)
	}
	if got := Ceiling(16000, Config{JobCeilingFraction: 0.5}); got != 8000 {
		t.Fatalf("half: %d", got)
	}
	if got := Ceiling(16000, Config{JobCeilingFraction: 1}); got != 0 {
		t.Fatalf("off: %d", got)
	}
}

func TestRoom(t *testing.T) {
	s := New(cfg(), 4, nil)
	if got := s.Room(t0, 100000); got != 4 {
		t.Fatalf("idle: %d", got)
	}
	s.Enqueue(Request{Command: "a", Weight: 1}, t0)
	s.Admit(t0, 100000)
	// One job warming up holds its 1000 MB estimate: 2500 free leaves 1 more.
	if got := s.Room(t0.Add(time.Second), 2500); got != 1 {
		t.Fatalf("by memory: %d", got)
	}
	if got := s.Room(t0.Add(time.Second), 100000); got != 3 {
		t.Fatalf("by slots: %d", got)
	}
	s.Enqueue(Request{Command: "b", Weight: 9}, t0)
	if got := s.Room(t0, 100000); got != 0 {
		t.Fatalf("with a job waiting: %d", got)
	}
}

func TestEstimateUsesThePriorUntilLearned(t *testing.T) {
	c := cfg()
	// A prior from similar work replaces the default cost of a weight-1 job.
	if est, w := Estimate(nil, 3500, 1, 8, c); est != 3500 || w != 4 {
		t.Fatalf("prior: %d %d", est, w)
	}
	if est, _ := Estimate(nil, 400, 1, 8, c); est != 400 {
		t.Fatalf("light prior: %d", est)
	}
	// An explicit weight above 1 still sets a floor.
	if est, _ := Estimate(nil, 400, 3, 8, c); est != 3000 {
		t.Fatalf("explicit weight: %d", est)
	}
	// A peak seen before enough samples raises it.
	if est, _ := Estimate([]uint64{5000}, 400, 1, 8, c); est != 5000 {
		t.Fatalf("early peak: %d", est)
	}
}

func TestEstimateUsesAHighPercentileOnceLearned(t *testing.T) {
	c := cfg()
	// One cold outlier among warm runs: the second highest of ten decides.
	peaks := []uint64{2400, 2400, 2500, 2400, 9000, 2400, 2450, 2400, 2500, 2400}
	if est, _ := Estimate(peaks, 0, 1, 8, c); est != 2500 {
		t.Fatalf("got %d, want 2500", est)
	}
	// With few samples the high percentile is the maximum.
	if est, _ := Estimate([]uint64{100, 9000, 200}, 0, 1, 8, c); est != 9000 {
		t.Fatalf("few samples: %d", est)
	}
}

func TestLargestPicksTheBiggestBackgroundJob(t *testing.T) {
	s := New(cfg(), 4, nil)
	small := s.Enqueue(Request{Command: "small", Weight: 1, Priority: Background}, t0)
	big := s.Enqueue(Request{Command: "big", Weight: 1, Priority: Background}, t0)
	person := s.Enqueue(Request{Command: "person", Weight: 1, Priority: Interactive}, t0)
	s.Admit(t0, 100000)
	if _, _, ok := s.Largest(); ok {
		t.Fatal("chose a job that never reported")
	}
	s.Report(small.ID, 300)
	s.Report(big.ID, 900)
	s.Report(person.ID, 5000)
	if id, mb, ok := s.Largest(); !ok || id != big.ID || mb != 900 {
		t.Fatalf("got %d %d %v, want the big background job", id, mb, ok)
	}
}

type durationEstimator struct{ fakeEstimator }

func (durationEstimator) Durations(string) []int64 { return []int64{9000, 1000, 3000} }

func TestSnapshotAtExplainsHeldMemoryAndWaits(t *testing.T) {
	s := New(cfg(), 2, durationEstimator{fakeEstimator{}})
	a := s.Enqueue(Request{Command: "a", Weight: 1}, t0)
	if a.ExpectedMS != 3000 {
		t.Fatalf("expected duration: %d", a.ExpectedMS)
	}
	s.Admit(t0, 100000)
	s.Enqueue(Request{Command: "big", Weight: 2}, t0)
	s.Enqueue(Request{Command: "c", Weight: 1}, t0)

	snap := s.SnapshotAt(t0.Add(time.Second), 5000)
	if snap.Running[0].HeldMB != 1000 || snap.HeldMB != 1000 {
		t.Fatalf("held during warmup: %+v", snap)
	}
	if snap.Waiting[0].Reason != "slots" || snap.Waiting[1].Reason != "queue" {
		t.Fatalf("reasons: %q %q", snap.Waiting[0].Reason, snap.Waiting[1].Reason)
	}
	s.Remove(snap.Waiting[0].ID)
	if got := s.SnapshotAt(t0.Add(time.Second), 1500).Waiting[0].Reason; got != "memory" {
		t.Fatalf("memory reason: %q", got)
	}
}
