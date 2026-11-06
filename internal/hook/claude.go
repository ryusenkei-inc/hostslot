// Package hook rewrites the commands of coding agents to run through
// jobgate. It only transforms text; it never runs anything.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// DefaultHeavy matches one shell command (after leading variable
// assignments) that is worth gating: package scripts that check, test or
// build, test runners, type checkers and compilers. Light commands such as
// formatters, prose linters and git stay ungated, so they never wait.
var DefaultHeavy = regexp.MustCompile(`^(?:` +
	`(?:pnpm|npm|yarn|bun)\s+(?:(?:-r|--recursive|-w|--workspace-root|--if-present|(?:--filter|-F)\s+\S+)\s+)*(?:run\s+)?(?:(?:check|test|build|lint|typecheck)(?:\s|$)|(?:test|build):)` +
	`|(?:npx\s+|pnpm\s+(?:exec|dlx)\s+|yarn\s+|bunx\s+)?(?:vitest|jest|tsc|playwright)(?:\s|$)` +
	`|go\s+(?:test|build|vet)(?:\s|$)` +
	`|cargo\s+(?:test|build|check|clippy)(?:\s|$)` +
	`)`)

var (
	separators = regexp.MustCompile(`&&|\|\||[;|\n]`)
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s+`)
)

// ClaudeOptions configure the Claude Code hook.
type ClaudeOptions struct {
	// Heavy matches the commands to gate (DefaultHeavy when nil).
	Heavy *regexp.Regexp
	// Weight is passed to `jobgate run --weight` (1 when below 1).
	Weight int
}

type claudeInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// Claude reads a Claude Code PreToolUse event from in and, when it is a
// Bash call with a heavy command, writes a response that replaces the
// command with `jobgate run ... -- sh -c '<command>'`. Other events produce
// no output, which leaves the call unchanged.
func Claude(in io.Reader, out io.Writer, o ClaudeOptions) error {
	var ev claudeInput
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if ev.ToolName != "Bash" {
		return nil
	}
	command, _ := ev.ToolInput["command"].(string)
	if !IsHeavy(command, o.Heavy) {
		return nil
	}
	input := make(map[string]any, len(ev.ToolInput))
	for k, v := range ev.ToolInput {
		input[k] = v
	}
	input["command"] = Wrap(command, o.Weight)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse",
			"updatedInput":  input,
		},
	})
}

// IsHeavy reports whether any command of a shell command line matches
// heavy (DefaultHeavy when nil). A line that already starts with jobgate is
// never heavy, so the hook does not wrap twice.
func IsHeavy(line string, heavy *regexp.Regexp) bool {
	if heavy == nil {
		heavy = DefaultHeavy
	}
	if strings.HasPrefix(strings.TrimSpace(line), "jobgate ") {
		return false
	}
	for _, part := range separators.Split(line, -1) {
		part = strings.TrimSpace(part)
		for assignment.MatchString(part) {
			part = assignment.ReplaceAllString(part, "")
		}
		if part != "" && heavy.MatchString(part) {
			return true
		}
	}
	return false
}

// Wrap returns line as one jobgate job. Agents are not at a terminal, so
// the job runs at background priority.
func Wrap(line string, weight int) string {
	return "jobgate run --weight " + strconv.Itoa(max(weight, 1)) + " --priority background -- sh -c " + shellQuote(line)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
