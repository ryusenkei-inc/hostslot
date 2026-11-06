package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ryusenkei-inc/loadslot/internal/daemon"
	"github.com/ryusenkei-inc/loadslot/internal/platform"
	"github.com/ryusenkei-inc/loadslot/internal/setup"
)

// check is one line of `loadslot doctor`.
type check struct {
	Level string `json:"level"` // ok, info, warn, error
	Text  string `json:"text"`
	// Fix names the command that fixes the problem ("setup"), so the Orca
	// plugin can offer to run it.
	Fix string `json:"fix,omitempty"`
}

// cmdDoctor reports whether loadslot is installed and wired up, with the fix
// for each problem. It exits 1 when there are warnings or errors.
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the checks as JSON (for integrations such as the Orca plugin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	checks := []check{{Level: "ok", Text: version() + " at " + exe}}
	checks = append(checks, pathCheck(exe))
	if p, err := nativePlatform(); err == nil {
		checks = append(checks, daemonCheck(p))
	} else {
		checks = append(checks, check{Level: "error", Text: "data directory: " + err.Error()})
	}
	for _, a := range setup.Agents {
		if c, ok := hookCheck(a, exe); ok {
			checks = append(checks, c)
		}
	}
	if os.Getenv("LOADSLOT_DISABLE") == "1" {
		checks = append(checks, check{Level: "warn", Text: "LOADSLOT_DISABLE=1 is set: commands run without the gate"})
	}
	if *asJSON {
		code := 0
		for _, c := range checks {
			if c.Level == "warn" || c.Level == "error" {
				code = 1
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(checks)
		return code
	}
	return printChecks(os.Stdout, checks)
}

func pathCheck(exe string) check {
	onPath, err := exec.LookPath("loadslot")
	if err != nil {
		return check{Level: "info", Text: "loadslot is not on the PATH. Hooks use absolute paths and work anyway; add its directory to the PATH to run loadslot yourself"}
	}
	if sameFile(onPath, exe) {
		return check{Level: "ok", Text: "on the PATH as " + onPath}
	}
	// Typical after updating from the Orca panel over a copy installed by
	// hand: the plugin installs into its own directory.
	return check{Level: "warn", Text: fmt.Sprintf("loadslot on the PATH (%s) is another file than this one (%s). To keep this one, delete the other and add %s to the PATH; ignore this for a package manager's shim", onPath, exe, filepath.Dir(exe))}
}

func daemonCheck(p *platform.Platform) check {
	s, err := daemon.QueryStatus(p)
	if err != nil {
		return check{Level: "info", Text: "daemon not running; the next loadslot run starts it"}
	}
	text := fmt.Sprintf("daemon pid %d %s, slots %d/%d used, %d MB free", s.PID, s.Version, s.Snapshot.Used, s.Snapshot.Slots, s.Memory.FreeMB)
	if daemon.Older(s.Version, daemon.Version()) {
		return check{Level: "warn", Text: text + ": older than this loadslot; the next loadslot run retires it"}
	}
	return check{Level: "ok", Text: text}
}

// hookCheck checks an agent's hook. It reports nothing for an agent that is
// neither installed nor configured.
func hookCheck(a setup.Agent, exe string) (check, bool) {
	file, err := setup.SettingsPath(a)
	if err != nil {
		return check{Level: "error", Text: string(a) + ": " + err.Error()}, true
	}
	command, found, err := setup.Find(file, a)
	switch {
	case err != nil:
		return check{Level: "error", Text: fmt.Sprintf("%s: cannot read %s: %v", a, file, err)}, true
	case !found && !setup.Detected(a):
		return check{}, false
	case !found:
		return check{Level: "warn", Text: fmt.Sprintf("%s: hook not registered in %s. Run: loadslot setup", a, file), Fix: "setup"}, true
	}
	hookExe := filepath.FromSlash(setup.HookExe(command, a))
	if _, err := os.Stat(hookExe); err != nil {
		if _, lookErr := exec.LookPath(hookExe); lookErr != nil {
			return check{Level: "error", Text: fmt.Sprintf("%s: the hook runs %s, which does not exist. Run: loadslot setup", a, hookExe), Fix: "setup"}, true
		}
	}
	if resolved, err := exec.LookPath(hookExe); err == nil && !sameFile(resolved, exe) {
		return check{Level: "warn", Text: fmt.Sprintf("%s: the hook runs another loadslot (%s). Run: loadslot setup", a, hookExe), Fix: "setup"}, true
	}
	return check{Level: "ok", Text: fmt.Sprintf("%s: hook registered in %s", a, file)}, true
}

func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

func printChecks(w io.Writer, checks []check) int {
	code := 0
	for _, c := range checks {
		fmt.Fprintf(w, "%-5s  %s\n", c.Level, c.Text)
		if c.Level == "warn" || c.Level == "error" {
			code = 1
		}
	}
	return code
}
