package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryusenkei-inc/jobgate/internal/gate"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/store"
)

// LogFile is the daemon's stdout and stderr inside the data directory.
const LogFile = "daemon.log"

// Connect dials a running daemon, or starts one and dials it. It returns an
// error when no daemon can be reached, and the caller falls back to locks.
func Connect(p *platform.Platform) (net.Conn, error) {
	if c, err := p.IPC.Dial(300 * time.Millisecond); err == nil {
		return c, nil
	}
	if err := Spawn(p); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := p.IPC.Dial(300 * time.Millisecond)
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("daemon did not start: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Spawn starts `jobgate daemon` detached from the current process.
func Spawn(p *platform.Platform) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// A daemon started from a `go run` binary keeps the temp file open, so go
	// cannot delete it and exits with an error.
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return errors.New("not starting a daemon from a `go run` build")
	}
	if err := p.Paths.Ensure(); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(p.Paths.Data, LogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = platform.DetachedAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Lease is an admitted job held over an open connection.
type Lease struct {
	conn net.Conn
	// Job is the admitted job with its estimate and effective weight.
	Job gate.Job
	// Status is the gate state at admission.
	Status Status
}

// ErrBusy means no slot was granted before the deadline.
var ErrBusy = errors.New("no slot before the deadline")

// Acquire sends the request and blocks until the daemon admits it, or until
// deadline (none when zero). onWait receives each wait update. On ErrBusy the
// caller closes the connection, which takes the job out of the queue.
func Acquire(c net.Conn, req gate.Request, deadline time.Time, onWait func(Message)) (*Lease, error) {
	if err := json.NewEncoder(c).Encode(Message{Op: OpAcquire, Request: &req}); err != nil {
		return nil, err
	}
	if !deadline.IsZero() {
		_ = c.SetReadDeadline(deadline)
		defer c.SetReadDeadline(time.Time{})
	}
	r := bufio.NewReader(c)
	for {
		var m Message
		if err := readMessage(r, &m); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return nil, ErrBusy
			}
			return nil, fmt.Errorf("daemon connection: %w", err)
		}
		switch m.Type {
		case TypeWait:
			if onWait != nil {
				onWait(m)
			}
		case TypeGrant:
			if m.Job == nil || m.Status == nil {
				return nil, errors.New("malformed grant")
			}
			return &Lease{conn: c, Job: *m.Job, Status: *m.Status}, nil
		case TypeError:
			return nil, errors.New(m.Error)
		}
	}
}

// Release reports the finished run and closes the connection.
func (l *Lease) Release(sample *store.Sample) {
	_ = json.NewEncoder(l.conn).Encode(Message{Op: OpRelease, Sample: sample})
	l.conn.Close()
}

// QueryStatus asks a running daemon for its status without starting one.
func QueryStatus(p *platform.Platform) (*Status, error) {
	c, err := p.IPC.Dial(500 * time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(Message{Op: OpStatus}); err != nil {
		return nil, err
	}
	var m Message
	if err := readMessage(bufio.NewReader(c), &m); err != nil {
		return nil, err
	}
	if m.Status == nil {
		return nil, errors.New("malformed status")
	}
	return m.Status, nil
}
