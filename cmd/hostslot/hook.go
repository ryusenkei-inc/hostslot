package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ryusenkei-inc/hostslot/internal/hook"
)

// cmdHook exits 1 on errors: Claude Code and Codex report exit 1 as a
// non-blocking hook error and run the command unchanged, while 2 would block
// it.
func cmdHook(args []string) int {
	if len(args) == 0 || (args[0] != "claude" && args[0] != "codex") {
		fmt.Fprintln(os.Stderr, "hostslot hook: missing agent (hostslot hook claude|codex)")
		return 1
	}
	agent := args[0]
	fs := flag.NewFlagSet("hook "+agent, flag.ContinueOnError)
	weight := fs.Int("weight", 1, "weight of the wrapped commands; a hint until samples exist")
	match := fs.String("match", "", "regular expression for the commands to gate (default: built-in list)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if disabled() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	var heavy *regexp.Regexp
	if *match != "" {
		re, err := regexp.Compile(*match)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hostslot hook: --match: %v\n", err)
			return 1
		}
		heavy = re
	}
	var err error
	if agent == "claude" {
		err = hook.Claude(os.Stdin, os.Stdout, hook.ClaudeOptions{Heavy: heavy, Weight: *weight, Exe: hookExe(), DevContext: devContext})
	} else {
		o := hook.CodexOptions{Heavy: heavy, Weight: *weight, Exe: hookExe(), PowerShell: runtime.GOOS == "windows", DevContext: devContext}
		err = hook.Codex(os.Stdin, os.Stdout, o)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hostslot hook: %v\n", err)
		return 1
	}
	return 0
}

// hookExe is the hostslot the hook's rewritten commands run: this binary, so
// they need no PATH lookup, unless it is a `go run` build that is about to
// be deleted.
func hookExe() string {
	exe, err := os.Executable()
	if err != nil || strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "hostslot"
	}
	return exe
}
