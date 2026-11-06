package gate

import (
	"testing"
	"time"
)

type fakeEstimator map[string][]uint64

func (f fakeEstimator) Peaks(key string) []uint64 { return f[key] }

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
	if est, w := Estimate(nil, 2, 8, c); est != 2000 || w != 2 {
		t.Fatalf("no samples: got %d MB weight %d", est, w)
	}
	// A large early peak raises the estimate above the hint.
	if est, w := Estimate([]uint64{3500}, 1, 8, c); est != 3500 || w != 4 {
		t.Fatalf("one large sample: got %d MB weight %d", est, w)
	}
	// With enough samples, a small learned peak replaces a pessimistic hint.
	if est, w := Estimate([]uint64{300, 400, 350}, 3, 8, c); est != 400 || w != 1 {
		t.Fatalf("learned: got %d MB weight %d", est, w)
	}
	// Effective weight never exceeds the slot count.
	if _, w := Estimate([]uint64{9000, 9000, 9000}, 1, 4, c); w != 4 {
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
