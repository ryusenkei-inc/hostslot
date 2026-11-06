package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func exeName() string {
	if runtime.GOOS == "windows" {
		return "jobgate.exe"
	}
	return "jobgate"
}

func TestStableExePrefersALinkOnThePath(t *testing.T) {
	real := filepath.Join(t.TempDir(), "1.2.3", exeName())
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	link := filepath.Join(bin, exeName())
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("PATH", bin)
	if got := StableExe(real); got != link {
		t.Fatalf("got %s, want the link %s", got, link)
	}

	// Another file named jobgate on the PATH, such as a shim, is not used.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, exeName()), []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	if got := StableExe(real); got != real {
		t.Fatalf("got %s, want %s", got, real)
	}
}

func TestFind(t *testing.T) {
	path := write(t, existing)
	if _, found, err := Find(path, Claude); err != nil || found {
		t.Fatalf("found %v err %v before registering", found, err)
	}
	cmd := HookCommand("C:/bin/jobgate.exe", Claude)
	if _, err := Register(path, Claude, cmd, false, false); err != nil {
		t.Fatal(err)
	}
	got, found, err := Find(path, Claude)
	if err != nil || !found || got != cmd || HookExe(got, Claude) != "C:/bin/jobgate.exe" {
		t.Fatalf("got %q %v %v", got, found, err)
	}
	if _, found, err := Find(filepath.Join(t.TempDir(), "none.json"), Claude); err != nil || found {
		t.Fatalf("missing file: %v %v", found, err)
	}
}
