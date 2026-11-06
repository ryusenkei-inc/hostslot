// Package platform holds the OS ports the daemon and the CLI depend on:
// resource metrics, process tracking, IPC, machine-wide locks and paths.
// Each port has one implementation per OS selected by build tags.
package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os/exec"
	"strings"
	"time"
)

// Memory is a reading of physical memory in MiB, already limited by a
// container's cgroup when one applies.
type Memory struct {
	TotalMB uint64 `json:"totalMB"`
	FreeMB  uint64 `json:"freeMB"`
}

// Metrics reads machine resources.
type Metrics interface {
	Memory() (Memory, error)
	CPUs() int
}

// Process is a started command whose whole process tree is tracked.
type Process interface {
	Pid() int
	// Wait waits for the root process and returns its error as exec.Cmd.Wait does.
	Wait() error
	// PeakMB is the peak memory of the tree seen so far; final after Wait.
	PeakMB() uint64
	// CurrentMB is the memory of the tree now, measured the same way as
	// PeakMB; 0 when unknown.
	CurrentMB() uint64
	// KillTree terminates the root process and its descendants.
	KillTree() error
}

// Capabilities reports what a ProcessTracker can do on this OS, so callers
// degrade instead of failing (e.g. watch only, no hard limit).
type Capabilities struct {
	// TreeMemory is true when PeakMB covers descendants, not only the root.
	TreeMemory bool `json:"treeMemory"`
	// Method names how memory is measured, for status output.
	Method string `json:"method"`
}

// ProcessTracker starts commands and tracks their process tree.
type ProcessTracker interface {
	Start(cmd *exec.Cmd) (Process, error)
	Capabilities() Capabilities
}

// IPC is the local transport between the CLI and the daemon.
type IPC interface {
	Listen() (net.Listener, error)
	Dial(timeout time.Duration) (net.Conn, error)
	// Address is the socket path or pipe name, for messages.
	Address() string
}

// Locker hands out machine-wide named locks that the OS releases when the
// holding process dies.
type Locker interface {
	// TryLock acquires the named lock without waiting. ok is false when
	// another process holds it.
	TryLock(name string) (unlock func(), ok bool, err error)
}

// Platform bundles the ports of the running OS.
type Platform struct {
	Paths   Paths
	Metrics Metrics
	Tracker ProcessTracker
	IPC     IPC
	Locks   Locker
	// Alive reports whether a process with the pid exists.
	Alive func(pid int) bool
}

// Native returns the ports of the running OS rooted at paths.
func Native(paths Paths) *Platform {
	return &Platform{
		Paths:   paths,
		Metrics: nativeMetrics{},
		Tracker: nativeTracker{},
		IPC:     newIPC(paths),
		Locks:   newLocker(paths),
		Alive:   processAlive,
	}
}

// instanceID distinguishes independent homes (e.g. tests) in global
// namespaces such as pipe and mutex names.
func instanceID(dir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(dir)))
	return hex.EncodeToString(sum[:])[:16]
}

const mib = 1024 * 1024
