package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/ports"
)

// cmdPort prints the port of a service of the current worktree, assigning
// one on first use, or lists every assignment.
func cmdPort(args []string) int {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	def := fs.Int("default", 0, "the port the service uses by default; the first worktree to ask gets it while it is free")
	list := fs.Bool("list", false, "list every assigned port")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*list && fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "hostslot port: name the service (hostslot port web), or --list")
		return 2
	}
	p, err := nativePlatform()
	if err != nil {
		return fail(err)
	}
	if err := p.Paths.Ensure(); err != nil {
		return fail(err)
	}
	unlock, err := lockPorts(p)
	if err != nil {
		return fail(err)
	}
	defer unlock()
	reg := ports.Open(filepath.Join(p.Paths.Data, ports.FileName))

	if *list {
		all, err := reg.List()
		if err != nil {
			return fail(err)
		}
		sorted := ports.Sorted(all, p.Alive)
		if *asJSON {
			return printJSON(sorted)
		}
		for _, e := range sorted {
			state := ""
			if e.Running {
				state = "running"
			}
			fmt.Printf("%-6d %-12s %-8s %s\n", e.Port, e.Name, state, e.Worktree)
		}
		return 0
	}

	name := fs.Arg(0)
	if strings.ContainsAny(name, "\n\r") {
		fmt.Fprintln(os.Stderr, "hostslot port: a name is one line")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	s := ports.For(dir, name)
	port, err := reg.Assign(s, *def, time.Now())
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return printJSON(map[string]any{"port": port, "name": name, "worktree": s.Worktree})
	}
	fmt.Println(port)
	return 0
}

// lockPorts serializes assignments across processes, so two worktrees
// asking at once don't get the same port.
func lockPorts(p *platform.Platform) (func(), error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		unlock, ok, err := p.Locks.TryLock("ports")
		if err != nil {
			return nil, err
		}
		if ok {
			return unlock, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another hostslot is assigning ports; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(err)
	}
	return 0
}
