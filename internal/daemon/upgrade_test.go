package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ryusenkei-inc/jobgate/internal/gate"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/store"
)

func TestOlder(t *testing.T) {
	cases := []struct {
		daemon, client string
		want           bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.0.0-20261106120524-a378131df5ca", "v0.0.0-20261106125924-ff86f55768c0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"", "v0.1.0", false},
		{"v0.1.0", "", false},
		{"(devel)", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Older(c.daemon, c.client); got != c.want {
			t.Errorf("Older(%q, %q) = %v", c.daemon, c.client, got)
		}
	}
}

func TestRetireFreesTheLockAndWaitsForJobs(t *testing.T) {
	dir := t.TempDir()
	p := platform.Native(platform.Paths{Data: dir, Runtime: dir})
	srv := &Server{
		Metrics:    fakeMetrics{platform.Memory{TotalMB: 16384, FreeMB: 12000}},
		Alive:      func(int) bool { return true },
		Store:      store.Open(filepath.Join(dir, "samples.json")),
		LoadConfig: func() (gate.Config, error) { return gate.Config{Slots: 2, CostMB: 1000}.Normalize(), nil },
		Interval:   20 * time.Millisecond,
		Version:    "v0.1.0",
	}
	done := make(chan error, 1)
	go func() { done <- Run(p, srv) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := QueryStatus(p); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ch, _ := acquireAsync(t, p, gate.Request{Command: "build", Dir: "/r", Weight: 1, PID: 1})
	lease := waitLease(t, ch)

	if RetireOlder(p, "v0.0.1") {
		t.Fatal("an older client retired a newer daemon")
	}
	if !RetireOlder(p, "v0.2.0") {
		t.Fatal("the older daemon was not retired")
	}
	unlock, ok, err := p.Locks.TryLock(DaemonLock)
	if err != nil || !ok {
		t.Fatalf("daemon lock still held after retiring: %v %v", ok, err)
	}
	unlock()
	select {
	case <-done:
		t.Fatal("retired daemon exited while a job was running")
	case <-time.After(200 * time.Millisecond):
	}
	lease.Release(nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retired daemon did not exit after its last job")
	}
}
