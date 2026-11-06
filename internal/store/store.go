// Package store persists learning samples (peak memory per command and
// directory) as a small JSON file.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// FileName is the samples file inside the data directory.
const FileName = "samples.json"

// Keep is the number of recent samples kept per key.
const Keep = 10

// Sample is one finished run of a command.
type Sample struct {
	PeakMB     uint64    `json:"peakMB"`
	DurationMS int64     `json:"durationMs"`
	ExitCode   int       `json:"exitCode"`
	At         time.Time `json:"at"`
	// Outcome is how the run ended: ok, failed, timeout, ceiling (over its
	// memory ceiling) or machine (stopped because memory ran out).
	Outcome string `json:"outcome,omitempty"`
}

// Learnable reports whether a finished run tells the job's cost. A run
// stopped by its timeout says little; one stopped for memory is kept, since
// its peak shows it needs at least that much.
func (x Sample) Learnable() bool { return x.PeakMB > 0 && x.Outcome != "timeout" }

// Store reads and writes samples.json. Methods are safe for concurrent use
// within one process; across processes the caller serializes writes (the
// daemon is the only writer, or the fallback holds a lock).
type Store struct {
	path string
	mu   sync.Mutex
	data map[string][]Sample
}

// Open loads the file at path. A missing or unreadable file starts empty,
// because samples are only an optimization.
func Open(path string) *Store {
	s := &Store{path: path}
	s.data = s.read()
	return s
}

func (s *Store) read() map[string][]Sample {
	data := map[string][]Sample{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return data
	}
	if json.Unmarshal(b, &data) != nil {
		return map[string][]Sample{}
	}
	return data
}

// Peaks returns the recent peak memory of a key, oldest first.
func (s *Store) Peaks(key string) []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uint64
	for _, x := range s.data[key] {
		out = append(out, x.PeakMB)
	}
	return out
}

// Durations returns how long a key's recent runs took, in milliseconds.
func (s *Store) Durations(key string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int64
	for _, x := range s.data[key] {
		if x.DurationMS > 0 {
			out = append(out, x.DurationMS)
		}
	}
	return out
}

// Prior estimates the peak memory of a key that has no samples yet, from
// keys of similar work: the same command elsewhere (another repository or
// directory), else the same tool and subcommand (`cargo test`), else the same
// tool (`cargo`). It is the median of those keys' highest recent peaks. Keys
// of the old format (before learning keys named a repository and a label)
// hold whole command lines and are ignored.
func (s *Store) Prior(key string) (uint64, bool) {
	words := strings.Fields(label(key))
	if len(words) == 0 {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var groups [3][]uint64
	for k, list := range s.data {
		if k == key || len(list) == 0 || !(strings.HasPrefix(k, "git:") || strings.HasPrefix(k, "dir:")) {
			continue
		}
		other := strings.Fields(label(k))
		level := similarity(words, other)
		if level < 0 {
			continue
		}
		var peak uint64
		for _, x := range list {
			peak = max(peak, x.PeakMB)
		}
		groups[level] = append(groups[level], peak)
	}
	for _, g := range groups {
		if len(g) > 0 {
			slices.Sort(g)
			return g[len(g)/2], true
		}
	}
	return 0, false
}

// label is the last line of a learning key: the name of the work.
func label(key string) string {
	if i := strings.LastIndexByte(key, '\n'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// similarity is 0 for the same command, 1 for the same tool and subcommand,
// 2 for the same tool, and -1 otherwise.
func similarity(a, b []string) int {
	switch {
	case len(b) == 0 || a[0] != b[0]:
		return -1
	case slices.Equal(a, b):
		return 0
	case len(a) > 1 && len(b) > 1 && a[1] == b[1]:
		return 1
	default:
		return 2
	}
}

// Record appends a sample and writes the file. It rereads the file first so
// samples written by another process since Open are kept.
func (s *Store) Record(key string, x Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = s.read()
	list := append(s.data[key], x)
	if len(list) > Keep {
		list = list[len(list)-Keep:]
	}
	s.data[key] = list
	return s.write()
}

// The temp file and rename keep readers from seeing a half-written file.
func (s *Store) write() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".samples-*")
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
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
