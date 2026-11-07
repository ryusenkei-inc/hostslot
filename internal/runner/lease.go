package runner

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
)

// lease is an admission from either the daemon or the lock fallback.
type lease struct {
	job     gate.Job
	slots   int
	cpus    int
	mode    string
	release func(*store.Sample)
	// report sends the job's current memory (nil in lock mode).
	report func(currentMB uint64)
	// stopped is closed when the daemon asks to stop the job (nil in lock
	// mode).
	stopped <-chan struct{}
}

const statusEvery = 5 * time.Second

// acquire returns daemon.ErrBusy when deadline (none when zero) passes
// first, in either mode.
func acquire(p *platform.Platform, cfg gate.Config, req gate.Request, deadline time.Time, noDaemon bool, stderr io.Writer) (*lease, error) {
	if !noDaemon {
		if daemon.RetireOlder(p, daemon.Version()) {
			fmt.Fprintln(stderr, "hostslot: retired an older daemon; it exits after its current jobs")
		}
		c, err := daemon.Connect(p)
		if err == nil {
			var last time.Time
			l, err := daemon.Acquire(c, req, deadline, func(m daemon.Message) {
				if time.Since(last) < statusEvery || m.Status == nil {
					return
				}
				last = time.Now()
				s := m.Status
				fmt.Fprintf(stderr, "hostslot: waiting (position %d of %d, %s; slots %d/%d used, %d MB free, needs ~%d MB)\n",
					m.Position, len(s.Snapshot.Waiting), waitReason(s, m.Position), s.Snapshot.Used, s.Snapshot.Slots, s.Memory.FreeMB, m.Job.EstimateMB)
			})
			if err == nil {
				return &lease{job: l.Job, slots: l.Status.Snapshot.Slots, cpus: l.Status.CPUs, mode: "daemon", release: l.Release, report: l.Report, stopped: l.Stopped()}, nil
			}
			c.Close()
			if errors.Is(err, daemon.ErrBusy) {
				return nil, err
			}
			fmt.Fprintf(stderr, "hostslot: daemon error, using lock fallback: %v\n", err)
		} else {
			fmt.Fprintf(stderr, "hostslot: no daemon (%v), using lock fallback\n", err)
		}
	}
	return acquireLocks(p, cfg, req, deadline, stderr)
}

// acquireLocks emulates the slots with one named lock per slot. The OS
// releases locks of a crashed process, so a crash returns its slots. There is
// no priority or queue order in this mode.
func acquireLocks(p *platform.Platform, cfg gate.Config, req gate.Request, deadline time.Time, stderr io.Writer) (*lease, error) {
	if err := p.Paths.Ensure(); err != nil {
		return nil, err
	}
	mem, err := p.Metrics.Memory()
	if err != nil {
		return nil, err
	}
	cpus := p.Metrics.CPUs()
	cfg = cfg.ForMachine(mem.TotalMB)
	slots := gate.Slots(mem.TotalMB, cpus, cfg)
	st := store.Open(filepath.Join(p.Paths.Data, store.FileName))
	prior, _ := st.Prior(req.Key())
	est, weight := gate.Estimate(st.Peaks(req.Key()), prior, req.Weight, slots, cfg)
	job := gate.Job{Request: req, EstimateMB: est, Slots: weight, Started: time.Now()}

	var last time.Time
	for {
		held, others, err := tryLocks(p.Locks, slots, weight)
		if err != nil {
			return nil, err
		}
		if held != nil {
			if !others {
				return lockLease(p, st, job, slots, cpus, held), nil
			}
			if m, err := p.Metrics.Memory(); err != nil || m.FreeMB >= est {
				return lockLease(p, st, job, slots, cpus, held), nil
			}
			releaseAll(held)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, daemon.ErrBusy
		}
		if time.Since(last) >= statusEvery {
			last = time.Now()
			m, _ := p.Metrics.Memory()
			fmt.Fprintf(stderr, "hostslot: waiting (lock fallback, %d slots, %d MB free, needs ~%d MB)\n", slots, m.FreeMB, est)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// tryLocks takes weight free slot locks, or none. others reports whether
// any slot is held by another process.
func tryLocks(locks platform.Locker, slots, weight int) (held []func(), others bool, err error) {
	for i := 0; i < slots; i++ {
		unlock, ok, err := locks.TryLock("slot-" + strconv.Itoa(i))
		if err != nil {
			releaseAll(held)
			return nil, false, err
		}
		if !ok {
			others = true
			continue
		}
		if len(held) < weight {
			held = append(held, unlock)
		} else {
			unlock()
		}
	}
	if len(held) < weight {
		releaseAll(held)
		return nil, others, nil
	}
	return held, others, nil
}

func releaseAll(held []func()) {
	for _, u := range held {
		u()
	}
}

func lockLease(p *platform.Platform, st *store.Store, job gate.Job, slots, cpus int, held []func()) *lease {
	return &lease{job: job, slots: slots, cpus: cpus, mode: "lock", release: func(s *store.Sample) {
		if s != nil && s.Learnable() {
			recordWithLock(p.Locks, st, job.Key(), *s)
		}
		releaseAll(held)
	}}
}

// waitReason says why the job at position (1-based) waits, from the
// daemon's status.
func waitReason(s *daemon.Status, position int) string {
	var reason string
	if position >= 1 && position <= len(s.Snapshot.Waiting) {
		reason = s.Snapshot.Waiting[position-1].Reason
	}
	switch reason {
	case "slots":
		return "all slots busy"
	case "cpu":
		if s.CPUBusyPct != nil {
			return fmt.Sprintf("CPUs %d%% busy", *s.CPUBusyPct)
		}
		return "CPUs busy"
	case "queue":
		return "behind other jobs"
	case "memory":
		return "not enough free memory"
	}
	return "waiting for room"
}

// recordWithLock serializes writers of samples.json across processes.
func recordWithLock(locks platform.Locker, st *store.Store, key string, s store.Sample) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		unlock, ok, err := locks.TryLock("store")
		if err != nil {
			return
		}
		if ok {
			_ = st.Record(key, s)
			unlock()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
