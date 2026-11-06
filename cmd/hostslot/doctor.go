package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/platform"
	"github.com/ryusenkei-inc/hostslot/internal/ports"
	"github.com/ryusenkei-inc/hostslot/internal/setup"
)

// check is one line of `hostslot doctor`.
type check struct {
	Level string `json:"level"` // ok, info, warn, error
	Text  string `json:"text"`
	// Fix names the command that fixes the problem ("setup"), so an
	// integration can offer to run it.
	Fix string `json:"fix,omitempty"`
	// Title and Detail say a warning or error briefly, for a panel; Text
	// is the full line for the terminal.
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// agentNames are the agents as people know them.
var agentNames = map[setup.Agent]string{setup.Claude: "Claude Code", setup.Codex: "Codex"}

// cmdDoctor reports whether hostslot is installed and wired up, with the fix
// for each problem. It exits 1 when there are warnings or errors.
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the checks as JSON (for integrations such as IDE plugins)")
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
		checks = append(checks, check{Level: "error", Text: "data directory: " + err.Error(),
			Title: "The data directory can't be used", Detail: err.Error()})
	}
	for _, a := range setup.Agents {
		if c, ok := hookCheck(a, exe); ok {
			checks = append(checks, c)
		}
	}
	if dir, err := os.Getwd(); err == nil {
		checks = append(checks, portChecks(dir)...)
	}
	if disabled() {
		checks = append(checks, check{Level: "warn", Text: "HOSTSLOT_DISABLE=1 is set: commands run without the gate",
			Title: "Hostslot is turned off", Detail: "HOSTSLOT_DISABLE=1 is set, so commands run without the gate."})
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
	onPath, err := exec.LookPath("hostslot")
	if err != nil {
		return check{Level: "info", Text: "hostslot is not on the PATH. Hooks use absolute paths and work anyway; add its directory to the PATH to run hostslot yourself"}
	}
	if sameFile(onPath, exe) {
		return check{Level: "ok", Text: "on the PATH as " + onPath}
	}
	// Typical after an IDE plugin updated over a copy installed by hand: the
	// plugin installs into its own directory.
	return check{Level: "warn", Text: fmt.Sprintf("hostslot on the PATH (%s) is another file than this one (%s). To keep this one, delete the other and add %s to the PATH; ignore this for a package manager's shim", onPath, exe, filepath.Dir(exe)),
		Title: "Another hostslot executable is on the PATH", Detail: fmt.Sprintf("%s runs in terminals. Delete it and add %s to the PATH.", onPath, filepath.Dir(exe))}
}

// portChecks warns about package scripts that write a port into their
// command: their dev servers can't take the worktree's own port.
func portChecks(dir string) []check {
	var checks []check
	for _, h := range ports.HardCodedPorts(dir) {
		checks = append(checks, check{
			Level:  "warn",
			Text:   fmt.Sprintf("script %q hard-codes port %s (%s); parallel worktrees would collide on it. Read PORT instead, or put {port} in the command and run it with hostslot serve", h.Script, h.Port, h.Body),
			Title:  "A script hard-codes port " + h.Port,
			Detail: fmt.Sprintf("%s: %s. Read PORT instead, so each worktree gets its own port.", h.Script, h.Body),
		})
	}
	return checks
}

func daemonCheck(p *platform.Platform) check {
	s, err := daemon.QueryStatus(p)
	if err != nil {
		return check{Level: "info", Text: "daemon not running; the next hostslot run starts it"}
	}
	text := fmt.Sprintf("daemon pid %d %s, slots %d/%d used, %d MB free", s.PID, s.Version, s.Snapshot.Used, s.Snapshot.Slots, s.Memory.FreeMB)
	if daemon.Older(s.Version, daemon.Version()) {
		return check{Level: "warn", Text: text + ": older than this hostslot; the next hostslot run retires it", Fix: "setup",
			Title: "An older daemon is running", Detail: fmt.Sprintf("%s (pid %d). The next heavy command replaces it.", s.Version, s.PID)}
	}
	return check{Level: "ok", Text: text}
}

// hookCheck checks an agent's hook. It reports nothing for an agent that is
// neither installed nor configured.
func hookCheck(a setup.Agent, exe string) (check, bool) {
	name := agentNames[a]
	file, err := setup.SettingsPath(a)
	if err != nil {
		return check{Level: "error", Text: string(a) + ": " + err.Error(),
			Title: name + "'s settings can't be found", Detail: err.Error()}, true
	}
	command, found, err := setup.Find(file, a)
	switch {
	case err != nil:
		return check{Level: "error", Text: fmt.Sprintf("%s: cannot read %s: %v", a, file, err),
			Title: name + "'s settings can't be read", Detail: err.Error()}, true
	case !found && !setup.Detected(a):
		return check{}, false
	case !found:
		return check{Level: "warn", Text: fmt.Sprintf("%s: hook not registered in %s. Run: hostslot setup", a, file), Fix: "setup",
			Title: name + " isn't connected", Detail: "No Hostslot hook in " + file + "."}, true
	}
	hookExe := filepath.FromSlash(setup.HookExe(command, a))
	if _, err := os.Stat(hookExe); err != nil {
		if _, lookErr := exec.LookPath(hookExe); lookErr != nil {
			return check{Level: "error", Text: fmt.Sprintf("%s: the hook runs %s, which does not exist. Run: hostslot setup", a, hookExe), Fix: "setup",
				Title: name + "'s hook points to a missing file", Detail: hookExe + " doesn't exist."}, true
		}
	}
	if resolved, err := exec.LookPath(hookExe); err == nil && !sameFile(resolved, exe) {
		return check{Level: "warn", Text: fmt.Sprintf("%s: the hook runs another hostslot (%s). Run: hostslot setup", a, hookExe), Fix: "setup",
			Title: name + "'s hook runs another hostslot executable", Detail: hookExe + "."}, true
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
