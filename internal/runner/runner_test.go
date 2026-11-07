package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/jobkey"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
)

// TestMain doubles as the wrapped command.
func TestMain(m *testing.M) {
	switch os.Getenv("HOSTSLOT_TEST_HELPER") {
	case "env":
		os.Stdout.WriteString("workers=" + os.Getenv("VITEST_MAX_WORKERS") + "\n")
		os.Stdout.WriteString("job=" + os.Getenv(JobVar) + "\n")
		buf := make([]byte, 64*1024*1024)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		time.Sleep(600 * time.Millisecond)
		os.Exit(7 + int(buf[0]) - 1)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "grow":
		// A runaway: 32 MB more every 100 ms for up to 10 s.
		var keep [][]byte
		for range 100 {
			buf := make([]byte, 32<<20)
			for i := 0; i < len(buf); i += 4096 {
				buf[i] = 1
			}
			keep = append(keep, buf)
			time.Sleep(100 * time.Millisecond)
		}
		os.Exit(len(keep) - 100)
	case "touch":
		os.WriteFile(os.Getenv("HOSTSLOT_TEST_MARKER"), nil, 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func setup(t *testing.T, helper string) (*platform.Platform, Options, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOSTSLOT_TEST_HELPER", helper)
	t.Setenv("HOSTSLOT_SLOTS", "1")
	t.Setenv("HOSTSLOT_COST_MB", "")
	t.Setenv("HOSTSLOT_RESERVE_MB", "")
	t.Setenv("CI", "")
	os.Unsetenv("CI")
	t.Setenv("VITEST_MAX_WORKERS", "")
	os.Unsetenv("VITEST_MAX_WORKERS")
	t.Setenv(JobVar, "")
	os.Unsetenv(JobVar)
	p := platform.Native(platform.Paths{Data: dir, Runtime: dir})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return p, Options{
		Args:     []string{exe},
		Weight:   1,
		Priority: gate.Interactive,
		Dir:      dir,
		Stdout:   &out,
		Stderr:   &out,
		NoDaemon: true,
	}, &out
}

func TestRunWithLockFallback(t *testing.T) {
	p, o, out := setup(t, "env")
	code, err := Run(p, o)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code %d, output %s", code, out)
	}
	if !strings.Contains(out.String(), "workers=") || strings.Contains(out.String(), "workers=\n") {
		t.Fatalf("VITEST_MAX_WORKERS not set: %q", out)
	}
	if !strings.Contains(out.String(), "job=1\n") {
		t.Fatalf("%s not set for the gated command: %q", JobVar, out)
	}
	st := store.Open(filepath.Join(p.Paths.Data, store.FileName))
	peaks := st.Peaks(jobkey.For(o.Dir, o.Args[0]))
	if len(peaks) != 1 || peaks[0] < 32 {
		t.Fatalf("sample not recorded: %v", peaks)
	}
}

func TestRunLearnsUnderTheLabel(t *testing.T) {
	p, o, _ := setup(t, "env")
	o.Label = "pnpm test"
	if code, err := Run(p, o); err != nil || code != 7 {
		t.Fatalf("code %d err %v", code, err)
	}
	st := store.Open(filepath.Join(p.Paths.Data, store.FileName))
	if peaks := st.Peaks(jobkey.For(o.Dir, "pnpm test")); len(peaks) != 1 {
		t.Fatalf("sample not recorded under the label: %v", peaks)
	}
}

func TestRunWaitsForSlot(t *testing.T) {
	p, o, _ := setup(t, "touch")
	marker := filepath.Join(p.Paths.Data, "ran")
	t.Setenv("HOSTSLOT_TEST_MARKER", marker)

	unlock, ok, err := p.Locks.TryLock("slot-0")
	if err != nil || !ok {
		t.Fatalf("hold slot: %v %v", ok, err)
	}
	done := make(chan int, 1)
	go func() {
		code, _ := Run(p, o)
		done <- code
	}()
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("command ran while the only slot was held")
	}
	unlock()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not admitted after the slot was released")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("command did not run")
	}
	// The slot is free again after the run.
	if u, ok, _ := p.Locks.TryLock("slot-0"); !ok {
		t.Fatal("slot not released")
	} else {
		u()
	}
}

func TestRunDisabledPassesThrough(t *testing.T) {
	p, o, out := setup(t, "env")
	o.Disable = true
	code, err := Run(p, o)
	if err != nil || code != 7 {
		t.Fatalf("code %d err %v", code, err)
	}
	if !strings.Contains(out.String(), "workers=\n") {
		t.Fatalf("disabled run set inner parallelism: %q", out)
	}
}

func TestRunGivesUpAfterMaxWait(t *testing.T) {
	p, o, out := setup(t, "touch")
	marker := filepath.Join(p.Paths.Data, "ran")
	t.Setenv("HOSTSLOT_TEST_MARKER", marker)
	o.MaxWait = 600 * time.Millisecond
	unlock, ok, err := p.Locks.TryLock("slot-0")
	if err != nil || !ok {
		t.Fatalf("hold slot: %v %v", ok, err)
	}
	defer unlock()
	start := time.Now()
	code, err := Run(p, o)
	if err != nil || code != ExitBusy {
		t.Fatalf("code %d err %v, output %s", code, err, out)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("gave up late: %s", time.Since(start))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("command ran without a slot")
	}
}

func TestRunStopsTheCommandAfterTimeout(t *testing.T) {
	p, o, out := setup(t, "sleep")
	o.Timeout = 500 * time.Millisecond
	start := time.Now()
	code, err := Run(p, o)
	if err != nil || code != ExitTimeout {
		t.Fatalf("code %d err %v, output %s", code, err, out)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("stopped late: %s", time.Since(start))
	}
	st := store.Open(filepath.Join(p.Paths.Data, store.FileName))
	if peaks := st.Peaks(jobkey.For(o.Dir, o.Args[0])); len(peaks) != 0 {
		t.Fatalf("recorded a killed run: %v", peaks)
	}
}

func TestRunNestedSkipsTheGate(t *testing.T) {
	p, o, out := setup(t, "env")
	o.Nested = true
	unlock, ok, err := p.Locks.TryLock("slot-0")
	if err != nil || !ok {
		t.Fatalf("hold slot: %v %v", ok, err)
	}
	defer unlock()
	done := make(chan int, 1)
	go func() {
		code, _ := Run(p, o)
		done <- code
	}()
	select {
	case code := <-done:
		if code != 7 {
			t.Fatalf("exit %d, output %s", code, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nested run waited for the slot held by its outer job")
	}
}

func TestRunCommandNotFound(t *testing.T) {
	p, o, _ := setup(t, "")
	o.Args = []string{"hostslot-no-such-command"}
	code, err := Run(p, o)
	if err != nil || code != 127 {
		t.Fatalf("code %d err %v", code, err)
	}
}

func TestWithWorkers(t *testing.T) {
	env := withWorkers([]string{"PATH=x", "VITEST_MAX_WORKERS=9"}, 3)
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "VITEST_MAX_WORKERS=9") || strings.Contains(joined, "VITEST_MAX_WORKERS=3") {
		t.Fatalf("existing value overwritten: %v", env)
	}
	if !strings.Contains(joined, "npm_config_workspace_concurrency=3") || !strings.Contains(joined, "pnpm_config_workspace_concurrency=3") || !strings.Contains(joined, "HOSTSLOT_WORKERS=3") {
		t.Fatalf("missing vars: %v", env)
	}
	ci := strings.Join(withWorkers([]string{"CI=true"}, 3), " ")
	if strings.Contains(ci, "npm_config") || !strings.Contains(ci, "HOSTSLOT_WORKERS=3") {
		t.Fatalf("CI handling: %v", ci)
	}
	for _, want := range []string{"CARGO_BUILD_JOBS=3", "GOFLAGS=-p=3", "MAKEFLAGS=-j3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s: %v", want, env)
		}
	}
	// GOFLAGS and MAKEFLAGS hold other flags too: a set one is left alone.
	kept := strings.Join(withWorkers([]string{"GOFLAGS=-mod=vendor", "MAKEFLAGS=-s"}, 3), " ")
	if !strings.Contains(kept, "GOFLAGS=-mod=vendor") || strings.Contains(kept, "GOFLAGS=-p=3") || strings.Contains(kept, "MAKEFLAGS=-j3") {
		t.Fatalf("flags overwritten: %v", kept)
	}
}

func TestJobContext(t *testing.T) {
	env := map[string]string{"ORCA_PANE_KEY": "pane-1", "ORCA_WORKTREE_ID": "wt", "HOME": "/h"}
	ctx := jobContext(func(k string) string { return env[k] })
	if len(ctx) != 2 || ctx["ORCA_PANE_KEY"] != "pane-1" || ctx["ORCA_WORKTREE_ID"] != "wt" {
		t.Fatalf("got %v", ctx)
	}
	if ctx := jobContext(func(string) string { return "" }); ctx != nil {
		t.Fatalf("empty environment gave %v", ctx)
	}
}

func TestRunStopsARunawayAtItsCeiling(t *testing.T) {
	p, o, out := setup(t, "grow")
	o.MaxMemoryMB = 150
	start := time.Now()
	code, err := Run(p, o)
	if err != nil || code != ExitOverMemory {
		t.Fatalf("code %d err %v, output %s", code, err, out)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatalf("stopped late: %s", time.Since(start))
	}
	if !strings.Contains(out.String(), "memory ceiling of 150 MB") {
		t.Fatalf("message: %s", out)
	}
	st := store.Open(filepath.Join(p.Paths.Data, store.FileName))
	if peaks := st.Peaks(jobkey.For(o.Dir, o.Args[0])); len(peaks) != 1 || peaks[0] < 150 {
		t.Fatalf("the stopped run's peak was not kept: %v", peaks)
	}
}
