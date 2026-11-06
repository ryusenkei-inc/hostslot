package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ryusenkei-inc/hostslot/internal/daemon"
	"github.com/ryusenkei-inc/hostslot/internal/setup"
)

// cmdSetup registers the hooks of the given agent, or of every installed
// agent when none is named.
func cmdSetup(args []string) int {
	agents := setup.Agents
	named := len(args) > 0 && !strings.HasPrefix(args[0], "-")
	if named {
		if !slices.Contains(setup.Agents, setup.Agent(args[0])) {
			fmt.Fprintf(os.Stderr, "hostslot setup: unknown agent %q (claude, codex)\n", args[0])
			return 2
		}
		agents = []setup.Agent{setup.Agent(args[0])}
		args = args[1:]
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "remove hostslot's hooks instead of adding them")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	path := fs.String("settings", "", "hooks file (only with an agent; default: the agent's user-level file)")
	asJSON := fs.Bool("json", false, "print what changed as JSON (for integrations such as IDE plugins)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path != "" && !named {
		fmt.Fprintln(os.Stderr, "hostslot setup: --settings needs an agent (hostslot setup claude --settings PATH)")
		return 2
	}
	exe, err := setupExe(*remove)
	if err != nil {
		return fail(err)
	}
	var report setupReport
	say := func(format string, a ...any) {
		if !*asJSON {
			fmt.Printf(format+"\n", a...)
		}
	}
	for _, a := range agents {
		if !named && !setup.Detected(a) {
			continue
		}
		file := *path
		if file == "" {
			if file, err = setup.SettingsPath(a); err != nil {
				return fail(err)
			}
		}
		command := setup.HookCommand(exe, a)
		res, err := setup.Register(file, a, command, *remove, *dryRun)
		if err != nil {
			return fail(err)
		}
		verb := map[setup.Result]string{
			setup.Unchanged: "already up to date",
			setup.Added:     "added the PreToolUse hook `" + command + "`",
			setup.Updated:   "updated the PreToolUse hook to `" + command + "`",
			setup.Removed:   "removed hostslot's PreToolUse hook",
		}[res]
		if *dryRun && res != setup.Unchanged {
			verb = "would have " + verb
		}
		say("%s: %s", file, verb)
		if a == setup.Codex && !*dryRun && (res == setup.Added || res == setup.Updated) {
			say("Codex runs a new or changed hook only after you trust it: start Codex and review it with /hooks. It gates sessions without approvals and outside the sandbox (full access)")
		}
		report.Agents = append(report.Agents, agentResult{Agent: string(a), File: file, Result: res.String()})
	}
	if len(report.Agents) == 0 {
		say("No supported agent found (Claude Code, Codex). Install one, or name it: hostslot setup claude")
	}
	// Setup follows an install or upgrade, so an older daemon is retired now
	// rather than at the next run (its running jobs still finish).
	if !*remove && !*dryRun {
		if p, err := nativePlatform(); err == nil && daemon.RetireOlder(p, daemon.Version()) {
			report.Retired = true
			say("Retired the older daemon; the next hostslot run starts this version's")
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}
	return 0
}

// setupReport is `hostslot setup --json`.
type setupReport struct {
	Agents  []agentResult `json:"agents"`
	Retired bool          `json:"retired"`
}

// agentResult is what setup did to one agent's hooks file: unchanged,
// added, updated or removed.
type agentResult struct {
	Agent  string `json:"agent"`
	File   string `json:"file"`
	Result string `json:"result"`
}

// setupExe is the hostslot path to write into hook settings: a stable path
// on the PATH when there is one for this binary (see setup.StableExe).
func setupExe(remove bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// A `go run` binary is deleted when it exits, which would leave a hook
	// that fails on every Bash call.
	if !remove && strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "", errors.New("run setup from an installed hostslot, not `go run`")
	}
	return setup.StableExe(exe), nil
}
