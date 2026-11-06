package setup

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// Agents are the agents `loadslot setup` registers when they are installed.
var Agents = []Agent{Claude, Codex}

// Detected reports whether the agent looks installed: its settings
// directory exists or its CLI is on the PATH.
func Detected(a Agent) bool {
	if path, err := SettingsPath(a); err == nil {
		if st, err := os.Stat(filepath.Dir(path)); err == nil && st.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath(string(a))
	return err == nil
}

// StableExe returns the path to write into hook settings for the running
// executable exe. When `loadslot` on the PATH is the same file, such as a
// package manager's symlink (Homebrew's bin, WinGet's Links), that path
// survives upgrades while exe, resolved to a versioned directory, does not.
// A different file on the PATH (a shim, another copy) is not used: a shim
// adds hundreds of milliseconds to every hooked call.
func StableExe(exe string) string {
	onPath, err := exec.LookPath("loadslot")
	if err != nil {
		return exe
	}
	if abs, err := filepath.Abs(onPath); err == nil {
		onPath = abs
	}
	if sameFile(onPath, exe) {
		return onPath
	}
	return exe
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

// Find returns the command of the agent's loadslot hook in the file at path,
// or found false when none is registered or the file does not exist.
func Find(path string, a Agent) (command string, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	root, err := parseObject(data)
	if err != nil {
		return "", false, err
	}
	hooks, err := root.object("hooks")
	if err != nil {
		return "", false, err
	}
	raw, ok := hooks.get("PreToolUse")
	if !ok {
		return "", false, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return "", false, err
	}
	for _, g := range groups {
		if c, ours := loadslotCommand(g, a); ours {
			return c, true, nil
		}
	}
	return "", false, nil
}

// HookExe returns the executable a registered hook command runs, without
// the quotes and arguments HookCommand adds.
func HookExe(command string, a Agent) string {
	exe, _ := cutHookArgs(command, a)
	return exe
}
