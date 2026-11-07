// Package daemon serves admission over local IPC. The CLI starts it on
// demand; it exits on its own after a period without jobs.
//
// The protocol is newline-delimited JSON. A client opens one connection per
// job and keeps it open while the job runs, so a crashed client releases its
// slot when the OS closes the connection.
package daemon

import (
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
)

// Client operations.
const (
	OpAcquire = "acquire"
	OpRelease = "release"
	OpStatus  = "status"
	// OpUsage reports the current memory of a running job.
	OpUsage = "usage"
	// OpRetire asks an older daemon to make way (see RetireOlder).
	OpRetire = "retire"
)

// Server message types.
const (
	TypeWait   = "wait"
	TypeGrant  = "grant"
	TypeStatus = "status"
	TypeError  = "error"
	// TypeRetired confirms OpRetire.
	TypeRetired = "retired"
	// TypeStop asks a client to stop its running job: the machine is nearly
	// out of memory and the job is the largest in the background.
	TypeStop = "stop"
)

// Message is the single envelope for both directions.
type Message struct {
	Op   string `json:"op,omitempty"`
	Type string `json:"type,omitempty"`

	Request *gate.Request `json:"request,omitempty"`
	Sample  *store.Sample `json:"sample,omitempty"`
	UsageMB uint64        `json:"usageMB,omitempty"`

	Job      *gate.Job `json:"job,omitempty"`
	Position int       `json:"position,omitempty"`
	Status   *Status   `json:"status,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Finished is a job that ended, for `status`'s recent list.
type Finished struct {
	Command    string `json:"command"`
	Dir        string `json:"dir"`
	Key        string `json:"key,omitempty"`
	Source     string `json:"source,omitempty"`
	PeakMB     uint64 `json:"peakMB,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
	ExitCode   int    `json:"exitCode"`
	// Outcome: ok, failed, timeout, ceiling, machine (as store.Sample), lost
	// (the client went away without reporting), or gave up (it left the queue
	// before it started).
	Outcome string    `json:"outcome"`
	Ended   time.Time `json:"ended"`
}

// Status describes the gate for `hostslot status`.
type Status struct {
	Mode     string                `json:"mode"`
	PID      int                   `json:"pid,omitempty"`
	Version  string                `json:"version,omitempty"`
	Memory   platform.Memory       `json:"memory"`
	CPUs     int                   `json:"cpus"`
	Tracker  platform.Capabilities `json:"tracker"`
	Snapshot gate.Snapshot         `json:"snapshot"`
	// Room is how many more weight-1 jobs would start now (see
	// gate.Scheduler.Room); without a daemon, the free slots.
	Room int `json:"room"`
	// ReserveMB is kept free for everything else; CeilingMB is the memory one
	// job may use (0: off).
	ReserveMB uint64 `json:"reserveMB,omitempty"`
	CeilingMB uint64 `json:"ceilingMB,omitempty"`
	// CPUBusyPct is how busy all CPUs were lately, in percent; missing when
	// the OS can't tell.
	CPUBusyPct *int `json:"cpuBusyPct,omitempty"`
	// CPUBusyLimitPct is where background jobs stop being admitted.
	CPUBusyLimitPct int `json:"cpuBusyLimitPct,omitempty"`
	// Recent are the jobs that ended last, newest first.
	Recent []Finished `json:"recent,omitempty"`
}
