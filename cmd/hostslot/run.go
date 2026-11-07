package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ryusenkei-inc/hostslot/internal/gate"
	"github.com/ryusenkei-inc/hostslot/internal/runner"
)

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	weight := fs.Int("weight", 1, "relative cost of the job in slots; a hint until samples exist")
	prio := fs.String("priority", "", "interactive or background (default: interactive when stdin is a terminal)")
	label := fs.String("as", "", "name the work for learning (default: the command line)")
	source := fs.String("source", os.Getenv("HOSTSLOT_SOURCE"), "who starts the job, shown in status (default: $HOSTSLOT_SOURCE)")
	maxWait := fs.Duration("max-wait", 0, "give up waiting for a slot after this long and exit 75 (0 = wait forever)")
	timeout := fs.Duration("timeout", 0, "stop the command after it has run this long and exit 124 (0 = none)")
	keepChildren := fs.Bool("keep-children", false, "leave processes the command started running after it exits")
	maxMemory := fs.Uint64("max-memory", 0, "stop the command when it uses more MB and exit 77 (default: jobCeilingFraction of total memory)")
	gpuMB := fs.Uint64("gpu-mb", 0, "GPU memory in MB the command needs; it waits until the GPU has that much free")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(os.Stderr, "hostslot run: missing command (hostslot run -- <command> [args...])")
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
		GPUMB:        *gpuMB,
		Weight:       *weight,
		Priority:     priority,
		Dir:          dir,
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		NoDaemon:     os.Getenv("HOSTSLOT_NO_DAEMON") == "1",
		Disable:      disabled(),
		Nested:       os.Getenv(runner.JobVar) != "",
	})
	if err != nil {
		return fail(err)
	}
	return code
}

func resolvePriority(flagValue string) (gate.Priority, error) {
	v := flagValue
	if v == "" {
		v = os.Getenv("HOSTSLOT_PRIORITY")
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
