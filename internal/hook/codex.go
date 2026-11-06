package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CodexOptions configure the Codex hook.
type CodexOptions struct {
	// Heavy matches the commands to gate (DefaultHeavy when nil).
	Heavy *regexp.Regexp
	// Weight is passed to `loadslot run --weight` (1 when below 1).
	Weight int
	// Exe is the loadslot executable the rewritten command runs ("loadslot"
	// from the PATH when empty).
	Exe string
	// PowerShell is set on Windows, where Codex runs commands in PowerShell:
	// the prefix is then written in PowerShell syntax.
	PowerShell bool
}

type codexInput struct {
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	Cwd            string         `json:"cwd"`
	PermissionMode string         `json:"permission_mode"`
}

// Codex reads a Codex PreToolUse event from in and, when it is a shell call
// with heavy commands, writes a response that puts `loadslot run … --` before
// each of them, as Claude does. Codex applies a rewrite only together with
// an "allow" decision, which may also skip its approval of the call, so the
// hook rewrites only in sessions that never ask for approval
// (permission_mode "bypassPermissions"); elsewhere it leaves the call alone
// rather than approve commands on the user's behalf.
//
// Inside Codex's sandbox loadslot may not reach its daemon (on Windows the
// sandbox is another user); the command then runs ungated, unchanged.
func Codex(in io.Reader, out io.Writer, o CodexOptions) error {
	var ev codexInput
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if ev.ToolName != "Bash" || ev.PermissionMode != "bypassPermissions" {
		return nil
	}
	command, _ := ev.ToolInput["command"].(string)
	rewritten, labels, ok := GateInPlace(command, ev.Cwd, o.Heavy, func(label string, _ int) string {
		return codexPrefix(o.Exe, label, o.Weight, o.PowerShell)
	})
	if !ok {
		return nil
	}
	input := make(map[string]any, len(ev.ToolInput))
	for k, v := range ev.ToolInput {
		input[k] = v
	}
	input["command"] = rewritten
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": "loadslot: waits for room on this machine before running " + strings.Join(labels, ", "),
			"updatedInput":             input,
		},
	})
}

// codexPrefix is what goes before a heavy command of Codex: a background
// job that learns as label, in POSIX shell or PowerShell syntax.
func codexPrefix(exe, label string, weight int, powerShell bool) string {
	args := " run --weight " + strconv.Itoa(max(weight, 1)) + " --priority background --source codex --as "
	if !powerShell {
		return shellExe(exe) + args + shellQuote(label) + " -- "
	}
	// A quoted path is a string to PowerShell; the call operator runs it.
	call := "loadslot"
	if exe != "" && exe != "loadslot" {
		call = "& " + powerShellQuote(filepath.ToSlash(exe))
	}
	return call + args + powerShellQuote(label) + " -- "
}

// powerShellQuote doubles every character PowerShell reads as a single
// quote, which includes the typographic ones.
func powerShellQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		if strings.ContainsRune("'‘’‚‛", r) {
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}
