// Command jobgate is a machine-wide gate for heavy local jobs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ryusenkei-inc/jobgate/internal/config"
	"github.com/ryusenkei-inc/jobgate/internal/daemon"
	"github.com/ryusenkei-inc/jobgate/internal/gate"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/runner"
	"github.com/ryusenkei-inc/jobgate/internal/store"
)

const usage = `jobgate: a machine-wide gate for heavy local jobs

Usage:
  jobgate run [--weight N] [--priority interactive|background] -- <command> [args...]
  jobgate status [--json]
  jobgate daemon
  jobgate version

Environment:
  JOBGATE_SLOTS       override the computed number of slots
  JOBGATE_COST_MB     assumed peak memory of a weight-1 job without samples
  JOBGATE_RESERVE_MB  memory kept free for everything else
  JOBGATE_PRIORITY    default priority (interactive or background)
  JOBGATE_DISABLE=1   run commands directly without the gate
  JOBGATE_NO_DAEMON=1 use the machine-wide lock fallback instead of the daemon
  JOBGATE_HOME        data directory (also isolates the daemon)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "version", "--version":
		fmt.Println(version())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "jobgate: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return "jobgate " + info.Main.Version
	}
	return "jobgate (devel)"
}

func nativePlatform() (*platform.Platform, error) {
	paths, err := platform.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return platform.Native(paths), nil
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "jobgate: %v\n", err)
	return 2
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	weight := fs.Int("weight", 1, "relative cost of the job in slots; a hint until samples exist")
	prio := fs.String("priority", "", "interactive or background (default: interactive when stdin is a terminal)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(os.Stderr, "jobgate run: missing command (jobgate run -- <command> [args...])")
		return 2
	}
	priority, err := resolvePriority(*prio)
	if err != nil {
		return fail(err)
	}
	p, err := nativePlatform()
	if err != nil {
		return fail(err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	code, err := runner.Run(p, runner.Options{
		Args:     cmd,
		Weight:   *weight,
		Priority: priority,
		Dir:      dir,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		NoDaemon: os.Getenv("JOBGATE_NO_DAEMON") == "1",
		Disable:  os.Getenv("JOBGATE_DISABLE") == "1",
		Nested:   os.Getenv(runner.JobVar) != "",
	})
	if err != nil {
		return fail(err)
	}
	return code
}

func resolvePriority(flagValue string) (gate.Priority, error) {
	v := flagValue
	if v == "" {
		v = os.Getenv("JOBGATE_PRIORITY")
	}
	if v == "" {
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return gate.Interactive, nil
		}
		return gate.Background, nil
	}
	p, ok := gate.ParsePriority(v)
	if !ok {
		return 0, fmt.Errorf("unknown priority %q (interactive or background)", v)
	}
	return p, nil
}

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
	srv := &daemon.Server{
		Metrics:     p.Metrics,
		Tracker:     p.Tracker.Capabilities(),
		Alive:       p.Alive,
		Store:       store.Open(filepath.Join(p.Paths.Data, store.FileName)),
		LoadConfig:  func() (gate.Config, error) { return config.Load(p.Paths.Data) },
		IdleTimeout: *idle,
	}
	err = daemon.Run(p, srv)
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(os.Stderr, "jobgate: daemon already running")
		return 0
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, err := nativePlatform()
	if err != nil {
		return fail(err)
	}
	s, err := daemon.QueryStatus(p)
	if err != nil {
		if s, err = fallbackStatus(p); err != nil {
			return fail(err)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
		return 0
	}
	printStatus(os.Stdout, s)
	return 0
}

// fallbackStatus describes the machine without a daemon. Slot holders are
// found by probing the slot locks.
func fallbackStatus(p *platform.Platform) (*daemon.Status, error) {
	cfg, err := config.Load(p.Paths.Data)
	if err != nil {
		return nil, err
	}
	mem, err := p.Metrics.Memory()
	if err != nil {
		return nil, err
	}
	cpus := p.Metrics.CPUs()
	slots := gate.Slots(mem.TotalMB, cpus, cfg)
	used := 0
	for i := range slots {
		unlock, ok, err := p.Locks.TryLock("slot-" + strconv.Itoa(i))
		if err != nil {
			return nil, err
		}
		if ok {
			unlock()
		} else {
			used++
		}
	}
	return &daemon.Status{
		Mode:     "no daemon (lock fallback)",
		Memory:   mem,
		CPUs:     cpus,
		Tracker:  p.Tracker.Capabilities(),
		Snapshot: gate.Snapshot{Slots: slots, Used: used, Running: []gate.Job{}, Waiting: []gate.Job{}},
	}, nil
}

func printStatus(w io.Writer, s *daemon.Status) {
	mode := s.Mode
	if s.PID > 0 {
		mode += " pid " + strconv.Itoa(s.PID)
	}
	fmt.Fprintf(w, "mode:    %s\n", mode)
	fmt.Fprintf(w, "slots:   %d/%d used\n", s.Snapshot.Used, s.Snapshot.Slots)
	fmt.Fprintf(w, "memory:  %d MB free of %d MB\n", s.Memory.FreeMB, s.Memory.TotalMB)
	fmt.Fprintf(w, "cpus:    %d\n", s.CPUs)
	fmt.Fprintf(w, "tracker: %s\n", s.Tracker.Method)
	section := func(title string, jobs []gate.Job, since func(gate.Job) time.Time) {
		fmt.Fprintf(w, "\n%s (%d)\n", title, len(jobs))
		if len(jobs) == 0 {
			return
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tWEIGHT\tEST MB\tSAMPLES\tPRIORITY\tAGE\tCOMMAND\tDIR")
		for _, j := range jobs {
			fmt.Fprintf(tw, "  %d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n", j.ID, j.Slots, j.EstimateMB, j.Samples, j.Priority,
				time.Since(since(j)).Round(time.Second), truncate(j.Command, 60), j.Dir)
		}
		tw.Flush()
	}
	section("running", s.Snapshot.Running, func(j gate.Job) time.Time { return j.Started })
	section("waiting", s.Snapshot.Waiting, func(j gate.Job) time.Time { return j.Queued })
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-3]) + "..."
}
