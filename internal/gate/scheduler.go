package gate

import (
	"slices"
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
	// Source names who started the job, such as an agent ("claude").
	Source string `json:"source,omitempty"`
	// Context carries environment variables that tie the job to a place in
	// another tool, such as an agent IDE's pane, for integrations to show.
	Context map[string]string `json:"context,omitempty"`
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
	// CurrentMB is the memory the running job reported last; Reported is
	// false until it reports (clients before this field never do).
	CurrentMB uint64 `json:"currentMB,omitempty"`
	Reported  bool   `json:"reported,omitempty"`
	// ExpectedMS is the median duration of the job's recent runs (0 when
	// unknown), for estimates of when it ends.
	ExpectedMS int64 `json:"expectedMs,omitempty"`
	// HeldMB is the memory admission holds back for a running job beyond the
	// free-memory reading (see Scheduler.reserve); set by SnapshotAt.
	HeldMB uint64 `json:"heldMB,omitempty"`
	// Reason says why a waiting job waits: "slots" (not enough free slots),
	// "memory" (not enough free memory), or "queue" (behind another waiting
	// job); set by SnapshotAt.
	Reason string `json:"reason,omitempty"`
}

// DurationEstimator is an Estimator that also knows how long a key's recent
// runs took.
type DurationEstimator interface {
	Durations(key string) []int64
}

// Estimator returns the recent peaks recorded for a key, and a prior from
// similar work for a key without enough of them.
type Estimator interface {
	Peaks(key string) []uint64
	Prior(key string) (uint64, bool)
}

// Snapshot is a copy of the scheduler state for status output.
type Snapshot struct {
	Slots int `json:"slots"`
	Used  int `json:"used"`
	// HeldMB is the memory held back for running jobs (see Job.HeldMB).
	HeldMB  uint64 `json:"heldMB"`
	Running []Job  `json:"running"`
	Waiting []Job  `json:"waiting"`
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
	var prior uint64
	if s.est != nil {
		peaks = s.est.Peaks(r.Key())
		prior, _ = s.est.Prior(r.Key())
	}
	est, w := Estimate(peaks, prior, r.Weight, s.slots, s.cfg)
	j := &Job{Request: r, ID: s.seq, EstimateMB: est, Slots: w, Samples: len(peaks), Queued: now}
	if d, ok := s.est.(DurationEstimator); ok {
		j.ExpectedMS = median(d.Durations(r.Key()))
	}
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

// Report records the current memory of a running job.
func (s *Scheduler) Report(id uint64, currentMB uint64) {
	if j, ok := s.running[id]; ok {
		j.CurrentMB = currentMB
		j.Reported = true
	}
}

// Admit moves waiting jobs into running while slots and free memory allow.
// The queue head blocks later jobs, so a heavy job is never starved by a
// stream of lighter ones. When nothing runs, the head is always admitted so
// a job larger than the machine still makes progress.
//
// The free memory reading already contains what running jobs use now, so
// only what they may still grow to is held back: a learned job that reports
// its memory holds its remaining headroom (estimate - current), since a type
// check or test run can keep growing for a minute; other jobs hold their
// estimate during the warmup only, as their estimate is just the weight
// hint and would hold back too much for their whole run.
func (s *Scheduler) Admit(now time.Time, freeMB uint64) []*Job {
	used := s.used()
	var reserved uint64
	for _, j := range s.running {
		reserved += s.reserve(j, now)
	}
	var admitted []*Job
	for len(s.waiting) > 0 {
		j := s.waiting[0]
		if len(s.running) > 0 {
			if used+j.Slots > s.slots {
				break
			}
			// ReserveMB stays free for the editor, browsers and the agents
			// themselves. Admitting up to the last megabyte would only leave
			// the machine guard to stop the job again.
			if freeMB < reserved+j.EstimateMB+s.cfg.ReserveMB {
				break
			}
		}
		s.waiting = s.waiting[1:]
		j.Started = now
		s.running[j.ID] = j
		used += j.Slots
		reserved += j.EstimateMB
		admitted = append(admitted, j)
	}
	return admitted
}

// Room is how many more weight-1 jobs would be admitted now: the free slots,
// and the free memory left after what running jobs may still grow to and
// the reserve, in units of the default job cost. Waiting jobs come first, so there is no room
// while any wait.
func (s *Scheduler) Room(now time.Time, freeMB uint64) int {
	if len(s.waiting) > 0 {
		return 0
	}
	slots := s.slots - s.used()
	if len(s.running) == 0 {
		return max(slots, 0)
	}
	var reserved uint64
	for _, j := range s.running {
		reserved += s.reserve(j, now)
	}
	if freeMB <= reserved+s.cfg.ReserveMB {
		return 0
	}
	byMemory := int((freeMB - reserved - s.cfg.ReserveMB) / s.cfg.CostMB)
	return max(min(slots, byMemory), 0)
}

// Largest returns the running background job that uses the most memory
// now, by its reports. Interactive jobs are never chosen: a person is
// waiting on them.
func (s *Scheduler) Largest() (id uint64, currentMB uint64, ok bool) {
	for _, j := range s.running {
		if j.Priority == Background && j.Reported && j.CurrentMB > currentMB {
			id, currentMB, ok = j.ID, j.CurrentMB, true
		}
	}
	return id, currentMB, ok
}

// reserve is the memory a running job may still take beyond the free
// memory reading.
func (s *Scheduler) reserve(j *Job, now time.Time) uint64 {
	if j.Reported && j.Samples >= s.cfg.MinSamples {
		if j.CurrentMB >= j.EstimateMB {
			return 0
		}
		return j.EstimateMB - j.CurrentMB
	}
	if now.Sub(j.Started) < s.cfg.Warmup {
		return j.EstimateMB
	}
	return 0
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

// SnapshotAt is Snapshot with what admission sees at now with freeMB: the
// memory held back for each running job, and why each waiting job waits.
func (s *Scheduler) SnapshotAt(now time.Time, freeMB uint64) Snapshot {
	snap := s.Snapshot()
	for i := range snap.Running {
		snap.Running[i].HeldMB = s.reserve(s.running[snap.Running[i].ID], now)
		snap.HeldMB += snap.Running[i].HeldMB
	}
	for i := range snap.Waiting {
		j := &snap.Waiting[i]
		switch {
		case i > 0:
			j.Reason = "queue"
		case snap.Used+j.Slots > snap.Slots:
			j.Reason = "slots"
		default:
			j.Reason = "memory"
		}
	}
	return snap
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func (s *Scheduler) used() int {
	n := 0
	for _, j := range s.running {
		n += j.Slots
	}
	return n
}
