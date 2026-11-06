// Package hook rewrites the commands of coding agents to run through
// hostslot. It only transforms text; it never runs anything.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
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

// ClaudeOptions configure the Claude Code hook.
type ClaudeOptions struct {
	// Heavy matches the commands to gate (DefaultHeavy when nil).
	Heavy *regexp.Regexp
	// Weight is passed to `hostslot run --weight` (1 when below 1).
	Weight int
	// Exe is the hostslot executable the rewritten command runs ("hostslot"
	// from the PATH when empty). An absolute path works without hostslot on
	// the PATH and skips shims, which cost hundreds of milliseconds a call.
	Exe string
	// Dev matches dev-server commands, run with `hostslot serve` on their
	// worktree's port (DefaultDevServer when nil).
	Dev *regexp.Regexp
	// DevContext, when set, tells Claude where the dev servers listen.
	DevContext DevContext
}

type claudeInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Cwd       string         `json:"cwd"`
}

// Claude reads a Claude Code PreToolUse event from in and, when it is a
// Bash call with heavy or dev-server commands, writes a response that puts
// `hostslot run … --` or `hostslot serve --` before each of them. Other
// events produce no output, which leaves the call unchanged.
func Claude(in io.Reader, out io.Writer, o ClaudeOptions) error {
	var ev claudeInput
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if ev.ToolName != "Bash" {
		return nil
	}
	command, _ := ev.ToolInput["command"].(string)
	input := make(map[string]any, len(ev.ToolInput))
	for k, v := range ev.ToolInput {
		input[k] = v
	}
	budget := Budget{}
	var toolTimeout time.Duration
	if background, _ := input["run_in_background"].(bool); !background {
		ms, _ := input["timeout"].(float64)
		budget, toolTimeout = claudeBudget(time.Duration(ms) * time.Millisecond)
	}
	rules, prefix, devLabels := gateRules(o.Heavy, o.Dev, ev.Cwd, func(label string, count int) string {
		return claudePrefix(o.Exe, label, o.Weight, budget.share(count))
	}, shellExe(o.Exe)+" serve -- ")
	rewritten, _, ok := GateInPlace(command, ev.Cwd, rules, prefix)
	if !ok {
		return nil
	}
	if toolTimeout > 0 {
		input["timeout"] = toolTimeout.Milliseconds()
	}
	input["command"] = rewritten
	output := map[string]any{"hookEventName": "PreToolUse", "updatedInput": input}
	if c := devContextFor(o.DevContext, command, ev.Cwd, *devLabels); c != "" {
		output["additionalContext"] = c
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"hookSpecificOutput": output})
}

func devContextFor(f DevContext, line, dir string, labels []string) string {
	if f == nil || len(labels) == 0 {
		return ""
	}
	if changesDirectory(line) {
		dir = ""
	}
	return f(dir, labels)
}

// Heavy returns the commands of a shell command line that match heavy
// (DefaultHeavy when nil), as their words joined by spaces. The line is
// parsed as Bash, so text in quotes, heredocs and comments never counts as
// a command, and `sh -c` / `bash -c` scripts are looked into. A line that
// runs hostslot itself, sets HOSTSLOT_DISABLE=1 or does not parse has no heavy
// commands: the hook then leaves it alone rather than guess.
func Heavy(line string, heavy *regexp.Regexp) []string {
	return heavyCommands(line, heavy, nil, 0)
}

// heavyCommands is Heavy with package scripts to follow: a script call is
// heavy when its script is, up to maxScriptHops deep.
func heavyCommands(line string, heavy *regexp.Regexp, scripts packageScripts, hops int) []string {
	if heavy == nil {
		heavy = DefaultHeavy
	}
	// The same opt-out as for hostslot run, written into the line itself.
	if disabledIn(line) {
		return nil
	}
	commands, ok := simpleCommands(line, 0)
	if !ok {
		return nil
	}
	var found []string
	for _, words := range commands {
		if isSelf(words[0]) {
			return nil
		}
		c := strings.Join(words, " ")
		if heavy.MatchString(c) {
			found = append(found, c)
			continue
		}
		if name, ok := scriptCall(words); ok && hops < maxScriptHops {
			if body, ok := scripts[name]; ok && len(heavyCommands(body, heavy, scripts, hops+1)) > 0 {
				found = append(found, c)
			}
		}
	}
	return found
}

// maxScriptDepth bounds how deep `sh -c` scripts are parsed.
const maxScriptDepth = 3

// simpleCommands lists the words of every simple command in line, including
// those inside command substitutions, subshells and `sh -c` scripts.
func simpleCommands(line string, depth int) ([][]string, bool) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(line), "")
	if err != nil {
		return nil, false
	}
	var out [][]string
	ok := true
	syntax.Walk(file, func(n syntax.Node) bool {
		call, isCall := n.(*syntax.CallExpr)
		if !isCall || len(call.Args) == 0 {
			return true
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = wordText(w)
		}
		out = append(out, words)
		if script, isScript := shellScript(call.Args); isScript && depth < maxScriptDepth {
			inner, innerOK := simpleCommands(script, depth+1)
			out = append(out, inner...)
			ok = ok && innerOK
		}
		return true
	})
	return out, ok
}

// wordText is a word as written, with quotes removed when they only hold
// literal text (`'pnpm test'` reads as `pnpm test`).
func wordText(w *syntax.Word) string {
	if v, ok := literal(w); ok {
		return v
	}
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, w)
	return b.String()
}

func literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, isLit := q.(*syntax.Lit)
				if !isLit {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// shellScript returns the script of `sh -c <script>` or `bash -c <script>`.
func shellScript(args []*syntax.Word) (string, bool) {
	if len(args) < 3 {
		return "", false
	}
	switch args[0].Lit() {
	case "sh", "bash", "zsh", "dash":
	default:
		return "", false
	}
	if args[1].Lit() != "-c" {
		return "", false
	}
	return literal(args[2])
}

// Wrap returns line as one hostslot job of Claude Code that learns as label.
// Agents are not at a terminal, so the job runs at background priority.
func Wrap(exe, line, label string, weight int, b Budget) string {
	run := shellExe(exe) + " run --weight " + strconv.Itoa(max(weight, 1)) + " --priority background --source claude --as " + shellQuote(label)
	if b.MaxWait > 0 {
		run += " --max-wait " + b.MaxWait.String()
	}
	if b.Timeout > 0 {
		run += " --timeout " + b.Timeout.String()
	}
	return run + " -- sh -c " + shellQuote(line)
}

// claudePrefix is what goes before a heavy command of Claude Code: a
// background job that learns as label.
func claudePrefix(exe, label string, weight int, b Budget) string {
	p := shellExe(exe) + " run --weight " + strconv.Itoa(max(weight, 1)) + " --priority background --source claude --as " + shellQuote(label)
	if b.MaxWait > 0 {
		p += " --max-wait " + b.MaxWait.String()
	}
	if b.Timeout > 0 {
		p += " --timeout " + b.Timeout.String()
	}
	return p + " -- "
}

// share is the budget of one of count gated commands in a line: the wait is
// split so the line still fits the tool's timeout; each command keeps the
// whole run time it was asked for.
func (b Budget) share(count int) Budget {
	if count > 1 && b.MaxWait > 0 {
		b.MaxWait = (b.MaxWait / time.Duration(count)).Truncate(time.Second)
	}
	return b
}

// Budget splits an agent's tool timeout between waiting for a slot and
// running the command. Zero fields mean no limit.
type Budget struct {
	MaxWait time.Duration
	Timeout time.Duration
}

// Limits of Claude Code's Bash tool.
const (
	claudeDefaultTimeout = 2 * time.Minute
	claudeMaxTimeout     = 10 * time.Minute
	// minWait is the least time a job may wait, even when the agent asked
	// for the longest timeout; the command then gets that much less.
	minWait = time.Minute
)

// claudeBudget keeps the command's own timeout (as asked, or the default)
// and gives it the rest of the Bash tool's maximum to wait for a slot. The
// tool call itself gets the maximum, so waiting never counts against the
// command.
func claudeBudget(asked time.Duration) (b Budget, toolTimeout time.Duration) {
	run := claudeDefaultTimeout
	if asked > 0 {
		run = asked
	}
	run = min(run, claudeMaxTimeout-minWait)
	return Budget{MaxWait: claudeMaxTimeout - run, Timeout: run}, claudeMaxTimeout
}

// disabledIn reports whether line turns the gate off. A text match may also
// skip a line that only mentions it, which is the safe side.
func disabledIn(line string) bool {
	return strings.Contains(line, "HOSTSLOT_DISABLE=1")
}

// isSelf reports whether a command runs hostslot itself, so a line is not
// wrapped twice.
func isSelf(word string) bool {
	return word == "hostslot"
}

// shellExe is exe for a POSIX shell line; the plain name stays unquoted.
func shellExe(exe string) string {
	if exe == "" || exe == "hostslot" {
		return "hostslot"
	}
	return shellQuote(filepath.ToSlash(exe))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
