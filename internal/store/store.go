// Package store persists learning samples (peak memory per command and
// directory) as a small JSON file.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
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
}

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
