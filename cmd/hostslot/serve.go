package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/ports"
	"github.com/ryusenkei-inc/hostslot/internal/runner"
)

// How long a dev server may take to listen before serve says it seems not
// to use the port it was given.
const (
	listenGrace = 30 * time.Second
	listenPoll  = 2 * time.Second
)

// cmdServe runs a dev server on the port of its service in this worktree.
// It takes no capacity slot: a dev server runs for hours.
func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	name := fs.String("name", "", "service name (default: the package's name without its scope, else its directory)")
	def := fs.Int("default", 0, "the port it uses by default (known for Vite and Next.js)")
	fixed := fs.Int("fixed", 0, "use exactly this port, held by one worktree at a time (an OAuth callback, say)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	command := fs.Args()
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "hostslot serve: missing command (hostslot serve -- pnpm dev)")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	plan := ports.Detect(command, dir)
	if *name != "" {
		plan.Name = *name
	}
	p, err := nativePlatform()
	if err != nil {
		return fail(err)
	}
	if err := p.Paths.Ensure(); err != nil {
		return fail(err)
	}

	var port int
	if *fixed > 0 {
		port = *fixed
		if err := holdFixed(p, port); err != nil {
			return fail(err)
		}
	} else {
		if *def > 0 {
			plan.Default = *def
		}
		unlock, err := lockPorts(p)
		if err != nil {
			return fail(err)
		}
		port, err = ports.Open(filepath.Join(p.Paths.Data, ports.FileName)).Assign(ports.For(dir, plan.Name), plan.Default, time.Now())
		unlock()
		if err != nil {
			return fail(err)
		}
	}

	value := strconv.Itoa(port)
	for _, k := range []string{"PORT", ports.EnvName(plan.Name), "HOSTSLOT_PORT"} {
		_ = os.Setenv(k, value)
	}
	fmt.Fprintf(os.Stderr, "hostslot: %s on http://localhost:%d (PORT=%d)\n", plan.Name, port, port)
	go warnIfNotListening(port, plan.Name)

	code, err := runner.Run(p, runner.Options{
		Args:    plan.Apply(command, value),
		Dir:     dir,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Disable: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hostslot: %v\n", err)
	}
	return code
}

// holdFixed takes the machine-wide lease of a fixed port for the life of
// this process, waiting while another worktree's dev server holds it.
func holdFixed(p *platform.Platform, port int) error {
	said := false
	for {
		_, ok, err := p.Locks.TryLock("port-" + strconv.Itoa(port))
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !said {
			fmt.Fprintf(os.Stderr, "hostslot: port %d is held by another worktree's dev server; waiting (Ctrl+C to stop)\n", port)
			said = true
		}
		time.Sleep(time.Second)
	}
}

// warnIfNotListening tells, once, when nothing listens on the port after a
// while: the app likely ignores PORT and listens elsewhere, so agents and
// tests would reach another worktree's server.
func warnIfNotListening(port int, name string) {
	for waited := time.Duration(0); waited < listenGrace; waited += listenPoll {
		time.Sleep(listenPoll)
		if !ports.Available(port) {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "hostslot: nothing listens on %d after %s. If %s uses another port, make it read PORT (or %s), or put {port} in the command\n",
		port, listenGrace, name, ports.EnvName(name))
}
