// Command loadslot is a machine-wide gate for heavy local jobs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ryusenkei-inc/loadslot/internal/config"
	"github.com/ryusenkei-inc/loadslot/internal/daemon"
	"github.com/ryusenkei-inc/loadslot/internal/gate"
	"github.com/ryusenkei-inc/loadslot/internal/hook"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/runner"
	"github.com/ryusenkei-inc/loadslot/internal/setup"
	"github.com/ryusenkei-inc/loadslot/internal/store"
)

const usage = `loadslot: a machine-wide gate for heavy local jobs

Usage:
  loadslot run [--weight N] [--priority interactive|background] [--as LABEL] [--source NAME]
              [--max-wait DURATION] [--timeout DURATION] [--max-memory MB] [--keep-children]
              -- <command> [args...]
  loadslot status [--json]
  loadslot daemon
  loadslot hook claude [--weight N] [--match REGEX]
  loadslot setup [claude] [--remove] [--dry-run] [--settings PATH]
  loadslot doctor
  loadslot version

Environment:
  LOADSLOT_SLOTS       override the computed number of slots
  LOADSLOT_COST_MB     assumed peak memory of a weight-1 job without samples
  LOADSLOT_RESERVE_MB  memory kept free for everything else
  LOADSLOT_PRIORITY    default priority (interactive or background)
  LOADSLOT_DISABLE=1   run commands directly without the gate
  LOADSLOT_NO_DAEMON=1 use the machine-wide lock fallback instead of the daemon
  LOADSLOT_HOME        data directory (also isolates the daemon)
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
	case "hook":
		return cmdHook(args[1:])
	case "setup":
		return cmdSetup(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "version", "--version":
		fmt.Println(version())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "loadslot: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return "loadslot " + info.Main.Version
	}
	return "loadslot (devel)"
}

func nativePlatform() (*platform.Platform, error) {
	paths, err := platform.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return platform.Native(paths), nil
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "loadslot: %v\n", err)
	return 2
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	weight := fs.Int("weight", 1, "relative cost of the job in slots; a hint until samples exist")
	prio := fs.String("priority", "", "interactive or background (default: interactive when stdin is a terminal)")
	label := fs.String("as", "", "name the work for learning (default: the command line)")
	source := fs.String("source", os.Getenv("LOADSLOT_SOURCE"), "who starts the job, shown in status (default: $LOADSLOT_SOURCE)")
	maxWait := fs.Duration("max-wait", 0, "give up waiting for a slot after this long and exit 75 (0 = wait forever)")
	timeout := fs.Duration("timeout", 0, "stop the command after it has run this long and exit 124 (0 = none)")
	keepChildren := fs.Bool("keep-children", false, "leave processes the command started running after it exits")
	maxMemory := fs.Uint64("max-memory", 0, "stop the command when it uses more MB and exit 77 (default: jobCeilingFraction of total memory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(os.Stderr, "loadslot run: missing command (loadslot run -- <command> [args...])")
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
		Args:         cmd,
		Label:        *label,
		Source:       *source,
		MaxWait:      *maxWait,
		Timeout:      *timeout,
		KeepChildren: *keepChildren,
		MaxMemoryMB:  *maxMemory,
		Weight:       *weight,
		Priority:     priority,
		Dir:          dir,
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		NoDaemon:     os.Getenv("LOADSLOT_NO_DAEMON") == "1",
		Disable:      os.Getenv("LOADSLOT_DISABLE") == "1",
		// JOBGATE_JOB: an outer job of jobgate, the former name, during an upgrade
		Nested: os.Getenv(runner.JobVar) != "" || os.Getenv("JOBGATE_JOB") != "",
	})
	if err != nil {
		return fail(err)
	}
	return code
}

func resolvePriority(flagValue string) (gate.Priority, error) {
	v := flagValue
	if v == "" {
		v = os.Getenv("LOADSLOT_PRIORITY")
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

// cmdHook exits 1 on errors: Claude Code reports exit 1 as a non-blocking
// hook error and runs the command unchanged, while 2 would block it.
func cmdHook(args []string) int {
	if len(args) == 0 || args[0] != "claude" {
		fmt.Fprintln(os.Stderr, "loadslot hook: missing agent (loadslot hook claude)")
		return 1
	}
	fs := flag.NewFlagSet("hook claude", flag.ContinueOnError)
	weight := fs.Int("weight", 1, "weight of the wrapped commands; a hint until samples exist")
	match := fs.String("match", "", "regular expression for the commands to gate (default: built-in list)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if os.Getenv("LOADSLOT_DISABLE") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	o := hook.ClaudeOptions{Weight: *weight, Exe: hookExe()}
	if *match != "" {
		re, err := regexp.Compile(*match)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loadslot hook: --match: %v\n", err)
			return 1
		}
		o.Heavy = re
	}
	if err := hook.Claude(os.Stdin, os.Stdout, o); err != nil {
		fmt.Fprintf(os.Stderr, "loadslot hook: %v\n", err)
		return 1
	}
	return 0
}

// cmdSetup registers the hooks of the given agent, or of every installed
// agent when none is named.
func cmdSetup(args []string) int {
	agents := setup.Agents
	named := len(args) > 0 && !strings.HasPrefix(args[0], "-")
	if named {
		if !slices.Contains(setup.Agents, setup.Agent(args[0])) {
			fmt.Fprintf(os.Stderr, "loadslot setup: unknown agent %q (claude)\n", args[0])
			return 2
		}
		agents = []setup.Agent{setup.Agent(args[0])}
		args = args[1:]
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "remove loadslot's hooks instead of adding them")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	path := fs.String("settings", "", "hooks file (only with an agent; default: the agent's user-level file)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path != "" && !named {
		fmt.Fprintln(os.Stderr, "loadslot setup: --settings needs an agent (loadslot setup claude --settings PATH)")
		return 2
	}
	exe, err := setupExe(*remove)
	if err != nil {
		return fail(err)
	}
	done := 0
	for _, a := range agents {
		if !named && !setup.Detected(a) {
			continue
		}
		file := *path
		if file == "" {
			if file, err = setup.SettingsPath(a); err != nil {
				return fail(err)
			}
		}
		command := setup.HookCommand(exe, a)
		res, err := setup.Register(file, a, command, *remove, *dryRun)
		if err != nil {
			return fail(err)
		}
		verb := map[setup.Result]string{
			setup.Unchanged: "already up to date",
			setup.Added:     "added the PreToolUse hook `" + command + "`",
			setup.Updated:   "updated the PreToolUse hook to `" + command + "`",
			setup.Removed:   "removed loadslot's PreToolUse hook",
		}[res]
		if *dryRun && res != setup.Unchanged {
			verb = "would have " + verb
		}
		fmt.Printf("%s: %s\n", file, verb)
		done++
	}
	if done == 0 {
		fmt.Println("No supported agent found (Claude Code). Install one, or name it: loadslot setup claude")
	}
	return 0
}

// setupExe is the loadslot path to write into hook settings: a stable path
// on the PATH when there is one for this binary (see setup.StableExe).
func setupExe(remove bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// A `go run` binary is deleted when it exits, which would leave a hook
	// that fails on every Bash call.
	if !remove && strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "", errors.New("run setup from an installed loadslot, not `go run`")
	}
	return setup.StableExe(exe), nil
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
		Version:     daemon.Version(),
	}
	err = daemon.Run(p, srv)
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(os.Stderr, "loadslot: daemon already running")
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
	if s.Version != "" {
		mode += " " + s.Version
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
		fmt.Fprintln(tw, "  ID\tWEIGHT\tEST MB\tNOW MB\tSAMPLES\tPRIORITY\tSOURCE\tAGE\tCOMMAND\tDIR")
		for _, j := range jobs {
			now := "-"
			if j.Reported {
				now = strconv.FormatUint(j.CurrentMB, 10)
			}
			source := j.Source
			if source == "" {
				source = "-"
			}
			fmt.Fprintf(tw, "  %d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Slots, j.EstimateMB, now, j.Samples, j.Priority, source,
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

// hookExe is the loadslot the hook's rewritten commands run: this binary, so
// they need no PATH lookup, unless it is a `go run` build that is about to
// be deleted.
func hookExe() string {
	exe, err := os.Executable()
	if err != nil || strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "loadslot"
	}
	return exe
}
