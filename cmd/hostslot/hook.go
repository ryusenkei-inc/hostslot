package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

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
		o := hook.ClaudeOptions{Heavy: heavy, Weight: *weight, Exe: hookExe(), DevContext: devContext}
		if rtkEnabled() {
			o.RTK = rtkRewrite
		}
		err = hook.Claude(os.Stdin, os.Stdout, o)
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

// rtkEnabled reports whether the Claude Code hook passes lines through rtk
// first (HOSTSLOT_RTK=1). It is off by default: rtk changes what the agent
// reads, so it is turned on per machine after checking its output.
func rtkEnabled() bool {
	return os.Getenv("HOSTSLOT_RTK") == "1"
}

// rtkTimeout bounds `rtk rewrite`, which runs before every Bash call.
const rtkTimeout = 2 * time.Second

// rtkRewrite returns the line `rtk rewrite` makes of line. rtk prints a
// rewrite with exit code 0 (its allow rules cover the line) or 3 (approval
// is left to Claude Code); hostslot never decides approval, so both count.
// Any other result, or no rtk on the PATH, leaves the line as it is.
func rtkRewrite(line string) (string, bool) {
	exe, err := exec.LookPath("rtk")
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), rtkTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "rewrite", line)
	cmd.Stdout = &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 3) {
		return "", false
	}
	rewritten := strings.TrimRight(out.String(), "\r\n")
	return rewritten, rewritten != ""
}
