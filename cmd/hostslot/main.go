// Command hostslot is a machine-wide gate for heavy local jobs.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/ryusenkei-inc/hostslot/internal/platform"
)

const usage = `hostslot: a machine-wide gate for heavy local jobs

Usage:
  hostslot run [--weight N] [--priority interactive|background] [--as LABEL] [--source NAME]
              [--max-wait DURATION] [--timeout DURATION] [--max-memory MB] [--keep-children]
              -- <command> [args...]
  hostslot status [--json]
  hostslot daemon
  hostslot hook claude|codex [--weight N] [--match REGEX]
  hostslot setup [claude|codex] [--remove] [--dry-run] [--settings PATH]
  hostslot doctor
  hostslot port [--default PORT] [--json] <name> | --list
  hostslot serve [--name NAME] [--default PORT] [--fixed PORT] -- <command> [args...]
  hostslot version

Environment:
  HOSTSLOT_SLOTS       override the computed number of slots
  HOSTSLOT_COST_MB     assumed peak memory of a weight-1 job without samples
  HOSTSLOT_RESERVE_MB  memory kept free for everything else
  HOSTSLOT_PRIORITY    default priority (interactive or background)
  HOSTSLOT_DISABLE=1   run commands directly without the gate
  HOSTSLOT_NO_DAEMON=1 use the machine-wide lock fallback instead of the daemon
  HOSTSLOT_HOME        data directory (also isolates the daemon)
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
	case "port":
		return cmdPort(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "version", "--version":
		fmt.Println(version())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "hostslot: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return "hostslot " + info.Main.Version
	}
	return "hostslot (devel)"
}

// disabled reports whether the gate is turned off.
func disabled() bool {
	return os.Getenv("HOSTSLOT_DISABLE") == "1"
}

func nativePlatform() (*platform.Platform, error) {
	paths, err := platform.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return platform.Native(paths), nil
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "hostslot: %v\n", err)
	return 2
}
