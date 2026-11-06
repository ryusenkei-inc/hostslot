// Package ports assigns TCP ports to the services of worktrees, so the dev
// servers of parallel worktrees, in one repository or several, never take
// the same port. An assignment is recorded and stays with its service.
package ports

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/jobkey"
)

// FileName is the assignments file inside the data directory.
const FileName = "ports.json"

// The range ports are assigned from when a service's default is taken.
const (
	RangeStart = 20000
	RangeEnd   = 29999
)

// Service names a dev server of a worktree.
type Service struct {
	// Key identifies it: the repository (or the directory outside one),
	// the worktree and the name.
	Key string
	// Worktree is the directory whose removal frees the port.
	Worktree string
	Name     string
}

// For returns the service name of the worktree that contains dir. Outside
// a repository, dir itself is the worktree.
func For(dir, name string) Service {
	if common, top, ok := jobkey.Worktree(dir); ok {
		return Service{Key: "git:" + common + "\n" + top + "\n" + name, Worktree: top, Name: name}
	}
	d := filepath.ToSlash(filepath.Clean(dir))
	return Service{Key: "dir:" + d + "\n" + name, Worktree: d, Name: name}
}

// Assignment is a port recorded for a service.
type Assignment struct {
	Port     int       `json:"port"`
	Worktree string    `json:"worktree"`
	Name     string    `json:"name"`
	Assigned time.Time `json:"assigned"`
	// PID is the `hostslot serve` running the service, while it runs.
	PID int `json:"pid,omitempty"`
	// Listening are the ports its process tree was last seen listening on.
	Listening []int `json:"listening,omitempty"`
}

// Observe records what `hostslot serve` sees of its service: the serve
// process (0 once it ends) and the ports its tree listens on.
func (r *Registry) Observe(key string, pid int, listening []int) error {
	all, err := r.read()
	if err != nil {
		return err
	}
	a, ok := all[key]
	if !ok {
		return nil
	}
	a.PID, a.Listening = pid, listening
	all[key] = a
	return r.write(all)
}

// Registry is the assignments file. It is not safe for concurrent use:
// callers hold a machine-wide lock around Assign.
type Registry struct {
	path string
	// Free reports whether nothing listens on a port (Available when nil).
	Free func(port int) bool
	// Exists reports whether a worktree is still there (a directory check
	// when nil).
	Exists func(worktree string) bool
}

// Open returns the registry stored at path.
func Open(path string) *Registry { return &Registry{path: path} }

// Assign returns the port of s, assigning one when s has none: def when it
// is neither assigned to another service nor in use, else the first free
// port of the range from a start derived from the key. Assignments of
// worktrees that no longer exist are dropped first.
func (r *Registry) Assign(s Service, def int, now time.Time) (int, error) {
	all, err := r.read()
	if err != nil {
		return 0, err
	}
	r.reclaim(all)
	if a, ok := all[s.Key]; ok {
		return a.Port, nil
	}
	taken := map[int]bool{}
	for _, a := range all {
		taken[a.Port] = true
	}
	port, ok := r.pick(s.Key, def, taken)
	if !ok {
		return 0, fmt.Errorf("no free port between %d and %d", RangeStart, RangeEnd)
	}
	all[s.Key] = Assignment{Port: port, Worktree: s.Worktree, Name: s.Name, Assigned: now}
	return port, r.write(all)
}

// List returns every assignment by key, after dropping those of worktrees
// that no longer exist.
func (r *Registry) List() (map[string]Assignment, error) {
	all, err := r.read()
	if err != nil {
		return nil, err
	}
	if r.reclaim(all) {
		if err := r.write(all); err != nil {
			return nil, err
		}
	}
	return all, nil
}

func (r *Registry) pick(key string, def int, taken map[int]bool) (int, bool) {
	free := r.Free
	if free == nil {
		free = Available
	}
	if def > 0 && !taken[def] && free(def) {
		return def, true
	}
	size := RangeEnd - RangeStart + 1
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	start := int(h.Sum32() % uint32(size))
	for i := range size {
		port := RangeStart + (start+i)%size
		if !taken[port] && free(port) {
			return port, true
		}
	}
	return 0, false
}

func (r *Registry) reclaim(all map[string]Assignment) bool {
	exists := r.Exists
	if exists == nil {
		exists = func(dir string) bool {
			st, err := os.Stat(filepath.FromSlash(dir))
			return err == nil && st.IsDir()
		}
	}
	dropped := false
	for k, a := range all {
		if !exists(a.Worktree) {
			delete(all, k)
			dropped = true
		}
	}
	return dropped
}

func (r *Registry) read() (map[string]Assignment, error) {
	all := map[string]Assignment{}
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", r.path, err)
	}
	return all, nil
}

func (r *Registry) write(all map[string]Assignment) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), "ports-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

// Available reports whether a port can be listened on: on the loopback
// addresses and on all addresses, since dev servers bind any of them and a
// listener on one doesn't always block the others (Windows).
func Available(port int) bool {
	p := strconv.Itoa(port)
	for _, addr := range []string{"127.0.0.1:" + p, "[::1]:" + p, ":" + p} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			if addr == "[::1]:"+p && !hasIPv6Loopback() {
				continue
			}
			return false
		}
		l.Close()
	}
	return true
}

func hasIPv6Loopback() bool {
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// Entry is an assignment as listed: with its key, and whether its serve
// process still runs.
type Entry struct {
	Key string `json:"key"`
	Assignment
	Running bool `json:"running"`
}

// Sorted returns assignments ordered by port, for listing. alive tells
// whether a serve process still runs; a stale one doesn't count.
func Sorted(all map[string]Assignment, alive func(pid int) bool) []Entry {
	out := make([]Entry, 0, len(all))
	for k, a := range all {
		running := a.PID > 0 && alive(a.PID)
		if !running {
			a.PID, a.Listening = 0, nil
		}
		out = append(out, Entry{Key: k, Assignment: a, Running: running})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
