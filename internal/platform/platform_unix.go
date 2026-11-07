//go:build unix

package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ---- Process tracking: poll the tree's resident memory ----

const pollInterval = 250 * time.Millisecond

type nativeTracker struct{}

func (nativeTracker) Capabilities() Capabilities {
	return Capabilities{TreeMemory: treeSupported, Method: treeMethod}
}

// backgroundNice is the niceness of background jobs; descendants inherit it.
const backgroundNice = 10

// cleanupGrace is how long leftover processes get to exit after SIGTERM.
const cleanupGrace = 500 * time.Millisecond

func (nativeTracker) Start(cmd *exec.Cmd, o StartOptions) (Process, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if o.Background {
		// Right after start: a child forked in between keeps the normal
		// priority, the rest of the tree inherits this one.
		_ = unix.Setpriority(unix.PRIO_PROCESS, cmd.Process.Pid, backgroundNice)
	}
	p := &pollProcess{cmd: cmd, done: make(chan struct{}), cleanup: o.Cleanup, seen: map[int]struct{}{}}
	go p.poll()
	return p, nil
}

type pollProcess struct {
	cmd     *exec.Cmd
	peak    atomic.Uint64
	current atomic.Uint64
	cleanup bool
	// seen holds every descendant a poll found. Orphans are reparented when
	// the root exits, so they can only be found by what was seen before.
	mu   sync.Mutex
	seen map[int]struct{}
	done chan struct{}
	once sync.Once
}

func (p *pollProcess) Pid() int { return p.cmd.Process.Pid }

func (p *pollProcess) poll() {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		if rss, pids := treeRSS(p.Pid()); rss > 0 {
			p.current.Store(rss / mib)
			p.raise(rss / mib)
			p.mu.Lock()
			for _, pid := range pids {
				p.seen[pid] = struct{}{}
			}
			p.mu.Unlock()
		}
		select {
		case <-p.done:
			p.current.Store(0)
			return
		case <-t.C:
		}
	}
}

func (p *pollProcess) raise(mb uint64) {
	for {
		cur := p.peak.Load()
		if mb <= cur || p.peak.CompareAndSwap(cur, mb) {
			return
		}
	}
}

func (p *pollProcess) Wait() error {
	err := p.cmd.Wait()
	p.once.Do(func() { close(p.done) })
	if p.cleanup {
		p.stopLeftovers()
	}
	// ru_maxrss covers the root and descendants it waited for, so it catches
	// short commands that finish between two polls.
	if st := p.cmd.ProcessState; st != nil {
		if ru, ok := st.SysUsage().(*syscall.Rusage); ok {
			p.raise(maxRSSBytes(ru) / mib)
		}
	}
	return err
}

func (p *pollProcess) PeakMB() uint64 { return p.peak.Load() }

// CurrentMB checks done itself: the poller may store one more reading after
// Wait returns.
func (p *pollProcess) CurrentMB() uint64 {
	select {
	case <-p.done:
		return 0
	default:
		return p.current.Load()
	}
}

// stopLeftovers stops descendants that outlived the root: SIGTERM, then
// SIGKILL for those still there after cleanupGrace.
func (p *pollProcess) stopLeftovers() {
	p.mu.Lock()
	var alive []int
	for pid := range p.seen {
		if pid != p.Pid() && unix.Kill(pid, 0) == nil {
			alive = append(alive, pid)
		}
	}
	p.mu.Unlock()
	if len(alive) == 0 {
		return
	}
	for _, pid := range alive {
		_ = unix.Kill(pid, unix.SIGTERM)
	}
	time.Sleep(cleanupGrace)
	for _, pid := range alive {
		_ = unix.Kill(pid, unix.SIGKILL)
	}
}

func (p *pollProcess) KillTree() error {
	_, pids := treeRSS(p.Pid())
	for _, pid := range pids {
		_ = unix.Kill(pid, unix.SIGKILL)
	}
	return p.cmd.Process.Kill()
}

// ---- Liveness ----

func processAlive(pid int) bool {
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

// DetachedAttr returns process attributes for a daemon that outlives the CLI.
func DetachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func hideWindow(*exec.Cmd) {}

// ---- IPC: Unix socket ----

type socketIPC struct{ path string }

// Socket paths are limited to about 104 bytes on macOS, so a long home
// falls back to a hashed name in the temp directory.
func newIPC(p Paths) IPC {
	path := filepath.Join(p.Runtime, "hostslot.sock")
	if len(path) > 100 {
		path = filepath.Join(os.TempDir(), "hostslot-"+instanceID(p.Data)+".sock")
	}
	return socketIPC{path: path}
}

func (i socketIPC) Address() string { return i.path }

// Listen removes a stale socket file. Callers hold the daemon lock, so no
// live daemon can own it.
func (i socketIPC) Listen() (net.Listener, error) {
	_ = os.Remove(i.path)
	l, err := net.Listen("unix", i.path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(i.path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (i socketIPC) Dial(timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", i.path, timeout)
}

// ---- Locks: flock ----

type flockLocker struct{ dir string }

func newLocker(p Paths) Locker { return flockLocker{dir: filepath.Join(p.Runtime, "locks")} }

func (l flockLocker) TryLock(name string) (func(), bool, error) {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(l.dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
			f.Close()
		})
	}, true, nil
}
