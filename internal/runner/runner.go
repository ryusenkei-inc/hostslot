// Package runner implements `jobgate run`: acquire a slot, run the command
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

	"github.com/ryusenkei-inc/jobgate/internal/config"
	"github.com/ryusenkei-inc/jobgate/internal/gate"
	"github.com/ryusenkei-inc/jobgate/internal/jobkey"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/store"
)

// Options configure one run.
type Options struct {
	Args []string
	// Label names the work for learning (default: the command line), so a
	// wrapper such as `sh -c '<line>'` can learn as the heavy command inside.
	Label    string
	Weight   int
	Priority gate.Priority
	Dir      string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	// NoDaemon forces the lock fallback (JOBGATE_NO_DAEMON=1).
	NoDaemon bool
	// Disable runs the command without the gate (JOBGATE_DISABLE=1).
	Disable bool
	// Nested runs the command without the gate because an outer job already
	// holds a slot (JOBGATE_JOB is set). Waiting for a second slot could
	// deadlock when the outer job holds the last one.
	Nested bool
}

// JobVar marks the environment of a gated command, so jobgate runs nested
// in it pass through.
const JobVar = "JOBGATE_JOB"

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
		return execute(p, o, os.Environ(), nil)
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
	}
	l, err := acquire(p, cfg, req, o.NoDaemon, o.Stderr)
	if err != nil {
		return 2, err
	}
	workers := gate.Workers(l.cpus, l.job.Slots, l.slots)
	env := append(withWorkers(os.Environ(), workers), JobVar+"=1")
	start := time.Now()
	var peak uint64
	code, err := execute(p, o, env, &peak)
	sample := &store.Sample{PeakMB: peak, DurationMS: time.Since(start).Milliseconds(), ExitCode: code, At: time.Now()}
	if err != nil {
		sample = nil
	}
	l.release(sample)
	return code, err
}

// withWorkers returns env with the inner parallelism variables added.
func withWorkers(env []string, workers int) []string {
	has := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		has[strings.ToUpper(k)] = true
	}
	w := strconv.Itoa(workers)
	if !has["JOBGATE_WORKERS"] {
		env = append(env, "JOBGATE_WORKERS="+w)
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

func execute(p *platform.Platform, o Options, env []string, peak *uint64) (int, error) {
	cmd := exec.Command(o.Args[0], o.Args[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr

	// The child shares the console or terminal, so it receives Ctrl+C
	// itself. jobgate stays alive to release the slot; a second signal kills
	// the tree.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	proc, err := p.Tracker.Start(cmd)
	if err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			fmt.Fprintf(o.Stderr, "jobgate: %v\n", err)
			return 127, nil
		}
		return 1, err
	}
	waited := make(chan error, 1)
	go func() { waited <- proc.Wait() }()
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
		}
	}
	if peak != nil {
		*peak = proc.PeakMB()
	}
	return exitCode(cmd, werr), nil
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
