package main

import (
	"os"
	"testing"

	"github.com/ryusenkei-inc/hostslot/internal/platform"
)

// On Windows the directory cannot be removed while it is the working
// directory, so this fails without leaveWorkdir.
func TestLeaveWorkdirReleasesStartDir(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOSTSLOT_HOME", home)
	// Registered after TempDir so it runs first and the dirs can be removed.
	t.Cleanup(func() { _ = os.Chdir(orig) })
	start, err := os.MkdirTemp("", "hostslot-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(start) })
	if err := os.Chdir(start); err != nil {
		t.Fatal(err)
	}

	paths, err := platform.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := leaveWorkdir(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(start); err != nil {
		t.Fatalf("start directory still held: %v", err)
	}
	if wd, _ := os.Getwd(); wd == start {
		t.Fatalf("still in %s", wd)
	}
}
