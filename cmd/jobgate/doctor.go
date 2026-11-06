package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ryusenkei-inc/jobgate/internal/daemon"
	"github.com/ryusenkei-inc/jobgate/internal/platform"
	"github.com/ryusenkei-inc/jobgate/internal/setup"
)

// check is one line of `jobgate doctor`.
type check struct {
	level string // ok, info, warn, error
	text  string
}

// cmdDoctor reports whether jobgate is installed and wired up, with the fix
// for each problem. It exits 1 when there are warnings or errors.
func cmdDoctor(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "jobgate doctor: takes no arguments")
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	checks := []check{{"ok", version() + " at " + exe}}
	checks = append(checks, pathCheck(exe))
	if p, err := nativePlatform(); err == nil {
		checks = append(checks, daemonCheck(p))
	} else {
		checks = append(checks, check{"error", "data directory: " + err.Error()})
	}
	for _, a := range setup.Agents {
		if c, ok := hookCheck(a, exe); ok {
			checks = append(checks, c)
		}
	}
	if os.Getenv("JOBGATE_DISABLE") == "1" {
		checks = append(checks, check{"warn", "JOBGATE_DISABLE=1 is set: commands run without the gate"})
	}
	return printChecks(os.Stdout, checks)
}

func pathCheck(exe string) check {
	onPath, err := exec.LookPath("jobgate")
	if err != nil {
		return check{"info", "jobgate is not on the PATH. Hooks use absolute paths and work anyway; add its directory to the PATH to run jobgate yourself"}
	}
	if sameFile(onPath, exe) {
		return check{"ok", "on the PATH as " + onPath}
	}
	return check{"warn", "jobgate on the PATH (" + onPath + ") is another file than this one. Remove the copy you don't use, or ignore this for a package manager's shim"}
}

func daemonCheck(p *platform.Platform) check {
	s, err := daemon.QueryStatus(p)
	if err != nil {
		return check{"info", "daemon not running; the next jobgate run starts it"}
	}
	text := fmt.Sprintf("daemon pid %d %s, slots %d/%d used, %d MB free", s.PID, s.Version, s.Snapshot.Used, s.Snapshot.Slots, s.Memory.FreeMB)
	if daemon.Older(s.Version, daemon.Version()) {
		return check{"warn", text + ": older than this jobgate; the next jobgate run retires it"}
	}
	return check{"ok", text}
}

// hookCheck checks an agent's hook. It reports nothing for an agent that is
// neither installed nor configured.
func hookCheck(a setup.Agent, exe string) (check, bool) {
	file, err := setup.SettingsPath(a)
	if err != nil {
		return check{"error", string(a) + ": " + err.Error()}, true
	}
	command, found, err := setup.Find(file, a)
	switch {
	case err != nil:
		return check{"error", fmt.Sprintf("%s: cannot read %s: %v", a, file, err)}, true
	case !found && !setup.Detected(a):
		return check{}, false
	case !found:
		return check{"warn", fmt.Sprintf("%s: hook not registered in %s. Run: jobgate setup", a, file)}, true
	}
	hookExe := filepath.FromSlash(setup.HookExe(command, a))
	if _, err := os.Stat(hookExe); err != nil {
		if _, lookErr := exec.LookPath(hookExe); lookErr != nil {
			return check{"error", fmt.Sprintf("%s: the hook runs %s, which does not exist. Run: jobgate setup", a, hookExe)}, true
		}
	}
	if resolved, err := exec.LookPath(hookExe); err == nil && !sameFile(resolved, exe) {
		return check{"warn", fmt.Sprintf("%s: the hook runs another jobgate (%s). Run: jobgate setup", a, hookExe)}, true
	}
	return check{"ok", fmt.Sprintf("%s: hook registered in %s", a, file)}, true
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
		fmt.Fprintf(w, "%-5s  %s\n", c.level, c.text)
		if c.level == "warn" || c.level == "error" {
			code = 1
		}
	}
	return code
}
