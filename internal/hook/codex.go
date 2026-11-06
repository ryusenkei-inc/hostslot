package hook

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// CodexOptions configure the Codex hook.
type CodexOptions struct {
	// Heavy matches the commands to gate (DefaultHeavy when nil).
	Heavy *regexp.Regexp
	// Weight is passed to `jobgate run --weight` (1 when below 1).
	Weight int
	// PowerShell is the PowerShell executable to run the original command
	// with on Windows, where Codex runs commands in PowerShell; empty runs
	// it with `sh -c`, as on Linux and macOS.
	PowerShell string
}

type codexInput struct {
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	PermissionMode string         `json:"permission_mode"`
}

// Codex reads a Codex PreToolUse event from in and, when it is a shell call
// with a heavy command, writes a response that replaces the command with
// `jobgate run ...`. Codex applies a rewrite only together with an "allow"
// decision, which also skips its approval of the call. So the hook rewrites
// only in sessions that never ask for approval (permission_mode
// "bypassPermissions"); elsewhere it leaves the call alone rather than
// approve commands on the user's behalf.
func Codex(in io.Reader, out io.Writer, o CodexOptions) error {
	var ev codexInput
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if ev.ToolName != "Bash" || ev.PermissionMode != "bypassPermissions" {
		return nil
	}
	command, _ := ev.ToolInput["command"].(string)
	heavy := Heavy(command, o.Heavy)
	if len(heavy) == 0 {
		return nil
	}
	input := make(map[string]any, len(ev.ToolInput))
	for k, v := range ev.ToolInput {
		input[k] = v
	}
	input["command"] = WrapCodex(command, strings.Join(heavy, " && "), o.Weight, o.PowerShell)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": "jobgate: waits for a free memory slot before running " + strings.Join(heavy, " && "),
			"updatedInput":             input,
		},
	})
}

// WrapCodex returns line as one jobgate job of Codex. With powerShell set,
// the result is itself a PowerShell command line and the original line runs
// in PowerShell from -EncodedCommand (UTF-16LE base64), which needs no
// quoting at all; otherwise it is a POSIX shell line like Wrap's.
func WrapCodex(line, label string, weight int, powerShell string) string {
	run := "jobgate run --weight " + strconv.Itoa(max(weight, 1)) + " --priority background --source codex --as "
	if powerShell == "" {
		return run + shellQuote(label) + " -- sh -c " + shellQuote(line)
	}
	return run + powerShellQuote(label) + " -- " + powerShellQuote(powerShell) +
		" -NoProfile -NonInteractive -EncodedCommand " + encodePowerShell(line)
}

func powerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func encodePowerShell(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		b[2*i] = byte(u)
		b[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
