// Package daemon serves admission over local IPC. The CLI starts it on
// demand; it exits on its own after a period without jobs.
//
// The protocol is newline-delimited JSON. A client opens one connection per
// job and keeps it open while the job runs, so a crashed client releases its
// slot when the OS closes the connection.
package daemon

import (
	"github.com/ryusenkei-inc/loadslot/internal/gate"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/store"
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

// Status describes the gate for `loadslot status`.
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
}
