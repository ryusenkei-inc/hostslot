// Package runner implements `loadslot run`: acquire a slot, run the command
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

	"github.com/ryusenkei-inc/loadslot/internal/config"
	"github.com/ryusenkei-inc/loadslot/internal/daemon"
	"github.com/ryusenkei-inc/loadslot/internal/gate"
	"github.com/ryusenkei-inc/loadslot/internal/jobkey"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/store"
)

// Options configure one run.
type Options struct {
	Args []string
	// Label names the work for learning (default: the command line), so a
	// wrapper such as `sh -c '<line>'` can learn as the heavy command inside.
	Label string
	// Source names who started the job (`--source`, or LOADSLOT_SOURCE).
	Source   string
	Weight   int
	Priority gate.Priority
	Dir      string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	// NoDaemon forces the lock fallback (LOADSLOT_NO_DAEMON=1).
	NoDaemon bool
	// Disable runs the command without the gate (LOADSLOT_DISABLE=1).
	Disable bool
	// Nested runs the command without the gate because an outer job already
	// holds a slot (LOADSLOT_JOB is set). Waiting for a second slot could
	// deadlock when the outer job holds the last one.
	Nested bool
	// MaxWait gives up waiting for a slot after this long (0 = wait forever)
	// and exits with ExitBusy. Agents' tool calls have a timeout that the
	// wait counts toward.
	MaxWait time.Duration
	// Timeout kills the command's process tree after this long once it runs
	// (0 = none) and exits with ExitTimeout.
	Timeout time.Duration
}

// Exit codes of loadslot itself.
const (
	// ExitBusy: no slot within MaxWait (EX_TEMPFAIL, retry later).
	ExitBusy = 75
	// ExitTimeout: the command ran longer than Timeout (as GNU timeout).
	ExitTimeout = 124
)

// JobVar marks the environment of a gated command, so loadslot runs nested
// in it pass through.
const JobVar = "LOADSLOT_JOB"

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
		code, _, err := execute(p, o, os.Environ(), nil, nil)
		return code, err
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
		fmt.Fprintf(o.Stderr, "loadslot: no free slot within %s; the machine is busy (see `loadslot status`). Retry later, or allow a longer wait.\n", o.MaxWait)
		return ExitBusy, nil
	}
	if err != nil {
		return 2, err
	}
	workers := gate.Workers(l.cpus, l.job.Slots, l.slots)
	env := append(withWorkers(os.Environ(), workers), JobVar+"=1")
	start := time.Now()
	var peak uint64
	code, timedOut, err := execute(p, o, env, &peak, l.report)
	sample := &store.Sample{PeakMB: peak, DurationMS: time.Since(start).Milliseconds(), ExitCode: code, At: time.Now()}
	// A killed run's peak is only a lower bound of its cost.
	if err != nil || timedOut {
		sample = nil
	}
	l.release(sample)
	return code, err
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
	if !has["LOADSLOT_WORKERS"] {
		env = append(env, "LOADSLOT_WORKERS="+w)
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

// execute runs the command; timedOut reports that o.Timeout killed it.
// report, when set, receives the tree's current memory every usageEvery.
func execute(p *platform.Platform, o Options, env []string, peak *uint64, report func(uint64)) (code int, timedOut bool, err error) {
	cmd := exec.Command(o.Args[0], o.Args[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr

	// The child shares the console or terminal, so it receives Ctrl+C
	// itself. loadslot stays alive to release the slot; a second signal kills
	// the tree.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	proc, err := p.Tracker.Start(cmd)
	if err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			fmt.Fprintf(o.Stderr, "loadslot: %v\n", err)
			return 127, false, nil
		}
		return 1, false, err
	}
	waited := make(chan error, 1)
	go func() { waited <- proc.Wait() }()
	var expired <-chan time.Time
	if o.Timeout > 0 {
		t := time.NewTimer(o.Timeout)
		defer t.Stop()
		expired = t.C
	}
	var usage <-chan time.Time
	if report != nil {
		t := time.NewTicker(usageEvery)
		defer t.Stop()
		usage = t.C
	}
	var werr error
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
			timedOut = true
			_ = proc.KillTree()
		case <-usage:
			if mb := proc.CurrentMB(); mb > 0 {
				report(mb)
			}
		}
	}
	if peak != nil {
		*peak = proc.PeakMB()
	}
	if timedOut {
		fmt.Fprintf(o.Stderr, "loadslot: the command ran longer than %s and was stopped\n", o.Timeout)
		return ExitTimeout, true, nil
	}
	return exitCode(cmd, werr), false, nil
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
