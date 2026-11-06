package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ryusenkei-inc/loadslot/internal/gate"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/store"
)

// ErrRunning is returned by Run when another daemon holds the daemon lock.
var ErrRunning = errors.New("daemon already running")

// DaemonLock is the lock name that keeps a single daemon per home.
const DaemonLock = "daemon"

// Server owns the scheduler and serves clients.
type Server struct {
	Metrics platform.Metrics
	Tracker platform.Capabilities
	Alive   func(pid int) bool
	Store   *store.Store
	// LoadConfig is called on every cycle so config file edits apply live.
	LoadConfig func() (gate.Config, error)
	// Interval between admission cycles and wait updates to clients.
	Interval time.Duration
	// IdleTimeout ends Serve after this long without jobs or clients.
	IdleTimeout time.Duration
	// Version is reported in Status so newer clients can retire this daemon.
	Version string

	// retire stops accepting clients and frees the daemon lock (set by Run).
	retire   func()
	retiring bool

	mu         sync.Mutex
	sched      *gate.Scheduler
	grants     map[uint64]chan struct{}
	clients    int
	lastActive time.Time
	memory     platform.Memory
}

// Run takes the daemon lock, listens and serves until idle.
func Run(p *platform.Platform, srv *Server) error {
	if err := p.Paths.Ensure(); err != nil {
		return err
	}
	unlock, ok, err := p.Locks.TryLock(DaemonLock)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRunning
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	l, err := p.IPC.Listen()
	if err != nil {
		return err
	}
	srv.retire = func() {
		l.Close()
		release()
	}
	return srv.Serve(l)
}

// Serve accepts clients on l until IdleTimeout passes without activity or l
// is closed. It closes l before returning.
func (s *Server) Serve(l net.Listener) error {
	defer l.Close()
	if s.Interval <= 0 {
		s.Interval = time.Second
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	s.memory, err = s.Metrics.Memory()
	if err != nil {
		return err
	}
	s.sched = gate.New(cfg, gate.Slots(s.memory.TotalMB, s.Metrics.CPUs(), cfg), s.Store)
	s.grants = map[uint64]chan struct{}{}
	s.lastActive = time.Now()

	stop := make(chan struct{})
	defer close(stop)
	go s.loop(l, stop)
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.awaitClients()
				return nil
			}
			return err
		}
		go s.handle(c)
	}
}

// awaitClients lets the jobs of a retired daemon finish; after an idle
// close there are none.
func (s *Server) awaitClients() {
	for {
		s.mu.Lock()
		n := s.clients
		s.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(s.Interval)
	}
}

func (s *Server) loop(l net.Listener, stop <-chan struct{}) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		s.mu.Lock()
		s.cycleLocked(true)
		snap := s.sched.Snapshot()
		idle := s.IdleTimeout > 0 && s.clients == 0 && len(snap.Running) == 0 && len(snap.Waiting) == 0 &&
			time.Since(s.lastActive) > s.IdleTimeout
		s.mu.Unlock()
		if idle {
			l.Close()
			return
		}
	}
}

// cycleLocked refreshes resources, returns slots of dead clients and admits
// waiting jobs. Config and slot count are reloaded only on timer cycles.
func (s *Server) cycleLocked(full bool) {
	if m, err := s.Metrics.Memory(); err == nil {
		s.memory = m
	}
	if full {
		if cfg, err := s.LoadConfig(); err == nil {
			s.sched.SetSlots(gate.Slots(s.memory.TotalMB, s.Metrics.CPUs(), cfg))
		}
		if s.Alive != nil {
			for _, j := range s.sched.Reap(s.Alive) {
				delete(s.grants, j.ID)
			}
		}
	}
	for _, j := range s.sched.Admit(time.Now(), s.memory.FreeMB) {
		if ch, ok := s.grants[j.ID]; ok {
			close(ch)
			delete(s.grants, j.ID)
		}
	}
}

func (s *Server) status() *Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode := "daemon"
	if s.retiring {
		mode = "daemon (retiring)"
	}
	return &Status{
		Mode:     mode,
		PID:      os.Getpid(),
		Version:  s.Version,
		Memory:   s.memory,
		CPUs:     s.Metrics.CPUs(),
		Tracker:  s.Tracker,
		Snapshot: s.sched.Snapshot(),
		Room:     s.sched.Room(time.Now(), s.memory.FreeMB),
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	s.clients++
	s.lastActive = time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.clients--
		s.lastActive = time.Now()
		s.mu.Unlock()
	}()

	r := bufio.NewReader(c)
	enc := json.NewEncoder(c)
	var m Message
	if err := readMessage(r, &m); err != nil {
		return
	}
	switch m.Op {
	case OpStatus:
		_ = enc.Encode(Message{Type: TypeStatus, Status: s.status()})
	case OpRetire:
		s.mu.Lock()
		retire := s.retire
		s.retiring = retire != nil
		s.mu.Unlock()
		if retire == nil {
			_ = enc.Encode(Message{Type: TypeError, Error: "cannot retire"})
			return
		}
		retire()
		_ = enc.Encode(Message{Type: TypeRetired})
	case OpAcquire:
		if m.Request == nil {
			_ = enc.Encode(Message{Type: TypeError, Error: "acquire without request"})
			return
		}
		s.serveJob(r, enc, *m.Request)
	default:
		_ = enc.Encode(Message{Type: TypeError, Error: "unknown op " + m.Op})
	}
}

func (s *Server) serveJob(r *bufio.Reader, enc *json.Encoder, req gate.Request) {
	granted := make(chan struct{})
	s.mu.Lock()
	job := s.sched.Enqueue(req, time.Now())
	s.grants[job.ID] = granted
	s.cycleLocked(false)
	s.mu.Unlock()

	// The reader sees usage reports, then the release message or the
	// connection closing.
	released := make(chan *store.Sample, 1)
	go func() {
		defer close(released)
		for {
			var m Message
			if readMessage(r, &m) != nil {
				return
			}
			switch m.Op {
			case OpUsage:
				s.mu.Lock()
				s.sched.Report(job.ID, m.UsageMB)
				s.mu.Unlock()
			case OpRelease:
				released <- m.Sample
				return
			}
		}
	}()

	done := func(sample *store.Sample) {
		s.mu.Lock()
		s.sched.Remove(job.ID)
		delete(s.grants, job.ID)
		s.cycleLocked(false)
		s.mu.Unlock()
		if sample != nil && sample.PeakMB > 0 && s.Store != nil {
			_ = s.Store.Record(req.Key(), *sample)
		}
	}

	t := time.NewTicker(s.Interval)
	defer t.Stop()
wait:
	for {
		select {
		case <-granted:
			break wait
		case <-released:
			done(nil)
			return
		case <-t.C:
			s.mu.Lock()
			pos := s.sched.Position(job.ID)
			snap := *job
			s.mu.Unlock()
			if pos == 0 {
				continue
			}
			if enc.Encode(Message{Type: TypeWait, Job: &snap, Position: pos, Status: s.status()}) != nil {
				done(nil)
				return
			}
		}
	}
	s.mu.Lock()
	grant := *job
	s.mu.Unlock()
	if enc.Encode(Message{Type: TypeGrant, Job: &grant, Status: s.status()}) != nil {
		done(nil)
		return
	}
	done(<-released)
}

func readMessage(r *bufio.Reader, m *Message) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, m)
}
