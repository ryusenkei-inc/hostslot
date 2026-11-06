package ports

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// registry returns a registry where every port is free except busy, and
// every worktree exists except gone.
func registry(t *testing.T, busy map[int]bool, gone map[string]bool) *Registry {
	t.Helper()
	r := Open(filepath.Join(t.TempDir(), FileName))
	r.Free = func(p int) bool { return !busy[p] }
	r.Exists = func(w string) bool { return !gone[w] }
	return r
}

func svc(worktree, name string) Service {
	return Service{Key: "git:/r/.git\n" + worktree + "\n" + name, Worktree: worktree, Name: name}
}

func TestTheFirstServiceGetsTheDefaultAndKeepsIt(t *testing.T) {
	r := registry(t, nil, nil)
	main, err := r.Assign(svc("/r", "web"), 5173, t0)
	if err != nil || main != 5173 {
		t.Fatalf("first: %d %v", main, err)
	}
	other, err := r.Assign(svc("/w/fix", "web"), 5173, t0)
	if err != nil || other == 5173 || other < RangeStart || other > RangeEnd {
		t.Fatalf("second: %d %v", other, err)
	}
	// Both stay, whichever asks again first.
	for _, c := range []struct {
		s    Service
		want int
	}{{svc("/w/fix", "web"), other}, {svc("/r", "web"), main}} {
		if got, _ := r.Assign(c.s, 5173, t0); got != c.want {
			t.Fatalf("%s: got %d, want %d", c.s.Worktree, got, c.want)
		}
	}
}

func TestADefaultInUseElsewhereIsSkipped(t *testing.T) {
	r := registry(t, map[int]bool{5173: true}, nil)
	got, err := r.Assign(svc("/r", "web"), 5173, t0)
	if err != nil || got == 5173 {
		t.Fatalf("got %d %v", got, err)
	}
}

func TestPortsOfARemovedWorktreeAreFreed(t *testing.T) {
	gone := map[string]bool{}
	r := registry(t, nil, gone)
	if _, err := r.Assign(svc("/w/old", "web"), 5173, t0); err != nil {
		t.Fatal(err)
	}
	gone["/w/old"] = true
	got, err := r.Assign(svc("/w/new", "web"), 5173, t0)
	if err != nil || got != 5173 {
		t.Fatalf("got %d %v", got, err)
	}
	all, _ := r.List()
	if len(all) != 1 {
		t.Fatalf("left: %+v", all)
	}
}

func TestTheRangeStartsWhereTheKeyPoints(t *testing.T) {
	a := registry(t, nil, nil)
	b := registry(t, nil, nil)
	pa, _ := a.Assign(svc("/w/x", "api"), 0, t0)
	pb, _ := b.Assign(svc("/w/x", "api"), 0, t0)
	if pa != pb {
		t.Fatalf("not deterministic: %d %d", pa, pb)
	}
}

func TestForNamesTheWorktree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	s := For(sub, "web")
	if !strings.HasPrefix(s.Key, "git:") || !strings.HasSuffix(s.Key, "\nweb") || filepath.Base(filepath.FromSlash(s.Worktree)) != filepath.Base(dir) {
		t.Fatalf("got %+v", s)
	}
	outside := For(t.TempDir(), "web")
	if !strings.HasPrefix(outside.Key, "dir:") {
		t.Fatalf("outside: %+v", outside)
	}
}

func TestAvailableSeesAListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if Available(l.Addr().(*net.TCPAddr).Port) {
		t.Fatal("a port with a listener was free")
	}
}
