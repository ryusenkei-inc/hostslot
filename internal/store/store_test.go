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

func TestPriorFromSimilarWork(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), FileName))
	rec := func(key string, peaks ...uint64) {
		for _, p := range peaks {
			if err := st.Record(key, Sample{PeakMB: p}); err != nil {
				t.Fatal(err)
			}
		}
	}
	rec("git:/a/.git\n.\npnpm test", 800, 1200)
	rec("git:/b/.git\n.\npnpm test", 1000)
	rec("git:/c/.git\n.\npnpm test", 3000)
	rec("git:/a/.git\n.\ncargo test -q", 4000)
	rec("git:/b/.git\n.\ncargo build", 2500)
	rec("C:/old\nsh -c pnpm test", 9999)

	cases := []struct {
		key  string
		want uint64
		ok   bool
	}{
		// The same command in other repositories: median of 1200, 1000, 3000.
		{"git:/d/.git\n.\npnpm test", 1200, true},
		// The same tool and subcommand: cargo test -q in /a.
		{"git:/d/.git\n.\ncargo test --workspace", 4000, true},
		// The same tool: median of 4000 and 2500 (the upper middle).
		{"git:/d/.git\n.\ncargo clippy", 4000, true},
		{"git:/d/.git\n.\ngo test ./...", 0, false},
	}
	for _, c := range cases {
		got, ok := st.Prior(c.key)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %d %v, want %d %v", c.key, got, ok, c.want, c.ok)
		}
	}
	// A key's own samples are not its prior; the old format is ignored.
	if got, _ := st.Prior("git:/c/.git\n.\npnpm test"); got != 1200 {
		t.Errorf("own key counted: %d", got)
	}
}

func TestDurationsAndLearnable(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), FileName))
	_ = st.Record("k", Sample{PeakMB: 100, DurationMS: 2000})
	_ = st.Record("k", Sample{PeakMB: 100})
	if d := st.Durations("k"); len(d) != 1 || d[0] != 2000 {
		t.Fatalf("durations: %v", d)
	}
	if (Sample{PeakMB: 100, Outcome: "timeout"}).Learnable() || !(Sample{PeakMB: 100, Outcome: "ceiling"}).Learnable() || (Sample{Outcome: "ok"}).Learnable() {
		t.Fatal("learnable rules")
	}
}
