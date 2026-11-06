package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordPersistsAndTrims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.json")
	s := Open(path)
	for i := range Keep + 3 {
		if err := s.Record("k", Sample{PeakMB: uint64(i), At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	peaks := Open(path).Peaks("k")
	if len(peaks) != Keep || peaks[0] != 3 || peaks[Keep-1] != Keep+2 {
		t.Fatalf("peaks %v", peaks)
	}
	if got := Open(path).Peaks("other"); len(got) != 0 {
		t.Fatalf("unknown key %v", got)
	}
}

func TestRecordKeepsOtherWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.json")
	a, b := Open(path), Open(path)
	if err := a.Record("a", Sample{PeakMB: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.Record("b", Sample{PeakMB: 2}); err != nil {
		t.Fatal(err)
	}
	s := Open(path)
	if len(s.Peaks("a")) != 1 || len(s.Peaks("b")) != 1 {
		t.Fatalf("lost a sample")
	}
}
