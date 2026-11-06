package gate

import (
	"sort"
	"time"
)

// Priority orders waiting jobs. Higher values are admitted first.
type Priority int

const (
	// Background is for jobs started by agents or scripts.
	Background Priority = iota
	// Interactive is for jobs a person is waiting on in a terminal.
	Interactive
)

// String returns the name used on the command line and in status output.
func (p Priority) String() string {
	if p == Interactive {
		return "interactive"
	}
	return "background"
}

// ParsePriority parses "interactive" or "background".
func ParsePriority(s string) (Priority, bool) {
	switch s {
	case "interactive":
		return Interactive, true
	case "background":
		return Background, true
	}
	return Background, false
}

// Request describes a job that asks for admission.
type Request struct {
	Command  string   `json:"command"`
	Dir      string   `json:"dir"`
	Weight   int      `json:"weight"`
	Priority Priority `json:"priority"`
	PID      int      `json:"pid"`
	// LearnKey groups the samples of runs that cost the same (see package
	// jobkey). Empty means the command in the directory.
	LearnKey string `json:"key,omitempty"`
}

// Key identifies a job for learning.
func (r Request) Key() string {
	if r.LearnKey != "" {
		return r.LearnKey
	}
	return r.Dir + "\n" + r.Command
}

// Job is a request known to the scheduler.
type Job struct {
	Request
	ID         uint64    `json:"id"`
	EstimateMB uint64    `json:"estimateMB"`
	Slots      int       `json:"slots"`
	Samples    int       `json:"samples"`
	Queued     time.Time `json:"queued"`
	Started    time.Time `json:"started,omitzero"`
}

// Estimator returns the recent peaks recorded for a key.
type Estimator interface {
	Peaks(key string) []uint64
}

// Snapshot is a copy of the scheduler state for status output.
type Snapshot struct {
	Slots   int   `json:"slots"`
	Used    int   `json:"used"`
	Running []Job `json:"running"`
	Waiting []Job `json:"waiting"`
}

// Scheduler admits jobs into a fixed number of slots. It is not safe for
// concurrent use; the daemon serializes access.
type Scheduler struct {
	cfg     Config
	slots   int
	est     Estimator
	seq     uint64
	waiting []*Job
	running map[uint64]*Job
}

// New returns a scheduler with the given slot count.
func New(cfg Config, slots int, est Estimator) *Scheduler {
	return &Scheduler{cfg: cfg.Normalize(), slots: max(slots, 1), est: est, running: map[uint64]*Job{}}
}

// SetSlots changes the slot count, e.g. after a config reload.
func (s *Scheduler) SetSlots(n int) { s.slots = max(n, 1) }

// Enqueue adds a request to the waiting queue.
func (s *Scheduler) Enqueue(r Request, now time.Time) *Job {
	s.seq++
	var peaks []uint64
	if s.est != nil {
		peaks = s.est.Peaks(r.Key())
	}
	est, w := Estimate(peaks, r.Weight, s.slots, s.cfg)
	j := &Job{Request: r, ID: s.seq, EstimateMB: est, Slots: w, Samples: len(peaks), Queued: now}
	s.waiting = append(s.waiting, j)
	// Stable sort keeps FIFO order within a priority.
	sort.SliceStable(s.waiting, func(a, b int) bool { return s.waiting[a].Priority > s.waiting[b].Priority })
	return j
}

// Remove drops a job whether it is waiting or running, returning its slots.
// It reports whether the job was known.
func (s *Scheduler) Remove(id uint64) bool {
	if _, ok := s.running[id]; ok {
		delete(s.running, id)
		return true
	}
	for i, j := range s.waiting {
		if j.ID == id {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			return true
		}
	}
	return false
}

// Admit moves waiting jobs into running while slots and free memory allow.
// The queue head blocks later jobs, so a heavy job is never starved by a
// stream of lighter ones. When nothing runs, the head is always admitted so
// a job larger than the machine still makes progress.
func (s *Scheduler) Admit(now time.Time, freeMB uint64) []*Job {
	used := s.used()
	var warming uint64
	for _, j := range s.running {
		if now.Sub(j.Started) < s.cfg.Warmup {
			warming += j.EstimateMB
		}
	}
	var admitted []*Job
	for len(s.waiting) > 0 {
		j := s.waiting[0]
		if len(s.running) > 0 {
			if used+j.Slots > s.slots {
				break
			}
			if freeMB < warming+j.EstimateMB {
				break
			}
		}
		s.waiting = s.waiting[1:]
		j.Started = now
		s.running[j.ID] = j
		used += j.Slots
		warming += j.EstimateMB
		admitted = append(admitted, j)
	}
	return admitted
}

// Reap removes running jobs whose process no longer exists and returns them.
// It is the safety net behind connection loss: a crashed job always returns
// its slot.
func (s *Scheduler) Reap(alive func(pid int) bool) []*Job {
	var dead []*Job
	for id, j := range s.running {
		if j.PID > 0 && !alive(j.PID) {
			delete(s.running, id)
			dead = append(dead, j)
		}
	}
	return dead
}

// Position returns the 1-based queue position of a waiting job, or 0.
func (s *Scheduler) Position(id uint64) int {
	for i, j := range s.waiting {
		if j.ID == id {
			return i + 1
		}
	}
	return 0
}

// Snapshot copies the current state.
func (s *Scheduler) Snapshot() Snapshot {
	snap := Snapshot{Slots: s.slots, Used: s.used(), Running: []Job{}, Waiting: []Job{}}
	for _, j := range s.running {
		snap.Running = append(snap.Running, *j)
	}
	sort.Slice(snap.Running, func(a, b int) bool { return snap.Running[a].ID < snap.Running[b].ID })
	for _, j := range s.waiting {
		snap.Waiting = append(snap.Waiting, *j)
	}
	return snap
}

func (s *Scheduler) used() int {
	n := 0
	for _, j := range s.running {
		n += j.Slots
	}
	return n
}
