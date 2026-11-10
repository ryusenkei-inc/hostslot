package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/config"
	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
)

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	idle := fs.Duration("idle", 10*time.Minute, "exit after this long without jobs (0 = never)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, err := nativePlatform()
	if err != nil {
		return fail(err)
	}
	// Paths are resolved above, so a relative HOSTSLOT_HOME is already absolute.
	if err := leaveWorkdir(p.Paths); err != nil {
		return fail(err)
	}
	srv := &daemon.Server{
		Metrics:     p.Metrics,
		Tracker:     p.Tracker.Capabilities(),
		Alive:       p.Alive,
		Store:       store.Open(filepath.Join(p.Paths.Data, store.FileName)),
		LoadConfig:  func() (gate.Config, error) { return config.Load(p.Paths.Data) },
		IdleTimeout: *idle,
		Version:     daemon.Version(),
	}
	err = daemon.Run(p, srv)
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(os.Stderr, "hostslot: daemon already running")
		return 0
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

// leaveWorkdir moves the daemon off the directory it was started from. On
// Windows a process's working directory cannot be deleted, so a daemon that
// kept the caller's worktree would block removing it. Jobs are unaffected:
// each runs in its caller's directory.
func leaveWorkdir(paths platform.Paths) error {
	dir := os.Getenv("HOSTSLOT_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			dir = paths.Data
		} else {
			dir = home
		}
	} else {
		dir = paths.Data
	}
	return os.Chdir(dir)
}
