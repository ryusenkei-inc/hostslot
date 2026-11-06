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

func (nativeTracker) Start(cmd *exec.Cmd) (Process, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &pollProcess{cmd: cmd, done: make(chan struct{})}
	go p.poll()
	return p, nil
}

type pollProcess struct {
	cmd     *exec.Cmd
	peak    atomic.Uint64
	current atomic.Uint64
	done    chan struct{}
	once    sync.Once
}

func (p *pollProcess) Pid() int { return p.cmd.Process.Pid }

func (p *pollProcess) poll() {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		if rss, _ := treeRSS(p.Pid()); rss > 0 {
			p.current.Store(rss / mib)
			p.raise(rss / mib)
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

func (p *pollProcess) CurrentMB() uint64 { return p.current.Load() }

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

// ---- IPC: Unix socket ----

type socketIPC struct{ path string }

// Socket paths are limited to about 104 bytes on macOS, so a long home
// falls back to a hashed name in the temp directory.
func newIPC(p Paths) IPC {
	path := filepath.Join(p.Runtime, "jobgate.sock")
	if len(path) > 100 {
		path = filepath.Join(os.TempDir(), "jobgate-"+instanceID(p.Data)+".sock")
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
