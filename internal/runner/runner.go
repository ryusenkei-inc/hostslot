// Package runner implements `hostslot run`: acquire a slot, run the command
// with its process tree tracked, release and record a learning sample.
package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/config"
	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/jobkey"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/store"
)

// Options configure one run.
type Options struct {
	Args []string
	// Label names the work for learning (default: the command line), so a
	// wrapper such as `sh -c '<line>'` can learn as the heavy command inside.
	Label string
	// Source names who started the job (`--source`, or HOSTSLOT_SOURCE).
	Source   string
	Weight   int
	Priority gate.Priority
	Dir      string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	// NoDaemon forces the lock fallback (HOSTSLOT_NO_DAEMON=1).
	NoDaemon bool
	// Disable runs the command without the gate (HOSTSLOT_DISABLE=1).
	Disable bool
	// Nested runs the command without the gate because an outer job already
	// holds a slot (HOSTSLOT_JOB is set). Waiting for a second slot could
	// deadlock when the outer job holds the last one.
	Nested bool
	// MaxWait gives up waiting for a slot after this long (0 = wait forever)
	// and exits with ExitBusy. Agents' tool calls have a timeout that the
	// wait counts toward.
	MaxWait time.Duration
	// Timeout kills the command's process tree after this long once it runs
	// (0 = none) and exits with ExitTimeout.
	Timeout time.Duration
	// KeepChildren leaves processes the command started running after it
	// exits; by default they are stopped.
	KeepChildren bool
	// MaxMemoryMB stops the command's process tree when it uses more and
	// exits with ExitOverMemory (0: the configured share of total memory).
	MaxMemoryMB uint64
}

// Exit codes of hostslot itself.
const (
	// ExitBusy: no slot within MaxWait (EX_TEMPFAIL, retry later).
	ExitBusy = 75
	// ExitTimeout: the command ran longer than Timeout (as GNU timeout).
	ExitTimeout = 124
	// ExitOverMemory: the command used more memory than its ceiling.
	ExitOverMemory = 77
)

// JobVar marks the environment of a gated command, so hostslot runs nested
// in it pass through.
const JobVar = "HOSTSLOT_JOB"

// InnerParallelismVars are set to the job's worker share unless CI is set
// or the variable is already set. pnpm 10 reads only the npm_config_ prefix
// and pnpm 11 only pnpm_config_, so both are set. npm warns about the
// unknown npm_config_ key, but it is harmless.
var InnerParallelismVars = []string{"VITEST_MAX_WORKERS", "npm_config_workspace_concurrency", "pnpm_config_workspace_concurrency"}

// Run executes the command and returns its exit code. An error means the
// command could not be run at all.
func Run(p *platform.Platform, o Options) (int, error) {
	if len(o.Args) == 0 {
		return 2, errors.New("no command")
	}
	if o.Disable || o.Nested {
		res, err := execute(p, o, os.Environ(), watch{})
		return res.code, err
	}
	cfg, err := config.Load(p.Paths.Data)
	if err != nil {
		return 2, err
	}
	command := strings.Join(o.Args, " ")
	label := o.Label
	if label == "" {
		label = command
	}
	req := gate.Request{
		Command:  command,
		Dir:      o.Dir,
		LearnKey: jobkey.For(o.Dir, label),
		Weight:   max(o.Weight, 1),
		Priority: o.Priority,
		PID:      os.Getpid(),
		Source:   o.Source,
		Context:  jobContext(os.Getenv),
	}
	var deadline time.Time
	if o.MaxWait > 0 {
		deadline = time.Now().Add(o.MaxWait)
	}
	l, err := acquire(p, cfg, req, deadline, o.NoDaemon, o.Stderr)
	if errors.Is(err, daemon.ErrBusy) {
		fmt.Fprintf(o.Stderr, "hostslot: no free slot within %s; the machine is busy (see `hostslot status`). Retry later, or allow a longer wait.\n", o.MaxWait)
		return ExitBusy, nil
	}
	if err != nil {
		return 2, err
	}
	workers := gate.Workers(l.cpus, l.job.Slots, l.slots)
	env := append(withWorkers(os.Environ(), workers), JobVar+"=1")
	start := time.Now()
	res, err := execute(p, o, env, watch{report: l.report, ceilingMB: ceiling(p, cfg, o), stop: l.stopped})
	sample := &store.Sample{PeakMB: res.peakMB, DurationMS: time.Since(start).Milliseconds(), ExitCode: res.code, At: time.Now(), Outcome: res.outcome()}
	if err != nil {
		sample = nil
	}
	l.release(sample)
	return res.code, err
}

// ContextVars are the environment variables recorded with a job, so an
// integration can tell where it came from (Orca sets these in its terminals).
var ContextVars = []string{"ORCA_PANE_KEY", "ORCA_TAB_ID", "ORCA_WORKTREE_ID"}

func jobContext(getenv func(string) string) map[string]string {
	var ctx map[string]string
	for _, k := range ContextVars {
		if v := getenv(k); v != "" {
			if ctx == nil {
				ctx = map[string]string{}
			}
			ctx[k] = v
		}
	}
	return ctx
}

// withWorkers returns env with the inner parallelism variables added.
func withWorkers(env []string, workers int) []string {
	has := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		has[strings.ToUpper(k)] = true
	}
	w := strconv.Itoa(workers)
	if !has["HOSTSLOT_WORKERS"] {
		env = append(env, "HOSTSLOT_WORKERS="+w)
	}
	if has["CI"] {
		return env
	}
	for _, k := range InnerParallelismVars {
		if !has[strings.ToUpper(k)] {
			env = append(env, k+"="+w)
		}
	}
	return env
}

// usageEvery is how often a running job reports its memory to the daemon.
const usageEvery = 2 * time.Second

// ceilingEvery is how often memory is checked against the ceiling: a runaway
// test runner can grow by gigabytes a second.
const ceilingEvery = 500 * time.Millisecond

// watch is what execute observes while the command runs. Zero fields turn
// the observation off.
type watch struct {
	// report receives the tree's current memory every usageEvery.
	report func(currentMB uint64)
	// ceilingMB stops the tree when its current memory passes it.
	ceilingMB uint64
	// stop stops the tree when it is closed: the daemon chose this job to
	// free memory.
	stop <-chan struct{}
}

type result struct {
	code       int
	peakMB     uint64
	timedOut   bool
	overMemory bool
	// machineOut: stopped because the machine was nearly out of memory.
	machineOut bool
}

// outcome names how the run ended, for status and learning.
func (r result) outcome() string {
	switch {
	case r.machineOut:
		return "machine"
	case r.overMemory:
		return "ceiling"
	case r.timedOut:
		return "timeout"
	case r.code == 0:
		return "ok"
	default:
		return "failed"
	}
}

// ceiling is the job's memory ceiling: --max-memory, or the configured share
// of total memory.
func ceiling(p *platform.Platform, cfg gate.Config, o Options) uint64 {
	if o.MaxMemoryMB > 0 {
		return o.MaxMemoryMB
	}
	m, err := p.Metrics.Memory()
	if err != nil {
		return 0
	}
	return gate.Ceiling(m.TotalMB, cfg)
}

// execute runs the command under the watch w.
func execute(p *platform.Platform, o Options, env []string, w watch) (result, error) {
	cmd := exec.Command(o.Args[0], o.Args[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr

	// The child shares the console or terminal, so it receives Ctrl+C
	// itself. hostslot stays alive to release the slot; a second signal kills
	// the tree.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	proc, err := p.Tracker.Start(cmd, platform.StartOptions{Background: o.Priority == gate.Background, Cleanup: !o.KeepChildren})
	if err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			fmt.Fprintf(o.Stderr, "hostslot: %v\n", err)
			return result{code: 127}, nil
		}
		return result{code: 1}, err
	}
	waited := make(chan error, 1)
	go func() { waited <- proc.Wait() }()
	var tickers []*time.Ticker
	defer func() {
		for _, t := range tickers {
			t.Stop()
		}
	}()
	tick := func(enabled bool, every time.Duration) <-chan time.Time {
		if !enabled {
			return nil
		}
		t := time.NewTicker(every)
		tickers = append(tickers, t)
		return t.C
	}
	var expired <-chan time.Time
	if o.Timeout > 0 {
		t := time.NewTimer(o.Timeout)
		defer t.Stop()
		expired = t.C
	}
	usage := tick(w.report != nil, usageEvery)
	check := tick(w.ceilingMB > 0, ceilingEvery)
	var res result
	var werr error
	var overMB uint64
	signals := 0
loop:
	for {
		select {
		case werr = <-waited:
			break loop
		case s := <-sigs:
			signals++
			if signals == 1 {
				forward(cmd.Process, s)
			} else {
				_ = proc.KillTree()
			}
		case <-expired:
			res.timedOut = true
			_ = proc.KillTree()
		case <-usage:
			if mb := proc.CurrentMB(); mb > 0 {
				w.report(mb)
			}
		case <-check:
			if mb := proc.CurrentMB(); mb > w.ceilingMB && !res.overMemory {
				res.overMemory, overMB = true, mb
				_ = proc.KillTree()
			}
		case <-w.stop:
			res.machineOut = true
			w.stop = nil
			_ = proc.KillTree()
		}
	}
	res.peakMB = proc.PeakMB()
	switch {
	case res.machineOut:
		fmt.Fprintln(o.Stderr, "hostslot: the machine was nearly out of memory and this was the largest job running in the background, so it was stopped; run it again when there is room")
		res.code = ExitOverMemory
	case res.overMemory:
		fmt.Fprintf(o.Stderr, "hostslot: the command used %d MB, over its memory ceiling of %d MB, and was stopped (--max-memory or jobCeilingFraction in config.json to change)\n", overMB, w.ceilingMB)
		res.code = ExitOverMemory
	case res.timedOut:
		fmt.Fprintf(o.Stderr, "hostslot: the command ran longer than %s and was stopped\n", o.Timeout)
		res.code = ExitTimeout
	default:
		res.code = exitCode(cmd, werr)
	}
	return res, nil
}

func exitCode(cmd *exec.Cmd, err error) int {
	if st := cmd.ProcessState; st != nil {
		if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return st.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// forward passes signals that did not reach the child on their own. Ctrl+C
// already reaches every process of the terminal or console; Windows has no
// other signal to pass.
func forward(p *os.Process, s os.Signal) {
	if runtime.GOOS == "windows" || s == os.Interrupt {
		return
	}
	_ = p.Signal(s)
}
