// Package setup registers jobgate's agent hooks in the agents' settings
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

// ClaudeSettingsPath is the user settings file of Claude Code
// ($CLAUDE_CONFIG_DIR/settings.json, or ~/.claude/settings.json).
func ClaudeSettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

const claudeHookArgs = " hook claude"

// HookCommand is the hook command for the jobgate executable at exe. Claude
// Code runs it through a shell, so a path with spaces is quoted.
func HookCommand(exe string) string {
	exe = filepath.ToSlash(exe)
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	return exe + claudeHookArgs
}

// Result says what Claude changed or would change.
type Result int

const (
	// Unchanged: the settings already were as asked.
	Unchanged Result = iota
	// Added: the hook was registered.
	Added
	// Updated: an existing jobgate hook now points at a different command.
	Updated
	// Removed: jobgate hooks were removed.
	Removed
)

func (r Result) String() string {
	return [...]string{"unchanged", "added", "updated", "removed"}[r]
}

// Claude registers command as a PreToolUse hook for Bash in the settings
// file at path, or removes jobgate's hooks when remove is set. The entry is
// appended after existing PreToolUse groups. With dryRun, nothing is
// written. A missing file is created.
func Claude(path, command string, remove, dryRun bool) (Result, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte("{}")
	} else if err != nil {
		return Unchanged, err
	}
	out, res, err := editClaude(data, command, remove)
	if err != nil {
		return Unchanged, fmt.Errorf("%s: %w", path, err)
	}
	if res == Unchanged || dryRun {
		return res, nil
	}
	return res, writeAtomic(path, out)
}

// editClaude returns the edited settings, formatted with two spaces as
// Claude Code writes them.
func editClaude(data []byte, command string, remove bool) ([]byte, Result, error) {
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
		current, ours := jobgateCommand(g)
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
			kept = append(kept, claudeGroup(command))
		}
	}
	if !remove && !found {
		kept = append(kept, claudeGroup(command))
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

// claudeHook and claudeGroupJSON fix the key order to the one Claude Code's
// documentation and other tools use.
type claudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type claudeGroupJSON struct {
	Matcher string       `json:"matcher"`
	Hooks   []claudeHook `json:"hooks"`
}

func claudeGroup(command string) json.RawMessage {
	raw, _ := json.Marshal(claudeGroupJSON{Matcher: "Bash", Hooks: []claudeHook{{Type: "command", Command: command, Timeout: 10}}})
	return raw
}

// jobgateCommand returns the command of a group that runs `jobgate hook
// claude`, whatever path the executable has.
func jobgateCommand(group json.RawMessage) (string, bool) {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return "", false
	}
	for _, h := range g.Hooks {
		exe, ok := strings.CutSuffix(strings.TrimSpace(h.Command), claudeHookArgs)
		exe = strings.Trim(strings.TrimSpace(exe), `"'`)
		if ok && strings.Contains(strings.ToLower(filepath.Base(filepath.FromSlash(exe))), "jobgate") {
			return h.Command, true
		}
	}
	return "", false
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
