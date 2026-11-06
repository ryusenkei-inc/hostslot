package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryusenkei-inc/jobgate/internal/gate"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/store"
)

// TestMain doubles as the wrapped command.
func TestMain(m *testing.M) {
	switch os.Getenv("JOBGATE_TEST_HELPER") {
	case "env":
		os.Stdout.WriteString("workers=" + os.Getenv("VITEST_MAX_WORKERS") + "\n")
		os.Stdout.WriteString("job=" + os.Getenv(JobVar) + "\n")
		buf := make([]byte, 64*1024*1024)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		time.Sleep(600 * time.Millisecond)
		os.Exit(7 + int(buf[0]) - 1)
	case "touch":
		os.WriteFile(os.Getenv("JOBGATE_TEST_MARKER"), nil, 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func setup(t *testing.T, helper string) (*platform.Platform, Options, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JOBGATE_TEST_HELPER", helper)
	t.Setenv("JOBGATE_SLOTS", "1")
	t.Setenv("JOBGATE_COST_MB", "")
	t.Setenv("JOBGATE_RESERVE_MB", "")
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
	key := gate.Request{Command: o.Args[0], Dir: o.Dir}.Key()
	peaks := st.Peaks(key)
	if len(peaks) != 1 || peaks[0] < 32 {
		t.Fatalf("sample not recorded: %v", peaks)
	}
}

func TestRunWaitsForSlot(t *testing.T) {
	p, o, _ := setup(t, "touch")
	marker := filepath.Join(p.Paths.Data, "ran")
	t.Setenv("JOBGATE_TEST_MARKER", marker)

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
	o.Args = []string{"jobgate-no-such-command"}
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
	if !strings.Contains(joined, "npm_config_workspace_concurrency=3") || !strings.Contains(joined, "pnpm_config_workspace_concurrency=3") || !strings.Contains(joined, "JOBGATE_WORKERS=3") {
		t.Fatalf("missing vars: %v", env)
	}
	ci := strings.Join(withWorkers([]string{"CI=true"}, 3), " ")
	if strings.Contains(ci, "npm_config") || !strings.Contains(ci, "JOBGATE_WORKERS=3") {
		t.Fatalf("CI handling: %v", ci)
	}
}
