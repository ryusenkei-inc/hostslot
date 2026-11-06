// Package setup registers loadslot's agent hooks in the agents' settings
// files. It edits only its own entries and keeps everything else, including
// the order of keys, so other tools' hooks (an IDE's, for example) survive.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent is a coding agent whose hooks loadslot registers. Claude Code's
// settings file and Codex's hooks.json share the `hooks.PreToolUse` layout.
type Agent string

// The agents loadslot has hooks for.
const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// SettingsPath is the user-level file that holds the agent's hooks:
// $CLAUDE_CONFIG_DIR/settings.json or ~/.claude/settings.json for Claude
// Code, $CODEX_HOME/hooks.json or ~/.codex/hooks.json for Codex.
func SettingsPath(a Agent) (string, error) {
	dirVar, dir, file := "CLAUDE_CONFIG_DIR", ".claude", "settings.json"
	if a == Codex {
		dirVar, dir, file = "CODEX_HOME", ".codex", "hooks.json"
	}
	if d := os.Getenv(dirVar); d != "" {
		return filepath.Join(d, file), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dir, file), nil
}

func (a Agent) hookArgs() string { return " hook " + string(a) }

// HookCommand is the hook command for the loadslot executable at exe. The
// agents run it through a shell (cmd on Windows for Codex), so a path with
// spaces is quoted.
func HookCommand(exe string, a Agent) string {
	exe = filepath.ToSlash(exe)
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	return exe + a.hookArgs()
}

// Result says what Register changed or would change.
type Result int

const (
	// Unchanged: the settings already were as asked.
	Unchanged Result = iota
	// Added: the hook was registered.
	Added
	// Updated: an existing loadslot hook now points at a different command.
	Updated
	// Removed: loadslot hooks were removed.
	Removed
)

func (r Result) String() string {
	return [...]string{"unchanged", "added", "updated", "removed"}[r]
}

// Register adds command as the agent's PreToolUse hook for Bash in the file
// at path, or removes loadslot's hooks for the agent when remove is set. The
// entry is appended after existing PreToolUse groups, which also keeps the
// position-based keys Codex trusts other tools' hooks by. With dryRun,
// nothing is written. A missing file is created.
func Register(path string, a Agent, command string, remove, dryRun bool) (Result, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte("{}")
	} else if err != nil {
		return Unchanged, err
	}
	out, res, err := edit(data, a, command, remove)
	if err != nil {
		return Unchanged, fmt.Errorf("%s: %w", path, err)
	}
	if res == Unchanged || dryRun {
		return res, nil
	}
	return res, writeAtomic(path, out)
}

// edit returns the edited settings, formatted with two spaces as Claude
// Code writes them.
func edit(data []byte, a Agent, command string, remove bool) ([]byte, Result, error) {
	root, err := parseObject(data)
	if err != nil {
		return nil, Unchanged, err
	}
	hooks, err := root.object("hooks")
	if err != nil {
		return nil, Unchanged, err
	}
	var groups []json.RawMessage
	if raw, ok := hooks.get("PreToolUse"); ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, Unchanged, fmt.Errorf("hooks.PreToolUse: %w", err)
		}
	}

	res := Unchanged
	var kept []json.RawMessage
	found := false
	for _, g := range groups {
		current, ours := loadslotCommand(g, a)
		switch {
		case !ours:
			kept = append(kept, g)
		case remove:
			res = Removed
		case found:
			// A duplicate from an earlier manual edit.
			res = Updated
		case current == command:
			found = true
			kept = append(kept, g)
		default:
			found = true
			res = Updated
			kept = append(kept, hookGroup(command))
		}
	}
	if !remove && !found {
		kept = append(kept, hookGroup(command))
		res = Added
	}
	if res == Unchanged {
		return data, res, nil
	}

	if len(kept) == 0 {
		hooks.delete("PreToolUse")
	} else {
		raw, err := json.Marshal(kept)
		if err != nil {
			return nil, Unchanged, err
		}
		hooks.set("PreToolUse", raw)
	}
	if len(hooks) == 0 {
		root.delete("hooks")
	} else {
		root.set("hooks", hooks.marshal())
	}
	var out bytes.Buffer
	if err := json.Indent(&out, root.marshal(), "", "  "); err != nil {
		return nil, Unchanged, err
	}
	out.WriteByte('\n')
	return out.Bytes(), res, nil
}

// hookJSON and groupJSON fix the key order to the one the agents'
// documentation and other tools use.
type hookJSON struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type groupJSON struct {
	Matcher string     `json:"matcher"`
	Hooks   []hookJSON `json:"hooks"`
}

func hookGroup(command string) json.RawMessage {
	raw, _ := json.Marshal(groupJSON{Matcher: "Bash", Hooks: []hookJSON{{Type: "command", Command: command, Timeout: 10}}})
	return raw
}

// loadslotCommand returns the command of a group that runs `loadslot hook
// <agent>`, whatever path the executable has.
func loadslotCommand(group json.RawMessage, a Agent) (string, bool) {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return "", false
	}
	for _, h := range g.Hooks {
		exe, ok := cutHookArgs(h.Command, a)
		if ok && isOurExe(exe) {
			return h.Command, true
		}
	}
	return "", false
}

// cutHookArgs splits a hook command into its unquoted executable and
// reports whether it ends in `hook <agent>`.
func cutHookArgs(command string, a Agent) (string, bool) {
	exe, ok := strings.CutSuffix(strings.TrimSpace(command), a.hookArgs())
	return strings.Trim(strings.TrimSpace(exe), `"'`), ok
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// isOurExe reports whether a hook's executable is loadslot, or jobgate, its
// former name, so setup updates an old entry in place instead of adding a
// second hook.
func isOurExe(exe string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(exe)))
	return strings.Contains(base, "loadslot") || strings.Contains(base, "jobgate")
}
