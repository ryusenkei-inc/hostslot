package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ryusenkei-inc/hostslot/internal/config"
	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
)

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
	cfg = cfg.ForMachine(mem.TotalMB)
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
		Room:     slots - used,
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
	fmt.Fprintf(w, "slots:   %d/%d used, room for %d more\n", s.Snapshot.Used, s.Snapshot.Slots, s.Room)
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
	if len(s.Recent) == 0 {
		return
	}
	const recentShown = 5
	fmt.Fprintf(w, "\nrecent (%d)\n", len(s.Recent))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  OUTCOME\tPEAK MB\tTOOK\tENDED\tCOMMAND\tDIR")
	for _, f := range s.Recent[:min(len(s.Recent), recentShown)] {
		label := f.Command
		if i := strings.LastIndexByte(f.Key, '\n'); i >= 0 {
			label = f.Key[i+1:]
		}
		fmt.Fprintf(tw, "  %s\t%d\t%s\t%s ago\t%s\t%s\n", f.Outcome, f.PeakMB, (time.Duration(f.DurationMS) * time.Millisecond).Round(time.Second),
			time.Since(f.Ended).Round(time.Second), truncate(label, 60), f.Dir)
	}
	tw.Flush()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-3]) + "..."
}
